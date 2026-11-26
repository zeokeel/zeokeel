package agent

// Agent presets: a named persona/prompt fragment, optional default model and
// optional model-visible tool allowlist. A session stores only the preset ID
// (one settings row, contracts.SessionPresetSettingKey); every run resolves the
// preset fresh from the presets.agents row plus the built-in roles so edits
// apply on the next turn. Preset instructions are appended after the base
// prompt and workspace instructions: base → AGENTS.md → persona → prompt.
//
// Two boundaries live here:
//
//   - The allowlist is a real veto, not prompt hygiene. toolsForSession already
//     hid the tools, but a hidden tool is only "not offered": the model can
//     still ask for it (a stale transcript, a teammate's serialized call). The
//     gate branch in presetToolsDenial refuses it, which is what makes the lead
//     role unable to write files or run commands rather than merely unwilling.
//   - A delegated child does not inherit an orchestrator's persona. Children are
//     executors: they get the executor role's persona and tool surface instead
//     of the parent's "do not touch anything" discipline (see
//     delegatedRolePreset / delegatedSystemPrompt).

import (
	"context"
	"fmt"
	"sort"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/tools/surface"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// SetSessionPreset implements contracts.SessionPresetSetter: it selects one
// configured agent preset for a session ("" clears it). Unknown sessions and
// unknown preset IDs are rejected.
//
// The selection is persisted as a `session.<id>.preset` settings row, so it
// survives a restart with the conversation. The in-memory map this used to
// write meant a gateway restart silently demoted an orchestrating session back
// to a plain coding agent.
func (s *service) SetSessionPreset(sessionID, presetID string) error {
	sessionID = strings.TrimSpace(sessionID)
	presetID = strings.TrimSpace(presetID)
	s.mu.Lock()
	_, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: unknown chat session %q", contracts.ErrSessionPresetInvalid, sessionID)
	}
	if presetID != "" {
		if _, found := s.agentPresetByID(presetID); !found {
			return fmt.Errorf("%w: unknown agent preset %q", contracts.ErrSessionPresetInvalid, presetID)
		}
	}
	if s.settings == nil {
		return ErrSettingsUnavailable
	}
	return s.settings.Set(contracts.SessionPresetSettingKey(sessionID), presetID)
}

// SessionPreset reports the session's selected preset ID ("" when none).
func (s *service) SessionPreset(sessionID string) string {
	return s.sessionPresetRow(sessionID)
}

// sessionPresetRow reads the raw selection row ("" when the session has none).
func (s *service) sessionPresetRow(sessionID string) string {
	if s.settings == nil {
		return ""
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	return strings.TrimSpace(s.settings.Get(contracts.SessionPresetSettingKey(sessionID), ""))
}

// clearSessionPresetRow forgets a deleted session's selection. SettingsService
// has no delete call, so the row is cleared to "" — which reads back as "no
// preset" everywhere, exactly like a row that never existed.
func (s *service) clearSessionPresetRow(sessionID string) {
	if s.settings == nil {
		return
	}
	if err := s.settings.Set(contracts.SessionPresetSettingKey(sessionID), ""); err != nil {
		s.logger.Printf("[agent] clear preset of deleted session %s: %v", sessionID, err)
	}
}

// isBuiltinPreset reports whether id names a shipped role.
func (s *service) isBuiltinPreset(id string) bool {
	id = strings.TrimSpace(id)
	for _, preset := range contracts.BuiltinAgentPresets() {
		if preset.ID == id {
			return true
		}
	}
	return false
}

// effectiveAgentPresets merges the built-in roles under the presets.agents row:
// a user row overrides a built-in with the same ID, extra user presets are
// appended in their own order. Rows without an ID/name are dropped (malformed
// JSON behaves as "no user presets" rather than as "no roles at all").
//
// A user row that reuses a built-in role ID replaces that role field by field,
// with ONE exception: an omitted `tools` field keeps the built-in allowlist
// (mergeUserPresetOverBuiltin). See that function for why.
func (s *service) effectiveAgentPresets() []contracts.AgentPreset {
	var user []contracts.AgentPreset
	if s.settings != nil {
		parsed, err := contracts.ParseAgentPresets(s.settings.Get(contracts.SettingAgentPresets, ""))
		if err != nil {
			s.logger.Printf("[agent] parse %s: %v", contracts.SettingAgentPresets, err)
		}
		user = parsed
	}
	builtin := normalizeAgentPresets(contracts.BuiltinAgentPresets())
	out := make([]contracts.AgentPreset, 0, len(builtin)+len(user))
	index := make(map[string]int, len(builtin)+len(user))
	for _, preset := range builtin {
		index[preset.ID] = len(out)
		out = append(out, preset)
	}
	for _, preset := range normalizeAgentPresets(user) {
		if at, ok := index[preset.ID]; ok {
			out[at] = mergeUserPresetOverBuiltin(out[at], preset)
			continue
		}
		index[preset.ID] = len(out)
		out = append(out, preset)
	}
	return out
}

// mergeUserPresetOverBuiltin folds one user row over the built-in role it
// reuses the ID of.
//
// Every field the row carries wins (name, persona, prompt, model, order). The
// tool allowlist is the exception: an ABSENT `tools` field inherits the built-in
// one, and only an explicit `"tools": []` clears it. The reason is the product
// semantics written everywhere else — an empty allowlist means "every tool" —
// so a user row that only edits the persona used to widen the built-in lead into
// the full surface silently, while the persona text it just edited still said
// "your tool set has none of these tools" (2026-11-26 audit, 中高). One of the
// two statements had to lose; the enforced one wins, and the sentence the model
// reads stays true.
//
// The inherited slice is copied: a built-in preset is returned fresh per call,
// but sharing a backing array with the caller's view invites an aliasing bug the
// day somebody mutates a preset in place.
func mergeUserPresetOverBuiltin(builtin, row contracts.AgentPreset) contracts.AgentPreset {
	merged := row
	if merged.Tools == nil {
		merged.Tools = append([]string(nil), builtin.Tools...)
	}
	return merged
}

// normalizeAgentPresets trims one preset list and drops rows that cannot be
// selected (no id or no display name).
func normalizeAgentPresets(presets []contracts.AgentPreset) []contracts.AgentPreset {
	out := make([]contracts.AgentPreset, 0, len(presets))
	for _, preset := range presets {
		preset.ID = strings.TrimSpace(preset.ID)
		preset.Name = strings.TrimSpace(preset.Name)
		if preset.ID == "" || preset.Name == "" {
			continue
		}
		preset.Persona = strings.TrimSpace(preset.Persona)
		preset.Prompt = strings.TrimSpace(preset.Prompt)
		preset.Model = strings.TrimSpace(preset.Model)
		preset.Tools = normalizePresetTools(preset.Tools)
		out = append(out, preset)
	}
	return out
}

// normalizePresetTools trims one allowlist while preserving the difference the
// rest of this file depends on: nil (the `tools` field is absent) is not the same
// as an empty non-nil slice (an explicit `"tools": []`). An absent field inherits
// a built-in role's allowlist; an explicit empty one clears it (and, for a plain
// preset, means "every tool").
//
// The loop used to rebuild the slice unconditionally, which turned every absent
// field into an explicit empty one and made the two indistinguishable before the
// merge ever saw them.
func normalizePresetTools(tools []string) []string {
	if tools == nil {
		return nil
	}
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		if trimmed := strings.TrimSpace(tool); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// agentPresetByID resolves one preset from the effective set.
func (s *service) agentPresetByID(id string) (contracts.AgentPreset, bool) {
	id = strings.TrimSpace(id)
	for _, preset := range s.effectiveAgentPresets() {
		if preset.ID == id {
			return preset, true
		}
	}
	return contracts.AgentPreset{}, false
}

// agentPresetForSessionLocked resolves the selected preset for a session. The
// caller holds s.mu (reading settings itself is goroutine-safe); the name is
// historical and kept so the call sites read the same.
func (s *service) agentPresetForSessionLocked(sessionID string) (contracts.AgentPreset, bool) {
	id := s.sessionPresetRow(sessionID)
	if id == "" {
		return contracts.AgentPreset{}, false
	}
	return s.agentPresetByID(id)
}

// withPresetInstructions appends the preset persona and prompt to the run's
// system prompt (persona first, prompt second). Missing/empty fragments are
// skipped; the base prompt and workspace instructions stay first.
func (s *service) withPresetInstructions(sessionID string, cfg contracts.ChatConfig) contracts.ChatConfig {
	for _, fragment := range s.presetInstructionFragments(sessionID) {
		cfg.SystemPrompt = appendPromptSection(cfg.SystemPrompt, fragment)
	}
	return cfg
}

// presetInstructionFragments returns the selected preset's instruction
// fragments in prompt order (persona first, then the extra prompt). It is the
// section source of the preset prompt sections.
func (s *service) presetInstructionFragments(sessionID string) []string {
	preset, ok := s.agentPresetForSession(sessionID)
	if !ok {
		return nil
	}
	return presetFragments(preset)
}

// presetFragments renders one preset's instruction fragments in prompt order.
func presetFragments(preset contracts.AgentPreset) []string {
	fragments := make([]string, 0, 2)
	for _, fragment := range []string{preset.Persona, preset.Prompt} {
		if fragment = strings.TrimSpace(fragment); fragment != "" {
			fragments = append(fragments, fragment)
		}
	}
	return fragments
}

// agentPresetForSession reads the selected preset without the service lock. A
// row that names a preset this build does not have reads back as "not found",
// exactly like "no preset" — callers that must tell the two apart use
// sessionPresetResolution.
func (s *service) agentPresetForSession(sessionID string) (contracts.AgentPreset, bool) {
	id := s.sessionPresetRow(sessionID)
	if id == "" {
		return contracts.AgentPreset{}, false
	}
	return s.agentPresetByID(id)
}

// sessionPresetResolution resolves a session's role and keeps the raw row, so a
// caller can distinguish the two "no preset" cases:
//
//   - rowID == "": the session never selected a role. Unrestricted, the
//     historical behaviour, and the reason the role stays optional.
//   - rowID != "" and !found: the session HAS a selection this build cannot
//     resolve (the preset was deleted, or `settings.set` wrote
//     `session.<id>.preset` directly and bypassed SetSessionPreset's check).
//     This is a broken role, not an absent one, and every caller treats it
//     fail-closed: the gate refuses tool calls (presetToolsDenial) and the
//     surface offers nothing (toolsForSession) instead of falling back to "no
//     role ⇒ every tool" — which is what a bad settings write used to buy.
func (s *service) sessionPresetResolution(sessionID string) (preset contracts.AgentPreset, rowID string, found bool) {
	rowID = s.sessionPresetRow(sessionID)
	if rowID == "" {
		return contracts.AgentPreset{}, "", false
	}
	preset, found = s.agentPresetByID(rowID)
	return preset, rowID, found
}

// presetIDsFromRows lists the role ids this build can resolve, from ONE settings
// snapshot: the built-in roles plus the `presets.agents` row. Malformed JSON
// contributes nothing, which is exactly what the read side does — such a row is
// now visible as "the role does not resolve" instead of only being logged.
func presetIDsFromRows(rows map[string]string) map[string]bool {
	builtin := normalizeAgentPresets(contracts.BuiltinAgentPresets())
	ids := make(map[string]bool, len(builtin))
	for _, preset := range builtin {
		ids[preset.ID] = true
	}
	user, err := contracts.ParseAgentPresets(rows[contracts.SettingAgentPresets])
	if err != nil {
		return ids
	}
	for _, preset := range normalizeAgentPresets(user) {
		ids[preset.ID] = true
	}
	return ids
}

// fillPresetDisclosure sets PresetID / PresetProblem from a single settings
// snapshot.
//
// The fail-closed half of a broken role already existed — no tools, every call
// refused — but only in the gate and in one log line, so the chat screen looked
// like an ordinary conversation that had stopped working (2026-11-26 audit, 高).
// This is the same judgement in a form a client can draw: the stored id, and a
// code (never a message, a path or a stack) when it does not resolve.
//
// rows is the frame's snapshot, not a per-session read: the session list cache
// exists so a steady-state frame costs O(1) settings reads, and a disclosure is
// not a reason to give that up (AR-20).
func (s *service) fillPresetDisclosure(infos []contracts.ChatSessionInfo, rows map[string]string) {
	if len(infos) == 0 || s.settings == nil {
		return
	}
	known := presetIDsFromRows(rows)
	for index := range infos {
		rowID := strings.TrimSpace(rows[contracts.SessionPresetSettingKey(infos[index].ID)])
		if rowID == "" {
			// No stored role: nothing to disclose, and the session is unrestricted.
			continue
		}
		infos[index].PresetID = rowID
		if !known[rowID] {
			infos[index].PresetProblem = contracts.PresetProblemUnknown
		}
	}
}

// unknownPresetDenial is the model-visible refusal for a session whose stored
// role does not exist. It names the row and the two ways out: selecting a role
// that exists, or clearing the row.
func unknownPresetDenial(sessionID, rowID, name string) string {
	reason := fmt.Sprintf("会话选的角色 %q 在本产品里不存在", rowID)
	if sessionID != "" {
		reason = fmt.Sprintf("会话选的角色 %q 在本产品里不存在（%s）", rowID, contracts.SessionPresetSettingKey(sessionID))
	}
	if name != "" {
		reason += fmt.Sprintf("；按未知角色拒绝执行工具 %s", name)
	}
	return reason + "。这不等于\"没有角色\"：请用 session.preset.set 重新选择一个存在的角色，或把该行清空。"
}

// delegatedRolePreset resolves the role a delegated child run of sessionID
// executes as. An orchestrator's children are executors: the lead either writes
// nothing (enforced by its allowlist) or delegates work that must be done by
// someone who can. Any other preset keeps the historical behaviour (children
// inherit nothing extra), so this only changes sessions that opted into a role.
func (s *service) delegatedRolePreset(sessionID string) (contracts.AgentPreset, bool) {
	parent, ok := s.agentPresetForSession(sessionID)
	if !ok {
		return contracts.AgentPreset{}, false
	}
	switch parent.ID {
	case contracts.PresetLeadID, contracts.PresetAuditorID:
		return s.agentPresetByID(contracts.PresetExecutorID)
	}
	return contracts.AgentPreset{}, false
}

// delegatedToolsForSession is the model-visible surface of a delegated child.
// When the parent session runs as an orchestrator the child gets the executor
// role's surface (an orchestrator allowlist would otherwise leave the child
// unable to write the very files it was asked to write); every other session
// keeps the historical "same surface as the parent" rule.
func (s *service) delegatedToolsForSession(sessionID string) []rig.Tool {
	if role, ok := s.delegatedRolePreset(sessionID); ok {
		return s.filterSessionToolsWith(s.enabledRegistryTools(), role)
	}
	return s.toolsForSession(sessionID)
}

// delegatedSystemPrompt builds the system prompt of a delegated child that runs
// as a different role from its parent session.
//
// The parent's preset sections are replaced, not appended to: appending "you
// are an executor, do the work" after "you never write files" is a
// contradiction the model has to resolve, and the section order decides who
// wins. Workspace instructions, the base prompt and every runtime section stay
// byte-identical, so the child still knows where it is and what it may do.
func (s *service) delegatedSystemPrompt(ctx context.Context, sessionID string, cfg contracts.ChatConfig,
	toolNames map[string]bool, workspaceRoot string) string {
	prompt, sections := s.buildRunSystemPromptSections(ctx, sessionID, cfg, toolNames, workspaceRoot)
	role, ok := s.delegatedRolePreset(sessionID)
	if !ok {
		return prompt
	}
	roleSections := make([]contracts.PromptSection, 0, 2)
	for index, fragment := range presetFragments(role) {
		id := PromptSectionPresetPersona
		if index > 0 {
			id = PromptSectionPresetPrompt
		}
		if section := newPromptSection(id, "preset", fragment); section.ID != "" {
			roleSections = append(roleSections, section)
		}
	}
	out := make([]contracts.PromptSection, 0, len(sections)+len(roleSections))
	inserted := false
	for _, section := range sections {
		if section.ID == PromptSectionPresetPersona || section.ID == PromptSectionPresetPrompt {
			// Replace at the position the parent preset occupied, so the order
			// stays base → workspace → persona → prompt.
			if !inserted {
				out = append(out, roleSections...)
				inserted = true
			}
			continue
		}
		out = append(out, section)
	}
	if !inserted {
		out = append(out, roleSections...)
	}
	return renderPromptSections(out)
}

// toolsForSession returns the registry tools the model may see in one session:
// the user's disabled-tool settings are removed, the selected preset's allowlist
// narrows the surface (empty allowlist keeps every tool, registration order is
// preserved), and the runtime mode (plan/minimal/creator) keeps only the tools
// its gate would allow. The gate still denies non-whitelisted calls fail-closed:
// filtering is prompt hygiene, the security boundary is presetToolsDenial.
// Delegated child runs (subagent/team) resolve their own role's surface first
// (delegatedToolsForSession) and then exclude orchestration tools.
func (s *service) toolsForSession(sessionID string) []rig.Tool {
	return s.filterSessionTools(sessionID, s.enabledRegistryTools())
}

// filterSessionToolsWith applies one preset's allowlist and the runtime mode to
// a tool set.
func (s *service) filterSessionToolsWith(tools []rig.Tool, preset contracts.AgentPreset) []rig.Tool {
	if len(preset.Tools) > 0 {
		tools = filterToolsByAllowlist(tools, preset.Tools)
	}
	// Role-scoped MCP servers (M3b): a tool from a server the role does not name
	// is not offered at all.
	if len(preset.MCPServers) > 0 {
		allowed := mcpServerSet(preset.MCPServers)
		kept := make([]rig.Tool, 0, len(tools))
		for _, tool := range tools {
			if server, isMCP := mcpToolServer(tool.Name()); isMCP && !allowed[server] {
				continue
			}
			kept = append(kept, tool)
		}
		tools = kept
	}
	// Deny (M3) is applied after the allowlist and before the runtime mode: a
	// denied tool is never offered, even by a role whose allowlist is "everything".
	if len(preset.Deny) > 0 {
		tools = withoutToolNames(tools, preset.Deny)
	}
	return filterToolsForMode(tools, s.runtimeMode())
}

// filterSessionTools applies the session preset allowlist and the runtime mode
// to a tool set (the session form of filterSessionToolsWith).
//
// A session whose stored role does not exist gets NO tools: the gate refuses
// every call anyway (presetToolsDenial), and offering a surface the gate will
// deny only teaches the model to retry.
func (s *service) filterSessionTools(sessionID string, tools []rig.Tool) []rig.Tool {
	preset, rowID, found := s.sessionPresetResolution(sessionID)
	switch {
	case rowID == "":
		return filterToolsForMode(tools, s.runtimeMode())
	case !found:
		return nil
	default:
		return s.filterSessionToolsWith(tools, preset)
	}
}

// presetToolsDenial returns the model-visible reason a session's preset
// allowlist refuses name, or "" when the call may proceed.
//
// It is the enforcement half of the surface filter: a tool that is not offered
// is still callable — the model may replay an old call, a teammate may reuse a
// serialized one — so the gate refuses it here, before plan mode, the session
// permission override, the sandbox exemption and the approval dialog (each of
// those can only widen). An empty allowlist means "every tool", a session with
// no preset is unrestricted, and the runtime capability tools (spill_read,
// tool_search) are never removed by an allowlist because they are the
// read-back path for oversized results rather than a capability to grant.
func (s *service) presetToolsDenial(sessionID, name string) string {
	preset, rowID, found := s.sessionPresetResolution(sessionID)
	switch {
	case rowID == "":
		return ""
	case !found:
		return unknownPresetDenial(sessionID, rowID, name)
	default:
		return presetToolsDenialForPreset(preset, name)
	}
}

// presetToolsDenialForRole is presetToolsDenial for a role resolved by id: a
// delegated child runs as the executor role while its session may be a lead.
func (s *service) presetToolsDenialForRole(presetID, name string) string {
	presetID = strings.TrimSpace(presetID)
	if presetID == "" {
		return ""
	}
	preset, ok := s.agentPresetByID(presetID)
	if !ok {
		// A role id that resolves to nothing (the delegated role of a session
		// whose preset row is broken) is refused, not treated as "every tool".
		return unknownPresetDenial("", presetID, name)
	}
	return presetToolsDenialForPreset(preset, name)
}

// presetToolsDenialForPreset is the shared body of the two above.
func presetToolsDenialForPreset(preset contracts.AgentPreset, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	// Deny is checked first and independently of the allowlist: "everything
	// except X" is exactly the role shape an empty allowlist cannot express, and
	// a denied name must not be rescued by a remembered "always allow" or an
	// approval dialog further down the gate.
	for _, denied := range preset.Deny {
		if strings.TrimSpace(denied) == name {
			return fmt.Sprintf("角色 %s 禁止使用工具 %s；这次调用被拒绝", preset.ID, name)
		}
	}
	// Role-scoped MCP servers (M3b) are checked before the allowlist: an MCP tool
	// from a server the role does not name is refused whether the allowlist is
	// empty ("everything") or names it.
	if len(preset.MCPServers) > 0 {
		if server, isMCP := mcpToolServer(name); isMCP && !mcpServerSet(preset.MCPServers)[server] {
			return fmt.Sprintf("角色 %s 只允许 MCP server %v；%s 属于 %s，这次调用被拒绝", preset.ID, preset.MCPServers, name, server)
		}
	}
	if len(preset.Tools) == 0 {
		return ""
	}
	if runtimeCapabilityTools[name] {
		return ""
	}
	for _, allowed := range preset.Tools {
		if allowed == name {
			return ""
		}
	}
	return fmt.Sprintf("角色 %s 只允许这些工具：%v；这次调用 %s 被拒绝", preset.ID, preset.Tools, name)
}

// runtimeCapabilityTools are kept even when a preset allowlist does not name
// them: they are the read-back path for oversized tool results, which is a
// runtime capability rather than a capability the user chooses to grant (a
// surface without spill_read leaves every spilled result unreadable).
var runtimeCapabilityTools = map[string]bool{
	"spill_read":  true,
	"tool_search": true,
}

// filterToolsByAllowlist keeps the named tools in registration order; runtime
// capability tools stay even when a preset does not name them.
func filterToolsByAllowlist(tools []rig.Tool, allow []string) []rig.Tool {
	return surface.OnlyNames(tools, allow, runtimeCapabilityNames()...)
}

// runtimeCapabilityNames lists the capability tools a preset may not remove.
func runtimeCapabilityNames() []string {
	names := make([]string, 0, len(runtimeCapabilityTools))
	for name := range runtimeCapabilityTools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// withoutToolNames drops the named tools from a surface (SDK filter).
func withoutToolNames(tools []rig.Tool, names []string) []rig.Tool {
	return surface.WithoutNames(tools, names)
}

// enabledRegistryTools returns the registry tools without the user-disabled
// ones. Every agent surface that hands tools to a model starts from this set —
// the main run (then narrowed by the preset allowlist) and the delegated child
// runs (subagent/team members) — so a disabled tool never reaches any model.
func (s *service) enabledRegistryTools() []rig.Tool {
	tools := s.registry.Tools()
	disabled := s.disabledTools()
	if len(disabled) == 0 {
		return tools
	}
	filtered := make([]rig.Tool, 0, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name())
		if disabled[name] && !runtimeCapabilityTools[name] {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

// disabledTools reads the union of tools.disabled and mcp.disabledTools (JSON
// string arrays). A malformed row is logged and ignored, matching the toolkit's
// best-effort policy: a bad settings value must never remove every tool.
func (s *service) disabledTools() map[string]bool {
	if s.settings == nil {
		return nil
	}
	disabled := make(map[string]bool)
	for _, key := range []string{contracts.SettingToolsDisabled, contracts.SettingMCPDisabledTools} {
		names, err := contracts.ParseStringListJSON(s.settings.Get(key, ""))
		if err != nil {
			s.logger.Printf("[agent] parse %s: %v", key, err)
			continue
		}
		for _, name := range names {
			if trimmed := strings.TrimSpace(name); trimmed != "" {
				disabled[trimmed] = true
			}
		}
	}
	return disabled
}

// mcpToolServer splits a model-visible MCP tool name (mcp__<server>__<tool>) into
// its server. Anything else is not an MCP tool.
func mcpToolServer(name string) (string, bool) {
	const prefix = "mcp__"
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(name, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(name, prefix)
	index := strings.Index(rest, "__")
	if index <= 0 {
		return "", false
	}
	return rest[:index], true
}

// mcpServerSet is the lookup form of a preset's server list.
func mcpServerSet(servers []string) map[string]bool {
	set := make(map[string]bool, len(servers))
	for _, server := range servers {
		if trimmed := strings.TrimSpace(server); trimmed != "" {
			set[trimmed] = true
		}
	}
	return set
}

// AllowedSkills implements contracts.SkillScope: the skills the session's role
// names, or "unrestricted" when it names none (or has no role).
func (s *service) AllowedSkills(ctx context.Context) ([]string, bool) {
	if s == nil {
		return nil, false
	}
	sessionID := strings.TrimSpace(contracts.AgentSessionFromContext(ctx))
	if sessionID == "" {
		return nil, false
	}
	preset, ok := s.agentPresetForSession(sessionID)
	if !ok || len(preset.Skills) == 0 {
		return nil, false
	}
	return append([]string(nil), preset.Skills...), true
}
