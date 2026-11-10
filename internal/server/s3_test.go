package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestS3SettingsPermissionsAndSecrets(t *testing.T) {
	ts, a, _ := regServer(t)
	ad, rd, ot := client(), client(), client()
	login(t, ad, ts, "admin", "admins-long-password")
	login(t, rd, ts, "reader", "readers-long-password")
	login(t, ot, ts, "other", "others-long-password")
	body := `{"host":"web1","url":"https://s3.example.com","access_key":"AKIAEXAMPLE","secret_key":"S3-SECRET-VALUE","buckets":["photos"],"size":true}`

	if code(rd, "POST", ts.URL+"/api/v1/s3", body) != 403 {
		t.Fatal("a read-only user must not change settings")
	}
	if code(rd, "GET", ts.URL+"/api/v1/s3", "") != 200 {
		t.Fatal("a read-only user may look")
	}
	r, b := do(ad, "POST", ts.URL+"/api/v1/s3", body)
	var out map[string]any
	json.Unmarshal(b, &out)
	id, _ := out["id"].(string)
	if r.StatusCode != 200 || id == "" || out["has_secret"] != true {
		t.Fatalf("create: %d %s", r.StatusCode, b)
	}
	for _, where := range []string{"create", "list", "update"} {
		switch where {
		case "list":
			_, b = do(rd, "GET", ts.URL+"/api/v1/s3", "")
		case "update":
			_, b = do(ad, "PUT", ts.URL+"/api/v1/s3/"+id, `{"host":"web1","url":"https://s3.example.com","access_key":"AKIAEXAMPLE","size":false}`)
		}
		if strings.Contains(string(b), "S3-SECRET-VALUE") || strings.Contains(string(b), "secret_enc") || strings.Contains(string(b), "secret_key") {
			t.Fatalf("%s: the secret came back: %s", where, b)
		}
	}
	if code(ot, "PUT", ts.URL+"/api/v1/s3/"+id, body) != 404 || code(ot, "DELETE", ts.URL+"/api/v1/s3/"+id, "") != 404 {
		t.Fatal("another tenant reached it")
	}
	if _, lb := do(ot, "GET", ts.URL+"/api/v1/s3", ""); strings.Contains(string(lb), id) {
		t.Fatal("another tenant sees it")
	}
	// the secret reaches only the agent of that machine, and an update without a secret keeps it
	plain, _, _ := a.Store.CreateKey("acme", "web1")
	if c, cfg := get2(t, ts.URL, plain, "/api/v1/agent/config?host=web1"); c != 200 || !strings.Contains(cfg, "S3-SECRET-VALUE") {
		t.Fatalf("agent config: %d %s", c, cfg)
	}
	if _, cfg := get2(t, ts.URL, plain, "/api/v1/agent/config?host=web2"); strings.Contains(cfg, "S3-SECRET-VALUE") {
		t.Fatal("another machine got the secret")
	}
	if code(ad, "DELETE", ts.URL+"/api/v1/s3/"+id, "") != 200 {
		t.Fatal("remove")
	}
}
