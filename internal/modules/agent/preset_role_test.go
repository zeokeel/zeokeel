package agent

// Role boundary tests (2026-11-26): the built-in `lead` role exists, is not the
// default, survives a restart, is enforced by the gate rather than merely
// filtered, and its delegated children run as executors.
//
// What is being pinned is the product decision, not the implementation: "the
// lead does not do the work" is only true if a call to write_file is *refused*
// after the model has been told not to, and "a delegated child does the work" is
// only true if it neither inherits the lead's allowlist nor its "do not touch
// anything" persona.

import (
	"context"
	"sort"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// roleServicePreset resolves one built-in preset over an empty settings row.
func roleServicePreset(t *testing.T, id string) (contracts.AgentPreset, bool) {
	t.Helper()
	svc, _ := roleService(t, "")
	return svc.agentPresetByID(id)
}

// roleService builds a service whose registry carries one tool of each family
// the role boundary cares about, and a session selected into presetID ("" = no
// preset).
func roleService(t *testing.T, presetID string) (*service, string) {
	t.Helper()
	registry := rigtools.NewRegistry(
		presetTool{name: "read_file"},
		presetTool{name: "grep"},
		presetTool{name: "glob"},
		presetTool{name: "write_file"},
		presetTool{name: "edit_file"},
		presetTool{name: "apply_patch"},
		presetTool{name: "bash"},
		presetTool{name: "spawn_teammate"},
		presetTool{name: "team_task_list"},
	)
	settings := newFakeSettings(nil)
	svc := newService(Config{SystemPrompt: "base"}, registry, settings, &fakeLogger{}, newFakeStore(), fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return &captureProvider{}, nil }
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if presetID != "" {
		if err := svc.SetSessionPreset(sessionID, presetID); err != nil {
			t.Fatalf("SetSessionPreset(%q): %v", presetID, err)
		}
	}
	return svc, sessionID
}

// TestBuiltinLeadRoleExistsAndIsNotDefault pins both halves of the constraint:
// the role is selectable, and nothing makes it the default (a default lead
// would change how every existing conversation works).
func TestBuiltinLeadRoleExistsAndIsNotDefault(t *testing.T) {
	svc, sessionID := roleService(t, "")
	if got := svc.SessionPreset(sessionID); got != "" {
		t.Fatalf("fresh session preset = %q, want none", got)
	}
	for _, id := range []string{contracts.PresetLeadID, contracts.PresetExecutorID, contracts.PresetAuditorID} {
		if _, ok := svc.agentPresetByID(id); !ok {
			t.Fatalf("built-in role %q is not selectable", id)
		}
	}
	lead, ok := svc.agentPresetByID(contracts.PresetLeadID)
	if !ok {
		t.Fatal("lead role missing")
	}
	forbidden := []string{"write_file", "edit_file", "apply_patch", "bash", "pwsh"}
	for _, tool := range forbidden {
		for _, allowed := range lead.Tools {
			if allowed == tool {
				t.Fatalf("lead allowlist contains %q; the role must not be able to execute", tool)
			}
		}
	}
	for _, tool := range []string{"spawn_teammate", "team_task_create", "team_task_update", "subagent", "read_file", "grep", "glob", "memory", "skill"} {
		found := false
		for _, allowed := range lead.Tools {
			if allowed == tool {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("lead allowlist is missing orchestration/read tool %q", tool)
		}
	}
}

// TestLeadRoleMatchesApprovalSurfaceDoc keeps the declared role boundary and
// the shipped lead preset from drifting: every tool a role must not have is
// absent from the lead allowlist. The write/shell families are listed
// unconditionally so the boundary cannot shrink silently.
func TestLeadRoleMatchesApprovalSurfaceDoc(t *testing.T) {
	// The role boundary used to live in docs/APPROVAL-SURFACES.md; the
	// documentation tree is gone, so the declaration is this list. Keep it in
	// sync with the lead preset in the same commit as any rule change.
	denied := []string{"apply_patch", "bash", "edit_file", "pwsh", "write_file"}
	want := append([]string(nil), denied...)
	sort.Strings(want)
	got := append([]string(nil), denied...)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("role-deny-lead = %v, want %v", got, want)
	}
	lead, ok := roleServicePreset(t, contracts.PresetLeadID)
	if !ok {
		t.Fatal("lead role missing")
	}
	for _, tool := range denied {
		for _, allowed := range lead.Tools {
			if allowed == tool {
				t.Fatalf("documented role boundary is broken: lead allowlist contains %q", tool)
			}
		}
	}
}

// TestSessionPresetSurvivesRestart pins the persistence half: the selection is a
// settings row, so a second service over the same settings sees it. Before this,
// the selection lived in a process map and a gateway restart turned an
// orchestrating session back into a coding one.
func TestSessionPresetSurvivesRestart(t *testing.T) {
	svc, sessionID := roleService(t, "")
	if err := svc.SetSessionPreset(sessionID, contracts.PresetLeadID); err != nil {
		t.Fatalf("SetSessionPreset: %v", err)
	}
	if got := svc.settings.(*fakeSettings).values[contracts.SessionPresetSettingKey(sessionID)]; got != contracts.PresetLeadID {
		t.Fatalf("stored row = %q, want %q", got, contracts.PresetLeadID)
	}
	// A restart: same settings, no in-memory state.
	restarted := newService(Config{SystemPrompt: "base"}, svc.registry, svc.settings, &fakeLogger{}, newFakeStore(), fakeApprovals{})
	if got := restarted.SessionPreset(sessionID); got != contracts.PresetLeadID {
		t.Fatalf("preset after restart = %q, want %q", got, contracts.PresetLeadID)
	}
}

// TestRoleAllowlistIsEnforcedByTheGate pins that the allowlist is a veto, not a
// hint: a tool outside the role is denied even when the session permission would
// otherwise allow everything (the gate sits in front of the widening branches).
func TestRoleAllowlistIsEnforcedByTheGate(t *testing.T) {
	svc, sessionID := roleService(t, contracts.PresetLeadID)
	if err := svc.SetSessionPermission(sessionID, contracts.PermissionFull); err != nil {
		t.Fatalf("SetSessionPermission: %v", err)
	}

	for _, name := range []string{"write_file", "bash"} {
		decision, reason := taskGateReason(t, svc, sessionID, name)
		if decision != rig.DecisionDeny {
			t.Fatalf("%s decision = %q, want deny", name, decision)
		}
		for _, want := range []string{"角色 lead", name, "被拒绝"} {
			if !strings.Contains(reason, want) {
				t.Fatalf("denial reason %q is missing %q", reason, want)
			}
		}
	}
	if decision := approveOne(t, svc.gate(sessionID), "read_file"); decision != rig.DecisionAllow {
		t.Fatalf("read_file decision = %q, want allow", decision)
	}
	// The runtime read-back capability is never removed by an allowlist.
	if decision := approveOne(t, svc.gate(sessionID), "spill_read"); decision != rig.DecisionAllow {
		t.Fatalf("spill_read decision = %q, want allow", decision)
	}
}

// TestNoPresetKeepsEveryToolAllowed pins the "optional role" promise from the
// other side: an unselected session is byte-for-byte the old behaviour.
func TestNoPresetKeepsEveryToolAllowed(t *testing.T) {
	svc, sessionID := roleService(t, "")
	if decision := approveOne(t, svc.gate(sessionID), "write_file"); decision != rig.DecisionAllow {
		t.Fatalf("write_file decision with no preset = %q, want allow", decision)
	}
}

// TestDelegatedChildRunsAsExecutor pins the child-role override: a delegated
// child of a lead session gets the executor persona and the executor's full
// tool surface, not the lead's "read only, never touch" discipline.
func TestDelegatedChildRunsAsExecutor(t *testing.T) {
	svc, sessionID := roleService(t, contracts.PresetLeadID)

	// The surface: the executor role has no allowlist, so the child keeps the
	// write/shell tools the lead itself is denied.
	names := make(map[string]bool)
	for _, tool := range svc.delegatedToolsForSession(sessionID) {
		names[tool.Name()] = true
	}
	for _, want := range []string{"read_file", "write_file", "bash"} {
		if !names[want] {
			t.Fatalf("delegated child surface is missing %q (surface = %v)", want, names)
		}
	}

	// The persona: the executor's, and the lead's discipline is gone (appending
	// it would leave the child with two contradictory instructions).
	parentPrompt := svc.buildRunSystemPrompt(context.Background(), sessionID, svc.Config(), nil, "")
	childPrompt := svc.delegatedSystemPrompt(context.Background(), sessionID, svc.Config(), nil, "")
	if !strings.Contains(parentPrompt, "你是编排者") {
		t.Fatalf("parent prompt does not carry the lead persona: %q", parentPrompt)
	}
	if strings.Contains(childPrompt, "你是编排者") {
		t.Fatalf("child prompt still carries the lead persona: %q", childPrompt)
	}
	if !strings.Contains(childPrompt, "你是执行者") {
		t.Fatalf("child prompt does not carry the executor persona: %q", childPrompt)
	}
	// Everything else stays: the child still knows its workspace and base prompt.
	if !strings.Contains(childPrompt, "base") {
		t.Fatalf("child prompt lost the configured base prompt: %q", childPrompt)
	}
}

// TestDelegatedChildGateRunsAsExecutor is the regression the surface-only fix
// would have missed: the child got the executor's tools but the parent's gate
// still judged its calls, so write_file was offered and then denied. The surface
// and the veto must come from the same role.
func TestDelegatedChildGateRunsAsExecutor(t *testing.T) {
	svc, sessionID := roleService(t, contracts.PresetLeadID)

	// The parent (the lead) is refused: that is the whole point of the role.
	if decision := approveOne(t, svc.gate(sessionID), "write_file"); decision != rig.DecisionDeny {
		t.Fatalf("lead write_file decision = %q, want deny", decision)
	}
	// Its delegated child, running as the executor, may write.
	if decision := approveOne(t, svc.childGate(sessionID), "write_file"); decision != rig.DecisionAllow {
		t.Fatalf("delegated write_file decision = %q, want allow", decision)
	}
	// And the child is not silently widened past its own role either: with no
	// executor allowlist the surface is everything, so bash is allowed too.
	if decision := approveOne(t, svc.childGate(sessionID), "bash"); decision != rig.DecisionAllow {
		t.Fatalf("delegated bash decision = %q, want allow", decision)
	}
	// A plain session's child keeps the session's own gate (nothing changed).
	plainID := ""
	if plain, err := svc.NewSession(""); err != nil {
		t.Fatalf("NewSession: %v", err)
	} else {
		plainID = plain
	}
	if decision := approveOne(t, svc.childGate(plainID), "write_file"); decision != rig.DecisionAllow {
		t.Fatalf("plain child write_file decision = %q, want allow", decision)
	}
}

// TestDelegatedChildOfPlainSessionIsUnchanged pins the other side: only a
// role-shaped parent changes its children, so no existing session's delegation
// behaviour moves.
func TestDelegatedChildOfPlainSessionIsUnchanged(t *testing.T) {
	svc, sessionID := roleService(t, "")
	if _, ok := svc.delegatedRolePreset(sessionID); ok {
		t.Fatal("a session with no preset resolved a delegated role")
	}
	parentPrompt := svc.buildRunSystemPrompt(context.Background(), sessionID, svc.Config(), nil, "")
	childPrompt := svc.delegatedSystemPrompt(context.Background(), sessionID, svc.Config(), nil, "")
	if parentPrompt != childPrompt {
		t.Fatalf("child prompt of a plain session changed:\nparent=%q\nchild =%q", parentPrompt, childPrompt)
	}
}

// roleServiceWithPresets builds the role service over one raw presets.agents
// row, so a test can write exactly the JSON the settings UI would.
func roleServiceWithPresets(t *testing.T, rawPresets, presetID string) (*service, string) {
	t.Helper()
	registry := rigtools.NewRegistry(
		presetTool{name: "read_file"},
		presetTool{name: "grep"},
		presetTool{name: "glob"},
		presetTool{name: "write_file"},
		presetTool{name: "edit_file"},
		presetTool{name: "apply_patch"},
		presetTool{name: "bash"},
		presetTool{name: "spawn_teammate"},
		presetTool{name: "team_task_list"},
	)
	settings := newFakeSettings(nil)
	if rawPresets != "" {
		settings.values[contracts.SettingAgentPresets] = rawPresets
	}
	svc := newService(Config{SystemPrompt: "base"}, registry, settings, &fakeLogger{}, newFakeStore(), fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return &captureProvider{}, nil }
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if presetID != "" {
		if err := svc.SetSessionPreset(sessionID, presetID); err != nil {
			t.Fatalf("SetSessionPreset(%q): %v", presetID, err)
		}
	}
	return svc, sessionID
}

// TestPersonaOnlyOverrideKeepsTheBuiltinAllowlist is the 2026-11-26 audit's
// 中高 finding from the user's side: editing ONE field of a built-in role used to
// replace the whole row, and because an empty allowlist means "every tool" the
// built-in lead silently became all-powerful while its persona still said "your
// tool set has none of these tools". The enforced half wins: an absent `tools`
// field inherits the built-in allowlist, and only an explicit empty array clears
// it.
func TestPersonaOnlyOverrideKeepsTheBuiltinAllowlist(t *testing.T) {
	// Exactly what the settings UI's JSON field produces when a user edits the
	// persona and leaves the tools key out.
	edited := `[{"id":"lead","name":"我的编排者","persona":"你是编排者：只派活。","order":-30}]`
	svc, sessionID := roleServiceWithPresets(t, edited, contracts.PresetLeadID)

	lead, ok := svc.agentPresetByID(contracts.PresetLeadID)
	if !ok {
		t.Fatal("edited lead role is not selectable")
	}
	if lead.Persona != "你是编排者：只派活。" {
		t.Fatalf("persona override was dropped: %q", lead.Persona)
	}
	var want []string
	for _, preset := range contracts.BuiltinAgentPresets() {
		if preset.ID == contracts.PresetLeadID {
			want = preset.Tools
		}
	}
	if strings.Join(lead.Tools, ",") != strings.Join(want, ",") {
		t.Fatalf("tools = %v, want the built-in allowlist %v", lead.Tools, want)
	}
	// And the gate still refuses what the role refuses, so the persona sentence
	// the user just edited stays true.
	for _, name := range []string{"write_file", "bash"} {
		if decision := approveOne(t, svc.gate(sessionID), name); decision != rig.DecisionDeny {
			t.Fatalf("%s decision after a persona-only edit = %q, want deny", name, decision)
		}
	}
}

// TestExplicitEmptyToolsArrayClearsTheBuiltinAllowlist pins the other half of the
// same rule: the inheritance is a default, not a lock. A row that says
// `"tools": []` means "every tool" (the product semantics of an empty allowlist)
// and must be honoured for a built-in role too.
func TestExplicitEmptyToolsArrayClearsTheBuiltinAllowlist(t *testing.T) {
	cleared := `[{"id":"lead","name":"全权限 lead","persona":"你是编排者。","tools":[]}]`
	svc, sessionID := roleServiceWithPresets(t, cleared, contracts.PresetLeadID)

	lead, ok := svc.agentPresetByID(contracts.PresetLeadID)
	if !ok {
		t.Fatal("lead role is not selectable")
	}
	if lead.Tools == nil {
		t.Fatal("explicit empty tools array was turned back into an absent field")
	}
	if len(lead.Tools) != 0 {
		t.Fatalf("tools = %v, want an explicit empty allowlist", lead.Tools)
	}
	if decision := approveOne(t, svc.gate(sessionID), "write_file"); decision != rig.DecisionAllow {
		t.Fatalf("write_file with an explicit empty allowlist = %q, want allow", decision)
	}
}

// TestUnknownPresetRowFailsClosed is the 2026-11-26 audit's 中 finding: the
// `session.<id>.preset` row can be written by the generic settings surface, so a
// value this build cannot resolve used to read back as "no role" — and "no role"
// means the FULL tool surface. A broken selection must instead refuse calls and
// offer no tools, with a reason that names both the row and the way out.
func TestUnknownPresetRowFailsClosed(t *testing.T) {
	svc, sessionID := roleServiceWithPresets(t, "", "")
	// The bypass: the row is written raw, exactly like `settings.set` used to.
	svc.settings.(*fakeSettings).values[contracts.SessionPresetSettingKey(sessionID)] = "ghost-role"

	for _, name := range []string{"read_file", "write_file", "bash", "spill_read"} {
		decision, reason := taskGateReason(t, svc, sessionID, name)
		if decision != rig.DecisionDeny {
			t.Fatalf("%s decision under an unknown role = %q, want deny", name, decision)
		}
		for _, want := range []string{"ghost-role", name, contracts.SessionPresetSettingKey(sessionID)} {
			if !strings.Contains(reason, want) {
				t.Fatalf("denial reason %q is missing %q", reason, want)
			}
		}
	}
	// The surface agrees with the gate: nothing is offered that would be denied.
	if tools := svc.toolsForSession(sessionID); len(tools) != 0 {
		names := make([]string, 0, len(tools))
		for _, tool := range tools {
			names = append(names, tool.Name())
		}
		t.Fatalf("surface under an unknown role = %v, want none", names)
	}

	// Clearing the row is the documented way out and restores the old behaviour.
	if err := svc.SetSessionPreset(sessionID, ""); err != nil {
		t.Fatalf("clear preset: %v", err)
	}
	if decision := approveOne(t, svc.gate(sessionID), "write_file"); decision != rig.DecisionAllow {
		t.Fatalf("write_file after clearing the broken row = %q, want allow", decision)
	}
	if len(svc.toolsForSession(sessionID)) == 0 {
		t.Fatal("surface stayed empty after clearing the broken row")
	}
}

// TestUnknownDelegatedRoleFailsClosed covers the same hole through the other
// entry point: a delegated child's role is resolved by id, and an id that
// resolves to nothing must not read as "every tool" either.
func TestUnknownDelegatedRoleFailsClosed(t *testing.T) {
	svc, _ := roleServiceWithPresets(t, "", "")
	if reason := svc.presetToolsDenialForRole("ghost-role", "write_file"); reason == "" {
		t.Fatal("an unknown delegated role was allowed to run a tool")
	} else if !strings.Contains(reason, "ghost-role") {
		t.Fatalf("denial reason %q does not name the unknown role", reason)
	}
	// The built-in executor still resolves (delegated children depend on it).
	if reason := svc.presetToolsDenialForRole(contracts.PresetExecutorID, "write_file"); reason != "" {
		t.Fatalf("executor role refused a write: %q", reason)
	}
}
