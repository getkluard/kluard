package alerts

import "testing"

func TestOldStatusNamesAreAccepted(t *testing.T) {
	for old, now := range map[string]string{"lumen_disk": "kluard_disk", "lumen_elasticsearch": "kluard_elasticsearch", "lumen_clickhouse": "kluard_clickhouse", "host_down": "host_down"} {
		if got := canonStatus(old); got != now {
			t.Errorf("%s -> %s, want %s", old, got, now)
		}
	}
	r := Rule{Name: "old", Kind: KindStatus, Status: "lumen_disk", Severity: "warning"}
	if err := r.Normalize(); err != nil || r.Status != "kluard_disk" {
		t.Fatalf("a rule with the old status name must validate and be renamed: %v %q", err, r.Status)
	}
}
