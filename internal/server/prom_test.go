package server

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/getkluard/kluard/internal/model"
)

func promFixture(t *testing.T) (base string, readKey, agentKey string, fs *fakeStore) {
	ts, fs, _ := authServer(t)
	c := client()
	login(t, c, ts, "alice", "alices-long-password")
	mk := func(body string) string {
		var k struct{ Key string }
		json.NewDecoder(post(c, ts.URL+"/api/v1/keys", body).Body).Decode(&k)
		return k.Key
	}
	readKey, agentKey = mk(`{"name":"grafana","kind":"read"}`), mk(`{"name":"vm1"}`)
	now := time.Now().UnixMilli()
	var cpu, mem []model.Sample
	for i := int64(0); i < 20; i++ {
		ts := now - (20-i)*15000
		cpu = append(cpu, model.Sample{T: ts, Name: "system.cpu.load", Service: "host", Attrs: map[string]string{"host": "a"}, V: float64(i)})
		cpu = append(cpu, model.Sample{T: ts, Name: "system.cpu.load", Service: "host", Attrs: map[string]string{"host": "b"}, V: 100})
		mem = append(mem, model.Sample{T: ts, Name: "system.mem", Service: "host", Attrs: map[string]string{"host": "a"}, V: 7})
	}
	fs.samples = map[string][]model.Sample{"system_cpu_load": cpu, "system_mem": mem}
	return ts.URL, readKey, agentKey, fs
}

func promGet(url, key string) (int, map[string]any) {
	resp := get(http.DefaultClient, url, "Authorization", "Bearer "+key)
	var out map[string]any
	b, _ := io.ReadAll(resp.Body)
	json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func TestReadKeyIsReadOnly(t *testing.T) {
	base, rk, ak, _ := promFixture(t)
	if !strings.HasPrefix(rk, "klr_") {
		t.Fatalf("read keys start with klr_: %q", rk)
	}
	if r := post(http.DefaultClient, base+"/v1/traces", tracesJSON, "Authorization", "Bearer "+rk); r.StatusCode != 403 {
		t.Fatalf("a read key must not ingest, got %d", r.StatusCode)
	}
	if r := post(http.DefaultClient, base+"/api/v1/login", `{"api_key":"`+rk+`"}`); r.StatusCode == 200 {
		t.Fatal("a read key must not open a browser session")
	}
	for _, p := range []string{"/api/v1/keys", "/api/v1/dashboards", "/api/v1/traces", "/api/v1/logs"} {
		if r := get(http.DefaultClient, base+p, "Authorization", "Bearer "+rk); r.StatusCode == 200 {
			t.Fatalf("a read key must not reach %s", p)
		}
	}
	// the key is accepted for the Prometheus API, as bearer and as basic auth password
	if code, _ := promGet(base+"/prom/api/v1/query?query=1%2B1", rk); code != 200 {
		t.Fatalf("read key on the prom API: %d", code)
	}
	basic := base64.StdEncoding.EncodeToString([]byte("grafana:" + rk))
	if r := get(http.DefaultClient, base+"/prom/api/v1/query?query=1%2B1", "Authorization", "Basic "+basic); r.StatusCode != 200 {
		t.Fatalf("basic auth: %d", r.StatusCode)
	}
	// an agent key is not meant for reading, but is allowed: it already reads metrics
	if code, _ := promGet(base+"/prom/api/v1/query?query=1%2B1", ak); code != 200 {
		t.Fatalf("agent key: %d", code)
	}
	if code, _ := promGet(base+"/prom/api/v1/query?query=1%2B1", "klr_nonsense"); code != 401 {
		t.Fatalf("unknown key: %d", code)
	}
	if r := get(http.DefaultClient, base+"/prom/api/v1/query?query=1%2B1"); r.StatusCode != 401 {
		t.Fatalf("no key: %d", r.StatusCode)
	}
}

func TestPromQueries(t *testing.T) {
	base, rk, _, fs := promFixture(t)
	code, out := promGet(base+"/prom/api/v1/query?query=sum(system_cpu_load)", rk)
	if code != 200 || out["status"] != "success" || fs.tenant != "acme" {
		t.Fatalf("%d %+v tenant=%q", code, out, fs.tenant)
	}
	res := out["data"].(map[string]any)
	if res["resultType"] != "vector" || len(res["result"].([]any)) != 1 {
		t.Fatalf("%+v", res)
	}
	v := res["result"].([]any)[0].(map[string]any)["value"].([]any)[1].(string)
	if v != "119" { // 19 + 100
		t.Fatalf("sum = %s", v)
	}
	// label matcher
	_, out = promGet(base+`/prom/api/v1/query?query=system_cpu_load{host="a"}`, rk)
	if r := out["data"].(map[string]any)["result"].([]any); len(r) != 1 || r[0].(map[string]any)["metric"].(map[string]any)["host"] != "a" {
		t.Fatalf("%+v", out)
	}
	// range query
	now := time.Now()
	u := base + "/prom/api/v1/query_range?query=system_mem*2&step=15&start=" + itoa(now.Add(-4*time.Minute).Unix()) + "&end=" + itoa(now.Unix())
	code, out = promGet(u, rk)
	if code != 200 {
		t.Fatalf("%d %+v", code, out)
	}
	m := out["data"].(map[string]any)
	if m["resultType"] != "matrix" || len(m["result"].([]any)) != 1 {
		t.Fatalf("%+v", m)
	}
	vals := m["result"].([]any)[0].(map[string]any)["values"].([]any)
	if len(vals) < 10 || vals[len(vals)-1].([]any)[1] != "14" {
		t.Fatalf("%+v", vals)
	}
	// errors
	if code, out = promGet(base+"/prom/api/v1/query?query=sum(", rk); code != 400 || out["errorType"] != "bad_data" {
		t.Fatalf("bad query: %d %+v", code, out)
	}
	if code, _ = promGet(base+"/prom/api/v1/query_range?query=up&step=15", rk); code != 400 {
		t.Fatalf("missing start/end: %d", code)
	}
	if code, _ = promGet(base+"/prom/api/v1/query_range?query=up&step=1&start=0&end=9999999999", rk); code != 400 {
		t.Fatalf("too wide: %d", code)
	}
	// series
	code, out = promGet(base+`/prom/api/v1/series?match[]=system_cpu_load`, rk)
	if code != 200 || len(out["data"].([]any)) != 2 {
		t.Fatalf("series: %d %+v", code, out)
	}
	if code, _ = promGet(base+`/prom/api/v1/series`, rk); code != 400 {
		t.Fatalf("series without match: %d", code)
	}
	// label values for a selector
	_, out = promGet(base+`/prom/api/v1/label/host/values?match[]=system_cpu_load`, rk)
	if d := out["data"].([]any); len(d) != 2 || d[0] != "a" {
		t.Fatalf("label values: %+v", out)
	}
	_, out = promGet(base+`/prom/api/v1/labels?match[]=system_cpu_load`, rk)
	if d := out["data"].([]any); len(d) < 3 {
		t.Fatalf("labels: %+v", out)
	}
	if code, out = promGet(base+`/prom/api/v1/status/buildinfo`, rk); code != 200 {
		t.Fatalf("buildinfo: %d", code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
