package status

import (
	"sort"
	"strings"
	"time"

	"github.com/getkluard/kluard/internal/model"
	"github.com/getkluard/kluard/internal/registry"
)

// Minio is one MinIO as the UI shows it. State is up, down, auth (it wants a token for its metrics that is missing or wrong)
// or pending (set up in the UI, nothing reported yet).
type Minio struct {
	Host         string   `json:"host"`
	Name         string   `json:"name"`
	Container    string   `json:"container,omitempty"`
	URL          string   `json:"url,omitempty"`
	State        string   `json:"state"`
	Reason       string   `json:"reason,omitempty"`
	LastSeen     int64    `json:"last_seen"`
	Stale        bool     `json:"stale,omitempty"`
	RecordID     string   `json:"record_id,omitempty"`
	HasToken     bool     `json:"has_token"`
	Free         *float64 `json:"free,omitempty"`
	Total        *float64 `json:"total,omitempty"`
	Used         *float64 `json:"used,omitempty"`
	DrivesOffl   *float64 `json:"drives_offline,omitempty"`
	DrivesTotal  *float64 `json:"drives_total,omitempty"`
	NodesOffline *float64 `json:"nodes_offline,omitempty"`
	Healthy      *bool    `json:"healthy,omitempty"`
	Buckets      *float64 `json:"buckets,omitempty"`
}

// MinioList builds the MinIO list from the latest samples and the MinIO set up in the UI.
func MinioList(now time.Time, rows []model.Latest, recs []registry.Minio) []Minio {
	type key struct{ host, name string }
	m := map[key]*Minio{}
	newest := map[key]int64{}
	get := func(k key) *Minio {
		if m[k] == nil {
			m[k] = &Minio{Host: k.host, Name: k.name, State: "down"}
		}
		return m[k]
	}
	for _, r := range rows {
		if !strings.HasPrefix(r.Name, "minio_") || r.Attrs["minio"] == "" || r.Attrs["host"] == "" {
			continue
		}
		k := key{r.Attrs["host"], r.Attrs["minio"]}
		x := get(k)
		v := r.Value
		switch r.Name {
		case "minio_up":
			if r.T >= newest[k] {
				newest[k] = r.T
				x.LastSeen = r.T
				if v >= 1 {
					x.State = "up"
				} else if x.State != "auth" {
					x.State = "down"
				}
			}
		case "minio_auth_required":
			if v >= 1 && r.T >= newest[k] {
				x.State = "auth"
			}
		case "minio_info":
			if r.T >= newest[k] || x.URL == "" {
				x.Container, x.URL, x.Reason = r.Attrs["container"], r.Attrs["url"], r.Attrs["reason"]
			}
		case "minio_cluster_capacity_usable_free_bytes":
			x.Free = &v
		case "minio_cluster_capacity_usable_total_bytes":
			x.Total = &v
		case "minio_cluster_usage_total_bytes":
			x.Used = &v
		case "minio_cluster_drive_offline_total":
			x.DrivesOffl = &v
		case "minio_cluster_drive_total":
			x.DrivesTotal = &v
		case "minio_cluster_nodes_offline_total":
			x.NodesOffline = &v
		case "minio_cluster_health_status":
			h := v >= 1
			x.Healthy = &h
		case "minio_cluster_bucket_total":
			x.Buckets = &v
		}
	}
	for _, x := range m {
		if x.State == "auth" && x.Reason == "" {
			x.Reason = "needs a token for its metrics"
		}
		if x.State != "up" {
			x.Free, x.Total, x.Used, x.DrivesOffl, x.DrivesTotal, x.NodesOffline, x.Healthy, x.Buckets = nil, nil, nil, nil, nil, nil, nil, nil
		}
		if now.Sub(time.Unix(x.LastSeen, 0)) > Fresh {
			x.Stale = true
		}
	}
	has := func(host, name, container string) bool {
		for _, r := range recs {
			if r.Host == host && (r.Name == name || (r.Container != "" && (r.Container == container || r.Container == name))) {
				return true
			}
		}
		return false
	}
	for k, x := range m { // found by itself, not set up here, and gone for a while
		if x.Stale && now.Sub(time.Unix(x.LastSeen, 0)) > 10*time.Minute && !has(k.host, k.name, x.Container) {
			delete(m, k)
		}
	}
	for _, rec := range recs {
		var x *Minio
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
			x = &Minio{Host: rec.Host, Name: rec.Name, State: "pending", Container: rec.Container, URL: rec.URL}
			m[key{rec.Host, rec.Name}] = x
		}
		x.RecordID, x.HasToken = rec.ID, rec.TokenEnc != ""
	}
	out := make([]Minio, 0, len(m))
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
