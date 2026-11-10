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
