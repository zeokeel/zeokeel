// Package prompt composes ZeoKeel's system instruction from ordered,
// locale-aware sections in the style of DeepSeek Harness (DSH).
//
// The package owns only composition: it never calls a model, reads the
// session, or discovers tools. The caller supplies a Context and receives a
// single deterministic string suitable for runtime.Config.Instruction.
//
// Sections are rendered in ascending Order. Static sections use their EN or ZH
// text, while dynamic sections (working directory, policies, AGENTS.md, skills,
// time, and a few tool hints) derive their text from the Context. Tool sections
// are included only when at least one of their gated tools is present, which
// mirrors DSH's per-plugin registrations without requiring a service registry.
//
// Composition is pure and safe for concurrent use. The returned prompt has no
// leading or trailing whitespace and no run of three or more newlines.
package prompt

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Section order constants mirror DSH's SECTION_ORDERS. Dynamic runtime-context
// sections use the lower CONTEXT_ORDERS range, so they are inlined before the
// plan policy: ZeoKeel passes one Instruction string instead of DSH's separate
// system prompt and runtime-context snapshot.
const (
	// OrderHarnessIdentity matches DSH HARNESS_IDENTITY (-1000).
	OrderHarnessIdentity = -1000
	// OrderPersonaPrefix matches DSH DEPLOYMENT_PERSONA_PREFIX (0).
	OrderPersonaPrefix = 0

	// OrderWorkingDirectory matches DSH CONTEXT_ORDERS.WORKING_DIRECTORY.
	OrderWorkingDirectory = 100
	// OrderSandboxPolicy matches DSH CONTEXT_ORDERS.SANDBOX_POLICY.
	OrderSandboxPolicy = 110
	// OrderApprovalPolicy matches DSH CONTEXT_ORDERS.APPROVAL_POLICY.
	OrderApprovalPolicy = 115
	// OrderSubagentDelegation matches DSH CONTEXT_ORDERS.SUBAGENT_DELEGATION.
	OrderSubagentDelegation = 120
	// OrderAgentsMD is an extension with no DSH constant: DSH delivers
	// AGENTS.md as a message, while ZeoKeel inlines it into the instruction.
	OrderAgentsMD = 130
	// OrderSkillsCatalog is an extension with no DSH constant: DSH delivers the
	// skill catalog as a message, while ZeoKeel inlines it here.
	OrderSkillsCatalog = 140
	// OrderMemoryContext is an extension with no DSH constant: persistent
	// workspace memories sit between the skill catalog and the clock.
	OrderMemoryContext = 145
	// OrderTimeContext is an extension with no DSH constant: DSH appends time
	// readings to request history, while ZeoKeel inlines the current clock.
	OrderTimeContext = 150

	// OrderPlanPolicy matches DSH PLAN_POLICY (500).
	OrderPlanPolicy = 500
	// OrderTeamPolicy matches DSH TEAM_POLICY (600).
	OrderTeamPolicy = 600
	// OrderModePTC matches DSH PTC_ONLY (800): the pipeline-style PTC policy.
	OrderModePTC = 800
	// OrderModeMinimal is an extension with no DSH constant; the minimal mode
	// policy sits just after PTC and before file-reference guidance.
	OrderModeMinimal = 810
	// OrderModeCreator is an extension with no DSH constant; the Creator mode
	// policy sits next to the other agent-mode policies.
	OrderModeCreator = 820
	// OrderFileReference matches DSH FILE_REFERENCE (900).
	OrderFileReference = 900

	// OrderToolBash matches DSH TOOL_BASH (1000).
	OrderToolBash = 1000
	// OrderToolRead matches DSH TOOL_READ (1100).
	OrderToolRead = 1100
	// OrderToolWrite matches DSH TOOL_WRITE (1200).
	OrderToolWrite = 1200
	// OrderToolEdit matches DSH TOOL_EDIT (1300).
	OrderToolEdit = 1300
	// OrderToolGlob matches DSH TOOL_GLOB (1400).
	OrderToolGlob = 1400
	// OrderToolGrep matches DSH TOOL_GREP (1500).
	OrderToolGrep = 1500
	// OrderToolJobs matches DSH TOOL_JOBS (1600).
	OrderToolJobs = 1600
	// OrderToolTerminal matches DSH TOOL_PTY (1700).
	OrderToolTerminal = 1700
	// OrderToolSkill is an extension with no DSH constant; it sits after the
	// terminal tool and before the web tools.
	OrderToolSkill = 1800
	// OrderToolTodo is an extension with no DSH constant.
	OrderToolTodo = 1900
	// OrderToolAskUser is an extension with no DSH constant.
	OrderToolAskUser = 1950
	// OrderToolWebSearch matches DSH TOOL_WEB_SEARCH (2000).
	OrderToolWebSearch = 2000
	// OrderToolWebFetch matches DSH TOOL_WEB_FETCH (2100).
	OrderToolWebFetch = 2100
	// OrderToolSessionQuery matches DSH TOOL_SESSION_QUERY (2300).
	OrderToolSessionQuery = 2300
	// OrderToolGoal matches DSH TOOL_GOAL (2400).
	OrderToolGoal = 2400
	// OrderToolWorkflow matches DSH TOOL_WORKFLOW (2600).
	OrderToolWorkflow = 2600
	// OrderToolSubagent matches DSH TOOL_SUBAGENT (2800).
	OrderToolSubagent = 2800
	// OrderToolReport matches DSH TOOL_REPORT (2900).
	OrderToolReport = 2900
	// OrderToolPresent reuses DSH's unused TOOL_COMPUTER_USE slot (3000),
	// which ZeoKeel does not have.
	OrderToolPresent = 3000
	// OrderToolMCP matches DSH MCP_SERVERS (3100).
	OrderToolMCP = 3100

	// OrderDeliverableReferences matches DSH DELIVERABLE_FILE_REFERENCES (9000).
	OrderDeliverableReferences = 9000
	// OrderStructuredOutput matches DSH STRUCTURED_OUTPUT (9900).
	OrderStructuredOutput = 9900
	// OrderHarnessNote matches DSH HARNESS_SOURCE (10000).
	OrderHarnessNote = 10000
	// OrderWebSurface matches DSH WEB_SURFACE (10100).
	OrderWebSurface = 10100
	// OrderPersonaSuffix matches DSH DEPLOYMENT_PERSONA_SUFFIX (10200).
	OrderPersonaSuffix = 10200
)

// Context is the composition input: the runtime facts and available tool names
// for one model step. Zero values are safe: an empty Context composes only the
// always-on sections with the default read-only file policy and ask approval
// policy.
type Context struct {
	// WorkDir is the session's current working directory. Empty omits the
	// working-directory section.
	WorkDir string
	// SandboxPolicy is "read-only", "workspace-write", or "danger-full-access".
	// Empty (or an unknown value) uses read-only, the DSH deployment default.
	SandboxPolicy string
	// ApprovalPolicy is "ask" or "never". Empty (or an unknown value) uses ask.
	ApprovalPolicy string
	// Skills is the session skill catalog, as skill names. The catalog is
	// rendered sorted and deduplicated; empty omits the catalog.
	Skills []string
	// AgentsMD is the workspace AGENTS.md content. Empty omits the section.
	AgentsMD string
	// Memories is the workspace's persistent memory notes, newest first. The
	// rendered section is bounded to 20 notes and 4000 characters and omitted
	// when empty. Entries are stored data, not instructions.
	Memories []string
	// Language selects EN or ZH. "zh", "zh-CN", "zh-Hans", and "chinese" select
	// Simplified Chinese; everything else selects English.
	Language string
	// Mode enables collaboration policy: "plan", "team", or "subagent" (see
	// modeIs for the accepted spellings).
	Mode string
	// PlanMode reports that the session is in plan mode. It enables the
	// plan:policy section independently of Mode, so the per-session plan review
	// gate can coexist with any agent preset.
	PlanMode bool
	// AgentMode enables the selected agent preset's policy: "standard",
	// "ptc", "minimal", or "creator". It is separate from Mode because a plan
	// or team collaboration state can coexist with an agent preset. For
	// convenience the policy sections also accept these names in Mode, which
	// keeps prompt-only callers simple.
	AgentMode string
	// Time is the reference clock for the current step. Zero omits the time
	// section.
	Time time.Time
	// Tools is the names of tools registered for this step. Tool sections are
	// included only when one of their gate names (or an alias) is present.
	Tools []string
}

// Section is one ordered prompt contribution. Static sections use EN/ZH;
// dynamic sections compute their text from Context and normally leave EN/ZH as
// the localized fallback text. Dynamic may return "" to omit the section.
type Section struct {
	ID      string
	Order   int
	EN      string
	ZH      string
	Dynamic func(Context) string
}

// registration pairs the public Section with its inclusion gates.
type registration struct {
	section Section
	// tools lists alternative tool names (aliases count). Empty means the
	// section is always on unless when rejects it.
	tools []string
	// toolPrefixes lists tool-name prefixes; any match includes the section.
	toolPrefixes []string
	// when is an optional extra condition; false omits the section.
	when func(Context) bool
}

// Compose renders the complete instruction for c.
func Compose(c Context) string {
	lang := normalizeLanguage(c.Language)
	tools := toolSet(c.Tools)

	parts := make([]string, 0, len(orderedRegistry))
	for _, reg := range orderedRegistry {
		if !reg.included(c, tools) {
			continue
		}
		text := reg.render(c, lang)
		text = normalizeBlock(text)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n")
}

// Sections returns the registry in composition order. The returned slice is a
// copy; callers may inspect IDs, orders, gate metadata is private, and dynamic
// sections can be rendered by calling Dynamic with a Context.
func Sections() []Section {
	out := make([]Section, 0, len(orderedRegistry))
	for _, reg := range orderedRegistry {
		out = append(out, reg.section)
	}
	return out
}

// included reports whether all gates for reg pass.
func (reg registration) included(c Context, tools map[string]bool) bool {
	if len(reg.tools) > 0 && !hasAnyTool(tools, reg.tools) {
		return false
	}
	if len(reg.toolPrefixes) > 0 && !hasAnyToolPrefix(tools, reg.toolPrefixes) {
		return false
	}
	return reg.when == nil || reg.when(c)
}

// render picks dynamic text when present, then the localized static text.
func (reg registration) render(c Context, lang string) string {
	if reg.section.Dynamic != nil {
		return reg.section.Dynamic(c)
	}
	if lang == "zh" && reg.section.ZH != "" {
		return reg.section.ZH
	}
	return reg.section.EN
}

// normalizeLanguage maps all accepted language spellings to "en" or "zh".
func normalizeLanguage(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	switch {
	case language == "zh", language == "chinese", language == "中文",
		strings.HasPrefix(language, "zh-"), strings.HasPrefix(language, "zh_"):
		return "zh"
	default:
		return "en"
	}
}

// isZH reports whether c selects Chinese.
func isZH(c Context) bool { return normalizeLanguage(c.Language) == "zh" }

// lowerTrim normalizes a policy or mode value.
func lowerTrim(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

// readToolName prefers ZeoKeel's read_file name and falls back to DSH's read.
func readToolName(c Context) string { return preferredToolName(c, "read_file", "read") }

// writeToolName prefers ZeoKeel's write_file name and falls back to DSH's write.
func writeToolName(c Context) string { return preferredToolName(c, "write_file", "write") }

// editToolName prefers ZeoKeel's edit_file name and falls back to DSH's edit.
func editToolName(c Context) string { return preferredToolName(c, "edit_file", "edit") }

// preferredToolName returns specific when it is registered, otherwise generic.
func preferredToolName(c Context, specific, generic string) string {
	if hasAnyTool(toolSet(c.Tools), []string{specific}) {
		return specific
	}
	return generic
}

// localized picks en or zh according to c.Language.
func localized(c Context, en, zh string) string {
	if isZH(c) && zh != "" {
		return zh
	}
	return en
}

// toolSet normalizes tool names for gate checks.
func toolSet(tools []string) map[string]bool {
	set := make(map[string]bool, len(tools))
	for _, name := range tools {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" {
			set[name] = true
		}
	}
	return set
}

// hasAnyTool reports whether any gate name is present.
func hasAnyTool(tools map[string]bool, names []string) bool {
	for _, name := range names {
		if tools[strings.ToLower(strings.TrimSpace(name))] {
			return true
		}
	}
	return false
}

// hasAnyToolPrefix reports whether any tool name starts with one of prefixes.
// A prefix matches the exact name too, so "mcp" covers "mcp", "mcp__fs__read",
// and "mcp:server".
func hasAnyToolPrefix(tools map[string]bool, prefixes []string) bool {
	for name := range tools {
		for _, prefix := range prefixes {
			if name == prefix || strings.HasPrefix(name, prefix+"_") ||
				strings.HasPrefix(name, prefix+"-") || strings.HasPrefix(name, prefix+":") ||
				strings.HasPrefix(name, prefix+".") {
				return true
			}
		}
	}
	return false
}

// modeIs reports whether c.Mode is one of names (case-insensitive).
func modeIs(c Context, names ...string) bool {
	return modeValueIs(c.Mode, names...)
}

// agentModeIs reports whether the selected agent preset is one of names. It
// checks AgentMode first and falls back to Mode so prompt-only callers and
// tests can select a preset without a separate field.
func agentModeIs(c Context, names ...string) bool {
	if modeValueIs(c.AgentMode, names...) {
		return true
	}
	return modeValueIs(c.Mode, names...)
}

// modeValueIs is the shared case-insensitive mode comparison.
func modeValueIs(mode string, names ...string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	for _, name := range names {
		if mode == name {
			return true
		}
	}
	return false
}

// normalizeBlock trims a rendered section and collapses runs of three or more
// newlines to one blank line, so joining sections never creates duplicate blank
// lines even when caller-supplied content contains them.
func normalizeBlock(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSpace(text)
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	return text
}

// maxAgentsMDBytes bounds the inlined AGENTS.md content, matching the spirit of
// DSH's workspace-instruction byte budget without a configurable knob.
const maxAgentsMDBytes = 64 * 1024

// truncateUTF8 cuts s to at most max bytes without splitting a rune and marks
// the cut.
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "\n\n[AGENTS.md truncated]"
}

// formatTimestamp renders t in DSH's ISO-shaped timestamp form, including the
// zone label when the location carries one.
func formatTimestamp(t time.Time) string {
	base := t.Format("2006-01-02T15:04:05-07:00")
	zone := t.Location().String()
	if zone == "" || zone == "Local" {
		return base
	}
	return fmt.Sprintf("%s[%s]", base, zone)
}

// skillNames returns skill names trimmed, deduplicated, and sorted for a stable
// catalog. Backticks and newlines are neutralized because the catalog renders
// each name inside a Markdown code span.
func skillNames(skills []string) []string {
	seen := make(map[string]bool, len(skills))
	out := make([]string, 0, len(skills))
	for _, skill := range skills {
		skill = strings.TrimSpace(skill)
		skill = strings.ReplaceAll(skill, "`", "'")
		skill = strings.ReplaceAll(skill, "\n", " ")
		if skill == "" || seen[skill] {
			continue
		}
		seen[skill] = true
		out = append(out, skill)
	}
	sort.Strings(out)
	return out
}

// sortRegistry returns regs sorted by Order then ID, the DSH canonical order.
func sortRegistry(regs []registration) []registration {
	out := append([]registration(nil), regs...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].section.Order != out[j].section.Order {
			return out[i].section.Order < out[j].section.Order
		}
		return out[i].section.ID < out[j].section.ID
	})
	return out
}
