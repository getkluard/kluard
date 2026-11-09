package server

import (
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/getkluard/kluard/internal/edition"
)

// An agent whose key the server does not know used to say so only in its own log ("401 Unauthorized"), and nothing on the
// server told which key it was. The server now remembers the refused keys of the last day (only the first 8 characters of
// each, never the whole key), writes a warning to its log at most once a minute per key, and shows them under Hosts.

const (
	rejectKeep    = 24 * time.Hour
	rejectLogGap  = time.Minute
	rejectMaxKeys = 50
)

type rejectedKey struct {
	Prefix string    `json:"prefix"` // the first 8 characters of the key that was refused
	From   string    `json:"from"`   // the address the requests came from
	Count  int       `json:"count"`
	First  time.Time `json:"first"`
	Last   time.Time `json:"last"`
	logged time.Time
}

type rejectLog struct {
	mu sync.Mutex
	m  map[string]*rejectedKey
}

// presentedKey is the API key a request carries, if any.
func presentedKey(r *http.Request) string {
	if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return strings.TrimSpace(r.Header.Get("X-API-Key"))
}

func (s *Server) noteRejectedKey(r *http.Request) {
	key := presentedKey(r)
	if key == "" {
		return
	}
	prefix := key
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	from, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		from = r.RemoteAddr
	}
	now := time.Now()
	l := &s.rejected
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]*rejectedKey{}
	}
	id := prefix + "@" + from
	e := l.m[id]
	if e == nil {
		for k, v := range l.m { // forget what is old, and keep the list small
			if now.Sub(v.Last) > rejectKeep {
				delete(l.m, k)
			}
		}
		if len(l.m) >= rejectMaxKeys {
			return
		}
		e = &rejectedKey{Prefix: prefix, From: from, First: now}
		l.m[id] = e
	}
	e.Count++
	e.Last = now
	if s.log != nil && now.Sub(e.logged) >= rejectLogGap {
		e.logged = now
		s.log.Warn("agent key refused: the server does not know this key", "key_starts_with", prefix, "from", from, "refused", e.Count,
			"hint", "create a key under Add a machine, or use a key from KLUARD_API_KEYS, and put it in the agent's config.json")
	}
}

// recentRejected lists the keys refused during the last day, most recent first.
func (s *Server) recentRejected() []rejectedKey {
	l := &s.rejected
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []rejectedKey{}
	now := time.Now()
	for _, v := range l.m {
		if now.Sub(v.Last) <= rejectKeep {
			out = append(out, *v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}

func (s *Server) listRejectedKeys(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	writeJSON(w, map[string]any{"rejected": s.recentRejected()})
}
