package server

import (
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/getkluard/kluard/internal/edition"
	"github.com/getkluard/kluard/internal/perm"
	"github.com/getkluard/kluard/internal/registry"
)

// Status pages: a public address that shows customers whether their service is up. It shows only the names the owner chose
// and a state for each, never addresses, machines or error texts. The id in the address is the secret, like a heartbeat's.

type statusPageIn struct {
	Title   string                `json:"title"`
	Message string                `json:"message"`
	Items   []registry.StatusItem `json:"items"`
}

type statusPageOut struct {
	ID      string                `json:"id"`
	Title   string                `json:"title"`
	Message string                `json:"message"`
	Items   []registry.StatusItem `json:"items"`
	Path    string                `json:"path"`
	URL     string                `json:"url,omitempty"`
}

type statusChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Server) statusPageOut(p registry.StatusPage) statusPageOut {
	o := statusPageOut{ID: p.ID, Title: p.Title, Message: p.Message, Items: p.Items, Path: "/status/" + p.ID}
	if s.publicURL != "" {
		o.URL = strings.TrimRight(s.publicURL, "/") + o.Path
	}
	return o
}

func (s *Server) statusPageRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/statuspages", s.need(perm.Hosts, false, s.listStatusPages))
	mux.Handle("POST /api/v1/statuspages", s.need(perm.Hosts, true, s.createStatusPage))
	mux.Handle("PUT /api/v1/statuspages/{id}", s.need(perm.Hosts, true, s.updateStatusPage))
	mux.Handle("DELETE /api/v1/statuspages/{id}", s.need(perm.Hosts, true, s.deleteStatusPage))
	mux.HandleFunc("GET /status/{id}", s.publicStatus(false))
	mux.HandleFunc("GET /status/{id}/json", s.publicStatus(true))
}

func (s *Server) listStatusPages(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	out := []statusPageOut{}
	for _, p := range s.reg.ListStatusPages(id.Tenant) {
		out = append(out, s.statusPageOut(p))
	}
	inst, web := []statusChoice{}, []statusChoice{}
	for _, i := range s.reg.ListInstances(id.Tenant) {
		inst = append(inst, statusChoice{i.ID, i.Name})
	}
	for _, x := range s.reg.ListWeb(id.Tenant) {
		web = append(web, statusChoice{x.ID, x.Name})
	}
	writeJSON(w, map[string]any{"data": out, "instances": inst, "web": web})
}

func (s *Server) createStatusPage(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	if len(s.reg.ListStatusPages(id.Tenant)) >= 50 {
		writeErr(w, http.StatusBadRequest, "too many status pages are set up here (max 50)")
		return
	}
	var in statusPageIn
	if !readJSON(w, r, &in) {
		return
	}
	p, err := s.reg.CreateStatusPage(id.Tenant, registry.StatusPageIn{Title: in.Title, Message: in.Message, Items: in.Items})
	if err != nil {
		s.regErr(w, err)
		return
	}
	s.auditOp(id, id.Tenant, "statuspage.add", p.ID, p.Title)
	writeJSON(w, s.statusPageOut(p))
}

func (s *Server) updateStatusPage(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	var in statusPageIn
	if !readJSON(w, r, &in) {
		return
	}
	p, err := s.reg.UpdateStatusPage(id.Tenant, r.PathValue("id"), registry.StatusPageIn{Title: in.Title, Message: in.Message, Items: in.Items})
	if err != nil {
		s.regErr(w, err)
		return
	}
	s.auditOp(id, id.Tenant, "statuspage.change", p.ID, p.Title)
	writeJSON(w, s.statusPageOut(p))
}

func (s *Server) deleteStatusPage(w http.ResponseWriter, r *http.Request, id edition.Identity) {
	if err := s.reg.DeleteStatusPage(id.Tenant, r.PathValue("id")); err != nil {
		s.regErr(w, err)
		return
	}
	s.auditOp(id, id.Tenant, "statuspage.remove", r.PathValue("id"), "")
	writeJSON(w, map[string]bool{"ok": true})
}

// publicLine is one line of the public page. State is up, down or unknown (no report yet).
type publicLine struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

type publicStatus struct {
	Title   string       `json:"title"`
	Message string       `json:"message,omitempty"`
	Overall string       `json:"overall"` // operational | partial | outage | unknown
	Items   []publicLine `json:"items"`
	Updated int64        `json:"updated"`
}

func normState(s string) string {
	if s == "up" || s == "down" {
		return s
	}
	return "unknown"
}

func (s *Server) buildPublicStatus(r *http.Request, p registry.StatusPage) (publicStatus, error) {
	sn, err := s.snapshot(r.Context(), p.Tenant)
	if err != nil {
		return publicStatus{}, err
	}
	inst := map[string]string{}
	for _, i := range sn.sum.Instances {
		inst[i.ID] = i.Status
	}
	web := map[string]string{}
	for _, x := range sn.sum.Web {
		web[x.ID] = x.State
	}
	out := publicStatus{Title: p.Title, Message: p.Message, Items: []publicLine{}, Updated: sn.at.Unix()}
	up, down := 0, 0
	for _, it := range p.Items {
		var st string
		var ok bool
		if it.Kind == "instance" {
			st, ok = inst[it.Ref]
		} else {
			st, ok = web[it.Ref]
		}
		if !ok {
			continue // removed since the page was made
		}
		l := publicLine{Name: it.Label, State: normState(st)}
		switch l.State {
		case "up":
			up++
		case "down":
			down++
		}
		out.Items = append(out.Items, l)
	}
	switch {
	case down == 0 && up > 0:
		out.Overall = "operational"
	case down > 0 && up > 0:
		out.Overall = "partial"
	case down > 0:
		out.Overall = "outage"
	default:
		out.Overall = "unknown"
	}
	return out, nil
}

func (s *Server) publicStatus(asJSON bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Robots-Tag", "noindex, nofollow")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors *")
		h.Set("Cache-Control", "public, max-age=15")
		p, err := s.reg.GetStatusPage(r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		st, err := s.buildPublicStatus(r, p)
		if err != nil {
			s.log.Error("status page", "err", err)
			http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
			return
		}
		if asJSON {
			h.Set("Access-Control-Allow-Origin", "*")
			writeJSON(w, st)
			return
		}
		h.Set("Content-Type", "text/html; charset=utf-8")
		_ = statusTmpl.Execute(w, struct {
			publicStatus
			Headline string
			Time     string
		}{st, statusHeadline(st.Overall), time.Unix(st.Updated, 0).UTC().Format("2006-01-02 15:04 UTC")})
	}
}

func statusHeadline(o string) string {
	switch o {
	case "operational":
		return "Everything works"
	case "partial":
		return "Some services have problems"
	case "outage":
		return "Services are down"
	}
	return "Waiting for the first report"
}

var statusTmpl = template.Must(template.New("status").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex,nofollow"><meta http-equiv="refresh" content="60">
<title>{{.Title}}</title>
<style>
:root{--bg:#f6f7f9;--card:#fff;--tx:#1b1f24;--mut:#667085;--line:#e4e7ec;--ok:#12805c;--bad:#c62828;--warn:#b26a00;--unk:#98a2b3}
@media(prefers-color-scheme:dark){:root{--bg:#0f1317;--card:#171c22;--tx:#e8ecf1;--mut:#9aa4b2;--line:#27303a;--ok:#3ccf91;--bad:#ff6b6b;--warn:#f5a524;--unk:#6b7684}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--tx);font:16px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif}
main{max-width:680px;margin:0 auto;padding:32px 16px 48px}
h1{font-size:1.5rem;margin:0 0 4px}p.msg{color:var(--mut);margin:0 0 20px}
.banner{border-radius:12px;padding:16px 18px;margin:20px 0;font-weight:600;color:#fff}
.banner.operational{background:var(--ok)}.banner.partial{background:var(--warn)}.banner.outage{background:var(--bad)}.banner.unknown{background:var(--unk)}
ul{list-style:none;margin:0;padding:0;background:var(--card);border:1px solid var(--line);border-radius:12px}
li{display:flex;justify-content:space-between;gap:12px;padding:14px 18px;border-top:1px solid var(--line)}li:first-child{border-top:0}
.s{font-weight:600;white-space:nowrap}.s.up{color:var(--ok)}.s.down{color:var(--bad)}.s.unknown{color:var(--unk)}
footer{color:var(--mut);font-size:.85rem;margin-top:16px}
</style></head><body><main>
<h1>{{.Title}}</h1>
{{if .Message}}<p class="msg">{{.Message}}</p>{{end}}
<div class="banner {{.Overall}}">{{.Headline}}</div>
<ul>{{range .Items}}<li><span>{{.Name}}</span><span class="s {{.State}}">{{if eq .State "up"}}Operational{{else if eq .State "down"}}Down{{else}}No report yet{{end}}</span></li>{{end}}</ul>
<footer>Updated {{.Time}}. This page refreshes by itself every minute.</footer>
</main></body></html>
`))
