package registry

import (
	"strings"
	"testing"
)

func TestMysqlSettings(t *testing.T) {
	s, _ := newReg(t)
	r, err := s.CreateMysql("acme", MysqlIn{Container: "db", Host: "web1", Username: "kluard", Password: "DB-SECRET", SSLMode: "require"})
	if err != nil || r.Name != "db" || !strings.HasPrefix(r.ID, "my") || !r.Out().HasPassword {
		t.Fatalf("%+v %v", r, err)
	}
	if r.PasswordEnc == "" || strings.Contains(r.PasswordEnc, "DB-SECRET") {
		t.Fatal("the password must be stored encrypted")
	}
	cfg, _ := s.AgentConfig("acme", "web1")
	if len(cfg.Mysql) != 1 || cfg.Mysql[0].Password != "DB-SECRET" || cfg.Mysql[0].SSLMode != "require" {
		t.Fatalf("%+v", cfg.Mysql)
	}
	if other, _ := s.AgentConfig("acme", "web2"); len(other.Mysql) != 0 {
		t.Fatal("another machine must not get the login")
	}
	if len(s.ListMysql("globex")) != 0 {
		t.Fatal("tenant leak")
	}
	if _, err := s.UpdateMysql("globex", r.ID, MysqlIn{Name: "x", Host: "web1", Addr: "10.0.0.1:3306"}); err == nil {
		t.Fatal("another tenant changed it")
	}
	s.UpdateMysql("acme", r.ID, MysqlIn{Name: "db", Host: "web1", Container: "db", Username: "kluard"})
	if cfg, _ := s.AgentConfig("acme", "web1"); cfg.Mysql[0].Password != "DB-SECRET" {
		t.Fatal("the password was lost on an update")
	}
	s.UpdateMysql("acme", r.ID, MysqlIn{Name: "db", Host: "web1", Container: "db", ClearPassword: true})
	if cfg, _ := s.AgentConfig("acme", "web1"); cfg.Mysql[0].Password != "" {
		t.Fatal("the password was not removed")
	}
	if err := s.DeleteMysql("acme", r.ID); err != nil || len(s.ListMysql("acme")) != 0 {
		t.Fatal("delete")
	}
}

func TestMysqlValidation(t *testing.T) {
	s, _ := newReg(t)
	for name, in := range map[string]MysqlIn{
		"no machine":    {Container: "db"},
		"nothing":       {Host: "web1"},
		"bad address":   {Host: "web1", Addr: "no-port"},
		"bad tls":       {Host: "web1", Container: "db", SSLMode: "verify-full"},
		"bad container": {Host: "web1", Container: "db; rm -rf /"},
	} {
		if _, err := s.CreateMysql("acme", in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := s.CreateMysql("acme", MysqlIn{Host: "web1", Addr: "10.0.0.5:3306"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMysql("acme", MysqlIn{Host: "web1", Addr: "10.0.0.5:3306"}); err == nil {
		t.Fatal("the same server twice")
	}
}
