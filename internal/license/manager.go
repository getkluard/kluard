package license

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/getkluard/kluard/internal/docstore"
)

const (
	coll   = "meta"
	docID  = "license"
	instID = "installation"
)

// Manager holds the licence in force. It is read from KLUARD_LICENSE_FILE if that is set and the file exists (so a renewal
// can be dropped in as a file), otherwise from what an administrator saved in the interface.
type Manager struct {
	db   docstore.Backend
	keys Keys
	file string
	Now  func() time.Time

	mu     sync.RWMutex
	lic    *License
	source string // "file" | "stored"
	err    string // why a licence that exists cannot be used
	inst   string // this installation's id
}

func NewManager(db docstore.Backend, keys Keys, file string) *Manager {
	return &Manager{db: db, keys: keys, file: file, Now: time.Now}
}

type instDoc struct {
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
}

// InstallationID is the id of this installation. It is made the first time it is asked for and then kept in the
// store, so it stays the same across restarts and upgrades. A licence bound to it works here and nowhere else.
func (m *Manager) InstallationID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inst != "" {
		return m.inst
	}
	if m.db != nil {
		c, cancel := ctx5()
		defer cancel()
		read := func() bool {
			if d, err := m.db.Get(c, coll, instID); err == nil {
				var x instDoc
				if d.Decode(&x) == nil && ValidInstallationID(x.ID) {
					m.inst = x.ID
					return true
				}
			}
			return false
		}
		if read() {
			return m.inst
		}
		id := NewInstallationID()
		err := m.db.Create(c, coll, instID, instDoc{ID: id, Created: m.Now().UTC()})
		if err == nil {
			m.inst = id
			return id
		}
		if read() { // another instance made it first
			return m.inst
		}
	}
	m.inst = NewInstallationID() // no store (tests): lasts as long as the process
	return m.inst
}

// checkBound refuses a licence bound to another installation.
func (m *Manager) checkBound(l License) error {
	if l.Installation == "" {
		return nil
	}
	if mine := m.InstallationID(); l.Installation != mine {
		return bad("this licence was issued for the installation %s, but this installation is %s; ask for a licence for this installation id (see Settings)", l.Installation, mine)
	}
	return nil
}

type stored struct {
	Raw     string    `json:"raw"`
	Updated time.Time `json:"updated"`
	By      string    `json:"by"`
}

func ctx5() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// Load (re)reads the licence. It is cheap, so the server calls it once a minute and a renewed file is picked up.
func (m *Manager) Load() {
	var lic *License
	var source, errText string
	try := func(raw []byte, src string) bool {
		l, err := Parse(raw, m.keys)
		if err == nil {
			err = m.checkBound(l)
		}
		if err != nil {
			errText = err.Error()
			return false
		}
		lic, source, errText = &l, src, ""
		return true
	}
	if m.file != "" {
		if raw, err := os.ReadFile(m.file); err == nil {
			try(raw, "file")
		} else if !errors.Is(err, os.ErrNotExist) {
			errText = "the licence file cannot be read: " + err.Error()
		}
	}
	if lic == nil && m.db != nil {
		c, cancel := ctx5()
		defer cancel()
		if d, err := m.db.Get(c, coll, docID); err == nil {
			var s stored
			if d.Decode(&s) == nil && s.Raw != "" {
				try([]byte(s.Raw), "stored")
			}
		}
	}
	m.mu.Lock()
	m.lic, m.source, m.err = lic, source, errText
	m.mu.Unlock()
}

// Set verifies and saves a licence. A licence that does not verify is refused and the one in force stays.
func (m *Manager) Set(raw []byte, by string) (License, error) {
	l, err := Parse(raw, m.keys)
	if err != nil {
		return License{}, err
	}
	if err := m.checkBound(l); err != nil {
		return License{}, err
	}
	m.mu.RLock()
	fromFile := m.source == "file"
	m.mu.RUnlock()
	if fromFile {
		return License{}, bad("the licence in force is read from the file %s; replace that file instead (it is picked up within a minute)", m.file)
	}
	c, cancel := ctx5()
	defer cancel()
	if err := m.db.Put(c, coll, docID, stored{Raw: string(raw), Updated: m.Now().UTC(), By: by}, ""); err != nil {
		return License{}, err
	}
	m.Load()
	return l, nil
}

// Remove deletes the saved licence: the installation goes back to the Community edition.
func (m *Manager) Remove() error {
	m.mu.RLock()
	fromFile := m.source == "file"
	m.mu.RUnlock()
	if fromFile {
		return bad("the licence in force is read from the file %s; remove that file instead", m.file)
	}
	c, cancel := ctx5()
	defer cancel()
	if err := m.db.Delete(c, coll, docID); err != nil && !errors.Is(err, docstore.ErrNotFound) {
		return err
	}
	m.Load()
	return nil
}

// State is the state of the licence in force.
func (m *Manager) State() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	switch {
	case m.lic != nil:
		return m.lic.State(m.Now())
	case m.err != "":
		return StateInvalid
	}
	return StateNone
}

// Allows tells whether a feature of an edition may be used now. Community features are always allowed.
func (m *Manager) Allows(edition string) bool {
	if edition == "" || edition == Community {
		return true
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lic != nil && m.lic.Has(edition) && m.lic.Active(m.Now())
}

// Info is what the interface shows.
type Info struct {
	State     State    `json:"state"`
	Customer  string   `json:"customer,omitempty"`
	ID        string   `json:"id,omitempty"`
	Editions  []string `json:"editions,omitempty"`
	Issued    string   `json:"issued,omitempty"`
	Expires   string   `json:"expires,omitempty"`
	GraceEnds string   `json:"grace_ends,omitempty"`
	DaysLeft  int      `json:"days_left"`
	Limits    Limits   `json:"limits"`
	Source    string   `json:"source,omitempty"`
	Trial     bool     `json:"trial,omitempty"`
	Problem   string   `json:"problem,omitempty"`
	Trusted   int      `json:"trusted_keys"`
	// Installation is this installation's id (always shown, also without a licence); Bound is the id the licence in
	// force is bound to, empty when it is not bound.
	Installation string `json:"installation"`
	Bound        string `json:"bound_to,omitempty"`
}

func (m *Manager) Info() Info {
	inst := m.InstallationID()
	m.mu.RLock()
	defer m.mu.RUnlock()
	in := Info{State: StateNone, Trusted: len(m.keys), Problem: m.err, Installation: inst}
	if m.lic == nil {
		if m.err != "" {
			in.State = StateInvalid
		}
		return in
	}
	l, now := *m.lic, m.Now()
	in.State, in.Customer, in.ID, in.Editions, in.Limits, in.Source = l.State(now), l.Customer, l.ID, l.Editions, l.Limits, m.source
	in.Issued, in.Expires, in.GraceEnds = l.Issued.Format(time.RFC3339), l.Expires.Format(time.RFC3339), l.Expires.Add(Grace).Format(time.RFC3339)
	in.DaysLeft, in.Trial, in.Bound = l.DaysLeft(now), l.Trial, l.Installation
	if l.Trial {
		in.GraceEnds = in.Expires
	}
	return in
}

// Warnings are the soft-limit messages for the given use.
func (m *Manager) Warnings(hosts, tenants int) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.lic == nil {
		return nil
	}
	return m.lic.Warnings(hosts, tenants)
}

var _ = json.Marshal
