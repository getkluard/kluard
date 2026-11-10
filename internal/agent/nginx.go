package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"encoding/json"
)

// Nginx monitoring reads nginx's own status page (the stub_status module, built into the usual nginx builds). The agent finds
// nginx in Docker by itself and looks for the status page on the usual addresses; if it is not switched on, the server is
// shown with the two lines to add. No password is involved.

// NginxTarget is one nginx to check: either a status page address given in the Kluard UI, or a container to look in.
type NginxTarget struct {
	Name      string `json:"name"`
	Container string `json:"container,omitempty"`
	URL       string `json:"url,omitempty"` // the status page, e.g. http://10.0.0.5:8080/nginx_status
}

// nginxStatusPaths are tried, in order, on a container that has no address of its own set.
var nginxStatusPaths = []string{"/nginx_status", "/stub_status", "/basic_status", "/status"}

// nginxImage says whether a Docker image is an nginx web server (and not an exporter, a proxy manager UI or an ingress controller).
func nginxImage(image string) bool {
	img := strings.ToLower(image)
	if i := strings.LastIndex(img, "/"); i >= 0 {
		img = img[i+1:]
	}
	img = strings.SplitN(img, ":", 2)[0]
	img = strings.SplitN(img, "@", 2)[0]
	for _, bad := range []string{"exporter", "prometheus", "proxy-manager", "ingress", "unit", "certbot", "gen", "ui", "admin"} {
		if strings.Contains(img, bad) {
			return false
		}
	}
	for _, good := range []string{"nginx", "openresty", "angie"} {
		if strings.Contains(img, good) {
			return true
		}
	}
	return false
}

// DiscoveredNginx is an nginx found in a Docker container.
type DiscoveredNginx struct {
	Container string
	Image     string
	Bases     []string // http://ip:port to look for the status page on
	Path      string   // from the label kluard.nginx.status, if set
}

func discoverNginx(ctx context.Context, socket string) ([]DiscoveredNginx, error) {
	if socket == "" {
		socket = dockerSocket()
	}
	if _, err := os.Stat(socket); err != nil {
		return nil, ErrNoDocker
	}
	cl := dockerClient(socket)
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://docker/containers/json", nil)
	resp, err := cl.Do(req)
	if err != nil {
		if errors.Is(err, os.ErrPermission) || strings.Contains(err.Error(), "permission denied") {
			return nil, fmt.Errorf("no access to the Docker socket %s: add the agent's user to the docker group (re-run the installer with --docker)", socket)
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("docker API: %s", resp.Status)
	}
	var cs []dockerRedisContainer
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 8<<20)).Decode(&cs); err != nil {
		return nil, err
	}
	return nginxFromContainers(cs), nil
}

func nginxFromContainers(cs []dockerRedisContainer) []DiscoveredNginx {
	var out []DiscoveredNginx
	for _, c := range cs {
		if c.State != "running" || len(c.Names) == 0 || !nginxImage(c.Image) || c.Labels["kluard.nginx"] == "false" {
			continue
		}
		port, public := 80, 0
		for _, p := range c.Ports {
			if p.PrivatePort == 80 && p.PublicPort != 0 && public == 0 {
				public = p.PublicPort
			}
		}
		if v := c.Labels["kluard.nginx.port"]; v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 65536 {
				port = n
				public = 0
			}
		}
		d := DiscoveredNginx{Container: strings.TrimPrefix(c.Names[0], "/"), Image: c.Image}
		if p := c.Labels["kluard.nginx.status"]; strings.HasPrefix(p, "/") && len(p) < 100 && !strings.ContainsAny(p, " \r\n") {
			d.Path = p
		}
		var nets []string
		for n := range c.NetworkSettings.Networks {
			nets = append(nets, n)
		}
		sort.Strings(nets)
		for _, n := range nets {
			if ip := c.NetworkSettings.Networks[n].IPAddress; ip != "" {
				d.Bases = append(d.Bases, "http://"+net.JoinHostPort(ip, strconv.Itoa(port)))
			}
		}
		if public != 0 {
			d.Bases = append(d.Bases, "http://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(public)))
		}
		out = append(out, d)
	}
	return out
}

// NginxStats is what the status page says.
type NginxStats struct {
	Version                  string
	Active, Reading, Writing float64
	Waiting                  float64
	Accepts, Handled, Reqs   float64
}

var errNoStatusPage = errors.New("no nginx status page found")

// ParseStubStatus reads the text of nginx's stub_status page:
//
//	Active connections: 1
//	server accepts handled requests
//	 7 7 7
//	Reading: 0 Writing: 1 Waiting: 0
func ParseStubStatus(body string) (NginxStats, error) {
	var st NginxStats
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) < 4 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "Active connections:") {
		return st, errNoStatusPage
	}
	n := func(s string) (float64, bool) {
		v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		return v, err == nil && v >= 0
	}
	var ok bool
	if st.Active, ok = n(strings.TrimPrefix(strings.TrimSpace(lines[0]), "Active connections:")); !ok {
		return st, errNoStatusPage
	}
	f := strings.Fields(lines[2])
	if len(f) != 3 {
		return st, errNoStatusPage
	}
	var ok1, ok2, ok3 bool
	st.Accepts, ok1 = n(f[0])
	st.Handled, ok2 = n(f[1])
	st.Reqs, ok3 = n(f[2])
	if !ok1 || !ok2 || !ok3 {
		return st, errNoStatusPage
	}
	g := strings.Fields(lines[3]) // Reading: 0 Writing: 1 Waiting: 0
	if len(g) != 6 || g[0] != "Reading:" || g[2] != "Writing:" || g[4] != "Waiting:" {
		return st, errNoStatusPage
	}
	st.Reading, _ = n(g[1])
	st.Writing, _ = n(g[3])
	st.Waiting, _ = n(g[5])
	return st, nil
}

var nginxClient = &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// NginxCheck fetches one status page.
func NginxCheck(ctx context.Context, pageURL string) (NginxStats, error) {
	var st NginxStats
	req, err := http.NewRequestWithContext(ctx, "GET", pageURL, nil)
	if err != nil {
		return st, err
	}
	req.Header.Set("User-Agent", "kluard-agent")
	resp, err := nginxClient.Do(req)
	if err != nil {
		return st, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != 200 {
		if resp.StatusCode == 403 {
			return st, errors.New("the status page refuses the agent (403): allow its address in the nginx config")
		}
		return st, errNoStatusPage
	}
	st, err = ParseStubStatus(string(b))
	if err != nil {
		return st, err
	}
	if s := resp.Header.Get("Server"); strings.HasPrefix(strings.ToLower(s), "nginx/") || strings.HasPrefix(strings.ToLower(s), "openresty/") {
		st.Version = s
	}
	return st, nil
}

// NginxPoints turns a check into metrics. Counters are "sum".
func NginxPoints(st NginxStats, attrs map[string]string, now int64) []Point {
	g := func(name, typ string, v float64) Point {
		return Point{Service: "host", Name: name, Type: typ, Value: v, TimeNs: now, Attrs: attrs}
	}
	return []Point{
		g("nginx_connections_active", "gauge", st.Active),
		g("nginx_connections_reading", "gauge", st.Reading),
		g("nginx_connections_writing", "gauge", st.Writing),
		g("nginx_connections_waiting", "gauge", st.Waiting),
		g("nginx_connections_accepted_total", "sum", st.Accepts),
		g("nginx_connections_handled_total", "sum", st.Handled),
		g("nginx_http_requests_total", "sum", st.Reqs),
	}
}

// the address that worked last time for a container, so the other paths are not tried every round
var (
	nginxMu    sync.Mutex
	nginxFound = map[string]string{}
)

func nginxFind(ctx context.Context, d DiscoveredNginx) (string, NginxStats, error) {
	nginxMu.Lock()
	known := nginxFound[d.Container]
	nginxMu.Unlock()
	if known != "" {
		if st, err := NginxCheck(ctx, known); err == nil {
			return known, st, nil
		}
	}
	paths := nginxStatusPaths
	if d.Path != "" {
		paths = []string{d.Path}
	}
	var last error = errNoStatusPage
	for _, b := range d.Bases {
		for _, p := range paths {
			u := b + p
			st, err := NginxCheck(ctx, u)
			if err == nil {
				nginxMu.Lock()
				nginxFound[d.Container] = u
				nginxMu.Unlock()
				return u, st, nil
			}
			if !errors.Is(err, errNoStatusPage) {
				last = err
			}
		}
	}
	return "", NginxStats{}, last
}

// CollectNginx checks every nginx it can find in Docker plus the status pages in targets.
func CollectNginx(ctx context.Context, socket string, targets []NginxTarget, now int64) ([]Point, error) {
	found, derr := discoverNginx(ctx, socket)
	if errors.Is(derr, ErrNoDocker) {
		derr = nil
	}
	byContainer := map[string]NginxTarget{}
	for _, t := range targets {
		if t.Container != "" {
			byContainer[t.Container] = t
		}
	}
	type job struct {
		t NginxTarget
		d DiscoveredNginx
	}
	var jobs []job
	seen := map[string]bool{}
	for _, d := range found {
		t := byContainer[d.Container]
		if t.Name == "" {
			t.Name = d.Container
		}
		t.Container = d.Container
		jobs = append(jobs, job{t, d})
		seen[d.Container] = true
	}
	for _, t := range targets {
		if t.URL == "" || (t.Container != "" && seen[t.Container]) {
			continue
		}
		if t.Name == "" {
			t.Name = t.URL
		}
		jobs = append(jobs, job{t, DiscoveredNginx{}})
	}
	if len(jobs) > 50 {
		jobs = jobs[:50]
	}
	var pts []Point
	for _, j := range jobs {
		var st NginxStats
		var err error
		page := j.t.URL
		switch {
		case page != "":
			st, err = NginxCheck(ctx, page)
		case len(j.d.Bases) == 0:
			err = errors.New("no address to reach it on")
		default:
			page, st, err = nginxFind(ctx, j.d)
		}
		up, missing := 1.0, 0.0
		reason := ""
		if err != nil {
			up = 0
			if errors.Is(err, errNoStatusPage) {
				missing = 1
				reason = "no status page: add  location = /nginx_status { stub_status; allow 127.0.0.1; allow 172.16.0.0/12; deny all; }"
			} else {
				reason = shortErr(err)
			}
		}
		base := map[string]string{"nginx": j.t.Name}
		pts = append(pts,
			Point{Service: "host", Name: "nginx_up", Type: "gauge", Value: up, TimeNs: now, Attrs: base},
			Point{Service: "host", Name: "nginx_status_missing", Type: "gauge", Value: missing, TimeNs: now, Attrs: base})
		ia := map[string]string{"nginx": j.t.Name, "container": j.t.Container, "url": safeURL(page), "reason": reason}
		if err == nil {
			ia["version"] = st.Version
			pts = append(pts, NginxPoints(st, base, now)...)
		}
		pts = append(pts, Point{Service: "host", Name: "nginx_info", Type: "gauge", Value: 1, TimeNs: now, Attrs: ia})
	}
	return pts, derr
}

// safeURL drops any password from an address before it is reported.
func safeURL(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	u.User = nil
	return u.String()
}
