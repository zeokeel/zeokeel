package toolkit

// Bidirectional approval-surface check over the REAL tool registry.
//
// docs/APPROVAL-SURFACES.md §S4.1 claims to be "the real trigger surface of the
// approval card". The 2026-11-26 audit found it had drifted: seven families of
// tools that genuinely park (settings_get, request_sandbox_access, job_kill,
// schedule_*, *worktree*, team_task_create/update) were missing from the parks
// list, and nothing failed — the document-side test only checked
// "documented ⊆ code".
//
// This test is the other direction, and it needs the registry: every tool this
// product registers must be classified somewhere in the documented block — an
// exempt family or parks. A new tool that is not classified fails here instead
// of silently becoming "the one surface nobody described".
//
// It lives in this package rather than in `agent` because the registry the model
// actually receives is built here (agent only receives tool names). The semantic
// checks (no parks entry may be auto-allowed / a shell / MCP / interaction tool)
// stay in internal/modules/agent/approval_surface_test.go.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// docFamilies is the machine-readable block of APPROVAL-SURFACES.md §S4.1.
type docFamilies struct {
	SandboxShell []string
	MCPFamily    []string
	Workspace    []string
	Interaction  []string
	Parks        []string
}

// parseDocFamilies reads the approval-exemptions block.
func parseDocFamilies(t *testing.T) docFamilies {
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
	var families docFamilies
	for _, line := range strings.Split(text[start+len(begin):finish], "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found {
			continue
		}
		var names []string
		for _, part := range strings.Split(value, ",") {
			if name := strings.TrimSpace(part); name != "" {
				names = append(names, name)
			}
		}
		switch strings.TrimSpace(key) {
		case "sandbox-shell":
			families.SandboxShell = names
		case "mcp-family":
			families.MCPFamily = names
		case "workspace-auto":
			families.Workspace = names
		case "interaction":
			families.Interaction = names
		case "parks":
			families.Parks = names
		}
	}
	if len(families.Parks) == 0 || len(families.Workspace) == 0 {
		t.Fatal("the documented table is missing its parks or workspace-auto family")
	}
	return families
}

// familyOf reports which documented family a tool belongs to ("" = none).
func (f docFamilies) familyOf(name string) string {
	for _, list := range []struct {
		family string
		names  []string
	}{
		{"sandbox-shell", f.SandboxShell},
		{"workspace-auto", f.Workspace},
		{"interaction", f.Interaction},
		{"parks", f.Parks},
	} {
		for _, member := range list.names {
			if member == name {
				return list.family
			}
		}
	}
	for _, prefix := range f.MCPFamily {
		if prefix != "" && strings.HasPrefix(name, prefix) {
			return "mcp-family"
		}
	}
	return ""
}

// registeredToolNames returns the names of every tool the product registers, from
// a real activation (the same path a run takes).
func registeredToolNames(t *testing.T) []string {
	t.Helper()
	_, registry, _, _ := activateMCPToolkitWithRoot(t,
		map[string]any{"root": t.TempDir()}, newMutableSettings(map[string]string{}))
	names := make([]string, 0, registry.Len())
	for _, spec := range registry.Specs() {
		names = append(names, spec.Name)
	}
	sort.Strings(names)
	return names
}

// TestEveryRegisteredToolIsClassified is the bidirectional half of §S4.1: no
// registered tool may be undocumented, and no documented park may be a name this
// product does not register (a typo or a removed tool).
func TestEveryRegisteredToolIsClassified(t *testing.T) {
	documented := parseDocFamilies(t)
	registered := registeredToolNames(t)

	registeredSet := make(map[string]bool, len(registered))
	var unclassified []string
	for _, name := range registered {
		registeredSet[name] = true
		if documented.familyOf(name) == "" {
			unclassified = append(unclassified, name)
		}
	}
	if len(unclassified) > 0 {
		t.Fatalf("%d registered tool(s) appear in no §S4.1 family — add each to the parks list "+
			"(it will ask) or to its exempt family, in docs/APPROVAL-SURFACES.md:\n  %s",
			len(unclassified), strings.Join(unclassified, "\n  "))
	}

	// Reverse: a documented park must be a real tool. Two families are registered
	// outside this package (the network tools come from the host's web surface),
	// so they are named here explicitly rather than silently skipped: if one of
	// them ever moves, this map is the thing that must be edited.
	registeredElsewhere := map[string]bool{"web_fetch": true, "web_search": true}
	var stale []string
	for _, name := range documented.Parks {
		if registeredSet[name] || registeredElsewhere[name] {
			continue
		}
		stale = append(stale, name)
	}
	if len(stale) > 0 {
		t.Fatalf("documented as parking but not registered by the product (stale or misspelled):\n  %s",
			strings.Join(stale, "\n  "))
	}

	// A name may belong to exactly one family: parks vs exemptions is a decision,
	// not a list to add to twice.
	var doubled []string
	for _, name := range registered {
		seen := map[string]bool{}
		for _, family := range []string{"sandbox-shell", "workspace-auto", "interaction", "parks"} {
			for _, member := range familyNames(documented, family) {
				if member == name {
					seen[family] = true
				}
			}
		}
		if len(seen) > 1 {
			doubled = append(doubled, name)
		}
	}
	if len(doubled) > 0 {
		t.Fatalf("tools listed in more than one family (a tool either parks or is exempt):\n  %s",
			strings.Join(doubled, "\n  "))
	}
}

// familyNames returns one documented family's members.
func familyNames(documented docFamilies, family string) []string {
	switch family {
	case "sandbox-shell":
		return documented.SandboxShell
	case "workspace-auto":
		return documented.Workspace
	case "interaction":
		return documented.Interaction
	case "parks":
		return documented.Parks
	}
	return nil
}

// TestParksIncludeTheToolsTheAuditFound guards the seven families the audit found
// missing: if one is moved back to an exempt family (or deleted from the block),
// the reason has to be deliberate rather than a quiet edit.
func TestParksIncludeTheToolsTheAuditFound(t *testing.T) {
	documented := parseDocFamilies(t)
	want := []string{
		"settings_get",
		"request_sandbox_access",
		"job_kill",
		"schedule_create", "schedule_list", "schedule_update", "schedule_delete",
		"enter_worktree", "exit_worktree", "list_worktrees",
		"team_task_create", "team_task_update",
	}
	parks := make(map[string]bool, len(documented.Parks))
	for _, name := range documented.Parks {
		parks[name] = true
	}
	var missing []string
	for _, name := range want {
		if !parks[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("these tools park on approval but are gone from the documented parks list: %v", missing)
	}
	// They must also not be auto-allowed anywhere: that would turn a park into a
	// silent allow while the document claims otherwise.
	for _, name := range want {
		if contracts.WorkspaceAutoAllowed(name) || contracts.ReadOnlyAutoAllowed(name) {
			t.Fatalf("%q is documented as parking but an automatic allow list contains it", name)
		}
	}
}
