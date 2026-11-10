package registry

import (
	"strings"
	"testing"
)

func TestPostgresSettings(t *testing.T) {
	s, _ := newReg(t)
	r, err := s.CreatePostgres("acme", PostgresIn{Container: "db", Host: "web1", Username: "kluard", Password: "DB-SECRET", Database: "app", SSLMode: "require"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Name != "db" || !strings.HasPrefix(r.ID, "pg") || r.Out().HasPassword != true || r.Out().SSLMode != "require" {
		t.Fatalf("%+v", r)
	}
	if r.PasswordEnc == "" || strings.Contains(r.PasswordEnc, "DB-SECRET") {
		t.Fatal("the password must be stored encrypted")
	}
	// only the agent of that machine receives the login, decrypted
	cfg, err := s.AgentConfig("acme", "web1")
	if err != nil || len(cfg.Postgres) != 1 || cfg.Postgres[0].Password != "DB-SECRET" || cfg.Postgres[0].Database != "app" || cfg.Postgres[0].SSLMode != "require" {
		t.Fatalf("%+v %v", cfg.Postgres, err)
	}
	if other, _ := s.AgentConfig("acme", "web2"); len(other.Postgres) != 0 {
		t.Fatal("another machine must not get the login")
	}
	// another tenant sees nothing and cannot change it
	if len(s.ListPostgres("globex")) != 0 {
		t.Fatal("tenant leak")
	}
	if _, err := s.UpdatePostgres("globex", r.ID, PostgresIn{Name: "x", Host: "web1", Addr: "10.0.0.1:5432"}); err == nil {
		t.Fatal("another tenant changed it")
	}
	// an update that gives no password keeps the old one; a new one replaces it; clear removes it
	if _, err := s.UpdatePostgres("acme", r.ID, PostgresIn{Name: "db", Host: "web1", Container: "db", Username: "kluard"}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := s.AgentConfig("acme", "web1"); cfg.Postgres[0].Password != "DB-SECRET" {
		t.Fatal("the password was lost on an update")
	}
	s.UpdatePostgres("acme", r.ID, PostgresIn{Name: "db", Host: "web1", Container: "db", Password: "NEW"})
	if cfg, _ := s.AgentConfig("acme", "web1"); cfg.Postgres[0].Password != "NEW" {
		t.Fatal("the password was not replaced")
	}
	s.UpdatePostgres("acme", r.ID, PostgresIn{Name: "db", Host: "web1", Container: "db", ClearPassword: true})
	if cfg, _ := s.AgentConfig("acme", "web1"); cfg.Postgres[0].Password != "" {
		t.Fatal("the password was not removed")
	}
	if err := s.DeletePostgres("acme", r.ID); err != nil || len(s.ListPostgres("acme")) != 0 {
		t.Fatal("delete")
	}
}

func TestPostgresValidation(t *testing.T) {
	s, _ := newReg(t)
	for name, in := range map[string]PostgresIn{
		"no machine":      {Container: "db"},
		"nothing to find": {Host: "web1"},
		"bad address":     {Host: "web1", Addr: "no-port"},
		"bad tls":         {Host: "web1", Container: "db", SSLMode: "verify-full"},
		"newline in user": {Host: "web1", Container: "db", Username: "a\nb"},
		"bad container":   {Host: "web1", Container: "db; rm -rf /"},
	} {
		if _, err := s.CreatePostgres("acme", in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := s.CreatePostgres("acme", PostgresIn{Host: "web1", Addr: "10.0.0.5:5432"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePostgres("acme", PostgresIn{Host: "web1", Addr: "10.0.0.5:5432"}); err == nil {
		t.Fatal("the same server twice on one machine")
	}
}
