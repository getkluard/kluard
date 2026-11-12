package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/getkluard/kluard/internal/docstore"
	"github.com/getkluard/kluard/internal/edition"
	"github.com/getkluard/kluard/internal/perm"
	"github.com/getkluard/kluard/internal/retention"
)

// RetentionApplier makes the stored telemetry expire after a number of days.
type RetentionApplier interface {
	ApplyRetention(ctx context.Context, days int) error
}

// Retention is what the setting "how long data is kept" needs.
type Retention struct {
	DB   docstore.Backend
	Days *retention.Days
	CH   RetentionApplier
}

func (s *Server) WithRetention(r *Retention) *Server { s.retention = r; return s }

func (s *Server) retentionRoutes(mux *http.ServeMux) {
	if s.retention == nil {
		return
	}
	mux.Handle("GET /api/v1/retention", s.need(perm.Backups, false, s.getRetention))
	mux.Handle("PUT /api/v1/retention", s.need(perm.Backups, true, s.putRetention))
	mux.Handle("DELETE /api/v1/retention", s.need(perm.Backups, true, s.deleteRetention))
}

func (s *Server) retentionView() map[string]any {
	d, k := s.retention.Days.Get()
	dd, dk, chosen := s.retention.Days.Default()
	src := "default"
	if chosen {
		src = "chosen"
	}
	return map[string]any{"data_days": d, "backup_days": k, "default_data_days": dd, "default_backup_days": dk, "source": src,
		"min_data_days": retention.MinData, "max_data_days": retention.MaxData, "max_backup_days": retention.MaxKeep, "backups": s.bk != nil}
}

func (s *Server) getRetention(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	if !s.ownerOnly(w, id) {
		return
	}
	writeJSON(w, s.retentionView())
}

// putRetention chooses the days. Live data older than the new limit is removed by ClickHouse in the background, so it is
// a change that cannot be undone for those days: the screen says so, and the backup of the days that are about to go is
// the person's to run first.
func (s *Server) putRetention(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	if !s.ownerOnly(w, id) {
		return
	}
	var in struct {
		Data int `json:"data_days"`
		Keep int `json:"backup_days"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if err := retention.Validate(in.Data, in.Keep); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.applyRetention(w, r, id, in.Data, in.Keep, true)
}

func (s *Server) deleteRetention(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	if !s.ownerOnly(w, id) {
		return
	}
	d, k, _ := s.retention.Days.Default()
	s.applyRetention(w, r, id, d, k, false)
}

func (s *Server) applyRetention(w http.ResponseWriter, r *http.Request, id edition.Identity, data, keep int, chosen bool) {
	rt := s.retention
	prevData, prevKeep := rt.Days.Get()
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err := rt.CH.ApplyRetention(ctx, data); err != nil {
		s.log.Error("changing the retention failed", "err", err)
		writeErr(w, http.StatusBadGateway, "ClickHouse did not accept the change: "+err.Error())
		return
	}
	var err error
	if chosen {
		err = retention.Save(r.Context(), rt.DB, data, keep, id.User)
	} else {
		err = retention.Clear(r.Context(), rt.DB)
	}
	if err != nil {
		_ = rt.CH.ApplyRetention(ctx, prevData) // keep the two in step: what is stored is what applies
		writeErr(w, http.StatusServiceUnavailable, "could not save the setting")
		return
	}
	rt.Days.Set(data, keep, chosen)
	s.auditOp(id, id.Tenant, "retention.set", fmt.Sprintf("data %d days, backups %d days", data, keep), fmt.Sprintf("from %d and %d", prevData, prevKeep))
	writeJSON(w, s.retentionView())
}
