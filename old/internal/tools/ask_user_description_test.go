package tools

import (
	"strings"
	"testing"
)

// TestAskUserDescriptionMatchesBothModes pins the FP-16 contract in the text
// the model sees: interactive sessions block and return an answered result,
// while sessions without a question channel keep the pending fallback. The old
// description claimed the tool always ends the turn, which contradicted the
// desktop runtime behaviour.
func TestAskUserDescriptionMatchesBothModes(t *testing.T) {
	description := NewAskUserTool().Description()
	for _, want := range []string{
		`"status":"answered"`,
		`"answer"`,
		`"status":"pending"`,
		"headless/ACP",
		"subagent",
	} {
		if !strings.Contains(description, want) {
			t.Fatalf("ask_user description missing %q:\n%s", want, description)
		}
	}
	if strings.Contains(description, "and stop the turn") {
		t.Fatalf("ask_user description still claims the tool always stops the turn:\n%s", description)
	}
}
