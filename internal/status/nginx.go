package status

import (
	"sort"
	"strings"
	"time"

	"github.com/getkluard/kluard/internal/model"
	"github.com/getkluard/kluard/internal/registry"
)

// Nginx is one nginx as the UI shows it. State is up, down, setup (running, but its status page is not switched on) or
// pending (set up in the UI, nothing reported yet).
type Nginx struct {
	Host      string   `json:"host"`
	Name      string   `json:"name"`
	Container string   `json:"container,omitempty"`
	URL       string   `json:"url,omitempty"`
	State     string   `json:"state"`
	Reason    string   `json:"reason,omitempty"`
	Version   string   `json:"version,omitempty"`
	LastSeen  int64    `json:"last_seen"`
	Stale     bool     `json:"stale,omitempty"`
	RecordID  string   `json:"record_id,omitempty"`
	Active    *float64 `json:"active,omitempty"`
	Reading   *float64 `json:"reading,omitempty"`
	Writing   *float64 `json:"writing,omitempty"`
	Waiting   *float64 `json:"waiting,omitempty"`
}

// NginxList builds the nginx list from the latest samples and the nginx set up in the UI.
func NginxList(now time.Time, rows []model.Latest, recs []registry.Nginx) []Nginx {
	type key struct{ host, name string }
	m := map[key]*Nginx{}
	newest := map[key]int64{}
	get := func(k key) *Nginx {
		if m[k] == nil {
			m[k] = &Nginx{Host: k.host, Name: k.name, State: "down"}
		}
		return m[k]
	}
	for _, r := range rows {
		if !strings.HasPrefix(r.Name, "nginx_") || r.Attrs["nginx"] == "" || r.Attrs["host"] == "" {
			continue
		}
		k := key{r.Attrs["host"], r.Attrs["nginx"]}
		x := get(k)
		v := r.Value
		switch r.Name {
		case "nginx_up":
			if r.T >= newest[k] {
				newest[k] = r.T
				x.LastSeen = r.T
				if v >= 1 {
					x.State = "up"
				} else if x.State != "setup" {
					x.State = "down"
				}
			}
		case "nginx_status_missing":
			if v >= 1 && r.T >= newest[k] {
				x.State = "setup"
			}
		case "nginx_info":
			if x.Version == "" || r.T >= newest[k] {
				x.Container, x.URL, x.Reason, x.Version = r.Attrs["container"], r.Attrs["url"], r.Attrs["reason"], r.Attrs["version"]
			}
		case "nginx_connections_active":
			x.Active = &v
		case "nginx_connections_reading":
			x.Reading = &v
		case "nginx_connections_writing":
			x.Writing = &v
		case "nginx_connections_waiting":
			x.Waiting = &v
		}
	}
	for _, x := range m {
		if x.State != "up" {
			x.Active, x.Reading, x.Writing, x.Waiting = nil, nil, nil, nil
		}
		if now.Sub(time.Unix(x.LastSeen, 0)) > Fresh {
			x.Stale = true
		}
	}
	configured := func(host, name, container string) bool {
		for _, r := range recs {
			if r.Host == host && (r.Name == name || (r.Container != "" && (r.Container == container || r.Container == name))) {
				return true
			}
		}
		return false
	}
	// found by itself, not set up here, and gone for a while: drop it from the list
	for k, x := range m {
		if x.Stale && now.Sub(time.Unix(x.LastSeen, 0)) > 10*time.Minute && !configured(k.host, k.name, x.Container) {
			delete(m, k)
		}
	}
	for _, rec := range recs {
		var x *Nginx
		var xk key
		for k, c := range m {
			if k.host == rec.Host && (k.name == rec.Name || (rec.Container != "" && (c.Container == rec.Container || k.name == rec.Container))) {
				if x == nil || c.LastSeen > x.LastSeen || (c.LastSeen == x.LastSeen && k.name == rec.Name) {
					if x != nil {
						delete(m, xk)
					}
					x, xk = c, k
				} else {
					delete(m, k)
				}
			}
		}
		if x == nil {
			x = &Nginx{Host: rec.Host, Name: rec.Name, State: "pending", Container: rec.Container, URL: rec.URL}
			m[key{rec.Host, rec.Name}] = x
		}
		x.RecordID = rec.ID
	}
	out := make([]Nginx, 0, len(m))
	for _, x := range m {
		if x.State == "setup" && x.Reason == "" {
			x.Reason = "no status page"
		}
		out = append(out, *x)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].State == "up") != (out[j].State == "up") {
			return out[i].State != "up"
		}
		return out[i].Host+"/"+out[i].Name < out[j].Host+"/"+out[j].Name
	})
	return out
}
