package auth

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/getkluard/kluard/internal/docstore"
	"github.com/getkluard/kluard/internal/perm"
)

const collGroups = "groups"

// Group is a named set of permissions. "admin" and "user" are built in (identical for every tenant, not
// stored, cannot be changed); other groups are created by an admin and belong to one tenant.
type Group struct {
	ID          string            `json:"id"`
	Tenant      string            `json:"tenant,omitempty"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Perms       map[string]string `json:"perms"`
	Builtin     bool              `json:"builtin"`
	Created     time.Time         `json:"created,omitempty"`
}

func builtins() []Group {
	return []Group{
		{ID: "admin", Name: "Admin", Description: "Full access to everything, including users and groups.", Perms: perm.Admin(), Builtin: true},
		{ID: "user", Name: "User", Description: "Read-only access to data, dashboards, hosts and instances.", Perms: perm.User(), Builtin: true},
	}
}

func (s *Store) groupFromCache(tenant, id string) (Group, bool, bool) {
	if s.ttl <= 0 {
		return Group{}, false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.groups[tenant+"/"+id]; ok && time.Now().Before(e.exp) {
		return e.g, e.ok, true
	}
	return Group{}, false, false
}

// GetGroup resolves a built-in or custom group of a tenant.
func (s *Store) GetGroup(tenant, id string) (Group, bool) {
	for _, g := range builtins() {
		if g.ID == id {
			g.Perms = perm.ForTenant(g.Perms, tenant)
			return g, true
		}
	}
	if g, ok, hit := s.groupFromCache(tenant, id); hit {
		return g, ok
	}
	c, cancel := ctx()
	defer cancel()
	d, err := s.b.Get(c, collGroups, id)
	var g Group
	ok := err == nil && d.Decode(&g) == nil && g.Tenant == tenant
	if err != nil && !errors.Is(err, docstore.ErrNotFound) {
		return Group{}, false // backend trouble: fail closed, do not cache
	}
	if s.ttl > 0 {
		s.mu.Lock()
		s.groups[tenant+"/"+id] = cachedGroup{g, ok, time.Now().Add(s.ttl)}
		s.mu.Unlock()
	}
	return g, ok
}

// ListGroups returns the built-in groups followed by the tenant's own, sorted by name.
func (s *Store) ListGroups(tenant string) []Group {
	out := builtins()
	for i := range out {
		out[i].Perms = perm.ForTenant(out[i].Perms, tenant) // shown as it applies in this tenant
	}
	c, cancel := ctx()
	defer cancel()
	docs, _ := s.b.List(c, collGroups, map[string]string{"tenant": tenant}, 500)
	var own []Group
	for _, d := range docs {
		var g Group
		if d.Decode(&g) == nil {
			own = append(own, g)
		}
	}
	sort.Slice(own, func(i, j int) bool { return strings.ToLower(own[i].Name) < strings.ToLower(own[j].Name) })
	return append(out, own...)
}

// EffectiveGroup is the group id a user is in; users created before groups existed count as admins.
func EffectiveGroup(u User) string {
	if u.Group == "" {
		return "admin"
	}
	return u.Group
}

// PermsFor returns what a user may do. A missing group means no access (fail closed).
func (s *Store) PermsFor(u User) map[string]string {
	if g, ok := s.GetGroup(u.Tenant, EffectiveGroup(u)); ok {
		return perm.ForTenant(g.Perms, u.Tenant)
	}
	return perm.Normalize2None()
}

// IsBuiltinGroup tells whether id is one of the groups that always exist (Admin and User).
func IsBuiltinGroup(id string) bool {
	for _, g := range builtins() {
		if g.ID == id {
			return true
		}
	}
	return false
}
