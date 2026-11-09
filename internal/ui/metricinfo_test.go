package ui

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every Nextcloud metric the agent sends is described in the interface (the Metrics page shows the description), and the
// ready-made health dashboard exists.
func TestEveryNextcloudMetricIsDescribed(t *testing.T) {
	html, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`"(nextcloud_[a-z_0-9]+)"`)
	seen := map[string]bool{}
	for _, f := range []string{"../agent/nextcloud.go", "../agent/nextcloud_deep.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			if !strings.HasSuffix(m[1], "_") {
				seen[m[1]] = true
			}
		}
	}
	if len(seen) < 30 {
		t.Fatalf("found only %d metrics in the agent", len(seen))
	}
	for name := range seen {
		if !regexp.MustCompile(`"` + name + `":\s*\[`).Match(html) {
			t.Errorf("%s is not described in METRIC_INFO", name)
		}
	}
	if !strings.Contains(string(html), "name:'Nextcloud health'") {
		t.Error("the Nextcloud health dashboard is missing")
	}
}
