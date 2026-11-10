package agent

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakePG is a PostgreSQL server that speaks just enough of the protocol: it asks for the login method it is told to,
// checks the password (SCRAM on the server side as the real one does it) and answers queries from a table.
type fakePG struct {
	ln       net.Listener
	method   string // trust | cleartext | md5 | scram
	user     string
	pass     string
	tls      byte // reply to SSLRequest: 'N' or 0 (close)
	answers  map[string][][]string
	cols     map[string][]string
	failures map[string]string // query fragment -> error message
	queries  []string
}

func startFake(t *testing.T, f *fakePG) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.ln = ln
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
}

func pgMsg(typ byte, body []byte) []byte {
	b := []byte{typ}
	b = binary.BigEndian.AppendUint32(b, uint32(len(body)+4))
	return append(b, body...)
}

func pgErrMsg(code, msg string) []byte {
	return pgMsg('E', []byte("SERROR\x00C"+code+"\x00M"+msg+"\x00\x00"))
}

func (f *fakePG) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	readStartup := func() []byte {
		var l [4]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return nil
		}
		b := make([]byte, binary.BigEndian.Uint32(l[:])-4)
		io.ReadFull(r, b)
		return b
	}
	b := readStartup()
	if len(b) == 4 && binary.BigEndian.Uint32(b) == 80877103 { // SSLRequest
		if f.tls == 0 {
			return
		}
		c.Write([]byte{f.tls})
		b = readStartup()
	}
	if len(b) < 4 || binary.BigEndian.Uint32(b) != 196608 {
		return
	}
	readMsg := func() (byte, []byte) {
		var h [5]byte
		if _, err := io.ReadFull(r, h[:]); err != nil {
			return 0, nil
		}
		body := make([]byte, binary.BigEndian.Uint32(h[1:])-4)
		io.ReadFull(r, body)
		return h[0], body
	}
	auth := func(kind uint32, extra ...byte) {
		c.Write(pgMsg('R', append(binary.BigEndian.AppendUint32(nil, kind), extra...)))
	}
	bad := func() { c.Write(pgErrMsg("28P01", `password authentication failed for user "`+f.user+`"`)) }
	switch f.method {
	case "cleartext":
		auth(3)
		_, body := readMsg()
		if string(body) != f.pass+"\x00" {
			bad()
			return
		}
	case "md5":
		salt := []byte{1, 2, 3, 4}
		auth(5, salt...)
		_, body := readMsg()
		if string(body) != "md5"+md5hex(md5hex(f.pass+f.user)+string(salt))+"\x00" {
			bad()
			return
		}
	case "scram":
		auth(10, []byte("SCRAM-SHA-256\x00\x00")...)
		_, body := readMsg() // mechanism, length, client-first
		if len(body) < len("SCRAM-SHA-256\x00")+4 {
			return
		}
		first := string(body[len("SCRAM-SHA-256\x00")+4:])
		bare := strings.TrimPrefix(first, "n,,")
		cn := scramAttrs(bare)["r"]
		salt := []byte("0123456789abcdef")
		sn := cn + "serverpart"
		serverFirst := "r=" + sn + ",s=" + base64.StdEncoding.EncodeToString(salt) + ",i=4096"
		auth(11, []byte(serverFirst)...)
		_, body = readMsg()
		final := string(body)
		a := scramAttrs(final)
		salted := pbkdf2SHA256(f.pass, salt, 4096)
		clientKey := hmacSHA256(salted, "Client Key")
		stored := sha256.Sum256(clientKey)
		withoutProof := final[:strings.LastIndex(final, ",p=")]
		authMsg := bare + "," + serverFirst + "," + withoutProof
		sig := hmacSHA256(stored[:], authMsg)
		proof, _ := base64.StdEncoding.DecodeString(a["p"])
		ok := len(proof) == len(sig) && a["r"] == sn
		if ok {
			ck := make([]byte, len(sig))
			for i := range ck {
				ck[i] = proof[i] ^ sig[i]
			}
			s2 := sha256.Sum256(ck)
			ok = hmac.Equal(s2[:], stored[:])
		}
		if !ok {
			bad()
			return
		}
		serverKey := hmacSHA256(salted, "Server Key")
		auth(12, []byte("v="+base64.StdEncoding.EncodeToString(hmacSHA256(serverKey, authMsg)))...)
	}
	auth(0)
	c.Write(pgMsg('Z', []byte{'I'}))
	for {
		t, body := readMsg()
		if t == 0 || t == 'X' {
			return
		}
		if t != 'Q' {
			continue
		}
		q := strings.TrimSuffix(string(body), "\x00")
		f.queries = append(f.queries, q)
		answered := false
		for frag, msg := range f.failures {
			if strings.Contains(q, frag) {
				c.Write(pgErrMsg("42501", msg))
				answered = true
			}
		}
		best := ""
		for frag := range f.answers { // the most specific fragment wins
			if strings.Contains(q, frag) && len(frag) > len(best) {
				best = frag
			}
		}
		for frag, rows := range f.answers {
			if answered || frag != best {
				continue
			}
			var rd []byte
			rd = binary.BigEndian.AppendUint16(rd, uint16(len(f.cols[frag])))
			for _, n := range f.cols[frag] {
				rd = append(rd, n...)
				rd = append(rd, 0)
				rd = append(rd, make([]byte, 18)...)
			}
			c.Write(pgMsg('T', rd))
			for _, row := range rows {
				var d []byte
				d = binary.BigEndian.AppendUint16(d, uint16(len(row)))
				for _, v := range row {
					d = binary.BigEndian.AppendUint32(d, uint32(len(v)))
					d = append(d, v...)
				}
				c.Write(pgMsg('D', d))
			}
			c.Write(pgMsg('C', []byte("SELECT\x00")))
			answered = true
		}
		if !answered {
			c.Write(pgMsg('C', []byte("SELECT\x00")))
		}
		c.Write(pgMsg('Z', []byte{'I'}))
	}
}

func (f *fakePG) addr() string { return f.ln.Addr().String() }

func healthyFake(method string) *fakePG {
	return &fakePG{method: method, user: "postgres", pass: "s3cret", tls: 'N',
		answers: map[string][][]string{
			"server_version":       {{"16.4", "100", "0", "86400"}},
			"GROUP BY 1":           {{"active", "3"}, {"idle", "10"}, {"idle in transaction", "2"}, {"", "1"}},
			"max(now()-xact_start": {{"42.5"}},
			"pg_locks":             {{"1"}},
			"pg_stat_replication":  {{"2"}},
			"pg_stat_database":     {{"app", "1000000", "500", "5", "2", "900", "100", "2048"}, {"postgres", "8000000", "10", "0", "0", "0", "0", "0"}},
		},
		cols: map[string][]string{},
	}
}

func TestPostgresLoginMethods(t *testing.T) {
	for _, m := range []string{"trust", "cleartext", "md5", "scram"} {
		f := healthyFake(m)
		startFake(t, f)
		st, err := PostgresCheck(context.Background(), f.addr(), PostgresTarget{Password: "s3cret"})
		if err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		if st.Version != "16.4" || st.MaxConns != 100 || st.Uptime != 86400 {
			t.Fatalf("%s: %+v", m, st)
		}
		if st.Conns != 16 || st.Active != 3 || st.IdleInTx != 2 || st.LongestTx != 42.5 || st.LocksWaiting != 1 || st.Replicas != 2 {
			t.Fatalf("%s: %+v", m, st)
		}
		if len(st.DBs) != 2 || st.DBs[0].Name != "app" || st.DBs[0].Commit != 500 {
			t.Fatalf("%s: %+v", m, st.DBs)
		}
		// a wrong or missing password is "needs a password", never a vague error
		if m != "trust" {
			if _, err := PostgresCheck(context.Background(), f.addr(), PostgresTarget{Password: "wrong"}); !errors.Is(err, ErrPostgresAuth) {
				t.Fatalf("%s: wrong password gave %v", m, err)
			}
			if _, err := PostgresCheck(context.Background(), f.addr(), PostgresTarget{}); !errors.Is(err, ErrPostgresAuth) {
				t.Fatalf("%s: no password gave %v", m, err)
			}
		}
	}
}

func TestPostgresTLSRequire(t *testing.T) {
	f := healthyFake("trust")
	startFake(t, f)
	if _, err := PostgresCheck(context.Background(), f.addr(), PostgresTarget{SSLMode: "require"}); err == nil || !strings.Contains(err.Error(), "TLS") {
		t.Fatalf("require against a server without TLS: %v", err)
	}
	if _, err := PostgresCheck(context.Background(), f.addr(), PostgresTarget{SSLMode: "disable"}); err != nil {
		t.Fatalf("disable: %v", err)
	}
}

func TestPostgresNotPostgres(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		c, _ := ln.Accept()
		c.Write([]byte("-ERR this is redis\r\n"))
		time.Sleep(50 * time.Millisecond)
		c.Close()
	}()
	if _, err := PostgresCheck(context.Background(), ln.Addr().String(), PostgresTarget{SSLMode: "disable"}); err == nil {
		t.Fatal("a server that is not PostgreSQL must give an error")
	}
}

func TestPostgresOptionalViewMissingKeepsTheRest(t *testing.T) {
	f := healthyFake("trust")
	f.failures = map[string]string{"pg_locks": "permission denied for view pg_locks"}
	startFake(t, f)
	st, err := PostgresCheck(context.Background(), f.addr(), PostgresTarget{})
	if err != nil || st.Conns != 16 || len(st.DBs) != 2 {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestCollectPostgresPoints(t *testing.T) {
	f := healthyFake("scram")
	startFake(t, f)
	pts, err := CollectPostgres(context.Background(), "/nonexistent/docker.sock", []PostgresTarget{{Name: "main", Addr: f.addr(), Password: "s3cret"}}, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]float64{}
	for _, p := range pts {
		if p.Attrs["database"] == "" {
			by[p.Name] = p.Value
		}
		if p.Attrs["postgres"] != "main" {
			t.Fatalf("%s lacks the postgres label: %v", p.Name, p.Attrs)
		}
	}
	for n, want := range map[string]float64{"postgres_up": 1, "postgres_auth_required": 0, "postgres_connections": 16, "postgres_connection_utilization": 0.16, "postgres_replicas": 2, "postgres_in_recovery": 0, "postgres_locks_waiting": 1} {
		if by[n] < want-1e-9 || by[n] > want+1e-9 {
			t.Errorf("%s = %v, want %v", n, by[n], want)
		}
	}
	var hit float64
	for _, p := range pts {
		if p.Name == "postgres_cache_hit_ratio" && p.Attrs["database"] == "app" {
			hit = p.Value
		}
	}
	if hit != 0.9 {
		t.Errorf("cache hit ratio %v", hit)
	}
	// no password: reported as such, with up=0
	pts, _ = CollectPostgres(context.Background(), "/nonexistent/docker.sock", []PostgresTarget{{Name: "main", Addr: f.addr()}}, "", 1)
	for _, p := range pts {
		if p.Name == "postgres_up" && p.Value != 0 {
			t.Error("up without a password")
		}
		if p.Name == "postgres_auth_required" && p.Value != 1 {
			t.Error("auth_required not set")
		}
		if p.Name == "postgres_info" && p.Attrs["reason"] != "needs a password" {
			t.Errorf("reason %q", p.Attrs["reason"])
		}
	}
}

func TestPostgresImage(t *testing.T) {
	for img, want := range map[string]bool{"postgres:16": true, "postgis/postgis:16-3.4": true, "timescale/timescaledb:latest-pg16": true, "bitnami/postgresql:15": true,
		"prometheuscommunity/postgres-exporter": false, "dpage/pgadmin4": false, "bitnami/pgbouncer": false, "redis:7": false, "nextcloud:29": false, "adminer": false} {
		if got := postgresImage(img); got != want {
			t.Errorf("%s: %v, want %v", img, got, want)
		}
	}
}

func TestPostgresDiscoveryFromContainers(t *testing.T) {
	var c dockerRedisContainer
	c.Names, c.Image, c.State = []string{"/db"}, "postgres:16", "running"
	c.NetworkSettings.Networks = map[string]struct{ IPAddress string }{"app": {"172.18.0.5"}}
	d := postgresFromContainers([]dockerRedisContainer{c})
	if len(d) != 1 || d[0].Container != "db" || d[0].Addrs[0] != "172.18.0.5:5432" {
		t.Fatalf("%+v", d)
	}
	c.Labels = map[string]string{"kluard.postgres": "false"}
	if len(postgresFromContainers([]dockerRedisContainer{c})) != 0 {
		t.Fatal("the opt-out label is ignored")
	}
}
