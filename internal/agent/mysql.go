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
	"sync"
	"time"
)

// MySQL and MariaDB monitoring, built like the PostgreSQL one: the agent finds MySQL and MariaDB running in Docker by itself
// and also checks the addresses it is told about. A server that wants a login is reported as "needs a password" until one
// is given in the Kluard UI. The user only needs to read statistics:
//
//	CREATE USER 'kluard'@'%' IDENTIFIED BY '...'; GRANT PROCESS, REPLICATION CLIENT ON *.* TO 'kluard'@'%';

// MysqlTarget is one MySQL or MariaDB server to check.
type MysqlTarget struct {
	Name      string `json:"name"`
	Container string `json:"container,omitempty"`
	Addr      string `json:"addr,omitempty"` // host:port
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
	SSLMode   string `json:"sslmode,omitempty"` // disable | prefer (default) | require
}

func myUser(t string) string {
	if t == "" {
		return "root"
	}
	return t
}

// mysqlImage says whether a Docker image is a MySQL or MariaDB server (and not an exporter, an admin UI, a proxy or a backup tool).
func mysqlImage(image string) bool {
	img := strings.ToLower(image)
	if i := strings.LastIndex(img, "/"); i >= 0 {
		img = img[i+1:]
	}
	img = strings.SplitN(img, ":", 2)[0]
	img = strings.SplitN(img, "@", 2)[0]
	for _, bad := range []string{"exporter", "phpmyadmin", "adminer", "proxysql", "router", "backup", "operator", "migrate", "client", "proxy", "admin", "workbench", "-ui", "mysqlsh", "shell"} {
		if strings.Contains(img, bad) {
			return false
		}
	}
	for _, good := range []string{"mysql", "mariadb", "percona-server", "percona", "galera"} {
		if strings.Contains(img, good) {
			return true
		}
	}
	return false
}

// DiscoveredMysql is a MySQL found in a Docker container (same idea as DiscoveredRedis).
type DiscoveredMysql struct {
	Container string
	Image     string
	Addrs     []string
}

func discoverMysql(ctx context.Context, socket string) ([]DiscoveredMysql, error) {
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
	return mysqlFromContainers(cs), nil
}

func mysqlFromContainers(cs []dockerRedisContainer) []DiscoveredMysql {
	var out []DiscoveredMysql
	for _, c := range cs {
		if c.State != "running" || len(c.Names) == 0 || !mysqlImage(c.Image) || c.Labels["kluard.mysql"] == "false" {
			continue
		}
		name := strings.TrimPrefix(c.Names[0], "/")
		private, public := 3306, 0
		for _, p := range c.Ports {
			if p.PrivatePort == 3306 && p.PublicPort != 0 && public == 0 {
				public = p.PublicPort
			}
		}
		if v := c.Labels["kluard.mysql.port"]; v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
				private = n
			}
		}
		d := DiscoveredMysql{Container: name, Image: c.Image}
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

// MysqlStats is what one check found.
type MysqlStats struct {
	Version, Flavor                 string
	MaxConns, Conns, Running        float64
	Uptime, Queries, Slow           float64
	AbortedConnects, AbortedClients float64
	BytesIn, BytesOut, LocksWaited  float64
	PoolReads, PoolRequests         float64
	LongestQuery                    float64
	ReadOnly                        bool
	Replica                         bool
	ReplicaRunning                  bool
	ReplicaLag                      float64
	Sizes                           []MysqlDB
}

type MysqlDB struct {
	Name string
	Size float64
}

// the database sizes need a scan of information_schema, so they are read every 5 minutes at most
var (
	mysqlSizeMu sync.Mutex
	mysqlSizeAt = map[string]time.Time{}
	mysqlSizes  = map[string][]MysqlDB{}
)

// MysqlCheck connects to one server and reads its statistics.
func MysqlCheck(ctx context.Context, addr string, t MysqlTarget) (MysqlStats, error) {
	var st MysqlStats
	c, err := MysqlDial(ctx, addr, myUser(t.Username), t.Password, t.SSLMode)
	if err != nil {
		return st, err
	}
	defer c.close()
	_, first, err := c.Query("SELECT VERSION(), @@global.max_connections, @@global.read_only", 1)
	if err != nil {
		return st, err
	}
	if len(first) == 1 && len(first[0]) == 3 {
		st.Version, st.MaxConns, st.ReadOnly = first[0][0], f64(first[0][1]), first[0][2] == "1"
		st.Flavor = "mysql"
		if strings.Contains(strings.ToLower(st.Version), "mariadb") {
			st.Flavor = "mariadb"
		}
	}
	// a later query failing (a missing privilege) only leaves its numbers out
	_, rows, _ := c.Query("SHOW GLOBAL STATUS", 1000)
	vars := map[string]float64{}
	for _, r := range rows {
		if len(r) == 2 {
			vars[r[0]] = f64(r[1])
		}
	}
	st.Conns, st.Running, st.Uptime = vars["Threads_connected"], vars["Threads_running"], vars["Uptime"]
	st.Queries, st.Slow = vars["Queries"], vars["Slow_queries"]
	st.AbortedConnects, st.AbortedClients = vars["Aborted_connects"], vars["Aborted_clients"]
	st.BytesIn, st.BytesOut, st.LocksWaited = vars["Bytes_received"], vars["Bytes_sent"], vars["Table_locks_waited"]
	st.PoolReads, st.PoolRequests = vars["Innodb_buffer_pool_reads"], vars["Innodb_buffer_pool_read_requests"]
	if _, r, _ := c.Query("SELECT COALESCE(MAX(TIME),0) FROM information_schema.processlist WHERE COMMAND NOT IN ('Sleep','Binlog Dump','Binlog Dump GTID') AND USER NOT IN ('system user','event_scheduler') AND ID <> CONNECTION_ID()", 1); len(r) == 1 && len(r[0]) == 1 {
		st.LongestQuery = f64(r[0][0])
	}
	for _, q := range []string{"SHOW REPLICA STATUS", "SHOW SLAVE STATUS"} {
		cols, r, err := c.Query(q, 4)
		if err != nil {
			continue
		}
		if len(r) > 0 {
			col := map[string]string{}
			for i, n := range cols {
				if i < len(r[0]) {
					col[n] = r[0][i]
				}
			}
			st.Replica = true
			io, sq, lag := col["Replica_IO_Running"], col["Replica_SQL_Running"], col["Seconds_Behind_Source"]
			if io == "" {
				io, sq, lag = col["Slave_IO_Running"], col["Slave_SQL_Running"], col["Seconds_Behind_Master"]
			}
			st.ReplicaRunning = io == "Yes" && sq == "Yes"
			st.ReplicaLag = f64(lag)
		}
		break
	}
	key := addr + "\x00" + myUser(t.Username)
	mysqlSizeMu.Lock()
	cached, fresh := mysqlSizes[key], time.Since(mysqlSizeAt[key]) < 5*time.Minute
	mysqlSizeMu.Unlock()
	if fresh {
		st.Sizes = cached
	} else {
		var sizes []MysqlDB
		_, r, err := c.Query("SELECT table_schema, COALESCE(SUM(data_length+index_length),0) FROM information_schema.tables WHERE table_schema NOT IN ('information_schema','performance_schema','mysql','sys') GROUP BY table_schema ORDER BY 2 DESC", 50)
		if err == nil {
			for _, x := range r {
				if len(x) == 2 {
					sizes = append(sizes, MysqlDB{Name: x[0], Size: f64(x[1])})
				}
			}
			mysqlSizeMu.Lock()
			mysqlSizes[key], mysqlSizeAt[key] = sizes, time.Now()
			mysqlSizeMu.Unlock()
			st.Sizes = sizes
		}
	}
	return st, nil
}

// MysqlPoints turns a check into metrics. Counters are "sum".
func MysqlPoints(st MysqlStats, attrs map[string]string, now int64) []Point {
	var pts []Point
	add := func(name, typ string, v float64, a map[string]string) {
		pts = append(pts, Point{Service: "host", Name: name, Type: typ, Value: v, TimeNs: now, Attrs: a})
	}
	g := func(name string, v float64) { add(name, "gauge", v, attrs) }
	c := func(name string, v float64) { add(name, "sum", v, attrs) }
	g("mysql_connections", st.Conns)
	g("mysql_connections_running", st.Running)
	g("mysql_connections_max", st.MaxConns)
	if st.MaxConns > 0 {
		g("mysql_connection_utilization", st.Conns/st.MaxConns)
	}
	g("mysql_uptime_seconds", st.Uptime)
	g("mysql_longest_query_seconds", st.LongestQuery)
	c("mysql_queries_total", st.Queries)
	c("mysql_slow_queries_total", st.Slow)
	c("mysql_connections_aborted_total", st.AbortedConnects)
	c("mysql_clients_aborted_total", st.AbortedClients)
	c("mysql_bytes_received_total", st.BytesIn)
	c("mysql_bytes_sent_total", st.BytesOut)
	c("mysql_table_locks_waited_total", st.LocksWaited)
	if st.PoolRequests > 0 {
		g("mysql_innodb_buffer_pool_hit_ratio", 1-st.PoolReads/st.PoolRequests)
	}
	ro := 0.0
	if st.ReadOnly {
		ro = 1
	}
	g("mysql_read_only", ro)
	if st.Replica {
		run := 0.0
		if st.ReplicaRunning {
			run = 1
		}
		g("mysql_replication_running", run)
		g("mysql_replication_lag_seconds", st.ReplicaLag)
	}
	for _, d := range st.Sizes {
		a := map[string]string{"database": d.Name}
		for k, v := range attrs {
			a[k] = v
		}
		add("mysql_database_size_bytes", "gauge", d.Size, a)
	}
	return pts
}

// CollectMysql checks every MySQL it can find (in Docker) plus the ones in targets. defaultPass is tried for a
// server without a password of its own.
func CollectMysql(ctx context.Context, socket string, targets []MysqlTarget, defaultPass string, now int64) ([]Point, error) {
	found, derr := discoverMysql(ctx, socket)
	if errors.Is(derr, ErrNoDocker) {
		derr = nil
	}
	type job struct {
		t     MysqlTarget
		addrs []string
	}
	var jobs []job
	byContainer := map[string]MysqlTarget{}
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
		attrs := map[string]string{"mysql": j.t.Name}
		if j.t.Container != "" {
			attrs["container"] = j.t.Container
		}
		var st MysqlStats
		var err error
		for _, a := range j.addrs {
			st, err = MysqlCheck(ctx, a, j.t)
			if err == nil || errors.Is(err, ErrMysqlAuth) {
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
			if errors.Is(err, ErrMysqlAuth) {
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
		base := map[string]string{"mysql": j.t.Name}
		pts = append(pts,
			Point{Service: "host", Name: "mysql_up", Type: "gauge", Value: up, TimeNs: now, Attrs: base},
			Point{Service: "host", Name: "mysql_auth_required", Type: "gauge", Value: auth, TimeNs: now, Attrs: base})
		ia := map[string]string{"mysql": j.t.Name, "container": j.t.Container, "addr": attrs["addr"], "reason": attrs["reason"]}
		if err == nil {
			ia["version"] = st.Version
			ia["flavor"] = st.Flavor
			ia["role"] = "primary"
			if st.Replica {
				ia["role"] = "replica"
			}
			pts = append(pts, MysqlPoints(st, base, now)...)
		}
		pts = append(pts, Point{Service: "host", Name: "mysql_info", Type: "gauge", Value: 1, TimeNs: now, Attrs: ia})
	}
	return pts, derr
}
