package registry

import "testing"

func TestWebChecks(t *testing.T) {
	s, _ := newReg(t)
	w, err := s.CreateWeb("acme", WebIn{Host: "web1", URL: "https://shop.example.com/", Contains: "Welcome"})
	if err != nil || w.Name != "shop.example.com" || w.ID[:2] != "wb" {
		t.Fatalf("%+v %v", w, err)
	}
	if cfg, _ := s.AgentConfig("acme", "web1"); len(cfg.Web) != 1 || cfg.Web[0].Contains != "Welcome" {
		t.Fatalf("%+v", cfg.Web)
	}
	if cfg, _ := s.AgentConfig("acme", "web2"); len(cfg.Web) != 0 {
		t.Fatal("another machine must not check it")
	}
	if len(s.ListWeb("globex")) != 0 {
		t.Fatal("tenant leak")
	}
	if _, err := s.UpdateWeb("globex", w.ID, WebIn{Host: "web1", URL: "https://x.example"}); err == nil {
		t.Fatal("another tenant changed it")
	}
	if _, err := s.UpdateWeb("acme", w.ID, WebIn{Name: "Shop", Host: "web1", URL: "https://shop.example.com/", Status: 200}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteWeb("acme", w.ID); err != nil || len(s.ListWeb("acme")) != 0 {
		t.Fatal("delete")
	}
}

func TestWebValidation(t *testing.T) {
	s, _ := newReg(t)
	for name, in := range map[string]WebIn{
		"no machine":     {URL: "https://a.example"},
		"no scheme":      {Host: "web1", URL: "a.example"},
		"ftp":            {Host: "web1", URL: "ftp://a.example"},
		"login in it":    {Host: "web1", URL: "https://u:p@a.example"},
		"space":          {Host: "web1", URL: "https://a.example/a b"},
		"bad status":     {Host: "web1", URL: "https://a.example", Status: 42},
		"multiline text": {Host: "web1", URL: "https://a.example", Contains: "a\nb"},
	} {
		if _, err := s.CreateWeb("acme", in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := s.CreateWeb("acme", WebIn{Host: "web1", URL: "https://a.example"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWeb("acme", WebIn{Host: "web1", URL: "https://a.example/other"}); err == nil {
		t.Fatal("the same name twice")
	}
}
