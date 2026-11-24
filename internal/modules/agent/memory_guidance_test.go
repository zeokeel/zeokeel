package agent

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Memory only gets written when the model decides to call the tool. A live audit
// showed the consequence: "我的项目代号是 ORCA-7，后端用 Rust 写" reached a fresh
// session and nothing was remembered, because the guidance only described *how* to
// use the memory tool — never that durable facts are worth recording unprompted.
// The next session then answered from the old memory and guessed the rest from the
// workspace.
func TestMemoryGuidanceAsksForUnpromptedRecording(t *testing.T) {
	prompt := systemPromptForRun("linux", "amd64", "/work/project",
		contracts.PermissionWorkspace, contracts.PermissionWorkspace, "", "",
		map[string]bool{"memory": true, "read_file": true})

	if !strings.Contains(prompt, "memory") {
		t.Fatalf("no memory guidance in the prompt: %q", prompt)
	}
	lower := strings.ToLower(prompt)
	// The behaviour the audit missed: record durable facts without being asked.
	if !strings.Contains(lower, "without being asked") {
		t.Fatalf("memory guidance does not ask for unprompted recording: %q", prompt)
	}
	for _, want := range []string{"project", "preference"} {
		if !strings.Contains(lower, want) {
			t.Fatalf("memory guidance does not name what to record (%q): %q", want, prompt)
		}
	}
	// It must still say memory is data, not instruction — that rule predates this
	// change and is not negotiable.
	if !strings.Contains(lower, "not instructions") {
		t.Fatalf("memory guidance dropped the 'data not instructions' rule: %q", prompt)
	}
}

// A run without the memory tool must not be told to remember anything: the same
// surface rule that keeps the shell sentence honest applies here.
func TestMemoryGuidanceOnlyWithTheTool(t *testing.T) {
	prompt := systemPromptForRun("linux", "amd64", "/work/project",
		contracts.PermissionWorkspace, contracts.PermissionWorkspace, "", "",
		map[string]bool{"read_file": true})
	if strings.Contains(strings.ToLower(prompt), "without being asked") {
		t.Fatalf("a run without the memory tool was told to record memories: %q", prompt)
	}
}
