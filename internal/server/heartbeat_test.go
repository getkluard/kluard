package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/getkluard/kluard/internal/registry"
)

func TestHeartbeatsPingWithoutLoginAndStayInTheirTenant(t *testing.T) {
	ts, _, _ := regServer(t)
	ad, rd, ot, anon := client(), client(), client(), client()
	login(t, ad, ts, "admin", "admins-long-password")
	login(t, rd, ts, "reader", "readers-long-password")
	login(t, ot, ts, "other", "others-long-password")

	if code(rd, "POST", ts.URL+"/api/v1/heartbeats", `{"name":"backup","period":86400}`) != 403 {
		t.Fatal("a read-only user must not create heartbeats")
	}
	if code(ad, "POST", ts.URL+"/api/v1/heartbeats", `{"name":"x","period":5}`) != 400 {
		t.Fatal("an interval under a minute is refused")
	}
	_, b := do(ad, "POST", ts.URL+"/api/v1/heartbeats", `{"name":"backup","period":86400}`)
	var out map[string]any
	json.Unmarshal(b, &out)
	id, _ := out["id"].(string)
	if !registry.ValidHeartbeatID(id) || out["state"] != "new" || out["path"] != "/hb/"+id || out["grace"] != float64(8640) {
		t.Fatalf("create: %s", b)
	}
	if code(ad, "POST", ts.URL+"/api/v1/heartbeats", `{"name":"BACKUP","period":3600}`) != 400 {
		t.Fatal("the same name twice")
	}
	// the job calls without any login
	if c := code(anon, "GET", ts.URL+"/hb/"+id, ""); c != 200 {
		t.Fatalf("ping: %d", c)
	}
	if c := code(anon, "POST", ts.URL+"/hb/"+id+"/fail", ""); c != 200 {
		t.Fatalf("fail: %d", c)
	}
	if code(anon, "GET", ts.URL+"/hb/hb000000000000000000000000", "") != 404 || code(anon, "GET", ts.URL+"/hb/nonsense", "") != 404 {
		t.Fatal("an unknown address must give 404")
	}
	_, b = do(rd, "GET", ts.URL+"/api/v1/heartbeats", "")
	if !strings.Contains(string(b), `"state":"failed"`) || !strings.Contains(string(b), `"pings":1`) {
		t.Fatalf("list: %s", b)
	}
	_, b = do(ot, "GET", ts.URL+"/api/v1/heartbeats", "")
	if strings.Contains(string(b), id) {
		t.Fatalf("another tenant sees it: %s", b)
	}
	if code(ot, "DELETE", ts.URL+"/api/v1/heartbeats/"+id, "") != 404 {
		t.Fatal("another tenant removed it")
	}
	if code(ad, "DELETE", ts.URL+"/api/v1/heartbeats/"+id, "") != 200 || code(anon, "GET", ts.URL+"/hb/"+id, "") != 404 {
		t.Fatal("remove")
	}
}

func TestHeartbeatStatesAndMetrics(t *testing.T) {
	now := time.Now()
	h := registry.Heartbeat{Name: "backup", Tenant: "acme", Period: 3600, Grace: 600, Created: now.Add(-30 * time.Minute)}
	if h.State(now) != "new" {
		t.Fatal("waiting for the first call")
	}
	h.Created = now.Add(-2 * time.Hour)
	if h.State(now) != "late" {
		t.Fatal("never called and overdue")
	}
	h.LastPing = now.Add(-30 * time.Minute).Unix()
	if h.State(now) != "up" {
		t.Fatal("called in time")
	}
	h.LastPing = now.Add(-80 * time.Minute).Unix()
	if h.State(now) != "late" {
		t.Fatal("called too long ago")
	}
	h.LastPing = now.Add(-10 * time.Minute).Unix()
	h.LastFail = now.Add(-5 * time.Minute).Unix()
	if h.State(now) != "failed" {
		t.Fatal("failed after the last good call")
	}
	h.LastPing = now.Unix()
	if h.State(now) != "up" {
		t.Fatal("a good call after a failure clears it")
	}
	h.LastFail = 0
	h.LastPing = now.Add(-80 * time.Minute).Unix()
	rows := heartbeatPoints([]registry.Heartbeat{h}, now)
	if len(rows) != 2 || rows[0].Name != "heartbeat_up" || rows[0].Value != 0 || rows[0].Tenant != "acme" || rows[0].Attrs["heartbeat"] != "backup" || rows[1].Value < 4799 {
		t.Fatalf("%+v", rows)
	}
}
