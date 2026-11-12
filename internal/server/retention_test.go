package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/getkluard/kluard/internal/auth"
	"github.com/getkluard/kluard/internal/dashboards"
	"github.com/getkluard/kluard/internal/docstore"
	"github.com/getkluard/kluard/internal/retention"
)

type fakeTTL struct {
	days int
	fail bool
}

func (f *fakeTTL) ApplyRetention(_ context.Context, days int) error {
	if f.fail {
		return errors.New("clickhouse is down")
	}
	f.days = days
	return nil
}

func TestRetentionIsChosenInSettings(t *testing.T) {
	f, _ := docstore.OpenFile(t.TempDir())
	st, _ := auth.Open(f, 0)
	st.CreateUser("admin", "acme", "admins-long-password")
	st.CreateUserIn("reader", "acme", "readers-long-password", "user")
	st.CreateUser("globexadmin", "globex", "globex-long-password")
	a := auth.New(st, nil, false)
	days := retention.New(30, 365)
	ch := &fakeTTL{days: 30}
	srv := New(&fakeStore{}, a, slog.New(slog.NewTextHandler(io.Discard, nil))).WithAuth(a).WithDashboards(dashboards.New(f)).WithOwnerTenant("acme").
		WithRetention(&Retention{DB: f, Days: days, CH: ch})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	ad, rd, gx := client(), client(), client()
	login(t, ad, ts, "admin", "admins-long-password")
	login(t, rd, ts, "reader", "readers-long-password")
	login(t, gx, ts, "globexadmin", "globex-long-password")
	read := func() (d, k int, src string) {
		_, b := do(ad, "GET", ts.URL+"/api/v1/retention", "")
		var o struct {
			D   int    `json:"data_days"`
			K   int    `json:"backup_days"`
			Src string `json:"source"`
		}
		json.Unmarshal(b, &o)
		return o.D, o.K, o.Src
	}
	if d, k, src := read(); d != 30 || k != 365 || src != "default" {
		t.Fatalf("to begin with, the environment: %d %d %s", d, k, src)
	}
	if c := code(rd, "GET", ts.URL+"/api/v1/retention", ""); c != 403 {
		t.Fatalf("without the permission: %d", c)
	}
	if c := code(gx, "PUT", ts.URL+"/api/v1/retention", `{"data_days":7,"backup_days":30}`); c != 403 {
		t.Fatalf("another tenant must not change what the installation keeps: %d", c)
	}
	for _, bad := range []string{`{"data_days":0,"backup_days":30}`, `{"data_days":99999,"backup_days":30}`, `{"data_days":30,"backup_days":-1}`} {
		if c := code(ad, "PUT", ts.URL+"/api/v1/retention", bad); c != 400 {
			t.Fatalf("%s: %d", bad, c)
		}
	}
	if c := code(ad, "PUT", ts.URL+"/api/v1/retention", `{"data_days":14,"backup_days":0}`); c != 200 {
		t.Fatalf("choosing: %d", c)
	}
	if d, k, src := read(); d != 14 || k != 0 || src != "chosen" || ch.days != 14 {
		t.Fatalf("chosen: %d %d %s, clickhouse %d", d, k, src, ch.days)
	}
	if d, k, ok := retention.Load(context.Background(), f); !ok || d != 14 || k != 0 {
		t.Fatalf("it must survive a restart: %d %d %v", d, k, ok)
	}
	if g, kp := days.Get(); g != 14 || kp != 0 {
		t.Fatalf("the live value: %d %d", g, kp)
	}
	// ClickHouse refusing leaves everything as it was
	ch.fail = true
	if c := code(ad, "PUT", ts.URL+"/api/v1/retention", `{"data_days":3,"backup_days":10}`); c != 502 {
		t.Fatalf("clickhouse down: %d", c)
	}
	ch.fail = false
	if d, k, _ := read(); d != 14 || k != 0 {
		t.Fatalf("unchanged after a failure: %d %d", d, k)
	}
	if c := code(ad, "DELETE", ts.URL+"/api/v1/retention", ""); c != 200 {
		t.Fatalf("back to the default: %d", c)
	}
	if d, k, src := read(); d != 30 || k != 365 || src != "default" || ch.days != 30 {
		t.Fatalf("default again: %d %d %s, clickhouse %d", d, k, src, ch.days)
	}
	if _, _, ok := retention.Load(context.Background(), f); ok {
		t.Fatal("the choice must be forgotten")
	}
}
