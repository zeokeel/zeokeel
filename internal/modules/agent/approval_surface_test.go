package agent

// The approval surface is user-visible behaviour that is easy to get wrong on
// paper: most command tools do NOT park on a card, because the OS sandbox
// already confines them (gate.AllowSandboxedCommands). docs/APPROVAL-SURFACES.md
// §S4.1 states which families are exempt, and this test keeps that statement
// true: change a rule without updating the document (or the reverse) and CI
// fails instead of a user concluding "approvals are broken".

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// documentedExemptions is the machine-readable block of APPROVAL-SURFACES.md §S4.1.
type documentedExemptions struct {
	SandboxShell []string
	Workspace    []string
	Interaction  []string
	Parks        []string
}

// parseExemptions reads the approval-exemptions block.
func parseExemptions(t *testing.T) documentedExemptions {
	t.Helper()
	path := filepath.Join("..", "..", "..", "docs", "APPROVAL-SURFACES.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(raw)
	const begin = "<!-- approval-exemptions:begin -->"
	const end = "<!-- approval-exemptions:end -->"
	start := strings.Index(text, begin)
	finish := strings.Index(text, end)
	if start < 0 || finish < start {
		t.Fatalf("%s has no approval-exemptions block", path)
	}
	var documented documentedExemptions
	for _, line := range strings.Split(text[start+len(begin):finish], "\n") {
		line = strings.TrimSpace(line)
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		names := splitNames(value)
		switch strings.TrimSpace(key) {
		case "sandbox-shell":
			documented.SandboxShell = names
		case "workspace-auto":
			documented.Workspace = names
		case "interaction":
			documented.Interaction = names
		case "parks":
			documented.Parks = names
		}
	}
	return documented
}

// splitNames splits a comma-separated list, dropping blanks.
func splitNames(value string) []string {
	var names []string
	for _, part := range strings.Split(value, ",") {
		if name := strings.TrimSpace(part); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// sortedCopy returns a sorted copy of a string slice.
func sortedCopy(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// sameSet compares two name lists as sets.
func sameSet(t *testing.T, what string, documented, actual []string) {
	t.Helper()
	want := sortedCopy(documented)
	got := sortedCopy(actual)
	if strings.Join(want, ",") != strings.Join(got, ",") {
		t.Fatalf("%s drifted from docs/APPROVAL-SURFACES.md §S4.1:\n  documented: %v\n  actual:     %v\n"+
			"update the document block (or the rule) in the same change", what, want, got)
	}
}

// TestApprovalExemptionsMatchTheGate pins the three exemption families.
func TestApprovalExemptionsMatchTheGate(t *testing.T) {
	documented := parseExemptions(t)

	// The sandbox exemption is judged against these names and nothing else (the
	// product pins its own list so an SDK default cannot widen it).
	sameSet(t, "sandbox-shell tools", documented.SandboxShell, sandboxShellTools)

	// The workspace preset's auto-allow set.
	var workspace []string
	for name, allowed := range contracts.WorkspaceAutoAllowedTools {
		if allowed {
			workspace = append(workspace, name)
		}
	}
	sameSet(t, "workspace auto-allowed tools", documented.Workspace, workspace)

	// Interaction tools render their own overlay instead of an approval card.
	// "goal" is the retired v1 name the gate still tolerates but nothing can
	// call, so it is not documented.
	var interaction []string
	for name := range interactionTools {
		if name == "goal" {
			continue
		}
		interaction = append(interaction, name)
	}
	sameSet(t, "interaction tools", documented.Interaction, interaction)
}

// TestDocumentedParksActuallyPark checks the "these will ask" examples: none may
// be auto-allowed, and the MCP wildcard must stay a prefix rather than a name.
func TestDocumentedParksActuallyPark(t *testing.T) {
	documented := parseExemptions(t)
	if len(documented.Parks) == 0 {
		t.Fatal("the document lists no tools that park on approval")
	}
	for _, name := range documented.Parks {
		if strings.HasSuffix(name, "__") || strings.HasSuffix(name, "*") {
			// The documented MCP family prefix: every concrete MCP tool name
			// carries the prefix, and none of them is auto-allowed.
			if name != "mcp__" {
				t.Fatalf("unexpected wildcard %q: document the family prefix mcp__ exactly", name)
			}
			if contracts.WorkspaceAutoAllowed("mcp__anything") {
				t.Fatal("MCP tools are auto-allowed, which contradicts the documented park list")
			}
			continue
		}
		if contracts.WorkspaceAutoAllowed(name) {
			t.Fatalf("%q is documented as parking on approval but the workspace preset auto-allows it", name)
		}
		for _, shell := range sandboxShellTools {
			if name == shell {
				t.Fatalf("%q is documented as parking on approval but it is in the sandbox-exempt shell family", name)
			}
		}
		if interactionTools[name] {
			t.Fatalf("%q is documented as parking on approval but it is an interaction tool", name)
		}
	}
}

// TestSandboxExemptionNeedsAConfinedRunner keeps the condition honest: the
// exemption only applies when the sandbox service is actually assembled. A
// service without one must never claim confinement.
func TestSandboxExemptionNeedsAConfinedRunner(t *testing.T) {
	bare := &service{}
	// A fully assembled service needs the whole kernel; the "no sandbox" case is
	// the one that can be built in isolation and it is the dangerous default.
	if bare.sandboxedCommand(t.Context(), t.TempDir(), nil, rig.ToolCall{Name: "bash"}) {
		t.Fatal("a service without a sandbox exempted a shell call")
	}
	if bare.sandboxService != nil {
		t.Fatal("the zero service unexpectedly carries a sandbox")
	}
}
