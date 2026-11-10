package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

// PostgreSQL monitoring, built like the Redis one: the agent finds PostgreSQL running in Docker by itself and also checks
// the addresses it is told about. A server that wants a password is reported as "needs a password" until one is given in
// the Kluard UI. The login only needs to read statistics: a user with the built-in role pg_monitor is enough.

// PostgresTarget is one PostgreSQL server to check.
type PostgresTarget struct {
	Name      string `json:"name"`
	Container string `json:"container,omitempty"`
	Addr      string `json:"addr,omitempty"` // host:port
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
	Database  string `json:"database,omitempty"` // the database to connect to (default postgres)
	SSLMode   string `json:"sslmode,omitempty"`  // disable | prefer (default) | require
}

func pgUser(t string) string {
	if t == "" {
		return "postgres"
	}
	return t
}

func pgDB(d string) string {
	if d == "" {
		return "postgres"
	}
	return d
}

// postgresImage says whether a Docker image is a PostgreSQL server (and not an exporter, an admin UI, a pooler or a backup tool).
func postgresImage(image string) bool {
	img := strings.ToLower(image)
	if i := strings.LastIndex(img, "/"); i >= 0 {
		img = img[i+1:]
	}
	img = strings.SplitN(img, ":", 2)[0]
	img = strings.SplitN(img, "@", 2)[0]
	for _, bad := range []string{"exporter", "pgadmin", "adminer", "pgbouncer", "pgpool", "backup", "pgbackrest", "barman", "operator", "patroni-", "migrate", "client", "proxy", "ui", "admin", "exporter"} {
		if strings.Contains(img, bad) {
			return false
		}
	}
	for _, good := range []string{"postgres", "postgis", "timescaledb", "citus", "pgvector", "cnpg"} {
		if strings.Contains(img, good) {
			return true
		}
	}
	return false
}

// DiscoveredPostgres is a PostgreSQL found in a Docker container (same idea as DiscoveredRedis).
type DiscoveredPostgres struct {
	Container string
	Image     string
	Addrs     []string
}

func discoverPostgres(ctx context.Context, socket string) ([]DiscoveredPostgres, error) {
	if socket == "" {
		socket = dockerSocket()
	}
	if _, err := os.Stat(socket); err != nil {
		return nil, ErrNoDocker
	}
	cl := dockerClient(socket)
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://docker/containers/json", nil)
	resp, err := cl.Do(req)
	if err != nil {
		if errors.Is(err, os.ErrPermission) || strings.Contains(err.Error(), "permission denied") {
			return nil, fmt.Errorf("no access to the Docker socket %s: add the agent's user to the docker group (re-run the installer with --docker)", socket)
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("docker API: %s", resp.Status)
	}
	var cs []dockerRedisContainer
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 8<<20)).Decode(&cs); err != nil {
		return nil, err
	}
	return postgresFromContainers(cs), nil
}

func postgresFromContainers(cs []dockerRedisContainer) []DiscoveredPostgres {
	var out []DiscoveredPostgres
	for _, c := range cs {
		if c.State != "running" || len(c.Names) == 0 || !postgresImage(c.Image) || c.Labels["kluard.postgres"] == "false" {
			continue
		}
		name := strings.TrimPrefix(c.Names[0], "/")
		private, public := 5432, 0
		for _, p := range c.Ports {
			if p.PrivatePort == 5432 && p.PublicPort != 0 && public == 0 {
				public = p.PublicPort
			}
		}
		if v := c.Labels["kluard.postgres.port"]; v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
				private = n
			}
		}
		d := DiscoveredPostgres{Container: name, Image: c.Image}
		var nets []string
		for n := range c.NetworkSettings.Networks {
			nets = append(nets, n)
		}
		sort.Strings(nets)
		for _, n := range nets {
			if ip := c.NetworkSettings.Networks[n].IPAddress; ip != "" {
				d.Addrs = append(d.Addrs, net.JoinHostPort(ip, strconv.Itoa(private)))
			}
		}
		if public != 0 {
			d.Addrs = append(d.Addrs, net.JoinHostPort("127.0.0.1", strconv.Itoa(public)))
		}
		out = append(out, d)
	}
	return out
}

// PostgresStats is what one check found.
type PostgresStats struct {
	Version      string
	MaxConns     float64
	Conns        float64
	Active       float64
	IdleInTx     float64
	LongestTx    float64
	LocksWaiting float64
	Uptime       float64
	InRecovery   bool
	ReplayLag    float64 // seconds, a standby only
	Replicas     float64 // a primary only
	DBs          []PostgresDB
}

type PostgresDB struct {
	Name                                         string
	Size, Commit, Rollback, Deadlocks, Hit, Read float64
	TempBytes                                    float64
}

func f64(s string) float64 { v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64); return v }

// PostgresCheck connects to one server and reads its statistics.
func PostgresCheck(ctx context.Context, addr string, t PostgresTarget) (PostgresStats, error) {
	var st PostgresStats
	c, err := PostgresDial(ctx, addr, pgUser(t.Username), t.Password, pgDB(t.Database), t.SSLMode)
	if err != nil {
		return st, err
	}
	defer c.close()
	// the first query failing is a real problem (the connection is bad); a later one failing (a missing permission on one view)
	// only leaves its numbers out
	_, first, err := c.Query("SELECT current_setting('server_version'), current_setting('max_connections'), pg_is_in_recovery()::int, extract(epoch FROM now()-pg_postmaster_start_time())", 1)
	if err != nil {
		return st, err
	}
	if len(first) == 1 && len(first[0]) == 4 {
		st.Version, st.MaxConns, st.InRecovery, st.Uptime = first[0][0], f64(first[0][1]), first[0][2] == "1", f64(first[0][3])
	}
	q := func(sql string, max int) [][]string {
		_, rows, _ := c.Query(sql, max)
		return rows
	}
	for _, r := range q("SELECT coalesce(state,''), count(*) FROM pg_stat_activity WHERE backend_type = 'client backend' GROUP BY 1", 20) {
		if len(r) < 2 {
			continue
		}
		n := f64(r[1])
		st.Conns += n
		switch r[0] {
		case "active":
			st.Active += n
		case "idle in transaction", "idle in transaction (aborted)":
			st.IdleInTx += n
		}
	}
	if r := q("SELECT coalesce(extract(epoch FROM max(now()-xact_start)),0) FROM pg_stat_activity WHERE backend_type = 'client backend' AND xact_start IS NOT NULL AND pid <> pg_backend_pid()", 1); len(r) == 1 && len(r[0]) == 1 {
		st.LongestTx = f64(r[0][0])
	}
	if r := q("SELECT count(*) FROM pg_locks WHERE NOT granted", 1); len(r) == 1 && len(r[0]) == 1 {
		st.LocksWaiting = f64(r[0][0])
	}
	if st.InRecovery {
		if r := q("SELECT coalesce(extract(epoch FROM now()-pg_last_xact_replay_timestamp()),0)", 1); len(r) == 1 && len(r[0]) == 1 {
			st.ReplayLag = f64(r[0][0])
		}
	} else if r := q("SELECT count(*) FROM pg_stat_replication", 1); len(r) == 1 && len(r[0]) == 1 {
		st.Replicas = f64(r[0][0])
	}
	for _, r := range q("SELECT datname, coalesce(pg_database_size(datname),0), xact_commit, xact_rollback, deadlocks, blks_hit, blks_read, temp_bytes FROM pg_stat_database WHERE datname IS NOT NULL AND datname NOT IN ('template0','template1') ORDER BY 2 DESC", 50) {
		if len(r) == 8 {
			st.DBs = append(st.DBs, PostgresDB{Name: r[0], Size: f64(r[1]), Commit: f64(r[2]), Rollback: f64(r[3]), Deadlocks: f64(r[4]), Hit: f64(r[5]), Read: f64(r[6]), TempBytes: f64(r[7])})
		}
	}
	return st, nil
}

// PostgresPoints turns a check into metrics. Counters are "sum".
func PostgresPoints(st PostgresStats, attrs map[string]string, now int64) []Point {
	var pts []Point
	g := func(name string, v float64) {
		pts = append(pts, Point{Service: "host", Name: name, Type: "gauge", Value: v, TimeNs: now, Attrs: attrs})
	}
	g("postgres_connections", st.Conns)
	g("postgres_connections_active", st.Active)
	g("postgres_connections_idle_in_transaction", st.IdleInTx)
	g("postgres_connections_max", st.MaxConns)
	if st.MaxConns > 0 {
		g("postgres_connection_utilization", st.Conns/st.MaxConns)
	}
	g("postgres_longest_transaction_seconds", st.LongestTx)
	g("postgres_locks_waiting", st.LocksWaiting)
	g("postgres_uptime_seconds", st.Uptime)
	rec := 0.0
	if st.InRecovery {
		rec = 1
		g("postgres_replication_lag_seconds", st.ReplayLag)
	} else {
		g("postgres_replicas", st.Replicas)
	}
	g("postgres_in_recovery", rec)
	for _, d := range st.DBs {
		a := map[string]string{"database": d.Name}
		for k, v := range attrs {
			a[k] = v
		}
		add := func(name, typ string, v float64) {
			pts = append(pts, Point{Service: "host", Name: name, Type: typ, Value: v, TimeNs: now, Attrs: a})
		}
		add("postgres_database_size_bytes", "gauge", d.Size)
		add("postgres_transactions_committed_total", "sum", d.Commit)
		add("postgres_transactions_rolled_back_total", "sum", d.Rollback)
		add("postgres_deadlocks_total", "sum", d.Deadlocks)
		add("postgres_temp_bytes_total", "sum", d.TempBytes)
		if d.Hit+d.Read > 0 {
			add("postgres_cache_hit_ratio", "gauge", d.Hit/(d.Hit+d.Read))
		}
	}
	return pts
}

// CollectPostgres checks every PostgreSQL it can find (in Docker) plus the ones in targets. defaultPass is tried for a
// server without a password of its own.
func CollectPostgres(ctx context.Context, socket string, targets []PostgresTarget, defaultPass string, now int64) ([]Point, error) {
	found, derr := discoverPostgres(ctx, socket)
	if errors.Is(derr, ErrNoDocker) {
		derr = nil
	}
	type job struct {
		t     PostgresTarget
		addrs []string
	}
	var jobs []job
	byContainer := map[string]PostgresTarget{}
	for _, t := range targets {
		if t.Container != "" {
			byContainer[t.Container] = t
		}
	}
	seen := map[string]bool{}
	for _, d := range found {
		t := byContainer[d.Container]
		if t.Name == "" {
			t.Name = d.Container
		}
		t.Container = d.Container
		jobs = append(jobs, job{t, d.Addrs})
		seen[d.Container] = true
	}
	for _, t := range targets {
		if t.Addr == "" || (t.Container != "" && seen[t.Container]) {
			continue
		}
		if t.Name == "" {
			t.Name = t.Addr
		}
		jobs = append(jobs, job{t, []string{t.Addr}})
	}
	for i := range jobs {
		if t, ok := byContainer[jobs[i].t.Container]; ok && t.Addr != "" {
			jobs[i].addrs = append([]string{t.Addr}, jobs[i].addrs...)
		}
		if jobs[i].t.Password == "" {
			jobs[i].t.Password = defaultPass
		}
	}
	if len(jobs) > 50 {
		jobs = jobs[:50]
	}
	var pts []Point
	for _, j := range jobs {
		attrs := map[string]string{"postgres": j.t.Name}
		if j.t.Container != "" {
			attrs["container"] = j.t.Container
		}
		var st PostgresStats
		var err error
		for _, a := range j.addrs {
			st, err = PostgresCheck(ctx, a, j.t)
			if err == nil || errors.Is(err, ErrPostgresAuth) {
				attrs["addr"] = a
				break
			}
		}
		if len(j.addrs) == 0 {
			err = errors.New("no address to reach it on")
		}
		up, auth := 1.0, 0.0
		if err != nil {
			up = 0
			if errors.Is(err, ErrPostgresAuth) {
				auth = 1
				if j.t.Password != "" {
					attrs["reason"] = "the password is wrong"
				} else {
					attrs["reason"] = "needs a password"
				}
			} else {
				attrs["reason"] = shortErr(err)
			}
		}
		base := map[string]string{"postgres": j.t.Name}
		pts = append(pts,
			Point{Service: "host", Name: "postgres_up", Type: "gauge", Value: up, TimeNs: now, Attrs: base},
			Point{Service: "host", Name: "postgres_auth_required", Type: "gauge", Value: auth, TimeNs: now, Attrs: base})
		ia := map[string]string{"postgres": j.t.Name, "container": j.t.Container, "addr": attrs["addr"], "reason": attrs["reason"]}
		if err == nil {
			ia["version"] = st.Version
			ia["role"] = "primary"
			if st.InRecovery {
				ia["role"] = "standby"
			}
			pts = append(pts, PostgresPoints(st, base, now)...)
		}
		pts = append(pts, Point{Service: "host", Name: "postgres_info", Type: "gauge", Value: 1, TimeNs: now, Attrs: ia})
	}
	return pts, derr
}
