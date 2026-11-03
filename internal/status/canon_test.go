package status

import (
	"testing"

	"github.com/getkluard/kluard/internal/model"
)

func TestAgentsFromBeforeTheRenameAreStillSeen(t *testing.T) {
	rows := CanonRows([]model.Latest{{Name: "lumen_agent_info"}, {Name: "lumen_agent_log_paths_rejected"}, {Name: "kluard_agent_info"}, {Name: "nextcloud_up"}})
	want := []string{"kluard_agent_info", "kluard_agent_log_paths_rejected", "kluard_agent_info", "nextcloud_up"}
	for i, r := range rows {
		if r.Name != want[i] {
			t.Errorf("row %d: %s, want %s", i, r.Name, want[i])
		}
	}
}
