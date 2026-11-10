package registry

import (
	"strings"
	"testing"
)

func TestMinioSettings(t *testing.T) {
	s, _ := newReg(t)
	r, err := s.CreateMinio("acme", MinioIn{Container: "minio", Host: "web1", Token: "TOKEN-SECRET"})
	if err != nil || !r.Out().HasToken || r.Out().ID[:2] != "mn" || strings.Contains(r.TokenEnc, "TOKEN-SECRET") {
		t.Fatalf("%+v %v", r, err)
	}
	if cfg, _ := s.AgentConfig("acme", "web1"); len(cfg.Minio) != 1 || cfg.Minio[0].Token != "TOKEN-SECRET" {
		t.Fatalf("%+v", cfg.Minio)
	}
	if cfg, _ := s.AgentConfig("acme", "web2"); len(cfg.Minio) != 0 {
		t.Fatal("another machine got the token")
	}
	if len(s.ListMinio("globex")) != 0 {
		t.Fatal("tenant leak")
	}
	s.UpdateMinio("acme", r.ID, MinioIn{Name: "minio", Host: "web1", Container: "minio"})
	if cfg, _ := s.AgentConfig("acme", "web1"); cfg.Minio[0].Token != "TOKEN-SECRET" {
		t.Fatal("the token was lost on an update")
	}
	s.UpdateMinio("acme", r.ID, MinioIn{Name: "minio", Host: "web1", Container: "minio", ClearToken: true})
	if cfg, _ := s.AgentConfig("acme", "web1"); cfg.Minio[0].Token != "" {
		t.Fatal("the token was not removed")
	}
	if err := s.DeleteMinio("acme", r.ID); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]MinioIn{"no machine": {Container: "m"}, "nothing": {Host: "h"}, "login in url": {Host: "h", URL: "http://a:b@x:9000"}, "space in token": {Host: "h", Container: "m", Token: "a b"}} {
		if _, err := s.CreateMinio("acme", in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
