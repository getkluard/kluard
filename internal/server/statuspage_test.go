package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/getkluard/kluard/internal/model"
)

func TestStatusPagePublicAndPrivate(t *testing.T) {
	ts, _, fs := regServer(t)
	ad, rd, ot, anon := client(), client(), client(), client()
	login(t, ad, ts, "admin", "admins-long-password")
	login(t, rd, ts, "reader", "readers-long-password")
	login(t, ot, ts, "other", "others-long-password")

	_, b := do(ad, "POST", ts.URL+"/api/v1/web", `{"host":"web1","url":"https://secret-internal.example.com/health"}`)
	var web map[string]any
	json.Unmarshal(b, &web)
	wid, _ := web["id"].(string)
	if wid == "" {
		t.Fatalf("web: %s", b)
	}
	if code(rd, "POST", ts.URL+"/api/v1/statuspages", `{"title":"x","items":[{"kind":"web","ref":"`+wid+`","label":"Site"}]}`) != 403 {
		t.Fatal("a read-only user must not create pages")
	}
	if code(ot, "POST", ts.URL+"/api/v1/statuspages", `{"title":"x","items":[{"kind":"web","ref":"`+wid+`","label":"Site"}]}`) != 400 {
		t.Fatal("another tenant's website on a page")
	}
	_, b = do(ad, "POST", ts.URL+"/api/v1/statuspages", `{"title":"Acme cloud","message":"Ask support","items":[{"kind":"web","ref":"`+wid+`","label":"Our shop"}]}`)
	var pg map[string]any
	json.Unmarshal(b, &pg)
	id, _ := pg["id"].(string)
	if !strings.HasPrefix(id, "sp") || pg["path"] != "/status/"+id {
		t.Fatalf("create: %s", b)
	}

	fs.latestFor = "acme"
	fs.latest = []model.Latest{{Name: "web_up", Attrs: map[string]string{"web": "secret-internal.example.com", "host": "web1"}, Value: 1, T: time.Now().Unix() - 3}}
	resp, b := do(anon, "GET", ts.URL+"/status/"+id, "")
	if resp.StatusCode != 200 || !strings.Contains(string(b), "Acme cloud") || !strings.Contains(string(b), "Operational") || !strings.Contains(string(b), "Everything works") {
		t.Fatalf("page: %d %s", resp.StatusCode, b)
	}
	if resp.Header.Get("X-Robots-Tag") == "" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatal("headers")
	}
	resp, b = do(anon, "GET", ts.URL+"/status/"+id+"/json", "")
	if resp.StatusCode != 200 {
		t.Fatalf("json: %d %s", resp.StatusCode, b)
	}
	for _, leak := range []string{"secret-internal", "web1", "https://", wid} {
		if strings.Contains(string(b), leak) {
			t.Fatalf("the public page leaks %q: %s", leak, b)
		}
	}
	if !strings.Contains(string(b), `"Our shop"`) || !strings.Contains(string(b), `"overall":"operational"`) {
		t.Fatalf("json: %s", b)
	}
	_, html := do(anon, "GET", ts.URL+"/status/"+id, "")
	for _, leak := range []string{"secret-internal", "web1", wid} {
		if strings.Contains(string(html), leak) {
			t.Fatalf("html leaks %q", leak)
		}
	}
	if c := code(anon, "GET", ts.URL+"/status/sp000000000000000000000000", ""); c != 404 {
		t.Fatalf("unknown page: %d", c)
	}
	if c := code(anon, "GET", ts.URL+"/status/nonsense", ""); c != 404 {
		t.Fatalf("bad id: %d", c)
	}
	if c := code(ot, "DELETE", ts.URL+"/api/v1/statuspages/"+id, ""); c != 404 {
		t.Fatalf("another tenant removed it: %d", c)
	}
	_, b = do(ot, "GET", ts.URL+"/api/v1/statuspages", "")
	if strings.Contains(string(b), id) {
		t.Fatalf("another tenant sees it: %s", b)
	}
	if code(ad, "DELETE", ts.URL+"/api/v1/statuspages/"+id, "") != 200 || code(anon, "GET", ts.URL+"/status/"+id, "") != 404 {
		t.Fatal("remove")
	}
}
