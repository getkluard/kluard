package registry

import (
	"net/url"
	"sort"
	"strings"
	"time"
)

// Web is an address that one agent checks once a minute: does it answer, how fast, and when does its certificate expire.
type Web struct {
	ID       string    `json:"id"`
	Tenant   string    `json:"tenant,omitempty"`
	Name     string    `json:"name"`
	Host     string    `json:"host"` // the machine whose agent makes the request
	URL      string    `json:"url"`
	Status   int       `json:"status,omitempty"`   // the status code that counts as up, 0 = anything below 400
	Contains string    `json:"contains,omitempty"` // text that the page must contain
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
}

type WebIn struct {
	Name, Host, URL, Contains string
	Status                    int
}

// AgentWeb is a website check as the agent receives it.
type AgentWeb struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Status   int    `json:"status,omitempty"`
	Contains string `json:"contains,omitempty"`
}

func (s *Service) ListWeb(tenant string) []Web {
	c, cancel := ctx()
	defer cancel()
	docs, _ := s.b.List(c, collWeb, map[string]string{"tenant": tenant}, 2000)
	out := []Web{}
	for _, d := range docs {
		var r Web
		if d.Decode(&r) == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(a, b int) bool { return strings.ToLower(out[a].Name) < strings.ToLower(out[b].Name) })
	return out
}

func (s *Service) getWeb(tenant, id string) (Web, string, error) {
	c, cancel := ctx()
	defer cancel()
	d, err := s.b.Get(c, collWeb, id)
	var r Web
	if err != nil || d.Decode(&r) != nil || r.Tenant != tenant {
		return Web{}, "", ErrNotFound
	}
	return r, d.Version, nil
}

func (s *Service) validateWeb(tenant, id string, in *WebIn) error {
	in.Name, in.Host, in.URL, in.Contains = strings.TrimSpace(in.Name), strings.TrimSpace(in.Host), strings.TrimSpace(in.URL), strings.TrimSpace(in.Contains)
	if !hostRe.MatchString(in.Host) {
		return invalid("choose which machine's agent checks this address")
	}
	u, err := url.Parse(in.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || len(in.URL) > 500 || strings.ContainsAny(in.URL, " \r\n\x00") {
		return invalid("the address must start with http:// or https:// and have no login in it")
	}
	if in.Name == "" {
		in.Name = u.Hostname()
	}
	if len(in.Name) > 100 || strings.ContainsAny(in.Name, "\r\n\x00") {
		return invalid("the name is too long or has control characters")
	}
	if in.Status != 0 && (in.Status < 100 || in.Status > 599) {
		return invalid("the status code must be between 100 and 599")
	}
	if len(in.Contains) > 200 || strings.ContainsAny(in.Contains, "\r\n\x00") {
		return invalid("the text to look for must be one line of at most 200 characters")
	}
	for _, o := range s.ListWeb(tenant) {
		if o.ID != id && strings.EqualFold(o.Name, in.Name) {
			return invalid("there is already a check with that name")
		}
	}
	return nil
}

func (s *Service) CreateWeb(tenant string, in WebIn) (Web, error) {
	if err := s.validateWeb(tenant, "", &in); err != nil {
		return Web{}, err
	}
	now := time.Now().UTC()
	r := Web{ID: "wb" + randID(), Tenant: tenant, Name: in.Name, Host: in.Host, URL: in.URL, Status: in.Status, Contains: in.Contains, Created: now, Updated: now}
	c, cancel := ctx()
	defer cancel()
	return r, s.b.Create(c, collWeb, r.ID, r)
}

func (s *Service) UpdateWeb(tenant, id string, in WebIn) (Web, error) {
	cur, ver, err := s.getWeb(tenant, id)
	if err != nil {
		return Web{}, err
	}
	if err := s.validateWeb(tenant, id, &in); err != nil {
		return Web{}, err
	}
	cur.Name, cur.Host, cur.URL, cur.Status, cur.Contains = in.Name, in.Host, in.URL, in.Status, in.Contains
	cur.Updated = time.Now().UTC()
	c, cancel := ctx()
	defer cancel()
	return cur, s.b.Put(c, collWeb, id, cur, ver)
}

func (s *Service) DeleteWeb(tenant, id string) error {
	if _, _, err := s.getWeb(tenant, id); err != nil {
		return err
	}
	c, cancel := ctx()
	defer cancel()
	return s.b.Delete(c, collWeb, id)
}
