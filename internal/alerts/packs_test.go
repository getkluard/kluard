package alerts

import "testing"

func TestPacksUseRealValidTemplates(t *testing.T) {
	byName := map[string]Rule{}
	for _, r := range Templates() {
		byName[r.Name] = r
	}
	for _, p := range Packs() {
		if len(p.Rules) == 0 || p.Title == "" || p.Desc == "" {
			t.Errorf("pack %s is incomplete", p.ID)
		}
		rules, ok := PackRules(p.ID)
		if !ok || len(rules) != len(p.Rules) {
			t.Errorf("pack %s: %d of %d rules found", p.ID, len(rules), len(p.Rules))
		}
		for _, r := range rules {
			if err := r.Normalize(); err != nil {
				t.Errorf("pack %s, rule %q: %v", p.ID, r.Name, err)
			}
			if r.Kind == KindStatus && (r.Status == "kluard_disk" || r.Status == "kluard_elasticsearch" || r.Status == "kluard_clickhouse") {
				t.Errorf("pack %s contains a rule about Kluard itself: %q", p.ID, r.Name)
			}
		}
	}
	if _, ok := PackRules("nope"); ok {
		t.Error("unknown pack")
	}
}
