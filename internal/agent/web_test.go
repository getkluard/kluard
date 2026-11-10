package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func webSrv() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte("<html>Welcome to the shop</html>"))
		case "/redirect":
			http.Redirect(w, r, "/ok", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/teapot":
			w.WriteHeader(418)
		case "/slow":
			time.Sleep(50 * time.Millisecond)
			w.Write([]byte("ok"))
		default:
			http.Error(w, "no", 500)
		}
	}))
}

func TestWebCheckRules(t *testing.T) {
	s := webSrv()
	defer s.Close()
	for name, c := range map[string]struct {
		t    WebTarget
		up   bool
		code int
	}{
		"ok":               {WebTarget{URL: s.URL + "/ok"}, true, 200},
		"follows redirect": {WebTarget{URL: s.URL + "/redirect"}, true, 200},
		"server error":     {WebTarget{URL: s.URL + "/broken"}, false, 500},
		"expected status":  {WebTarget{URL: s.URL + "/teapot", Status: 418}, true, 418},
		"wrong status":     {WebTarget{URL: s.URL + "/ok", Status: 204}, false, 200},
		"text found":       {WebTarget{URL: s.URL + "/ok", Contains: "the shop"}, true, 200},
		"text missing":     {WebTarget{URL: s.URL + "/ok", Contains: "maintenance"}, false, 200},
		"redirect loop":    {WebTarget{URL: s.URL + "/loop"}, false, 0},
		"nothing there":    {WebTarget{URL: "http://127.0.0.1:1/"}, false, 0},
	} {
		got := WebCheck(context.Background(), c.t)
		if got.Up != c.up || got.Code != c.code || (!got.Up && got.Reason == "") {
			t.Errorf("%s: %+v", name, got)
		}
	}
	if r := WebCheck(context.Background(), WebTarget{URL: s.URL + "/slow"}); r.Seconds < 0.05 {
		t.Errorf("the time was not measured: %v", r.Seconds)
	}
}

func TestCollectWebThrottlesAndReports(t *testing.T) {
	s := webSrv()
	defer s.Close()
	ts := []WebTarget{{Name: "shop", URL: s.URL + "/ok"}, {Name: "bad", URL: s.URL + "/broken"}, {Name: "weird", URL: "ftp://x/"}}
	pts := CollectWeb(context.Background(), ts, time.Now().UnixNano())
	got := map[string]float64{}
	for _, p := range pts {
		got[p.Attrs["web"]+"/"+p.Name] = p.Value
		if p.Attrs["web"] == "weird" {
			t.Fatal("an ftp address must be skipped")
		}
	}
	if got["shop/web_up"] != 1 || got["bad/web_up"] != 0 || got["shop/web_status_code"] != 200 || got["bad/web_status_code"] != 500 {
		t.Fatalf("%v", got)
	}
	if again := CollectWeb(context.Background(), ts, time.Now().UnixNano()); len(again) != 0 {
		t.Fatalf("a second round right away must not check again: %d points", len(again))
	}
}
