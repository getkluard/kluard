package status

import (
	"testing"
	"time"

	"github.com/getkluard/kluard/internal/model"
	"github.com/getkluard/kluard/internal/registry"
)

func pgRow(name string, v float64, t time.Time, attrs map[string]string) model.Latest {
	return model.Latest{Name: name, Value: v, T: t.Unix(), Attrs: attrs}
}

func TestPostgresGhostAndMerge(t *testing.T) {
	now := time.Now()
	old := now.Add(-3 * time.Minute)
	ancient := now.Add(-30 * time.Minute)
	rows := []model.Latest{
		// found by itself under the container name, before a login was set; it has stopped reporting
		pgRow("postgres_up", 0, old, map[string]string{"host": "h2", "postgres": "nextcloud-db"}),
		pgRow("postgres_auth_required", 1, old, map[string]string{"host": "h2", "postgres": "nextcloud-db"}),
		// the same container reporting under the name given in the UI
		pgRow("postgres_up", 1, now, map[string]string{"host": "h2", "postgres": "nextcloud"}),
		pgRow("postgres_info", 1, now, map[string]string{"host": "h2", "postgres": "nextcloud", "container": "nextcloud-db", "version": "16.15"}),
		// a found server that went away long ago
		pgRow("postgres_up", 0, ancient, map[string]string{"host": "h3", "postgres": "gone"}),
	}
	recs := []registry.Postgres{{ID: "pg1", Host: "h2", Name: "nextcloud", Container: "nextcloud-db"}}
	got := PostgresList(now, rows, recs)
	names := map[string]Postgres{}
	for _, g := range got {
		names[g.Name] = g
	}
	if _, ok := names["gone"]; ok {
		t.Fatal("a long-gone found server must drop out of the list")
	}
	if x := names["nextcloud"]; x.State != "up" || x.RecordID != "pg1" {
		t.Fatalf("%+v", x)
	}
	if _, ok := names["nextcloud-db"]; !ok {
		t.Log("old row merged away")
	}
}

func TestNginxList(t *testing.T) {
	now := time.Now()
	rows := []model.Latest{
		pgRow("nginx_up", 1, now, map[string]string{"host": "h1", "nginx": "web"}),
		pgRow("nginx_connections_active", 4, now, map[string]string{"host": "h1", "nginx": "web"}),
		pgRow("nginx_info", 1, now, map[string]string{"host": "h1", "nginx": "web", "container": "web", "version": "nginx/1.27.0"}),
		pgRow("nginx_up", 0, now, map[string]string{"host": "h1", "nginx": "bare"}),
		pgRow("nginx_status_missing", 1, now, map[string]string{"host": "h1", "nginx": "bare"}),
		pgRow("nginx_up", 0, now.Add(-time.Hour), map[string]string{"host": "h2", "nginx": "gone"}),
	}
	got := map[string]Nginx{}
	for _, x := range NginxList(now, rows, []registry.Nginx{{ID: "ng1", Host: "h1", Name: "web", Container: "web"}}) {
		got[x.Name] = x
	}
	if _, ok := got["gone"]; ok {
		t.Fatal("a long-gone nginx must drop out")
	}
	if x := got["web"]; x.State != "up" || x.RecordID != "ng1" || x.Active == nil || *x.Active != 4 || x.Version != "nginx/1.27.0" {
		t.Fatalf("%+v", x)
	}
	if x := got["bare"]; x.State != "setup" || x.Active != nil || x.Reason == "" {
		t.Fatalf("%+v", x)
	}
}

func TestWebList(t *testing.T) {
	now := time.Now()
	at := func(n string) map[string]string { return map[string]string{"host": "h1", "web": n} }
	rows := []model.Latest{
		pgRow("web_up", 1, now, at("shop")),
		pgRow("web_response_seconds", 0.25, now, at("shop")),
		pgRow("web_status_code", 200, now, at("shop")),
		pgRow("web_tls_expiry_seconds", 20*86400, now, at("shop")),
		pgRow("web_tls_valid", 1, now, at("shop")),
		pgRow("web_up", 0, now, at("blog")),
		pgRow("web_info", 1, now, map[string]string{"host": "h1", "web": "blog", "reason": "answered 500"}),
		pgRow("web_up", 1, now, at("removed")), // reported for a check that no longer exists
	}
	recs := []registry.Web{{ID: "wb1", Host: "h1", Name: "shop", URL: "https://shop.example"}, {ID: "wb2", Host: "h1", Name: "blog", URL: "https://blog.example"}, {ID: "wb3", Host: "h1", Name: "new", URL: "http://new.example"}}
	got := WebList(now, rows, recs)
	if len(got) != 3 || got[0].Name != "blog" || got[0].State != "down" || got[0].Reason != "answered 500" {
		t.Fatalf("%+v", got)
	}
	var shop, fresh Web
	for _, x := range got {
		switch x.Name {
		case "shop":
			shop = x
		case "new":
			fresh = x
		}
	}
	if shop.State != "up" || shop.Reason != "" || *shop.Seconds != 0.25 || int(*shop.CertDays) != 20 || shop.CertOK == nil || !*shop.CertOK || *shop.Code != 200 {
		t.Fatalf("%+v", shop)
	}
	if fresh.State != "pending" {
		t.Fatalf("%+v", fresh)
	}
}

func TestMysqlList(t *testing.T) {
	now := time.Now()
	at := func(n string) map[string]string { return map[string]string{"host": "h1", "mysql": n} }
	rows := []model.Latest{
		pgRow("mysql_up", 1, now, at("shop")),
		pgRow("mysql_connections", 12, now, at("shop")),
		pgRow("mysql_connections_max", 151, now, at("shop")),
		pgRow("mysql_longest_query_seconds", 5, now, at("shop")),
		pgRow("mysql_database_size_bytes", 100, now, map[string]string{"host": "h1", "mysql": "shop", "database": "a"}),
		pgRow("mysql_database_size_bytes", 50, now, map[string]string{"host": "h1", "mysql": "shop", "database": "b"}),
		pgRow("mysql_info", 1, now, map[string]string{"host": "h1", "mysql": "shop", "container": "shop", "version": "10.11.6-MariaDB", "role": "primary"}),
		pgRow("mysql_up", 0, now, at("locked")),
		pgRow("mysql_auth_required", 1, now, at("locked")),
		pgRow("mysql_up", 0, now.Add(-time.Hour), map[string]string{"host": "h2", "mysql": "gone"}),
	}
	got := map[string]Mysql{}
	for _, x := range MysqlList(now, rows, []registry.Mysql{{ID: "my1", Host: "h1", Name: "shop", Container: "shop"}}) {
		got[x.Name] = x
	}
	if _, ok := got["gone"]; ok {
		t.Fatal("a long-gone server must drop out")
	}
	if x := got["shop"]; x.State != "up" || x.RecordID != "my1" || *x.Conns != 12 || *x.Size != 150 || *x.LongestQuery != 5 || x.Version != "10.11.6-MariaDB" {
		t.Fatalf("%+v", x)
	}
	if x := got["locked"]; x.State != "auth" || x.Conns != nil || x.Reason != "needs a password" {
		t.Fatalf("%+v", x)
	}
}

func TestMinioList(t *testing.T) {
	now := time.Now()
	at := func(n string) map[string]string { return map[string]string{"host": "h1", "minio": n} }
	rows := []model.Latest{
		pgRow("minio_up", 1, now, at("store")),
		pgRow("minio_cluster_capacity_usable_free_bytes", 250, now, at("store")),
		pgRow("minio_cluster_capacity_usable_total_bytes", 1000, now, at("store")),
		pgRow("minio_cluster_drive_offline_total", 1, now, at("store")),
		pgRow("minio_cluster_health_status", 0, now, at("store")),
		pgRow("minio_up", 0, now, at("locked")),
		pgRow("minio_auth_required", 1, now, at("locked")),
		pgRow("minio_up", 0, now.Add(-time.Hour), map[string]string{"host": "h2", "minio": "gone"}),
	}
	got := map[string]Minio{}
	for _, x := range MinioList(now, rows, []registry.Minio{{ID: "mn1", Host: "h1", Name: "store", TokenEnc: "x"}}) {
		got[x.Name] = x
	}
	if _, ok := got["gone"]; ok {
		t.Fatal("a long-gone server must drop out")
	}
	if x := got["store"]; x.State != "up" || x.RecordID != "mn1" || !x.HasToken || *x.Free != 250 || *x.DrivesOffl != 1 || x.Healthy == nil || *x.Healthy {
		t.Fatalf("%+v", x)
	}
	if x := got["locked"]; x.State != "auth" || x.Free != nil || x.Reason != "needs a token for its metrics" {
		t.Fatalf("%+v", x)
	}
}
