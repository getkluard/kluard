// Package retention holds how long Kluard keeps data. The values come from the environment (KLUARD_RETENTION_DAYS and
// KLUARD_BACKUP_RETENTION_DAYS) unless somebody chose others in Settings; a choice made in Settings wins and is kept in the
// document store, so it survives a restart.
package retention

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/getkluard/kluard/internal/docstore"
)

const (
	MinData = 1     // days of live data
	MaxData = 3650  // ten years
	MaxKeep = 36500 // backups: 0 means forever
)

const (
	coll = "meta"
	id   = "retention"
)

// Days is the live choice. Everything that needs it asks here, so a change applies without a restart.
type Days struct {
	mu           sync.RWMutex
	data, keep   int
	chosen       bool
	defData, def int
}

// New starts with the values from the environment.
func New(data, keep int) *Days { return &Days{data: data, keep: keep, defData: data, def: keep} }

// Get returns the days of live data and the days backups are kept (0 = forever).
func (d *Days) Get() (data, keep int) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.data, d.keep
}

// Default returns the values from the environment, and whether the current ones were chosen in Settings instead.
func (d *Days) Default() (data, keep int, chosen bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.defData, d.def, d.chosen
}

func (d *Days) Set(data, keep int, chosen bool) {
	d.mu.Lock()
	d.data, d.keep, d.chosen = data, keep, chosen
	d.mu.Unlock()
}

// Validate says what is wrong with a choice, in words for the person who made it.
func Validate(data, keep int) error {
	if data < MinData || data > MaxData {
		return fmt.Errorf("live data can be kept for %d to %d days", MinData, MaxData)
	}
	if keep < 0 || keep > MaxKeep {
		return fmt.Errorf("backups can be kept for 0 (forever) to %d days", MaxKeep)
	}
	return nil
}

type saved struct {
	Data int       `json:"data_days"`
	Keep int       `json:"backup_days"`
	By   string    `json:"by"`
	At   time.Time `json:"at"`
}

// Load reads what somebody chose in Settings.
func Load(ctx context.Context, db docstore.Backend) (data, keep int, ok bool) {
	d, err := db.Get(ctx, coll, id)
	if err != nil {
		return 0, 0, false
	}
	var s saved
	if d.Decode(&s) != nil || Validate(s.Data, s.Keep) != nil {
		return 0, 0, false
	}
	return s.Data, s.Keep, true
}

// Save remembers the choice.
func Save(ctx context.Context, db docstore.Backend, data, keep int, by string) error {
	return db.Put(ctx, coll, id, saved{Data: data, Keep: keep, By: by, At: time.Now().UTC()}, "")
}

// Clear forgets the choice, so the values from the environment apply again.
func Clear(ctx context.Context, db docstore.Backend) error { return db.Delete(ctx, coll, id) }
