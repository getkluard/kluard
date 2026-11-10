package store

import (
	"strings"
	"testing"
	"time"

	"github.com/getkluard/kluard/internal/model"
)

func TestPromName(t *testing.T) {
	for in, want := range map[string]string{"system.cpu.load": "system_cpu_load", "nextcloud_users": "nextcloud_users", "9lives": "_9lives", "a-b c": "a_b_c", "host.name": "host_name"} {
		if got := PromName(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestSamplesQueryIsScopedAndParameterised(t *testing.T) {
	sql, p, err := buildSamplesQuery("acme", model.SampleQuery{Name: "system_cpu_load", Equal: map[string]string{"host": "a'; DROP TABLE x", "service": "svc"}, From: time.Unix(1, 0), To: time.Unix(100, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "tenant = {tenant:String}") || p["tenant"] != "acme" {
		t.Fatalf("the query must be tenant scoped: %s %v", sql, p)
	}
	if strings.Contains(sql, "DROP") {
		t.Fatalf("values must travel as parameters: %s", sql)
	}
	if _, _, err := buildSamplesQuery("acme", model.SampleQuery{Name: "x", Equal: map[string]string{"bad key!": "v"}}); err == nil {
		t.Fatal("an invalid label name must be refused")
	}
	if _, _, err := buildSamplesQuery("acme", model.SampleQuery{}); err == nil {
		t.Fatal("a name is required")
	}
}
