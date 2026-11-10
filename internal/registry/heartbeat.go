package registry

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/getkluard/kluard/internal/docstore"
)

// A heartbeat is an address that a job (a backup, a cron job) calls when it has run. Kluard raises an alarm when the call
// does not come in time. The id is the secret in the address, so it is long and random.
type Heartbeat struct {
	ID       string    `json:"id"`
	Tenant   string    `json:"tenant,omitempty"`
	Name     string    `json:"name"`
	Period   int       `json:"period"`    // seconds the job normally takes between two runs
	Grace    int       `json:"grace"`     // seconds of slack before it counts as late
	LastPing int64     `json:"last_ping"` // unix seconds, 0 = never
	LastFail int64     `json:"last_fail"` // unix seconds of the last call to /fail, 0 = never
	Pings    int       `json:"pings"`
	Created  time.Time `json:"created"`
	Updated  time.Time `json:"updated"`
}

// HeartbeatIn is what the UI sends.
type HeartbeatIn struct {
	Name          string
	Period, Grace int
}

var heartbeatIDRe = regexp.MustCompile(`^hb[0-9a-f]{24}$`)

// ValidHeartbeatID says whether s can be the id of a heartbeat (checked before anything is looked up).
func ValidHeartbeatID(s string) bool { return heartbeatIDRe.MatchString(s) }

// State is up (called in time), new (waiting for the first call), late (the call is overdue) or failed (the job said it failed).
func (h Heartbeat) State(now time.Time) string {
	limit := int64(h.Period + h.Grace)
	n := now.Unix()
	switch {
	case h.LastFail > 0 && h.LastFail >= h.LastPing:
		return "failed"
	case h.LastPing == 0:
		if n-h.Created.Unix() > limit {
			return "late"
		}
		return "new"
	case n-h.LastPing > limit:
		return "late"
	}
	return "up"
}

func newHeartbeatID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("no randomness available")
	}
	return "hb" + hex.EncodeToString(b)
}

func (s *Service) ListHeartbeats(tenant string) []Heartbeat {
	c, cancel := ctx()
	defer cancel()
	docs, _ := s.b.List(c, collHeartbeat, map[string]string{"tenant": tenant}, 1000)
	out := []Heartbeat{}
	for _, d := range docs {
		var r Heartbeat
		if d.Decode(&r) == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(a, b int) bool { return strings.ToLower(out[a].Name) < strings.ToLower(out[b].Name) })
	return out
}

// AllHeartbeats is every heartbeat of every tenant, for the loop that turns them into metrics.
func (s *Service) AllHeartbeats() []Heartbeat {
	c, cancel := ctx()
	defer cancel()
	docs, _ := s.b.List(c, collHeartbeat, nil, 10000)
	out := make([]Heartbeat, 0, len(docs))
	for _, d := range docs {
		var r Heartbeat
		if d.Decode(&r) == nil {
			out = append(out, r)
		}
	}
	return out
}

func (s *Service) getHeartbeat(id string) (Heartbeat, string, error) {
	c, cancel := ctx()
	defer cancel()
	d, err := s.b.Get(c, collHeartbeat, id)
	var r Heartbeat
	if err != nil || d.Decode(&r) != nil {
		return Heartbeat{}, "", ErrNotFound
	}
	return r, d.Version, nil
}

func (s *Service) validateHeartbeat(tenant, id string, in *HeartbeatIn) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 || strings.ContainsAny(in.Name, "\r\n\x00") {
		return invalid("give it a name (up to 100 characters)")
	}
	if in.Period < 60 || in.Period > 366*86400 {
		return invalid("the interval must be between 1 minute and 366 days")
	}
	if in.Grace == 0 {
		in.Grace = in.Period / 10
		if in.Grace < 300 {
			in.Grace = 300
		}
		if in.Grace > 86400 {
			in.Grace = 86400
		}
	}
	if in.Grace < 0 || in.Grace > 7*86400 {
		return invalid("the extra time must be between 0 and 7 days")
	}
	for _, o := range s.ListHeartbeats(tenant) {
		if o.ID != id && strings.EqualFold(o.Name, in.Name) {
			return invalid("there is already a heartbeat with that name")
		}
	}
	return nil
}

func (s *Service) CreateHeartbeat(tenant string, in HeartbeatIn) (Heartbeat, error) {
	if err := s.validateHeartbeat(tenant, "", &in); err != nil {
		return Heartbeat{}, err
	}
	now := time.Now().UTC()
	r := Heartbeat{ID: newHeartbeatID(), Tenant: tenant, Name: in.Name, Period: in.Period, Grace: in.Grace, Created: now, Updated: now}
	c, cancel := ctx()
	defer cancel()
	return r, s.b.Create(c, collHeartbeat, r.ID, r)
}

func (s *Service) UpdateHeartbeat(tenant, id string, in HeartbeatIn) (Heartbeat, error) {
	cur, ver, err := s.getHeartbeat(id)
	if err != nil || cur.Tenant != tenant {
		return Heartbeat{}, ErrNotFound
	}
	if err := s.validateHeartbeat(tenant, id, &in); err != nil {
		return Heartbeat{}, err
	}
	cur.Name, cur.Period, cur.Grace = in.Name, in.Period, in.Grace
	cur.Updated = time.Now().UTC()
	c, cancel := ctx()
	defer cancel()
	return cur, s.b.Put(c, collHeartbeat, id, cur, ver)
}

func (s *Service) DeleteHeartbeat(tenant, id string) error {
	cur, _, err := s.getHeartbeat(id)
	if err != nil || cur.Tenant != tenant {
		return ErrNotFound
	}
	c, cancel := ctx()
	defer cancel()
	return s.b.Delete(c, collHeartbeat, id)
}

// Ping records a call from the job: ok = the job ran, !ok = the job says it failed. It returns the heartbeat as it is now.
// Calls that come faster than every 5 seconds are not written down again.
func (s *Service) Ping(id string, ok bool, now time.Time) (Heartbeat, error) {
	for try := 0; try < 4; try++ {
		cur, ver, err := s.getHeartbeat(id)
		if err != nil {
			return Heartbeat{}, err
		}
		n := now.Unix()
		if ok {
			if cur.LastPing > 0 && n-cur.LastPing < 5 {
				return cur, nil
			}
			cur.LastPing = n
			cur.Pings++
		} else {
			if cur.LastFail > 0 && n-cur.LastFail < 5 {
				return cur, nil
			}
			cur.LastFail = n
		}
		c, cancel := ctx()
		err = s.b.Put(c, collHeartbeat, id, cur, ver)
		cancel()
		if errors.Is(err, docstore.ErrConflict) {
			continue
		}
		return cur, err
	}
	return Heartbeat{}, errors.New("heartbeat is busy, try again")
}
