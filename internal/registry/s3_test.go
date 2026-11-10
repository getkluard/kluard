package registry

import (
	"strings"
	"testing"
)

func TestS3Records(t *testing.T) {
	s, _ := newReg(t)
	in := S3In{Host: "web1", URL: "https://s3.example.com/", AccessKey: "AKIA1", Secret: "topsecret", Buckets: []string{" photos ", "", "backups"}, Size: true}
	r, err := s.CreateS3("acme", in)
	if err != nil || r.Name != "s3.example.com" || r.ID[:2] != "s3" || r.URL != "https://s3.example.com" || len(r.Buckets) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	if strings.Contains(r.SecretEnc, "topsecret") || r.SecretEnc == "" {
		t.Fatal("the secret must be stored encrypted")
	}
	if o := r.Out(); !o.HasSecret || o.AccessKey != "AKIA1" {
		t.Fatalf("%+v", o)
	}
	cfg, _ := s.AgentConfig("acme", "web1")
	if len(cfg.S3) != 1 || cfg.S3[0].SecretKey != "topsecret" || !cfg.S3[0].Size || cfg.S3[0].AccessKey != "AKIA1" {
		t.Fatalf("%+v", cfg.S3)
	}
	if cfg, _ := s.AgentConfig("acme", "web2"); len(cfg.S3) != 0 {
		t.Fatal("another machine must not get the secret")
	}
	if cfg, _ := s.AgentConfig("globex", "web1"); len(cfg.S3) != 0 {
		t.Fatal("tenant leak")
	}
	if len(s.ListS3("globex")) != 0 {
		t.Fatal("tenant leak")
	}
	// an update without a secret keeps the old one
	in.Secret, in.Name = "", "Backups S3"
	if _, err := s.UpdateS3("acme", r.ID, in); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := s.AgentConfig("acme", "web1"); cfg.S3[0].SecretKey != "topsecret" || cfg.S3[0].Name != "Backups S3" {
		t.Fatalf("%+v", cfg.S3)
	}
	in.Secret = "newsecret"
	if _, err := s.UpdateS3("globex", r.ID, in); err == nil {
		t.Fatal("another tenant changed it")
	}
	if _, err := s.UpdateS3("acme", r.ID, in); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := s.AgentConfig("acme", "web1"); cfg.S3[0].SecretKey != "newsecret" {
		t.Fatal("secret not replaced")
	}
	if err := s.DeleteS3("globex", r.ID); err == nil {
		t.Fatal("another tenant removed it")
	}
	if err := s.DeleteS3("acme", r.ID); err != nil || len(s.ListS3("acme")) != 0 {
		t.Fatal("delete")
	}
}

func TestS3Validation(t *testing.T) {
	s, _ := newReg(t)
	ok := S3In{Host: "web1", URL: "https://s3.example.com", AccessKey: "A", Secret: "S"}
	for name, mod := range map[string]func(*S3In){
		"no machine":   func(i *S3In) { i.Host = "" },
		"no scheme":    func(i *S3In) { i.URL = "s3.example.com" },
		"login in url": func(i *S3In) { i.URL = "https://u:p@s3.example.com" },
		"a path":       func(i *S3In) { i.URL = "https://s3.example.com/bucket" },
		"no key":       func(i *S3In) { i.AccessKey = "" },
		"no secret":    func(i *S3In) { i.Secret = "" },
		"spaces":       func(i *S3In) { i.Secret = "a b" },
		"bad bucket":   func(i *S3In) { i.Buckets = []string{"Bad Bucket"} },
		"bad region":   func(i *S3In) { i.Region = "US East" },
	} {
		in := ok
		mod(&in)
		if _, err := s.CreateS3("acme", in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := s.CreateS3("acme", ok); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateS3("acme", ok); err == nil {
		t.Fatal("the same name twice")
	}
}
