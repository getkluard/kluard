package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/getkluard/kluard/internal/edition"
	"github.com/getkluard/kluard/internal/model"
	"github.com/getkluard/kluard/internal/perm"
	"github.com/getkluard/kluard/internal/registry"
)

// Heartbeats: a job (a backup, a cron job) calls an address when it has run, and Kluard raises an alarm when the call does
// not come in time. The address holds a long random id and needs no login, so a plain curl in a script is enough.

type heartbeatIn struct {
	Name   string `json:"name"`
	Period int    `json:"period"`
	Grace  int    `json:"grace"`
}

type heartbeatOut struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Period   int    `json:"period"`
	Grace    int    `json:"grace"`
	State    string `json:"state"`
	LastPing int64  `json:"last_ping"`
	LastFail int64  `json:"last_fail"`
	Pings    int    `json:"pings"`
	Path     string `json:"path"`
	URL      string `json:"url,omitempty"`
}

func (s *Server) heartbeatOut(h registry.Heartbeat, now time.Time) heartbeatOut {
	o := heartbeatOut{ID: h.ID, Name: h.Name, Period: h.Period, Grace: h.Grace, State: h.State(now), LastPing: h.LastPing, LastFail: h.LastFail, Pings: h.Pings, Path: "/hb/" + h.ID}
	if s.publicURL != "" {
		o.URL = strings.TrimRight(s.publicURL, "/") + o.Path
	}
	return o
}

func (s *Server) heartbeatRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/heartbeats", s.need(perm.Hosts, false, s.listHeartbeats))
	mux.Handle("POST /api/v1/heartbeats", s.need(perm.Hosts, true, s.createHeartbeat))
	mux.Handle("PUT /api/v1/heartbeats/{id}", s.need(perm.Hosts, true, s.updateHeartbeat))
	mux.Handle("DELETE /api/v1/heartbeats/{id}", s.need(perm.Hosts, true, s.deleteHeartbeat))
	for _, m := range []string{"GET", "POST"} { // GET also answers HEAD
		mux.HandleFunc(m+" /hb/{id}", s.pingHeartbeat(true))
		mux.HandleFunc(m+" /hb/{id}/fail", s.pingHeartbeat(false))
	}
}

func (s *Server) pingHeartbeat(ok bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		id := r.PathValue("id")
		if !registry.ValidHeartbeatID(id) {
			http.NotFound(w, r)
			return
		}
		h, err := s.reg.Ping(id, ok, time.Now())
		if err != nil {
			if err == registry.ErrNotFound {
				http.NotFound(w, r)
				return
			}
			s.log.Error("heartbeat", "err", err)
			http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
			return
		}
		_ = h
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("ok\n"))
	}
}

func (s *Server) listHeartbeats(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	now := time.Now()
	out := []heartbeatOut{}
	for _, h := range s.reg.ListHeartbeats(id.Tenant) {
		out = append(out, s.heartbeatOut(h, now))
	}
	writeJSON(w, map[string]any{"data": out})
}

func (s *Server) createHeartbeat(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	if len(s.reg.ListHeartbeats(id.Tenant)) >= 500 {
		writeErr(w, http.StatusBadRequest, "too many heartbeats are set up here (max 500)")
		return
	}
	var in heartbeatIn
	if !readJSON(w, r, &in) {
		return
	}
	h, err := s.reg.CreateHeartbeat(id.Tenant, registry.HeartbeatIn{Name: in.Name, Period: in.Period, Grace: in.Grace})
	if err != nil {
		s.regErr(w, err)
		return
	}
	s.auditOp(id, id.Tenant, "heartbeat.add", h.ID, h.Name)
	writeJSON(w, s.heartbeatOut(h, time.Now()))
}

func (s *Server) updateHeartbeat(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	var in heartbeatIn
	if !readJSON(w, r, &in) {
		return
	}
	h, err := s.reg.UpdateHeartbeat(id.Tenant, r.PathValue("id"), registry.HeartbeatIn{Name: in.Name, Period: in.Period, Grace: in.Grace})
	if err != nil {
		s.regErr(w, err)
		return
	}
	s.auditOp(id, id.Tenant, "heartbeat.change", h.ID, h.Name)
	writeJSON(w, s.heartbeatOut(h, time.Now()))
}

func (s *Server) deleteHeartbeat(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	if err := s.reg.DeleteHeartbeat(id.Tenant, r.PathValue("id")); err != nil {
		s.regErr(w, err)
		return
	}
	s.auditOp(id, id.Tenant, "heartbeat.remove", r.PathValue("id"), "")
	writeJSON(w, map[string]bool{"ok": true})
}

// heartbeatPoints are the metrics of every heartbeat: heartbeat_up is 1 while the job is on time (or has not had its first
// turn yet) and 0 when it is late or reported a failure. Alert rules use that.
func heartbeatPoints(hs []registry.Heartbeat, now time.Time) []model.MetricPoint {
	ts := model.FormatTime(now.UnixNano())
	var rows []model.MetricPoint
	for _, h := range hs {
		a := map[string]string{"heartbeat": h.Name}
		up := 1.0
		if st := h.State(now); st == "late" || st == "failed" {
			up = 0
		}
		rows = append(rows, model.MetricPoint{Tenant: h.Tenant, Timestamp: ts, Name: "heartbeat_up", Type: "gauge", Service: "heartbeat", Value: up, Attrs: a})
		if h.LastPing > 0 {
			rows = append(rows, model.MetricPoint{Tenant: h.Tenant, Timestamp: ts, Name: "heartbeat_age_seconds", Type: "gauge", Service: "heartbeat", Value: float64(now.Unix() - h.LastPing), Attrs: a})
		}
	}
	return rows
}

// RunHeartbeats writes the heartbeat metrics every 30 seconds until ctx ends.
func (s *Server) RunHeartbeats(ctx context.Context) {
	if s.reg == nil {
		return
	}
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		if rows := heartbeatPoints(s.reg.AllHeartbeats(), time.Now()); len(rows) > 0 {
			c, cancel := context.WithTimeout(ctx, 20*time.Second)
			if err := s.store.InsertMetrics(c, rows); err != nil && ctx.Err() == nil {
				s.log.Warn("heartbeat metrics not stored", "err", err)
			}
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
