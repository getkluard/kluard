package status

import (
	"sort"
	"strings"
	"time"

	"github.com/getkluard/kluard/internal/model"
	"github.com/getkluard/kluard/internal/registry"
)

// Mysql is one Mysql server as the UI shows it. State is up, down, auth (it wants a password that is missing or wrong) or
// pending (set up in the UI, nothing reported yet).
type Mysql struct {
	Host         string   `json:"host"`
	Name         string   `json:"name"`
	Container    string   `json:"container,omitempty"`
	Addr         string   `json:"addr,omitempty"`
	State        string   `json:"state"`
	Reason       string   `json:"reason,omitempty"`
	Version      string   `json:"version,omitempty"`
	Role         string   `json:"role,omitempty"`
	LastSeen     int64    `json:"last_seen"`
	Stale        bool     `json:"stale,omitempty"`
	RecordID     string   `json:"record_id,omitempty"` // set when it is configured in the UI (login or address)
	HasPassword  bool     `json:"has_password"`
	Username     string   `json:"username,omitempty"`
	SSLMode      string   `json:"sslmode,omitempty"`
	Conns        *float64 `json:"connections,omitempty"`
	ConnsMax     *float64 `json:"connections_max,omitempty"`
	Running      *float64 `json:"running,omitempty"`
	LongestQuery *float64 `json:"longest_query,omitempty"`
	Size         *float64 `json:"size,omitempty"`
	Lag          *float64 `json:"replication_lag,omitempty"`
	Uptime       *float64 `json:"uptime,omitempty"`
}

// MysqlList builds the Mysql list from the latest samples and the Mysql set up in the UI.
func MysqlList(now time.Time, rows []model.Latest, recs []registry.Mysql) []Mysql {
	type key struct{ host, name string }
	m := map[key]*Mysql{}
	newest := map[key]int64{}
	get := func(k key) *Mysql {
		if m[k] == nil {
			m[k] = &Mysql{Host: k.host, Name: k.name, State: "down"}
		}
		return m[k]
	}
	for _, r := range rows {
		if !strings.HasPrefix(r.Name, "mysql_") || r.Attrs["mysql"] == "" || r.Attrs["host"] == "" {
			continue
		}
		k := key{r.Attrs["host"], r.Attrs["mysql"]}
		x := get(k)
		v := r.Value
		switch r.Name {
		case "mysql_up":
			if r.T >= newest[k] {
				newest[k] = r.T
				x.LastSeen = r.T
				if v >= 1 {
					x.State = "up"
				} else if x.State != "auth" {
					x.State = "down"
				}
			}
		case "mysql_auth_required":
			if v >= 1 && r.T >= newest[k] {
				x.State = "auth"
			}
		case "mysql_info":
			if x.Version == "" || r.T >= newest[k] {
				x.Container, x.Addr, x.Reason, x.Version, x.Role = r.Attrs["container"], r.Attrs["addr"], r.Attrs["reason"], r.Attrs["version"], r.Attrs["role"]
			}
		case "mysql_connections":
			x.Conns = &v
		case "mysql_connections_max":
			x.ConnsMax = &v
		case "mysql_connections_running":
			x.Running = &v
		case "mysql_longest_query_seconds":
			x.LongestQuery = &v
		case "mysql_replication_lag_seconds":
			x.Lag = &v
		case "mysql_uptime_seconds":
			x.Uptime = &v
		case "mysql_database_size_bytes":
			if x.Size == nil {
				z := 0.0
				x.Size = &z
			}
			*x.Size += v
		}
	}
	for k, x := range m {
		if x.State == "auth" && x.Reason == "" {
			x.Reason = "needs a password"
		}
		// numbers of a Mysql that is not answering are old: do not show them as if they were current
		if x.State != "up" {
			x.Conns, x.ConnsMax, x.Running, x.LongestQuery, x.Size, x.Lag, x.Uptime = nil, nil, nil, nil, nil, nil, nil
		}
		if now.Sub(time.Unix(x.LastSeen, 0)) > Fresh {
			x.Stale = true
		}
		_ = k
	}
	// a server that was found by itself, has not been set up here and has stopped reporting (the container was removed
	// or renamed) disappears after a while instead of staying in the list for ever
	for k, x := range m {
		if x.Stale && now.Sub(time.Unix(x.LastSeen, 0)) > 10*time.Minute && !mysqlConfigured(recs, k.host, k.name, x.Container) {
			delete(m, k)
		}
	}
	for _, rec := range recs {
		// every entry that belongs to this setting (same name, same container, or found under the container's name);
		// the one that reported last is the real one, the others are leftovers from before it was set up here
		var x *Mysql
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
			x = &Mysql{Host: rec.Host, Name: rec.Name, State: "pending", Container: rec.Container, Addr: rec.Addr}
			m[key{rec.Host, rec.Name}] = x
		}
		x.RecordID, x.HasPassword, x.Username, x.SSLMode = rec.ID, rec.PasswordEnc != "", rec.Username, rec.SSLMode
	}
	out := make([]Mysql, 0, len(m))
	for _, x := range m {
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

func mysqlConfigured(recs []registry.Mysql, host, name, container string) bool {
	for _, r := range recs {
		if r.Host == host && (r.Name == name || (r.Container != "" && (r.Container == container || r.Container == name))) {
			return true
		}
	}
	return false
}
