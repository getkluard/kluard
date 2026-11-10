package registry

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"time"
)

// A status page is a public address that shows customers whether their service is up. It lists the Nextcloud instances and
// website checks the owner picked, each under a name the owner chose, and nothing else: no addresses, machines or error
// texts. The id is the secret in the address, so anyone who has the link can read the page and nobody can guess it.
type StatusPage struct {
	ID      string       `json:"id"`
	Tenant  string       `json:"tenant,omitempty"`
	Title   string       `json:"title"`
	Message string       `json:"message,omitempty"` // a line under the title, for example "Questions? Write to support@..."
	Items   []StatusItem `json:"items"`
	Created time.Time    `json:"created"`
	Updated time.Time    `json:"updated"`
}

// StatusItem is one line on the page. Kind is "instance" or "web"; Ref is the id of that instance or website check.
type StatusItem struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Label string `json:"label"`
}

// StatusPageIn is what the UI sends.
type StatusPageIn struct {
	Title, Message string
	Items          []StatusItem
}

const maxStatusItems = 50

var statusPageIDRe = regexp.MustCompile(`^sp[0-9a-f]{24}$`)

// ValidStatusPageID says whether s can be the id of a status page (checked before anything is looked up).
func ValidStatusPageID(s string) bool { return statusPageIDRe.MatchString(s) }

func newStatusPageID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("no randomness available")
	}
	return "sp" + hex.EncodeToString(b)
}

func (s *Service) ListStatusPages(tenant string) []StatusPage {
	c, cancel := ctx()
	defer cancel()
	docs, _ := s.b.List(c, collStatusPage, map[string]string{"tenant": tenant}, 200)
	out := []StatusPage{}
	for _, d := range docs {
		var r StatusPage
		if d.Decode(&r) == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(a, b int) bool { return strings.ToLower(out[a].Title) < strings.ToLower(out[b].Title) })
	return out
}

// GetStatusPage is for the public address: it finds a page by its id alone, whoever owns it.
func (s *Service) GetStatusPage(id string) (StatusPage, error) {
	if !ValidStatusPageID(id) {
		return StatusPage{}, ErrNotFound
	}
	c, cancel := ctx()
	defer cancel()
	d, err := s.b.Get(c, collStatusPage, id)
	var r StatusPage
	if err != nil || d.Decode(&r) != nil {
		return StatusPage{}, ErrNotFound
	}
	return r, nil
}

func (s *Service) getStatusPageVer(id string) (StatusPage, string, error) {
	c, cancel := ctx()
	defer cancel()
	d, err := s.b.Get(c, collStatusPage, id)
	var r StatusPage
	if err != nil || d.Decode(&r) != nil {
		return StatusPage{}, "", ErrNotFound
	}
	return r, d.Version, nil
}

func (s *Service) validateStatusPage(tenant string, in *StatusPageIn) error {
	in.Title = strings.TrimSpace(in.Title)
	in.Message = strings.TrimSpace(in.Message)
	if in.Title == "" || len(in.Title) > 100 || strings.ContainsAny(in.Title, "\r\n\x00") {
		return invalid("give the page a title (up to 100 characters)")
	}
	if len(in.Message) > 300 || strings.ContainsAny(in.Message, "\r\n\x00") {
		return invalid("the message can be up to 300 characters, on one line")
	}
	if len(in.Items) == 0 || len(in.Items) > maxStatusItems {
		return invalid("pick between 1 and %d things to show", maxStatusItems)
	}
	insts := map[string]bool{}
	for _, i := range s.ListInstances(tenant) {
		insts[i.ID] = true
	}
	webs := map[string]bool{}
	for _, w := range s.ListWeb(tenant) {
		webs[w.ID] = true
	}
	seen := map[string]bool{}
	for i := range in.Items {
		it := &in.Items[i]
		it.Label = strings.TrimSpace(it.Label)
		if it.Label == "" || len(it.Label) > 100 || strings.ContainsAny(it.Label, "\r\n\x00") {
			return invalid("every line needs a name that customers see (up to 100 characters)")
		}
		switch {
		case it.Kind == "instance" && insts[it.Ref], it.Kind == "web" && webs[it.Ref]:
		default:
			return invalid("%q is not one of your instances or websites", it.Label)
		}
		if k := it.Kind + "/" + it.Ref; seen[k] {
			return invalid("%q is picked twice", it.Label)
		} else {
			seen[k] = true
		}
	}
	return nil
}

func (s *Service) CreateStatusPage(tenant string, in StatusPageIn) (StatusPage, error) {
	if err := s.validateStatusPage(tenant, &in); err != nil {
		return StatusPage{}, err
	}
	now := time.Now().UTC()
	r := StatusPage{ID: newStatusPageID(), Tenant: tenant, Title: in.Title, Message: in.Message, Items: in.Items, Created: now, Updated: now}
	c, cancel := ctx()
	defer cancel()
	return r, s.b.Create(c, collStatusPage, r.ID, r)
}

func (s *Service) UpdateStatusPage(tenant, id string, in StatusPageIn) (StatusPage, error) {
	cur, ver, err := s.getStatusPageVer(id)
	if err != nil || cur.Tenant != tenant {
		return StatusPage{}, ErrNotFound
	}
	if err := s.validateStatusPage(tenant, &in); err != nil {
		return StatusPage{}, err
	}
	cur.Title, cur.Message, cur.Items = in.Title, in.Message, in.Items
	cur.Updated = time.Now().UTC()
	c, cancel := ctx()
	defer cancel()
	return cur, s.b.Put(c, collStatusPage, id, cur, ver)
}

func (s *Service) DeleteStatusPage(tenant, id string) error {
	cur, _, err := s.getStatusPageVer(id)
	if err != nil || cur.Tenant != tenant {
		return ErrNotFound
	}
	c, cancel := ctx()
	defer cancel()
	return s.b.Delete(c, collStatusPage, id)
}
