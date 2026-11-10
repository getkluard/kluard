package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const stubOK = "Active connections: 3 \nserver accepts handled requests\n 120 118 450 \nReading: 1 Writing: 2 Waiting: 0 \n"

func TestParseStubStatus(t *testing.T) {
	st, err := ParseStubStatus(stubOK)
	if err != nil || st.Active != 3 || st.Accepts != 120 || st.Handled != 118 || st.Reqs != 450 || st.Reading != 1 || st.Writing != 2 || st.Waiting != 0 {
		t.Fatalf("%+v %v", st, err)
	}
	for _, bad := range []string{"", "<html>404</html>", "Active connections: x\nserver accepts handled requests\n 1 1 1\nReading: 0 Writing: 0 Waiting: 0", "Active connections: 1\nserver accepts handled requests\n 1 1\nReading: 0 Writing: 0 Waiting: 0", "Active connections: 1\nserver accepts handled requests\n 1 1 1\nfoo"} {
		if _, err := ParseStubStatus(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestNginxImage(t *testing.T) {
	for img, want := range map[string]bool{"nginx:alpine": true, "docker.io/library/nginx": true, "openresty/openresty:1.25": true, "nginxinc/nginx-unprivileged": true,
		"nginx/nginx-prometheus-exporter": false, "jc21/nginx-proxy-manager": false, "registry.k8s.io/ingress-nginx/controller": false, "redis": false} {
		if got := nginxImage(img); got != want {
			t.Errorf("%s: %v", img, got)
		}
	}
}

func nginxServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/nginx_status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Server", "nginx/1.27.0")
		w.Write([]byte(stubOK))
	}))
}

func TestNginxCheckAndCollect(t *testing.T) {
	srv := nginxServer()
	defer srv.Close()
	st, err := NginxCheck(context.Background(), srv.URL+"/nginx_status")
	if err != nil || st.Version != "nginx/1.27.0" || st.Reqs != 450 {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := NginxCheck(context.Background(), srv.URL+"/other"); err != errNoStatusPage {
		t.Fatalf("%v", err)
	}
	pts, _ := CollectNginx(context.Background(), "/nonexistent.sock", []NginxTarget{{Name: "web", URL: srv.URL + "/nginx_status"}, {Name: "bad", URL: srv.URL + "/missing"}}, 1)
	got := map[string]float64{}
	for _, p := range pts {
		if p.Attrs["nginx"] == "web" {
			got[p.Name] = p.Value
		}
		if p.Attrs["nginx"] == "bad" && p.Name == "nginx_status_missing" && p.Value != 1 {
			t.Error("a missing status page must be flagged")
		}
		if p.Attrs["nginx"] == "bad" && p.Name == "nginx_up" && p.Value != 0 {
			t.Error("nginx_up must be 0")
		}
	}
	if got["nginx_up"] != 1 || got["nginx_http_requests_total"] != 450 || got["nginx_connections_active"] != 3 || got["nginx_connections_waiting"] != 0 {
		t.Fatalf("%v", got)
	}
}

func TestNginxFindProbesPaths(t *testing.T) {
	srv := nginxServer()
	defer srv.Close()
	u, p, err := nginxFind(context.Background(), DiscoveredNginx{Container: "probe-test", Bases: []string{"http://127.0.0.1:1", srv.URL}})
	if err != nil || !strings.HasSuffix(u, "/nginx_status") || p.Active != 3 {
		t.Fatalf("%s %+v %v", u, p, err)
	}
	if _, _, err := nginxFind(context.Background(), DiscoveredNginx{Container: "probe-none", Bases: []string{srv.URL}, Path: "/nope"}); err != errNoStatusPage {
		t.Fatalf("%v", err)
	}
}

func TestNginxFromContainers(t *testing.T) {
	var a, b, c dockerRedisContainer
	a.Names, a.Image, a.State = []string{"/web"}, "nginx:alpine", "running"
	a.NetworkSettings.Networks = map[string]struct{ IPAddress string }{"bridge": {"172.17.0.5"}}
	a.Labels = map[string]string{"kluard.nginx.status": "/stub_status"}
	b.Names, b.Image, b.State, b.Labels = []string{"/skip"}, "nginx", "running", map[string]string{"kluard.nginx": "false"}
	c.Names, c.Image, c.State = []string{"/cache"}, "redis", "running"
	got := nginxFromContainers([]dockerRedisContainer{a, b, c})
	if len(got) != 1 || got[0].Container != "web" || got[0].Bases[0] != "http://172.17.0.5:80" || got[0].Path != "/stub_status" {
		t.Fatalf("%+v", got)
	}
}
