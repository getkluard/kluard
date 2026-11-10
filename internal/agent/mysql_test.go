package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeMysql speaks just enough of the MySQL protocol to test the client: a login with the chosen plugin and canned answers.
type fakeMysql struct {
	ln       net.Listener
	plugin   string // mysql_native_password or caching_sha2_password
	password string
	key      *rsa.PrivateKey
	answers  map[string][2]interface{} // sql prefix -> columns []string, rows [][]string
	notMySQL bool
}

func newFakeMysql(t *testing.T, plugin, password string) *fakeMysql {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	f := &fakeMysql{ln: ln, plugin: plugin, password: password, key: key, answers: map[string][2]interface{}{}}
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
	return f
}

func (f *fakeMysql) addr() string { return f.ln.Addr().String() }

func fmWrite(c net.Conn, seq byte, body []byte) {
	c.Write(append([]byte{byte(len(body)), byte(len(body) >> 8), byte(len(body) >> 16), seq}, body...))
}

func fmRead(c net.Conn) ([]byte, byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(c, h[:]); err != nil {
		return nil, 0, err
	}
	b := make([]byte, int(h[0])|int(h[1])<<8|int(h[2])<<16)
	_, err := io.ReadFull(c, b)
	return b, h[3], err
}

func lenencStr(s string) []byte { return append([]byte{byte(len(s))}, s...) }

func (f *fakeMysql) serve(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if f.notMySQL {
		c.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}
	nonce := []byte("0123456789abcdefghij") // 20 bytes
	hs := []byte{10}
	hs = append(hs, "8.0.36-fake\x00"...)
	hs = append(hs, 1, 0, 0, 0)
	hs = append(hs, nonce[:8]...)
	hs = append(hs, 0)
	hs = append(hs, 0xff, 0xf7) // capabilities low, without SSL
	hs = append(hs, 45, 2, 0)
	hs = append(hs, 0xff, 0xdf) // capabilities high (plugin auth)
	hs = append(hs, 21)
	hs = append(hs, make([]byte, 10)...)
	hs = append(hs, nonce[8:]...)
	hs = append(hs, 0)
	hs = append(hs, f.plugin...)
	hs = append(hs, 0)
	fmWrite(c, 0, hs)
	resp, seq, err := fmRead(c)
	if err != nil || len(resp) < 33 {
		return
	}
	rest := resp[32:]
	end := bytes.IndexByte(rest, 0)
	user, rest := string(rest[:end]), rest[end+1:]
	alen := int(rest[0])
	auth := rest[1 : 1+alen]
	var want []byte
	if f.plugin == "caching_sha2_password" {
		want = myCachingScramble(f.password, nonce)
	} else {
		want = myNativeScramble(f.password, nonce)
	}
	denied := func() {
		fmWrite(c, seq+1, append([]byte{0xff, 0x15, 0x04, '#', '2', '8', '0', '0', '0'}, "Access denied for user '"+user+"'"...))
	}
	ok := func(s byte) { fmWrite(c, s, []byte{0, 0, 0, 2, 0, 0, 0}) }
	if f.plugin == "caching_sha2_password" && f.password != "" {
		fmWrite(c, seq+1, []byte{0x01, 0x04}) // not cached: full authentication
		p, s, err := fmRead(c)
		if err != nil || len(p) != 1 || p[0] != 2 {
			return
		}
		der, _ := x509.MarshalPKIXPublicKey(&f.key.PublicKey)
		fmWrite(c, s+1, append([]byte{0x01}, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})...))
		enc, s, err := fmRead(c)
		if err != nil {
			return
		}
		pw, err := rsa.DecryptOAEP(sha1.New(), rand.Reader, f.key, enc, nil)
		if err != nil {
			denied()
			return
		}
		for i := range pw {
			pw[i] ^= nonce[i%len(nonce)]
		}
		if string(pw) != f.password+"\x00" {
			fmWrite(c, s+1, []byte{0xff, 0x15, 0x04, '#', '2', '8', '0', '0', '0', 'n', 'o'})
			return
		}
		ok(s + 1)
	} else {
		if !bytes.Equal(auth, want) {
			denied()
			return
		}
		ok(seq + 1)
	}
	for {
		p, _, err := fmRead(c)
		if err != nil || len(p) == 0 || p[0] == 0x01 {
			return
		}
		sql := string(p[1:])
		var hit [2]interface{}
		found := false
		for k, v := range f.answers {
			if strings.HasPrefix(sql, k) {
				hit, found = v, true
			}
		}
		if !found {
			fmWrite(c, 1, append([]byte{0xff, 0x28, 0x04, '#', '4', '2', '0', '0', '0'}, "unknown statement"...))
			continue
		}
		cols, rows := hit[0].([]string), hit[1].([][]string)
		s := byte(1)
		fmWrite(c, s, []byte{byte(len(cols))})
		for _, n := range cols {
			s++
			d := append(append(append(append(lenencStr("def"), lenencStr("")...), lenencStr("")...), lenencStr("")...), lenencStr(n)...)
			d = append(d, lenencStr("")...)
			d = append(d, 0x0c, 33, 0, 255, 0, 0, 0, 0xfd, 0, 0, 0, 0, 0)
			fmWrite(c, s, d)
		}
		s++
		fmWrite(c, s, []byte{0xfe, 0, 0, 2, 0})
		for _, r := range rows {
			s++
			var row []byte
			for _, v := range r {
				row = append(row, lenencStr(v)...)
			}
			fmWrite(c, s, row)
		}
		s++
		fmWrite(c, s, []byte{0xfe, 0, 0, 2, 0})
	}
}

func statusRows() [][]string {
	return [][]string{{"Threads_connected", "12"}, {"Threads_running", "3"}, {"Uptime", "86400"}, {"Queries", "5000"}, {"Slow_queries", "2"},
		{"Aborted_connects", "1"}, {"Bytes_received", "100"}, {"Bytes_sent", "200"}, {"Innodb_buffer_pool_reads", "10"}, {"Innodb_buffer_pool_read_requests", "1000"}}
}

func (f *fakeMysql) standard() {
	f.answers["SELECT VERSION()"] = [2]interface{}{[]string{"v", "m", "r"}, [][]string{{"10.11.6-MariaDB", "151", "0"}}}
	f.answers["SHOW GLOBAL STATUS"] = [2]interface{}{[]string{"Variable_name", "Value"}, statusRows()}
	f.answers["SELECT COALESCE(MAX(TIME)"] = [2]interface{}{[]string{"t"}, [][]string{{"42"}}}
	f.answers["SHOW SLAVE STATUS"] = [2]interface{}{[]string{"Slave_IO_Running", "Slave_SQL_Running", "Seconds_Behind_Master"}, [][]string{{"Yes", "Yes", "7"}}}
	f.answers["SELECT table_schema"] = [2]interface{}{[]string{"s", "b"}, [][]string{{"nextcloud", "1048576"}}}
}

func TestMysqlLoginMethods(t *testing.T) {
	for _, c := range []struct {
		name, plugin, password, given string
		ok                            bool
	}{
		{"native", "mysql_native_password", "s3cret", "s3cret", true},
		{"native empty", "mysql_native_password", "", "", true},
		{"native wrong", "mysql_native_password", "s3cret", "nope", false},
		{"native missing", "mysql_native_password", "s3cret", "", false},
		{"caching sha2 over RSA", "caching_sha2_password", "s3cret", "s3cret", true},
		{"caching sha2 wrong", "caching_sha2_password", "s3cret", "nope", false},
	} {
		f := newFakeMysql(t, c.plugin, c.password)
		f.standard()
		st, err := MysqlCheck(context.Background(), f.addr(), MysqlTarget{Username: "kluard", Password: c.given, SSLMode: "disable"})
		if c.ok && err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !c.ok {
			if !errors.Is(err, ErrMysqlAuth) {
				t.Errorf("%s: want the login error, got %v", c.name, err)
			}
			continue
		}
		if st.Version != "10.11.6-MariaDB" || st.Flavor != "mariadb" || st.MaxConns != 151 || st.Conns != 12 {
			t.Errorf("%s: %+v", c.name, st)
		}
	}
}

func TestMysqlRequireTLSRefusedWhenOfferedNot(t *testing.T) {
	f := newFakeMysql(t, "mysql_native_password", "")
	f.standard()
	if _, err := MysqlCheck(context.Background(), f.addr(), MysqlTarget{SSLMode: "require"}); err == nil || errors.Is(err, ErrMysqlAuth) {
		t.Fatalf("%v", err)
	}
}

func TestMysqlNotMysql(t *testing.T) {
	f := newFakeMysql(t, "mysql_native_password", "")
	f.notMySQL = true
	if _, err := MysqlCheck(context.Background(), f.addr(), MysqlTarget{}); err == nil || errors.Is(err, ErrMysqlAuth) {
		t.Fatalf("%v", err)
	}
}

func TestCollectMysqlPoints(t *testing.T) {
	f := newFakeMysql(t, "mysql_native_password", "pw")
	f.standard()
	good := MysqlTarget{Name: "shop", Addr: f.addr(), Username: "kluard", Password: "pw", SSLMode: "disable"}
	bad := MysqlTarget{Name: "locked", Addr: f.addr(), Username: "kluard", Password: "", SSLMode: "disable"}
	pts, _ := CollectMysql(context.Background(), "/nonexistent.sock", []MysqlTarget{good, bad}, "", 1)
	got := map[string]float64{}
	info := map[string]string{}
	for _, p := range pts {
		if p.Attrs["mysql"] == "shop" && p.Attrs["database"] == "" {
			got[p.Name] = p.Value
		}
		if p.Name == "mysql_database_size_bytes" && p.Attrs["database"] == "nextcloud" {
			got["size"] = p.Value
		}
		if p.Name == "mysql_info" {
			info[p.Attrs["mysql"]+"/role"], info[p.Attrs["mysql"]+"/reason"] = p.Attrs["role"], p.Attrs["reason"]
		}
		if p.Attrs["mysql"] == "locked" && p.Name == "mysql_auth_required" && p.Value != 1 {
			t.Error("a refused login must be flagged")
		}
	}
	if got["mysql_up"] != 1 || got["mysql_connections"] != 12 || got["mysql_connection_utilization"] <= 0.07 || got["mysql_longest_query_seconds"] != 42 ||
		got["mysql_replication_lag_seconds"] != 7 || got["mysql_replication_running"] != 1 || got["size"] != 1048576 || got["mysql_queries_total"] != 5000 {
		t.Fatalf("%v", got)
	}
	if h := got["mysql_innodb_buffer_pool_hit_ratio"]; h < 0.98 || h > 0.995 {
		t.Fatalf("hit ratio %v", h)
	}
	if info["shop/role"] != "replica" || info["locked/reason"] != "needs a password" {
		t.Fatalf("%v", info)
	}
}

func TestMysqlImageAndDiscovery(t *testing.T) {
	for img, want := range map[string]bool{"mariadb:11": true, "mysql:8.0": true, "docker.io/library/mysql": true, "percona/percona-server:8.0": true, "bitnami/mariadb-galera": true,
		"prom/mysqld-exporter": false, "phpmyadmin": false, "proxysql/proxysql": false, "mysql/mysql-router": false, "postgres": false} {
		if got := mysqlImage(img); got != want {
			t.Errorf("%s: %v", img, got)
		}
	}
	var a, b dockerRedisContainer
	a.Names, a.Image, a.State = []string{"/db"}, "mariadb:11", "running"
	a.NetworkSettings.Networks = map[string]struct{ IPAddress string }{"bridge": {"172.17.0.9"}}
	b.Names, b.Image, b.State, b.Labels = []string{"/old"}, "mysql:5.7", "running", map[string]string{"kluard.mysql": "false"}
	got := mysqlFromContainers([]dockerRedisContainer{a, b})
	if len(got) != 1 || got[0].Container != "db" || got[0].Addrs[0] != "172.17.0.9:3306" {
		t.Fatalf("%+v", got)
	}
}
