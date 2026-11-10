package agent

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// AccessCounter counts the answers in web server access logs (nginx, Apache and anything else that writes the common or
// combined log format) among the log lines the agent reads anyway, and turns them into metrics: how many requests per
// status class, and how large a share of the last half minute was server errors. Nothing is stored about who asked or what.
type AccessCounter struct {
	mu       sync.Mutex
	counts   map[string]float64 // status class -> requests since the agent started
	bytes    float64
	last     map[string]float64 // the counts at the previous report
	seen     bool
	minDelta float64
}

// "GET /path HTTP/1.1" 200 612
var accessRe = regexp.MustCompile(`"[A-Z]{3,10} [^"]* HTTP/[0-9.]+" (\d{3}) (\d+|-)`)

func NewAccessCounter() *AccessCounter {
	return &AccessCounter{counts: map[string]float64{}, last: map[string]float64{}, minDelta: 10}
}

func statusClass(code string) string {
	switch code[0] {
	case '2', '3', '4', '5':
		return code[:1] + "xx"
	}
	return "other"
}

// Observe counts the access log lines among logs.
func (a *AccessCounter) Observe(logs []Log) {
	var n map[string]float64
	var b float64
	for _, l := range logs {
		if !strings.Contains(l.Body, " HTTP/") {
			continue
		}
		m := accessRe.FindStringSubmatch(l.Body)
		if m == nil {
			continue
		}
		if n == nil {
			n = map[string]float64{}
		}
		n[statusClass(m[1])]++
		if v, err := strconv.ParseFloat(m[2], 64); err == nil {
			b += v
		}
	}
	if n == nil {
		return
	}
	a.mu.Lock()
	for k, v := range n {
		a.counts[k] += v
	}
	a.bytes += b
	a.seen = true
	a.mu.Unlock()
}

// Points returns the metrics, or nothing as long as no access log line has been seen.
func (a *AccessCounter) Points(service string, now int64) []Point {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.seen {
		return nil
	}
	var pts []Point
	for _, c := range []string{"2xx", "3xx", "4xx", "5xx", "other"} {
		pts = append(pts, Point{Service: service, Name: "access_log_requests_total", Type: "sum", Value: a.counts[c], TimeNs: now, Attrs: map[string]string{"class": c}})
	}
	pts = append(pts, Point{Service: service, Name: "access_log_bytes_total", Type: "sum", Value: a.bytes, TimeNs: now, Attrs: map[string]string{}})
	var total, errs float64
	for c, v := range a.counts {
		d := v - a.last[c]
		total += d
		if c == "5xx" {
			errs = d
		}
	}
	if total >= a.minDelta { // a handful of requests says nothing about a share
		pts = append(pts, Point{Service: service, Name: "access_log_error_ratio", Type: "gauge", Value: errs / total, TimeNs: now, Attrs: map[string]string{}})
		for c, v := range a.counts {
			a.last[c] = v
		}
	}
	return pts
}
