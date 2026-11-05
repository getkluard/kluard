// Kluard Enterprise Edition. Copyright (c) 2026 the Kluard licensor. NOT covered by the Apache-2.0 licence.
// Use in production requires a valid Kluard licence; see /ee/LICENSE.

package auth

import (
	"errors"
	"strings"
	"time"

	"github.com/getkluard/kluard/internal/perm"
)

// Custom groups: creating, changing and deleting groups with their own permissions. Reading and enforcing groups that
// already exist stays in groups.go, so a licence that ends never changes anyone's access.

func (s *Store) checkGroupName(tenant, name, exceptID string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return "", errors.New("the group name must be 1-60 characters")
	}
	for _, g := range s.ListGroups(tenant) {
		if g.ID != exceptID && strings.EqualFold(g.Name, name) {
			return "", errors.New("a group with that name already exists")
		}
	}
	return name, nil
}

func (s *Store) CreateGroup(tenant, name, desc string, perms map[string]string) (Group, error) {
	name, err := s.checkGroupName(tenant, name, "")
	if err != nil {
		return Group{}, err
	}
	p, err := perm.Normalize(perms)
	if err != nil {
		return Group{}, err
	}
	if err := operatorOnlyHere(tenant, p); err != nil {
		return Group{}, err
	}
	g := Group{ID: "g_" + RandomToken(6), Tenant: tenant, Name: name, Description: strings.TrimSpace(desc), Perms: p, Created: time.Now().UTC()}
	c, cancel := ctx()
	defer cancel()
	if err := s.b.Create(c, collGroups, g.ID, g); err != nil {
		return Group{}, err
	}
	s.invalidate()
	return g, nil
}

func (s *Store) UpdateGroup(tenant, id, name, desc string, perms map[string]string) (Group, error) {
	g, ok := s.GetGroup(tenant, id)
	if !ok {
		return Group{}, errors.New("no such group")
	}
	if g.Builtin {
		return Group{}, errors.New("the built-in groups cannot be changed; create a custom group instead")
	}
	name, err := s.checkGroupName(tenant, name, id)
	if err != nil {
		return Group{}, err
	}
	p, err := perm.Normalize(perms)
	if err != nil {
		return Group{}, err
	}
	if err := operatorOnlyHere(tenant, p); err != nil {
		return Group{}, err
	}
	g.Name, g.Description, g.Perms = name, strings.TrimSpace(desc), p
	c, cancel := ctx()
	defer cancel()
	if err := s.b.Put(c, collGroups, id, g, ""); err != nil {
		return Group{}, err
	}
	s.invalidate()
	return g, nil
}

// DeleteGroup refuses built-in groups and groups that still have members.
func (s *Store) DeleteGroup(tenant, id string) error {
	g, ok := s.GetGroup(tenant, id)
	if !ok {
		return errors.New("no such group")
	}
	if g.Builtin {
		return errors.New("the built-in groups cannot be deleted")
	}
	for _, u := range s.ListUsersIn(tenant) {
		if u.Group == id {
			return errors.New("the group still has members (" + u.Name + "...); move them to another group first")
		}
	}
	c, cancel := ctx()
	defer cancel()
	err := s.b.Delete(c, collGroups, id)
	s.invalidate()
	return err
}

// operatorOnlyHere refuses to give the operator console to a group of an ordinary tenant: a customer must never be
// able to manage other customers, whatever an administrator of that customer does.
func operatorOnlyHere(tenant string, p map[string]string) error {
	if tenant != perm.OperatorTenant && p[perm.Operator] != perm.None && p[perm.Operator] != "" {
		return errors.New("the operator console can only be given to groups in the operator tenant")
	}
	return nil
}
