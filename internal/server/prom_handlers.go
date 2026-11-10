package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/getkluard/kluard/internal/edition"
	"github.com/getkluard/kluard/internal/model"
	"github.com/getkluard/kluard/internal/perm"
	"github.com/getkluard/kluard/internal/promql"
	"github.com/getkluard/kluard/internal/store"
)

// The Prometheus-compatible read API. Point Grafana's Prometheus data source at https://<server>/prom and give it a read key
// (as a bearer token or as the password of basic auth). Metric names and label names are shown with dots turned into
// underscores (system.cpu.load becomes system_cpu_load), as Prometheus requires.

const (
	promMaxNames   = 50     // metrics one selector may expand to
	promMaxSeries  = 20000  // series one query may load
	promMaxSamples = 500000 // samples one metric may load
)

func (s *Server) promRoutes(mux *http.ServeMux) {
	for _, m := range []string{"GET", "POST"} {
		mux.Handle(m+" /prom/api/v1/query", s.need(perm.Metrics, false, s.promQuery))
		mux.Handle(m+" /prom/api/v1/query_range", s.need(perm.Metrics, false, s.promQueryRange))
		mux.Handle(m+" /prom/api/v1/series", s.need(perm.Metrics, false, s.promSeries))
		mux.Handle(m+" /prom/api/v1/labels", s.need(perm.Metrics, false, s.promLabels))
	}
	mux.Handle("GET /prom/api/v1/label/{name}/values", s.need(perm.Metrics, false, s.promLabelValues))
	mux.Handle("GET /prom/api/v1/metadata", s.need(perm.Metrics, false, s.promMetadata))
	mux.Handle("GET /prom/api/v1/status/buildinfo", s.need(perm.Metrics, false, s.promBuildInfo))
	mux.Handle("GET /prom/api/v1/query_exemplars", s.need(perm.Metrics, false, func(w http.ResponseWriter, _ *http.Request, _ edition.Identity) {
		promOK(w, []any{})
	}))
}

func promOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": data})
}

func promErr(w http.ResponseWriter, code int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"status": "error", "errorType": typ, "error": msg})
}

func promTime(s string, def time.Time) (time.Time, error) {
	if s == "" {
		return def, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		sec := math.Floor(f)
		return time.Unix(int64(sec), int64((f-sec)*1e9)), nil
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid time %q", s)
}

func promStep(s string) (time.Duration, error) {
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 {
		return time.Duration(f * float64(time.Second)), nil
	}
	if d, err := promql.ParseDuration(s); err == nil && d > 0 {
		return d, nil
	}
	return 0, fmt.Errorf("invalid step %q", s)
}

func promNum(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func promSec(ms int64) float64 { return float64(ms) / 1000 }

// formValue reads a parameter from the query string or, for POST, the form body.
func formValues(r *http.Request) {
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
		_ = r.ParseForm()
	} else {
		_ = r.ParseForm()
	}
}

func (s *Server) promFail(w http.ResponseWriter, err error) {
	var pe promError
	if asProm(err, &pe) {
		promErr(w, pe.code, pe.typ, pe.msg)
		return
	}
	msg := err.Error()
	// a failure inside the store is not the caller's fault and its text may name the database
	if strings.HasPrefix(msg, "store:") {
		s.log.Error("prom query failed", "err", err)
		promErr(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	promErr(w, http.StatusUnprocessableEntity, "execution", msg)
}

type promError struct {
	code     int
	typ, msg string
}

func (e promError) Error() string { return e.msg }

func asProm(err error, out *promError) bool {
	if e, ok := err.(promError); ok {
		*out = e
		return true
	}
	return false
}

func (s *Server) promQuery(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	formValues(r)
	q := r.Form.Get("query")
	x, err := promql.Parse(q)
	if err != nil {
		promErr(w, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	at, err := promTime(r.Form.Get("time"), time.Now())
	if err != nil {
		promErr(w, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	v, err := promql.Instant(ctx, x, at, s.promFetcher(id.Tenant), promql.Options{})
	if err != nil {
		s.promFail(w, err)
		return
	}
	ts := promSec(at.UnixMilli())
	switch {
	case v.Scalar:
		promOK(w, map[string]any{"resultType": "scalar", "result": []any{ts, promNum(v.F)}})
	case v.IsMat:
		promOK(w, map[string]any{"resultType": "matrix", "result": promMatrix(v.Mat)})
	default:
		res := make([]any, 0, len(v.Vec))
		for _, smp := range v.Vec {
			res = append(res, map[string]any{"metric": smp.Labels, "value": []any{ts, promNum(smp.V)}})
		}
		promOK(w, map[string]any{"resultType": "vector", "result": res})
	}
}

func promMatrix(m []promql.Series) []any {
	res := make([]any, 0, len(m))
	for _, se := range m {
		vals := make([]any, len(se.T))
		for i := range se.T {
			vals[i] = []any{promSec(se.T[i]), promNum(se.V[i])}
		}
		res = append(res, map[string]any{"metric": se.Labels, "values": vals})
	}
	return res
}

func (s *Server) promQueryRange(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	formValues(r)
	x, err := promql.Parse(r.Form.Get("query"))
	if err != nil {
		promErr(w, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	start, err1 := promTime(r.Form.Get("start"), time.Time{})
	end, err2 := promTime(r.Form.Get("end"), time.Time{})
	if err1 != nil || err2 != nil || start.IsZero() || end.IsZero() {
		promErr(w, http.StatusBadRequest, "bad_data", "start and end are required (unix seconds or RFC 3339)")
		return
	}
	step, err := promStep(r.Form.Get("step"))
	if err != nil {
		promErr(w, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	if end.Sub(start) > maxRange {
		promErr(w, http.StatusBadRequest, "bad_data", "the time range may be at most 90 days")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	m, err := promql.Range(ctx, x, start, end, step, s.promFetcher(id.Tenant), promql.Options{})
	if err != nil {
		if strings.Contains(err.Error(), "maximum resolution") || strings.Contains(err.Error(), "end is before") || strings.Contains(err.Error(), "cannot be graphed") {
			promErr(w, http.StatusBadRequest, "bad_data", err.Error())
			return
		}
		s.promFail(w, err)
		return
	}
	promOK(w, map[string]any{"resultType": "matrix", "result": promMatrix(m)})
}

// ---- selectors against the store ----

func isEqName(m promql.Matcher) bool { return m.Name == "__name__" && m.Op == "=" }

var pushable = regexp.MustCompile(`^[a-z0-9]+$`)

// metricNamesFor expands the name part of a selector into stored metric names (in Prometheus form).
func (s *Server) promNamesFor(ctx context.Context, tenant string, vs *promql.VectorSel, from, to time.Time) ([]string, error) {
	ms := append([]promql.Matcher(nil), vs.Matchers...)
	if vs.Name != "" {
		ms = append(ms, promql.Matcher{Name: "__name__", Op: "=", Value: vs.Name})
	}
	var nameMs []promql.Matcher
	for _, m := range ms {
		if m.Name != "__name__" {
			continue
		}
		if isEqName(m) {
			return []string{m.Value}, nil
		}
		nameMs = append(nameMs, m)
	}
	if len(nameMs) == 0 {
		return nil, promError{http.StatusBadRequest, "bad_data", "the selector needs a metric name (for example up{...} or {__name__=~\"system_.*\"})"}
	}
	rows, err := s.store.MetricNames(ctx, tenant, "", from, to)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	seen := map[string]bool{}
	var out []string
	for _, row := range rows {
		var n struct{ Name string }
		if json.Unmarshal(row, &n) != nil {
			continue
		}
		p := store.PromName(n.Name)
		if !seen[p] && promql.MatchLabels(promql.Labels{"__name__": p}, nameMs) {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) > promMaxNames {
		return nil, promError{http.StatusUnprocessableEntity, "execution", fmt.Sprintf("the selector matches %d metrics (limit %d); narrow it down", len(out), promMaxNames)}
	}
	return out, nil
}

func promLabelName(k string) string { return store.PromName(k) }

// promFetcher adapts the store to the evaluator for one tenant.
func (s *Server) promFetcher(tenant string) promql.Fetcher {
	return func(ctx context.Context, vs *promql.VectorSel, from, to time.Time) ([]promql.Series, error) {
		names, err := s.promNamesFor(ctx, tenant, vs, from, to)
		if err != nil {
			return nil, err
		}
		all := append([]promql.Matcher(nil), vs.Matchers...)
		eq := map[string]string{}
		for _, m := range vs.Matchers {
			if m.Op == "=" && m.Name != "__name__" && (m.Name == "service" || pushable.MatchString(m.Name)) {
				eq[m.Name] = m.Value
			}
		}
		var out []promql.Series
		for _, name := range names {
			samples, err := s.store.Samples(ctx, tenant, model.SampleQuery{Name: name, Equal: eq, From: from, To: to, Limit: promMaxSamples})
			if err != nil {
				return nil, fmt.Errorf("store: %w", err)
			}
			if len(samples) >= promMaxSamples {
				return nil, promError{http.StatusUnprocessableEntity, "execution", fmt.Sprintf("%s has too many samples in this range (limit %d); shorten the range, add label matchers or raise the step", name, promMaxSamples)}
			}
			byKey := map[string]*promql.Series{}
			for _, smp := range samples {
				l := promql.Labels{"__name__": name}
				if smp.Service != "" {
					l["service"] = smp.Service
				}
				for k, v := range smp.Attrs {
					l[promLabelName(k)] = v
				}
				key := fmt.Sprint(smp.Service, smp.Attrs)
				se := byKey[key]
				if se == nil {
					if !promql.MatchLabels(l, all) {
						byKey[key] = &promql.Series{} // remembered as a miss
						continue
					}
					se = &promql.Series{Labels: l}
					byKey[key] = se
				}
				if se.Labels == nil {
					continue
				}
				// samples arrive ordered by time; a second sample at the same millisecond replaces the first
				if n := len(se.T); n > 0 && se.T[n-1] == smp.T {
					se.V[n-1] = smp.V
					continue
				}
				se.T = append(se.T, smp.T)
				se.V = append(se.V, smp.V)
			}
			for _, se := range byKey {
				if se.Labels != nil {
					out = append(out, *se)
				}
			}
			if len(out) > promMaxSeries {
				return nil, promError{http.StatusUnprocessableEntity, "execution", fmt.Sprintf("more than %d series; add label matchers", promMaxSeries)}
			}
		}
		sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i].Labels) < fmt.Sprint(out[j].Labels) })
		return out, nil
	}
}

// ---- discovery ----

func promWindow(r *http.Request) (time.Time, time.Time, error) {
	end, err := promTime(r.Form.Get("end"), time.Now())
	if err != nil {
		return end, end, err
	}
	start, err := promTime(r.Form.Get("start"), end.Add(-time.Hour))
	if err != nil {
		return start, end, err
	}
	if !start.Before(end) {
		return start, end, fmt.Errorf("start must be before end")
	}
	if end.Sub(start) > maxRange {
		return start, end, fmt.Errorf("the time range may be at most 90 days")
	}
	return start, end, nil
}

// matchSets runs every match[] selector and returns the label sets found.
func (s *Server) matchSets(ctx context.Context, tenant string, sel []string, from, to time.Time) ([]promql.Labels, error) {
	var out []promql.Labels
	seen := map[string]bool{}
	for _, m := range sel {
		x, err := promql.Parse(m)
		if err != nil {
			return nil, promError{http.StatusBadRequest, "bad_data", err.Error()}
		}
		vs, ok := x.(*promql.VectorSel)
		if !ok {
			return nil, promError{http.StatusBadRequest, "bad_data", "match[] must be a plain selector such as up{job=\"x\"}"}
		}
		ser, err := s.promFetcher(tenant)(ctx, vs, from, to)
		if err != nil {
			return nil, err
		}
		for _, se := range ser {
			k := fmt.Sprint(se.Labels)
			if !seen[k] {
				seen[k] = true
				out = append(out, se.Labels)
			}
		}
	}
	return out, nil
}

func (s *Server) promSeries(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	formValues(r)
	sel := r.Form["match[]"]
	if len(sel) == 0 {
		promErr(w, http.StatusBadRequest, "bad_data", "at least one match[] selector is required")
		return
	}
	from, to, err := promWindow(r)
	if err != nil {
		promErr(w, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	sets, err := s.matchSets(ctx, id.Tenant, sel, from, to)
	if err != nil {
		s.promFail(w, err)
		return
	}
	if sets == nil {
		sets = []promql.Labels{}
	}
	promOK(w, sets)
}

// namesAndLabels lists metric names with the label names and a few values they carry, from the store's own discovery queries.
func (s *Server) namesAndLabels(ctx context.Context, tenant string, from, to time.Time, maxNames int) (names []string, labels map[string]map[string]bool, err error) {
	rows, err := s.store.MetricNames(ctx, tenant, "", from, to)
	if err != nil {
		return nil, nil, fmt.Errorf("store: %w", err)
	}
	labels = map[string]map[string]bool{"__name__": {}, "service": {}}
	raw := map[string]string{}
	for _, row := range rows {
		var n struct{ Name string }
		if json.Unmarshal(row, &n) == nil && n.Name != "" {
			p := store.PromName(n.Name)
			if _, ok := raw[p]; !ok {
				raw[p] = n.Name
				names = append(names, p)
				labels["__name__"][p] = true
			}
		}
	}
	sort.Strings(names)
	for i, p := range names {
		if i >= maxNames {
			break
		}
		lr, err := s.store.MetricLabels(ctx, tenant, raw[p], from, to)
		if err != nil {
			return nil, nil, fmt.Errorf("store: %w", err)
		}
		for _, row := range lr {
			var l struct {
				Label string
				Vals  []string
			}
			if json.Unmarshal(row, &l) != nil {
				continue
			}
			k := promLabelName(l.Label)
			if labels[k] == nil {
				labels[k] = map[string]bool{}
			}
			for _, v := range l.Vals {
				labels[k][v] = true
			}
		}
	}
	return names, labels, nil
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *Server) promLabels(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	formValues(r)
	from, to, err := promWindow(r)
	if err != nil {
		promErr(w, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	set := map[string]bool{}
	if sel := r.Form["match[]"]; len(sel) > 0 {
		sets, err := s.matchSets(ctx, id.Tenant, sel, from, to)
		if err != nil {
			s.promFail(w, err)
			return
		}
		for _, l := range sets {
			for k := range l {
				set[k] = true
			}
		}
	} else {
		_, labels, err := s.namesAndLabels(ctx, id.Tenant, from, to, 30)
		if err != nil {
			s.promFail(w, err)
			return
		}
		for k := range labels {
			set[k] = true
		}
	}
	promOK(w, sortedSet(set))
}

func (s *Server) promLabelValues(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	formValues(r)
	label := r.PathValue("name")
	from, to, err := promWindow(r)
	if err != nil {
		promErr(w, http.StatusBadRequest, "bad_data", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	set := map[string]bool{}
	if sel := r.Form["match[]"]; len(sel) > 0 {
		sets, err := s.matchSets(ctx, id.Tenant, sel, from, to)
		if err != nil {
			s.promFail(w, err)
			return
		}
		for _, l := range sets {
			if v, ok := l[label]; ok {
				set[v] = true
			}
		}
	} else {
		_, labels, err := s.namesAndLabels(ctx, id.Tenant, from, to, 30)
		if err != nil {
			s.promFail(w, err)
			return
		}
		set = labels[label]
		if label == "service" {
			if rows, err := s.store.Services(ctx, id.Tenant, from, to); err == nil {
				set = map[string]bool{}
				for _, row := range rows {
					var sv struct{ Svc string }
					if json.Unmarshal(row, &sv) == nil && sv.Svc != "" {
						set[sv.Svc] = true
					}
				}
			}
		}
	}
	promOK(w, sortedSet(set))
}

func (s *Server) promMetadata(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	rows, err := s.store.MetricNames(ctx, id.Tenant, "", time.Now().Add(-24*time.Hour), time.Now())
	if err != nil {
		s.promFail(w, fmt.Errorf("store: %w", err))
		return
	}
	out := map[string][]map[string]string{}
	for _, row := range rows {
		var n struct{ Name, Mtype string }
		if json.Unmarshal(row, &n) != nil || n.Name == "" {
			continue
		}
		typ := "gauge"
		if n.Mtype == "sum" || n.Mtype == "counter" {
			typ = "counter"
		}
		out[store.PromName(n.Name)] = []map[string]string{{"type": typ, "help": n.Name, "unit": ""}}
	}
	promOK(w, out)
}

func (s *Server) promBuildInfo(w http.ResponseWriter, _ *http.Request, _ edition.Identity) {
	promOK(w, map[string]string{"version": "2.45.0", "application": "kluard", "revision": "", "branch": "", "buildUser": "", "buildDate": "", "goVersion": ""})
}
