package contracts

// The preset denylist (M3): "everything except this" is the shape a role like
// the executor needs, and the writer must not be able to author a contradiction.

import (
	"strings"
	"testing"
)

func TestValidateAgentPresetsAcceptsDeny(t *testing.T) {
	row := `[{"id":"executor","name":"执行","deny":["task_review"]}]`
	if err := ValidateAgentPresets(row); err != nil {
		t.Fatalf("ValidateAgentPresets: %v", err)
	}
}

func TestValidateAgentPresetsRejectsBrokenDeny(t *testing.T) {
	cases := []struct {
		name string
		row  string
		want string
	}{
		{"blank", `[{"id":"a","name":"A","deny":["  "]}]`, "blank denied tool name"},
		{"duplicate", `[{"id":"a","name":"A","deny":["x","x"]}]`, "denies \"x\" twice"},
		{"contradiction", `[{"id":"a","name":"A","tools":["x"],"deny":["x"]}]`, "both allows and denies"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateAgentPresets(test.row)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want %q", err, test.want)
			}
		})
	}
}

// A row written by an older client (no deny field) must still validate: the
// field is additive, exactly like every other key in this row.
func TestValidateAgentPresetsWithoutDenyStillPasses(t *testing.T) {
	if err := ValidateAgentPresets(`[{"id":"a","name":"A","tools":["x"]}]`); err != nil {
		t.Fatalf("ValidateAgentPresets: %v", err)
	}
}
