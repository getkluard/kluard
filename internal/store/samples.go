package store

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/getkluard/kluard/internal/model"
)

var promNameRe = regexp.MustCompile(`[^a-zA-Z0-9_:]`)

// PromName turns a stored metric name (system.cpu.load) into its Prometheus form (system_cpu_load).
func PromName(n string) string {
	n = promNameRe.ReplaceAllString(n, "_")
	if n != "" && n[0] >= '0' && n[0] <= '9' {
		n = "_" + n
	}
	return n
}

const maxSamples = 500000

func buildSamplesQuery(tenant string, q model.SampleQuery) (string, map[string]string, error) {
	if q.Name == "" {
		return "", nil, fmt.Errorf("a metric name is required")
	}
	f, t := window(q.From, q.To)
	p := map[string]string{"tenant": tenant, "from": f, "to": t, "name": q.Name}
	where := "tenant = {tenant:String} AND ts >= {from:DateTime64(9)} AND ts <= {to:DateTime64(9)} AND replaceRegexpAll(name, '[^a-zA-Z0-9_:]', '_') = {name:String}"
	for i, k := range sortedKeys(q.Equal) {
		v := q.Equal[k]
		if k == "service" {
			where += fmt.Sprintf(" AND (service = {ev%d:String} OR attrs['service'] = {ev%d:String})", i, i)
			p[fmt.Sprintf("ev%d", i)] = v
			continue
		}
		if !keyRe.MatchString(k) {
			return "", nil, fmt.Errorf("invalid label %q", k)
		}
		where += fmt.Sprintf(" AND attrs[{ek%d:String}] = {ev%d:String}", i, i)
		p[fmt.Sprintf("ek%d", i)], p[fmt.Sprintf("ev%d", i)] = k, v
	}
	limit := q.Limit
	if limit <= 0 || limit > maxSamples {
		limit = maxSamples
	}
	return fmt.Sprintf("SELECT toUnixTimestamp64Milli(ts) AS t, name, service, attrs, value AS v FROM otel_metrics WHERE %s ORDER BY ts LIMIT %d FORMAT JSONEachRow", where, limit), p, nil
}

// Samples implements Store.
func (c *ClickHouse) Samples(ctx context.Context, tenant string, q model.SampleQuery) ([]model.Sample, error) {
	sql, p, err := buildSamplesQuery(tenant, q)
	if err != nil {
		return nil, err
	}
	rows, err := c.run(ctx, sql, p)
	if err != nil {
		return nil, err
	}
	out := make([]model.Sample, 0, len(rows))
	for _, l := range rows {
		var r struct {
			T       json.Number       `json:"t"`
			Name    string            `json:"name"`
			Service string            `json:"service"`
			Attrs   map[string]string `json:"attrs"`
			V       *float64          `json:"v"`
		}
		if json.Unmarshal(l, &r) != nil || r.V == nil {
			continue
		}
		t, _ := r.T.Int64()
		out = append(out, model.Sample{T: t, Name: r.Name, Service: r.Service, Attrs: r.Attrs, V: *r.V})
	}
	return out, nil
}

var _ = time.Second
