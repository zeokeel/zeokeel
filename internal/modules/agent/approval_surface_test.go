package agent

// The approval surface is user-visible behaviour that is easy to get wrong on
// paper. After the 2026-11-26 permission-model change (personal helper: MCP and
// commands never park, reads unfenced, writes workspace-only) the families that
// are exempt without asking are:
//
//   - the command family (bash/pwsh): exempt whenever the sandbox module is
//     assembled — silently when the SDK certifies confinement, with a recorded
//     "no OS confinement" line when it does not;
//   - the MCP family (mcp__ prefix);
//   - the workspace auto-allow set and the interaction tools.
//
// docs/APPROVAL-SURFACES.md §S4.1 states this, and this test keeps that
// statement true: change a rule without updating the document (or the reverse)
// and CI fails instead of a user concluding "approvals are broken".

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigsandbox "github.com/zeokeel/zeokeel-rig/sandbox"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// documentedExemptions is the machine-readable block of APPROVAL-SURFACES.md §S4.1.
type documentedExemptions struct {
	SandboxShell []string
	MCPFamily    []string
	Workspace    []string
	Interaction  []string
	Parks        []string
}

// declaredExemptions is the single declared approval surface. It used to be the
// machine-readable block of docs/APPROVAL-SURFACES.md §S4.1; the documentation
// tree is gone, so the declaration lives in the test and the tests below keep the
// code honest against it. Change a rule and this block changes in the same commit.
var declaredExemptions = documentedExemptions{
	SandboxShell: []string{"bash", "pwsh"},
	MCPFamily:    []string{"mcp__"},
	Workspace: strings.Fields(`apply_patch edit_file glob grep inspect job_list job_output job_wait
		list_dir lsp_definition lsp_diagnostics lsp_hover lsp_implementation lsp_references lsp_symbols
		memory read_file read_image sandbox_status search session_event_read session_event_search
		session_event_trace session_search session_trace skill spill_read str_replace_editor
		team_task_get team_task_list terminal_list terminal_read todo_write tool_search undo
		working_directory write_file`),
	Interaction: []string{"ask_user", "present", "plan_review", "exit_plan_mode", "get_goal", "create_goal", "update_goal"},
	Parks: strings.Fields(`enter_worktree exit_worktree job_kill list_worktrees request_sandbox_access
		schedule_create schedule_delete schedule_list schedule_update settings_get settings_set
		team_task_create team_task_update web_fetch web_search`),
}

// parseExemptions returns the declared surface.
func parseExemptions(t *testing.T) documentedExemptions {
	t.Helper()
	return declaredExemptions
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

// TestApprovalExemptionsMatchTheGate pins the exemption families.
func TestApprovalExemptionsMatchTheGate(t *testing.T) {
	documented := parseExemptions(t)

	// The command family is judged against these names and nothing else (the
	// product pins its own list so an SDK default cannot widen it).
	sameSet(t, "sandbox-shell tools", documented.SandboxShell, sandboxShellTools)

	// The MCP family is a prefix family: the document names the exact prefix the
	// code matches on, and the prefix is the only member.
	sameSet(t, "MCP family prefix", documented.MCPFamily, []string{contracts.MCPToolPrefix})

	// The workspace preset's auto-allow set. MCP tools are exempt by prefix and
	// therefore are not map members; the map must not grow a literal mcp__ entry
	// (it could never enumerate the real names, and a stale literal would look
	// like a rule).
	var workspace []string
	for name, allowed := range contracts.WorkspaceAutoAllowedTools {
		if !allowed {
			continue
		}
		if contracts.IsMCPTool(name) {
			t.Fatalf("the workspace auto-allow map holds the MCP tool %q: the family is matched by prefix", name)
		}
		workspace = append(workspace, name)
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

// TestMCPFamilyIsExemptNotParked is the direction change of 2026-11-26: the MCP
// family used to be the documented park list's prefix; it is now the documented
// exempt family, and the two assertions below would both catch a silent revert.
func TestMCPFamilyIsExemptNotParked(t *testing.T) {
	documented := parseExemptions(t)

	if len(documented.MCPFamily) != 1 || documented.MCPFamily[0] != "mcp__" {
		t.Fatalf("documented MCP family = %v, want the single prefix mcp__", documented.MCPFamily)
	}
	for _, name := range documented.Parks {
		if strings.HasSuffix(name, "__") || strings.HasSuffix(name, "*") || contracts.IsMCPTool(name) {
			t.Fatalf("%q is documented as parking on approval but MCP tools are exempt since 2026-11-26", name)
		}
	}
	if !contracts.WorkspaceAutoAllowed("mcp__anything") {
		t.Fatal("the MCP family is exempt in the document but contracts.WorkspaceAutoAllowed says no")
	}
	if contracts.ReadOnlyAutoAllowed("mcp__anything") {
		t.Fatal("the MCP family must stay out of the read-only preset (the hard restriction)")
	}
}

// TestDocumentedFamiliesAreDisjoint keeps the block a decision table: a tool
// either parks or is exempt, never both. Two families claiming one name is how
// the 2026-11-26 audit's missing-park list started (the name was somewhere else
// in the document, so it looked covered).
//
// The registry-side direction ("every registered tool appears in some family")
// lives in internal/modules/toolkit/approval_surface_registry_test.go: only the
// toolkit knows the real tool surface, and this package receives names, not the
// registry.
func TestDocumentedFamiliesAreDisjoint(t *testing.T) {
	documented := parseExemptions(t)
	owner := map[string]string{}
	families := []struct {
		name  string
		names []string
	}{
		{"sandbox-shell", documented.SandboxShell},
		{"workspace-auto", documented.Workspace},
		{"interaction", documented.Interaction},
		{"parks", documented.Parks},
	}
	for _, family := range families {
		for _, name := range family.names {
			if previous, seen := owner[name]; seen {
				t.Fatalf("%q is listed in both %s and %s: a tool either parks or is exempt",
					name, previous, family.name)
			}
			owner[name] = family.name
		}
	}
}

// TestDocumentedParksActuallyPark checks the remaining "these will ask" family:
// none may be auto-allowed, and none is a shell/mcp/interaction exemption.
func TestDocumentedParksActuallyPark(t *testing.T) {
	documented := parseExemptions(t)
	if len(documented.Parks) == 0 {
		t.Fatal("the document lists no tools that park on approval")
	}
	for _, name := range documented.Parks {
		if contracts.WorkspaceAutoAllowed(name) {
			t.Fatalf("%q is documented as parking on approval but the workspace preset auto-allows it", name)
		}
		if contracts.ReadOnlyAutoAllowed(name) {
			t.Fatalf("%q is documented as parking on approval but the read-only preset auto-allows it", name)
		}
		if contracts.IsMCPTool(name) {
			t.Fatalf("%q is documented as parking on approval but MCP tools are exempt", name)
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
// service without one must never claim confinement (and keeps the approval
// flow, so a stripped assembly cannot silently run commands unasked).
func TestSandboxExemptionNeedsAConfinedRunner(t *testing.T) {
	bare := &service{}
	// A fully assembled service needs the whole kernel; the "no sandbox" case is
	// the one that can be built in isolation and it is the dangerous default.
	if bare.sandboxedCommand(t.Context(), t.TempDir(), nil, rig.ToolCall{Name: "bash"}) {
		t.Fatal("a service without a sandbox exempted a shell call")
	}
	if bare.shellCommandExempt(t.TempDir(), nil, rig.ToolCall{Name: "bash"}) {
		t.Fatal("a service without a sandbox ran a shell call without asking")
	}
	if bare.sandboxService != nil {
		t.Fatal("the zero service unexpectedly carries a sandbox")
	}
}

// TestReadScopeIsNotFencedToTheWorkspace pins the read half of §S4.3: the policy
// the read-only file tools run under resolves absolute paths outside the
// workspace, while the workspace policy (write side) still refuses them. It does
// not build the toolkit (that would need the whole kernel); it pins the two
// properties the product relies on inside rig.
func TestReadScopeIsNotFencedToTheWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside\n"), 0o600); err != nil {
		t.Fatalf("write %s: %v", outsideFile, err)
	}

	readPolicy := rigsandbox.New(
		rigsandbox.WithRoots(workspace, string(filepath.Separator)),
		rigsandbox.WithMode(rigsandbox.ModeReadOnly),
	)
	if resolved, err := readPolicy.ResolvePath(outsideFile); err != nil || resolved == "" {
		t.Fatalf("read policy refused a path outside the workspace: %v", err)
	}
	if readPolicy.AllowsWrite(outsideFile) || readPolicy.AllowsWrite(filepath.Join(workspace, "in.txt")) {
		t.Fatal("the read policy allowed a write")
	}

	writePolicy := rigsandbox.New(
		rigsandbox.WithRoots(workspace),
		rigsandbox.WithMode(rigsandbox.ModeWorkspaceWrite),
	)
	if _, err := writePolicy.ResolvePath(outsideFile); err == nil {
		t.Fatal("the workspace policy resolved a path outside the workspace, so the write fence is gone too")
	}
	if err := writePolicy.CheckWritablePath(outsideFile); err == nil {
		t.Fatal("the workspace policy allowed a write outside the workspace")
	}
	if err := writePolicy.CheckWritablePath(filepath.Join(workspace, "in.txt")); err != nil {
		t.Fatalf("the workspace policy refused a write inside the workspace: %v", err)
	}
}
