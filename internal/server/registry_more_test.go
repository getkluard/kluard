package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// The settings of MySQL, nginx and website checks: who may change them, what the browser gets back, what another tenant sees.
func TestMysqlNginxWebSettingsPermissionsAndSecrets(t *testing.T) {
	ts, a, _ := regServer(t)
	ad, rd, ot := client(), client(), client()
	login(t, ad, ts, "admin", "admins-long-password")
	login(t, rd, ts, "reader", "readers-long-password")
	login(t, ot, ts, "other", "others-long-password")

	for path, body := range map[string]string{
		"/api/v1/mysql": `{"host":"web1","container":"db","password":"x"}`,
		"/api/v1/nginx": `{"host":"web1","container":"web"}`,
		"/api/v1/web":   `{"host":"web1","url":"https://a.example/"}`,
	} {
		if code(rd, "POST", ts.URL+path, body) != 403 {
			t.Errorf("%s: a read-only user must not change settings", path)
		}
		if code(rd, "GET", ts.URL+path, "") != 200 {
			t.Errorf("%s: a read-only user may look", path)
		}
		r, b := do(ad, "POST", ts.URL+path, body)
		var out map[string]any
		json.Unmarshal(b, &out)
		id, _ := out["id"].(string)
		if r.StatusCode != 200 || id == "" {
			t.Fatalf("%s create: %d %s", path, r.StatusCode, b)
		}
		if strings.Contains(string(b), `"password":"x"`) || strings.Contains(string(b), "password_enc") {
			t.Errorf("%s: the password came back: %s", path, b)
		}
		if code(ot, "PUT", ts.URL+path+"/"+id, body) != 404 || code(ot, "DELETE", ts.URL+path+"/"+id, "") != 404 {
			t.Errorf("%s: another tenant reached it", path)
		}
		if _, lb := do(ot, "GET", ts.URL+path, ""); strings.Contains(string(lb), id) {
			t.Errorf("%s: another tenant sees it", path)
		}
		if code(ad, "DELETE", ts.URL+path+"/"+id, "") != 200 {
			t.Errorf("%s: remove", path)
		}
	}
	// the login reaches only the agent of that machine
	do(ad, "POST", ts.URL+"/api/v1/mysql", `{"host":"web1","container":"db","password":"MYSQL-SECRET"}`)
	plain, _, _ := a.Store.CreateKey("acme", "web1")
	if c, body := get2(t, ts.URL, plain, "/api/v1/agent/config?host=web1"); c != 200 || !strings.Contains(body, "MYSQL-SECRET") {
		t.Fatalf("agent config: %d %s", c, body)
	}
	if _, body := get2(t, ts.URL, plain, "/api/v1/agent/config?host=web2"); strings.Contains(body, "MYSQL-SECRET") {
		t.Fatal("another machine got the login")
	}
}
