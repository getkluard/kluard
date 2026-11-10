package registry

import "testing"

func TestNginxSettings(t *testing.T) {
	s, _ := newReg(t)
	r, err := s.CreateNginx("acme", NginxIn{Container: "web", Host: "web1", URL: "http://10.0.0.5:8080/nginx_status"})
	if err != nil || r.Name != "web" || r.ID[:2] != "ng" {
		t.Fatalf("%+v %v", r, err)
	}
	if cfg, _ := s.AgentConfig("acme", "web1"); len(cfg.Nginx) != 1 || cfg.Nginx[0].URL != "http://10.0.0.5:8080/nginx_status" {
		t.Fatalf("%+v", cfg.Nginx)
	}
	if cfg, _ := s.AgentConfig("acme", "web2"); len(cfg.Nginx) != 0 {
		t.Fatal("another machine got it")
	}
	if len(s.ListNginx("globex")) != 0 {
		t.Fatal("tenant leak")
	}
	if _, err := s.UpdateNginx("globex", r.ID, NginxIn{Host: "web1", Container: "web"}); err == nil {
		t.Fatal("another tenant changed it")
	}
	if _, err := s.UpdateNginx("acme", r.ID, NginxIn{Name: "front", Host: "web1", Container: "web"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNginx("acme", r.ID); err != nil || len(s.ListNginx("acme")) != 0 {
		t.Fatal("delete")
	}
}

func TestNginxValidation(t *testing.T) {
	s, _ := newReg(t)
	for name, in := range map[string]NginxIn{
		"no machine":     {Container: "web"},
		"nothing":        {Host: "web1"},
		"ftp":            {Host: "web1", URL: "ftp://x/y"},
		"password in it": {Host: "web1", URL: "http://a:b@x/status"},
		"no host":        {Host: "web1", URL: "http:///status"},
		"bad container":  {Host: "web1", Container: "w; rm"},
	} {
		if _, err := s.CreateNginx("acme", in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := s.CreateNginx("acme", NginxIn{Host: "web1", Container: "web"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNginx("acme", NginxIn{Host: "web1", Container: "web"}); err == nil {
		t.Fatal("twice")
	}
}
