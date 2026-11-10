package agent

import "testing"

func TestAccessCounter(t *testing.T) {
	a := NewAccessCounter()
	if pts := a.Points("host", 1); pts != nil {
		t.Fatal("nothing must be reported before an access log line has been seen")
	}
	line := func(code, size string) Log {
		return Log{Body: `203.0.113.7 - - [10/Nov/2026:14:00:00 +0100] "GET /index.php?x=1 HTTP/1.1" ` + code + ` ` + size + ` "-" "curl/8"`}
	}
	var logs []Log
	for i := 0; i < 18; i++ {
		logs = append(logs, line("200", "612"))
	}
	logs = append(logs, line("404", "10"), line("502", "-"), line("500", "0"))
	logs = append(logs, Log{Body: "just an ordinary log line"}, Log{Body: `Go service: "GET /x" something HTTP/1.1 done`})
	a.Observe(logs)
	got := map[string]float64{}
	for _, p := range a.Points("host", 1) {
		k := p.Name
		if c := p.Attrs["class"]; c != "" {
			k += "/" + c
		}
		got[k] = p.Value
	}
	if got["access_log_requests_total/2xx"] != 18 || got["access_log_requests_total/4xx"] != 1 || got["access_log_requests_total/5xx"] != 2 || got["access_log_bytes_total"] != 18*612+10 {
		t.Fatalf("%v", got)
	}
	if r := got["access_log_error_ratio"]; r < 0.095 || r > 0.096 {
		t.Fatalf("2 of 21 are errors: %v", r)
	}
	// a quiet half minute reports the totals but no ratio
	a.Observe([]Log{line("500", "1"), line("200", "1")})
	if _, ok := func() (float64, bool) {
		for _, p := range a.Points("host", 2) {
			if p.Name == "access_log_error_ratio" {
				return p.Value, true
			}
		}
		return 0, false
	}(); ok {
		t.Fatal("two requests are too few for a share")
	}
}
