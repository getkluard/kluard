package status

import (
	"sort"
	"strings"
	"time"

	"github.com/getkluard/kluard/internal/model"
	"github.com/getkluard/kluard/internal/registry"
)

// S3Bucket is the size of one bucket as the agent last measured it. Partial means the agent stopped counting at its limit, so
// the real size is at least this.
type S3Bucket struct {
	Name    string  `json:"name"`
	Bytes   float64 `json:"bytes"`
	Objects float64 `json:"objects"`
	Partial bool    `json:"partial,omitempty"`
}

// S3 is a watched S3-compatible storage. Nothing secret is in it.
type S3 struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Host      string     `json:"host"`
	URL       string     `json:"url"`
	Region    string     `json:"region,omitempty"`
	AccessKey string     `json:"access_key"`
	HasSecret bool       `json:"has_secret"`
	Scope     []string   `json:"buckets"` // the buckets named in the settings (empty: all)
	Measure   bool       `json:"size_on"` // sizes are asked for
	State     string     `json:"state"`   // up | down | pending
	Reason    string     `json:"reason,omitempty"`
	LastSeen  int64      `json:"last_seen"`
	Stale     bool       `json:"stale,omitempty"`
	Seconds   *float64   `json:"seconds,omitempty"`
	Count     *float64   `json:"bucket_count,omitempty"`
	Items     []S3Bucket `json:"items"`
	Total     *float64   `json:"total_bytes,omitempty"`
	Partial   bool       `json:"partial,omitempty"`
}

// S3List joins the set-up storages with what the agents reported for them.
func S3List(now time.Time, rows []model.Latest, recs []registry.S3) []S3 {
	type key struct{ host, name string }
	by := map[key][]model.Latest{}
	for _, r := range rows {
		if strings.HasPrefix(r.Name, "s3_") && r.Attrs["s3"] != "" && r.Attrs["host"] != "" {
			k := key{r.Attrs["host"], r.Attrs["s3"]}
			by[k] = append(by[k], r)
		}
	}
	out := make([]S3, 0, len(recs))
	for _, rec := range recs {
		o := rec.Out()
		x := S3{ID: rec.ID, Name: rec.Name, Host: rec.Host, URL: rec.URL, Region: rec.Region, AccessKey: rec.AccessKey, HasSecret: o.HasSecret,
			Scope: o.Buckets, Measure: rec.Size, State: "pending", Items: []S3Bucket{}}
		buckets := map[string]*S3Bucket{}
		var newest int64
		for _, r := range by[key{rec.Host, rec.Name}] {
			v := r.Value
			b := r.Attrs["bucket"]
			switch r.Name {
			case "s3_up":
				if r.T >= newest {
					newest, x.LastSeen = r.T, r.T
					x.State = "down"
					if v >= 1 {
						x.State = "up"
					}
				}
			case "s3_info":
				x.Reason = r.Attrs["reason"]
			case "s3_response_seconds":
				x.Seconds = &v
			case "s3_buckets":
				x.Count = &v
			case "s3_bucket_size_bytes", "s3_bucket_objects", "s3_bucket_size_partial":
				if b == "" {
					continue
				}
				if buckets[b] == nil {
					buckets[b] = &S3Bucket{Name: b}
				}
				switch r.Name {
				case "s3_bucket_size_bytes":
					buckets[b].Bytes = v
				case "s3_bucket_objects":
					buckets[b].Objects = v
				default:
					buckets[b].Partial = v >= 1
				}
			}
		}
		if x.State == "up" {
			x.Reason = ""
		}
		if len(buckets) > 0 {
			t := 0.0
			for _, b := range buckets {
				x.Items = append(x.Items, *b)
				t += b.Bytes
				x.Partial = x.Partial || b.Partial
			}
			x.Total = &t
			sort.Slice(x.Items, func(i, j int) bool { return x.Items[i].Bytes > x.Items[j].Bytes })
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
