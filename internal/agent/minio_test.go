package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

const minioText = `# HELP minio_cluster_capacity_usable_free_bytes Total free usable capacity online in the cluster
# TYPE minio_cluster_capacity_usable_free_bytes gauge
minio_cluster_capacity_usable_free_bytes{server="127.0.0.1:9000"} 250
# TYPE minio_cluster_capacity_usable_total_bytes gauge
minio_cluster_capacity_usable_total_bytes{server="127.0.0.1:9000"} 1000
# TYPE minio_cluster_drive_offline_total gauge
minio_cluster_drive_offline_total{server="127.0.0.1:9000"} 1
# TYPE minio_cluster_health_status gauge
minio_cluster_health_status{server="127.0.0.1:9000"} 1
# TYPE minio_s3_requests_total counter
minio_s3_requests_total{api="GetObject",server="127.0.0.1:9000"} 42
# TYPE minio_s3_requests_ttfb_seconds_distribution gauge
minio_s3_requests_ttfb_seconds_distribution{api="GetObject",le="0.1",server="127.0.0.1:9000"} 7
# TYPE go_goroutines gauge
go_goroutines 33
`

func minioServer(token string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/minio/v2/metrics/cluster" {
			http.NotFound(w, r)
			return
		}
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "forbidden", 403)
			return
		}
		w.Write([]byte(minioText))
	}))
}

func TestMinioCheckKeepsOnlyTheUsefulMetrics(t *testing.T) {
	s := minioServer("")
	defer s.Close()
	pts, err := MinioCheck(context.Background(), s.URL, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Point{}
	for _, p := range pts {
		got[p.Name] = p
	}
	if len(got) != 5 || got["minio_cluster_capacity_usable_free_bytes"].Value != 250 || got["minio_cluster_drive_offline_total"].Type != "gauge" ||
		got["minio_s3_requests_total"].Type != "sum" || got["minio_s3_requests_total"].Attrs["api"] != "GetObject" {
		t.Fatalf("%+v", got)
	}
	if _, ok := got["go_goroutines"]; ok {
		t.Fatal("other metrics must not be kept")
	}
	if _, ok := got["minio_s3_requests_ttfb_seconds_distribution"]; ok {
		t.Fatal("distributions must not be kept")
	}
}

func TestMinioAuthAndCollect(t *testing.T) {
	s := minioServer("sekret")
	defer s.Close()
	if _, err := MinioCheck(context.Background(), s.URL, "", 1); !errors.Is(err, errMinioAuth) {
		t.Fatalf("%v", err)
	}
	if _, err := MinioCheck(context.Background(), s.URL, "wrong", 1); !errors.Is(err, errMinioAuth) {
		t.Fatalf("%v", err)
	}
	pts, _ := CollectMinio(context.Background(), "/nonexistent.sock", []MinioTarget{
		{Name: "ok", URL: s.URL, Token: "sekret"}, {Name: "locked", URL: s.URL}, {Name: "gone", URL: "http://127.0.0.1:1"}}, 1)
	up, auth, reason, util := map[string]float64{}, map[string]float64{}, map[string]string{}, map[string]float64{}
	for _, p := range pts {
		n := p.Attrs["minio"]
		switch p.Name {
		case "minio_up":
			up[n] = p.Value
		case "minio_auth_required":
			auth[n] = p.Value
		case "minio_info":
			reason[n] = p.Attrs["reason"]
		case "minio_cluster_capacity_utilization":
			util[n] = p.Value
		}
		if _, has := p.Attrs["server"]; has {
			t.Fatal("the server label must be dropped")
		}
	}
	if up["ok"] != 1 || up["locked"] != 0 || up["gone"] != 0 || auth["locked"] != 1 || auth["gone"] != 0 || reason["locked"] != "needs a token for its metrics" || util["ok"] != 0.75 {
		t.Fatalf("%v %v %v %v", up, auth, reason, util)
	}
	if _, ok := util["locked"]; ok {
		t.Fatal("no numbers for a MinIO that did not answer")
	}
}

func TestMinioImageAndDiscovery(t *testing.T) {
	for img, want := range map[string]bool{"minio/minio:RELEASE.2024": true, "quay.io/minio/minio": true, "bitnami/minio": true, "minio": true,
		"minio/mc": false, "minio/operator": false, "prom/minio-exporter": false, "redis": false} {
		if got := minioImage(img); got != want {
			t.Errorf("%s: %v", img, got)
		}
	}
	var a, b dockerRedisContainer
	a.Names, a.Image, a.State = []string{"/minio"}, "minio/minio", "running"
	a.NetworkSettings.Networks = map[string]struct{ IPAddress string }{"bridge": {"172.17.0.4"}}
	b.Names, b.Image, b.State, b.Labels = []string{"/skip"}, "minio/minio", "running", map[string]string{"kluard.minio": "false"}
	got := minioFromContainers([]dockerRedisContainer{a, b})
	if len(got) != 1 || got[0].Bases[0] != "http://172.17.0.4:9000" {
		t.Fatalf("%+v", got)
	}
}
