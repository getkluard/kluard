package registry

import (
	"net/url"
	"sort"
	"strings"
	"time"
)

// Nginx is what Kluard knows about one nginx beyond what its agent finds by itself: the address of its status page (needed
// when it is not in Docker, or the page is on an unusual port or path). An nginx in Docker is matched by Container on Host.
type Nginx struct {
	ID        string    `json:"id"`
	Tenant    string    `json:"tenant,omitempty"`
	Name      string    `json:"name"`
	Host      string    `json:"host"`
	Container string    `json:"container"`
	URL       string    `json:"url"` // the status page, optional for a container
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
}

type NginxIn struct {
	Name, Host, Container, URL string
}

// AgentNginx is an nginx as the agent receives it.
type AgentNginx struct {
	Name      string `json:"name"`
	Container string `json:"container,omitempty"`
	URL       string `json:"url,omitempty"`
}

func (s *Service) ListNginx(tenant string) []Nginx {
	c, cancel := ctx()
	defer cancel()
	docs, _ := s.b.List(c, collNginx, map[string]string{"tenant": tenant}, 1000)
	out := []Nginx{}
	for _, d := range docs {
		var r Nginx
		if d.Decode(&r) == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		return strings.ToLower(out[a].Host+"/"+out[a].Name) < strings.ToLower(out[b].Host+"/"+out[b].Name)
	})
	return out
}

func (s *Service) getNginx(tenant, id string) (Nginx, string, error) {
	c, cancel := ctx()
	defer cancel()
	d, err := s.b.Get(c, collNginx, id)
	var r Nginx
	if err != nil || d.Decode(&r) != nil || r.Tenant != tenant {
		return Nginx{}, "", ErrNotFound
	}
	return r, d.Version, nil
}

func (s *Service) validateNginx(tenant, id string, in *NginxIn) error {
	in.Name, in.Host, in.Container, in.URL = strings.TrimSpace(in.Name), strings.TrimSpace(in.Host), strings.TrimSpace(in.Container), strings.TrimSpace(in.URL)
	if in.Name == "" {
		in.Name = in.Container
	}
	if in.Name == "" {
		in.Name = in.URL
	}
	if !hostRe.MatchString(in.Host) {
		return invalid("choose which machine's agent checks this nginx")
	}
	if in.Container == "" && in.URL == "" {
		return invalid("give the Docker container name or the address of the status page")
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
			return invalid("the address must look like http://10.0.0.5:8080/nginx_status")
		}
	}
	for _, o := range s.ListNginx(tenant) {
		if o.ID != id && o.Host == in.Host && (strings.EqualFold(o.Name, in.Name) || (in.Container != "" && o.Container == in.Container)) {
			return invalid("this nginx is already set up on that machine")
		}
	}
	return nil
}

func (s *Service) CreateNginx(tenant string, in NginxIn) (Nginx, error) {
	if err := s.validateNginx(tenant, "", &in); err != nil {
		return Nginx{}, err
	}
	now := time.Now().UTC()
	r := Nginx{ID: "ng" + randID(), Tenant: tenant, Name: in.Name, Host: in.Host, Container: in.Container, URL: in.URL, Created: now, Updated: now}
	c, cancel := ctx()
	defer cancel()
	return r, s.b.Create(c, collNginx, r.ID, r)
}

func (s *Service) UpdateNginx(tenant, id string, in NginxIn) (Nginx, error) {
	cur, ver, err := s.getNginx(tenant, id)
	if err != nil {
		return Nginx{}, err
	}
	if err := s.validateNginx(tenant, id, &in); err != nil {
		return Nginx{}, err
	}
	cur.Name, cur.Host, cur.Container, cur.URL = in.Name, in.Host, in.Container, in.URL
	cur.Updated = time.Now().UTC()
	c, cancel := ctx()
	defer cancel()
	return cur, s.b.Put(c, collNginx, id, cur, ver)
}

func (s *Service) DeleteNginx(tenant, id string) error {
	if _, _, err := s.getNginx(tenant, id); err != nil {
		return err
	}
	c, cancel := ctx()
	defer cancel()
	return s.b.Delete(c, collNginx, id)
}
