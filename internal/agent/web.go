package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Website checks: the agent on a machine you choose fetches an address once a minute and reports whether it answers, how
// fast and, for https, when the certificate expires. Running it on an agent (and not on the server) keeps the server from
// being used to reach addresses that only an agent's network can see, and shows the site from where you chose to look.

// WebTarget is one address to check.
type WebTarget struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Status   int    `json:"status,omitempty"`   // the status code that counts as up; 0 = anything below 400
	Contains string `json:"contains,omitempty"` // text the page must contain
}

const (
	webEvery    = 55 * time.Second // one check per address per minute (the collector round is every 30 s)
	webTLSEvery = 6 * time.Hour
	webMaxBody  = 512 << 10
)

type webState struct {
	at    time.Time
	tlsAt time.Time
	tls   tlsInfo
	tlsOK bool
}

var (
	webMu     sync.Mutex
	webStates = map[string]*webState{}
)

// WebResult is what one check found.
type WebResult struct {
	Up      bool
	Reason  string
	Code    int
	Seconds float64
}

var webClient = func() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: 8 * time.Second}).DialContext,
			TLSClientConfig:     &tls.Config{RootCAs: tlsRoots},
			TLSHandshakeTimeout: 8 * time.Second,
			DisableKeepAlives:   true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

// WebCheck fetches one address and judges it by the target's rules.
func WebCheck(ctx context.Context, t WebTarget) WebResult {
	var r WebResult
	req, err := http.NewRequestWithContext(ctx, "GET", t.URL, nil)
	if err != nil {
		r.Reason = "bad address"
		return r
	}
	req.Header.Set("User-Agent", "kluard-agent (website check)")
	start := time.Now()
	resp, err := webClient().Do(req)
	if err != nil {
		r.Reason = shortErr(err)
		r.Seconds = time.Since(start).Seconds()
		return r
	}
	defer resp.Body.Close()
	r.Code = resp.StatusCode
	body := ""
	if t.Contains != "" {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, webMaxBody))
		body = string(b)
	}
	r.Seconds = time.Since(start).Seconds()
	switch {
	case t.Status != 0 && r.Code != t.Status:
		r.Reason = fmt.Sprintf("answered %d, expected %d", r.Code, t.Status)
	case t.Status == 0 && r.Code >= 400:
		r.Reason = fmt.Sprintf("answered %d", r.Code)
	case t.Contains != "" && !strings.Contains(body, t.Contains):
		r.Reason = "the page does not contain the text"
	default:
		r.Up = true
	}
	return r
}

// CollectWeb checks every target that is due and returns the metrics.
func CollectWeb(ctx context.Context, targets []WebTarget, now int64) []Point {
	if len(targets) > 100 {
		targets = targets[:100]
	}
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		pts []Point
		sem = make(chan struct{}, 8)
	)
	for _, t := range targets {
		u, err := url.Parse(t.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			continue
		}
		webMu.Lock()
		st := webStates[t.Name+"\x00"+t.URL]
		if st == nil {
			st = &webState{}
			webStates[t.Name+"\x00"+t.URL] = st
		}
		due := time.Since(st.at) >= webEvery
		if due {
			st.at = time.Now()
		}
		webMu.Unlock()
		if !due {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(t WebTarget, u *url.URL, st *webState) {
			defer wg.Done()
			defer func() { <-sem }()
			res := WebCheck(ctx, t)
			base := map[string]string{"web": t.Name}
			g := func(name string, v float64, a map[string]string) Point {
				return Point{Service: "host", Name: name, Type: "gauge", Value: v, TimeNs: now, Attrs: a}
			}
			up := 0.0
			if res.Up {
				up = 1
			}
			out := []Point{g("web_up", up, base), g("web_response_seconds", res.Seconds, base)}
			if res.Code != 0 {
				out = append(out, g("web_status_code", float64(res.Code), base))
			}
			out = append(out, g("web_info", 1, map[string]string{"web": t.Name, "url": safeURL(t.URL), "reason": res.Reason}))
			if u.Scheme == "https" {
				webMu.Lock()
				if time.Since(st.tlsAt) >= webTLSEvery || !st.tlsOK {
					webMu.Unlock()
					info, err := fetchTLS(ctx, u)
					webMu.Lock()
					st.tlsAt = time.Now()
					st.tls, st.tlsOK = info, err == nil
				}
				tl, ok := st.tls, st.tlsOK
				webMu.Unlock()
				if ok {
					valid := 0.0
					if tl.valid {
						valid = 1
					}
					out = append(out, g("web_tls_expiry_seconds", tl.notAfter.Sub(time.Unix(0, now)).Seconds(), base), g("web_tls_valid", valid, base))
				}
			}
			mu.Lock()
			pts = append(pts, out...)
			mu.Unlock()
		}(t, u, st)
	}
	wg.Wait()
	return pts
}
