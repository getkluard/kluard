package status

import (
	"sort"
	"strings"
	"time"

	"github.com/getkluard/kluard/internal/model"
	"github.com/getkluard/kluard/internal/registry"
)

// Web is one checked address as the UI shows it. State is up, down or pending (set up, nothing reported yet).
type Web struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Host     string   `json:"host"`
	URL      string   `json:"url"`
	Status   int      `json:"expect_status,omitempty"`
	Contains string   `json:"contains,omitempty"`
	State    string   `json:"state"`
	Reason   string   `json:"reason,omitempty"`
	LastSeen int64    `json:"last_seen"`
	Stale    bool     `json:"stale,omitempty"`
	Code     *float64 `json:"code,omitempty"`
	Seconds  *float64 `json:"seconds,omitempty"`
	CertDays *float64 `json:"cert_days,omitempty"`
	CertOK   *bool    `json:"cert_ok,omitempty"`
}

// WebList joins the set-up checks with what the agents reported for them.
func WebList(now time.Time, rows []model.Latest, recs []registry.Web) []Web {
	type key struct{ host, name string }
	by := map[key][]model.Latest{}
	for _, r := range rows {
		if strings.HasPrefix(r.Name, "web_") && r.Attrs["web"] != "" && r.Attrs["host"] != "" {
			k := key{r.Attrs["host"], r.Attrs["web"]}
			by[k] = append(by[k], r)
		}
	}
	out := make([]Web, 0, len(recs))
	for _, rec := range recs {
		x := Web{ID: rec.ID, Name: rec.Name, Host: rec.Host, URL: rec.URL, Status: rec.Status, Contains: rec.Contains, State: "pending"}
		var newest int64
		for _, r := range by[key{rec.Host, rec.Name}] {
			v := r.Value
			switch r.Name {
			case "web_up":
				if r.T >= newest {
					newest, x.LastSeen = r.T, r.T
					x.State = "down"
					if v >= 1 {
						x.State = "up"
					}
				}
			case "web_info":
				x.Reason = r.Attrs["reason"]
			case "web_status_code":
				x.Code = &v
			case "web_response_seconds":
				x.Seconds = &v
			case "web_tls_expiry_seconds":
				d := v / 86400
				x.CertDays = &d
			case "web_tls_valid":
				ok := v >= 1
				x.CertOK = &ok
			}
		}
		if x.State == "up" {
			x.Reason = ""
		}
		if x.State != "pending" && now.Sub(time.Unix(x.LastSeen, 0)) > 3*Fresh {
			x.Stale = true
		}
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].State == "up") != (out[j].State == "up") {
			return out[i].State != "up"
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}
