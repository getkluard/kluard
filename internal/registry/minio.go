package registry

import (
	"net/url"
	"sort"
	"strings"
	"time"
)

// Minio is what Kluard knows about one MinIO beyond what its agent finds by itself: the token for its metrics (encrypted)
// and, for a MinIO that is not in Docker, its address. A MinIO in Docker is matched by Container on Host.
type Minio struct {
	ID        string    `json:"id"`
	Tenant    string    `json:"tenant,omitempty"`
	Name      string    `json:"name"`
	Host      string    `json:"host"`
	Container string    `json:"container"`
	URL       string    `json:"url"` // http://host:9000, optional for a container
	TokenEnc  string    `json:"token_enc,omitempty"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
}

// MinioOut is a MinIO as the UI sees it: the token is never sent back, only whether one is set.
type MinioOut struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Host      string `json:"host"`
	Container string `json:"container"`
	URL       string `json:"url"`
	HasToken  bool   `json:"has_token"`
}

func (r Minio) Out() MinioOut {
	return MinioOut{ID: r.ID, Name: r.Name, Host: r.Host, Container: r.Container, URL: r.URL, HasToken: r.TokenEnc != ""}
}

type MinioIn struct {
	Name, Host, Container, URL, Token string
	ClearToken                        bool
}

// AgentMinio is a MinIO as the agent receives it (token decrypted).
type AgentMinio struct {
	Name      string `json:"name"`
	Container string `json:"container,omitempty"`
	URL       string `json:"url,omitempty"`
	Token     string `json:"token,omitempty"`
}

func (s *Service) ListMinio(tenant string) []Minio {
	c, cancel := ctx()
	defer cancel()
	docs, _ := s.b.List(c, collMinio, map[string]string{"tenant": tenant}, 1000)
	out := []Minio{}
	for _, d := range docs {
		var r Minio
		if d.Decode(&r) == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		return strings.ToLower(out[a].Host+"/"+out[a].Name) < strings.ToLower(out[b].Host+"/"+out[b].Name)
	})
	return out
}

func (s *Service) getMinio(tenant, id string) (Minio, string, error) {
	c, cancel := ctx()
	defer cancel()
	d, err := s.b.Get(c, collMinio, id)
	var r Minio
	if err != nil || d.Decode(&r) != nil || r.Tenant != tenant {
		return Minio{}, "", ErrNotFound
	}
	return r, d.Version, nil
}

func (s *Service) validateMinio(tenant, id string, in *MinioIn) error {
	in.Name, in.Host, in.Container, in.URL = strings.TrimSpace(in.Name), strings.TrimSpace(in.Host), strings.TrimSpace(in.Container), strings.TrimSpace(in.URL)
	in.Token = strings.TrimSpace(in.Token)
	if in.Name == "" {
		in.Name = in.Container
	}
	if in.Name == "" {
		in.Name = in.URL
	}
	if !hostRe.MatchString(in.Host) {
		return invalid("choose which machine's agent checks this MinIO")
	}
	if in.Container == "" && in.URL == "" {
		return invalid("give the Docker container name or the address of MinIO")
	}
	if in.Name == "" || len(in.Name) > 100 || strings.ContainsAny(in.Name+in.Container, "\r\n\x00") {
		return invalid("the name is empty, too long or has control characters")
	}
	if in.Container != "" && !nameRe.MatchString(in.Container) {
		return invalid("the container name must be letters, digits, . _ - (as Docker allows)")
	}
	if in.URL != "" {
		u, err := url.Parse(in.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || len(in.URL) > 300 || strings.ContainsAny(in.URL, " \r\n\x00") {
			return invalid("the address must look like http://10.0.0.5:9000")
		}
	}
	if len(in.Token) > 4000 || strings.ContainsAny(in.Token, " \r\n\x00") {
		return invalid("the token is too long or has spaces or line breaks in it")
	}
	for _, o := range s.ListMinio(tenant) {
		if o.ID != id && o.Host == in.Host && (strings.EqualFold(o.Name, in.Name) || (in.Container != "" && o.Container == in.Container)) {
			return invalid("this MinIO is already set up on that machine")
		}
	}
	return nil
}

func (s *Service) CreateMinio(tenant string, in MinioIn) (Minio, error) {
	if err := s.validateMinio(tenant, "", &in); err != nil {
		return Minio{}, err
	}
	now := time.Now().UTC()
	r := Minio{ID: "mn" + randID(), Tenant: tenant, Name: in.Name, Host: in.Host, Container: in.Container, URL: in.URL, TokenEnc: s.box.Seal(in.Token), Created: now, Updated: now}
	c, cancel := ctx()
	defer cancel()
	return r, s.b.Create(c, collMinio, r.ID, r)
}

func (s *Service) UpdateMinio(tenant, id string, in MinioIn) (Minio, error) {
	cur, ver, err := s.getMinio(tenant, id)
	if err != nil {
		return Minio{}, err
	}
	if err := s.validateMinio(tenant, id, &in); err != nil {
		return Minio{}, err
	}
	cur.Name, cur.Host, cur.Container, cur.URL = in.Name, in.Host, in.Container, in.URL
	switch {
	case in.ClearToken:
		cur.TokenEnc = ""
	case in.Token != "":
		cur.TokenEnc = s.box.Seal(in.Token)
	}
	cur.Updated = time.Now().UTC()
	c, cancel := ctx()
	defer cancel()
	return cur, s.b.Put(c, collMinio, id, cur, ver)
}

func (s *Service) DeleteMinio(tenant, id string) error {
	if _, _, err := s.getMinio(tenant, id); err != nil {
		return err
	}
	c, cancel := ctx()
	defer cancel()
	return s.b.Delete(c, collMinio, id)
}
