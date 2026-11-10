// Package toolcards contributes the tool-card renderers (the UI for one tool
// call). It is the RegisterToolRenderer injection point in practice: the module
// that owns a tool ships its card, and any screen that calls
// UIRuntime.RenderTool gets it without knowing which module drew it.
//
// The module registers one native renderer per common Rig tool name (bash,
// read_file, grep, web_search, subagent, ...) plus the original "demo" card the
// host uses as a capability probe. A tool call has exactly two looks: the compact
// tool row (internal/uikit.ToolRow) for real cards, and the complete card
// (internal/uikit.ToolCallCard) for the plan card and the demo probe. The
// compact row is one official mygo disclosure
// (ui.CollapsibleBase) whose trigger is a muted category icon, the friendly
// localized tool name (the protocol id stays its tooltip/AT description), the
// status with the official ui.Spinner while running, a one-line summary preview
// and the chevron, and whose panel is the truncated result/argument excerpt. No
// Canvas or hand-drawn widget is involved.
//
// MCP tools are named dynamically (mcp__<server>__<tool>) and the host matches
// renderers by exact tool name, so a wildcard renderer is impossible here: MCP
// calls keep the session screen's compact row fallback. A product that wants a
// dedicated card for one MCP tool registers a renderer for that exact name
// itself.
package toolcards

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// Renderer orders. The demo card keeps its historical order; real cards follow.
const (
	demoCardOrder = 10
	realCardOrder = 20
	// maxSummaryChars bounds the excerpt shown when a card is expanded.
	maxSummaryChars = 1200
)

// realToolNames are the Rig tool names that ship a dedicated native card.
var realToolNames = []string{
	// shell / files
	"bash",
	"read_file",
	"write_file",
	"edit_file",
	"str_replace_editor",
	"list_dir",
	"glob",
	"grep",
	"read_image",
	// web
	"web_fetch",
	"web_search",
	// jobs / terminals
	"job_start",
	"job_output",
	"terminal_send",
	"terminal_read",
	// worktrees
	"enter_worktree",
	"list_worktrees",
	// orchestration
	"subagent",
	"workflow",
	"ralph",
	// team tasks / planning
	"team_task_create",
	"team_task_update",
	"todo",
	"plan_review",
	// skills
	"skill",
}

// RealToolNames returns the tool names that get a real card, in registration
// order. The returned slice is a copy.
func RealToolNames() []string { return append([]string(nil), realToolNames...) }

// ToolNames returns every tool name the module registers a renderer for: the
// legacy demo card first, then the real cards.
func ToolNames() []string {
	names := make([]string, 0, len(realToolNames)+1)
	names = append(names, "demo")
	names = append(names, realToolNames...)
	return names
}

// Config is the module's typed assembly config.
type Config struct {
	Title string `json:"title"`
}

// foldStateLimit bounds the per-card disclosure state. One module instance lives
// for the whole app process and a long agent run can render thousands of tool
// calls, so the fold map must not grow without a bound: past the limit the
// least recently rendered card states are evicted and those cards fall back to
// their H15 default when they render again (mygo-maintenance: keep app state
// bounded).
const foldStateLimit = 512

// rendererState keeps the per-card disclosure state (expanded "摘要" sections)
// for the lifetime of one module instance. Builders run on the UI thread; the
// mutex only keeps the map safe if a host renders from more than one place.
type rendererState struct {
	mu   sync.Mutex
	open map[string]bool
	// seenAt is the runtime frame each card last rendered in: the fold map is
	// bounded by evicting the least recently rendered entries past
	// foldStateLimit.
	seenAt map[string]uint64
	// runtime is the host UI runtime: its frame number orders the fold states.
	runtime contracts.UIRuntime
	// locale localizes the real cards' copy per frame (zh is the default).
	locale contracts.LocaleService
	// ctx resolves the optional plan reviewer per frame.
	ctx cordis.Context
}

// tr returns the localized copy for the current frame's language.
func (s *rendererState) tr(zh, en string) string { return uikit.T(s.locale, zh, en) }

func newRendererState() *rendererState {
	return &rendererState{open: make(map[string]bool), seenAt: make(map[string]uint64)}
}

// frame is the runtime's current frame number (0 when the host provides none).
func (s *rendererState) frame() uint64 {
	if s.runtime == nil {
		return 0
	}
	return s.runtime.Frame()
}

// loadSeen reports the stored disclosure state and whether the reader ever
// touched it, so a fresh card can start from the H15 default (failures and very
// short results expanded).
func (s *rendererState) loadSeen(key string) (open, seen bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	open, seen = s.open[key]
	return open, seen
}

// touch records that a card rendered in the current frame and keeps the fold
// map bounded. A rendered card is the most recently used one, so a card that
// stays on screen never loses its state to cards that are gone.
func (s *rendererState) touch(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seenAt[key] = s.frame()
	s.evictLocked()
}

// store persists one reader-chosen disclosure state.
func (s *rendererState) store(key string, open bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.open[key] = open
	s.seenAt[key] = s.frame()
	s.evictLocked()
}

// evictLocked drops the least recently rendered card states while the map
// exceeds foldStateLimit. Callers hold s.mu.
func (s *rendererState) evictLocked() {
	for len(s.open) > foldStateLimit {
		oldestKey, oldestFrame := "", ^uint64(0)
		for key := range s.open {
			frame, ok := s.seenAt[key]
			if !ok {
				frame = 0
			}
			if frame < oldestFrame {
				oldestKey, oldestFrame = key, frame
			}
		}
		if oldestKey == "" {
			return
		}
		delete(s.open, oldestKey)
		delete(s.seenAt, oldestKey)
	}
}

// Module registers the demo renderer and the real Rig tool cards.
func Module() *module.Descriptor[Config] {
	return module.Define("zeokeel-ui/toolcards", func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		title := firstNonEmpty(cfg.Title, "工具调用")
		locale, _ := ctx.Get(contracts.KeyLocale)
		state := newRendererState()
		state.runtime = runtime
		state.locale = locale
		state.ctx = ctx

		// The demo card stays exactly as it was: the host's capability probe and
		// its integration test assert its text and raw status.
		if err := own(ctx, func() (func(), error) {
			return runtime.RegisterToolRenderer(contracts.ToolRenderer{
				Tool:  "demo",
				ID:    "toolcards.demo-card",
				Order: demoCardOrder,
				Build: demoBuild(title),
			})
		}); err != nil {
			return err
		}

		for _, name := range realToolNames {
			tool := name
			build := realBuild(state, runtime)
			if tool == "plan_review" {
				build = planReviewBuild(state)
			}
			if err := own(ctx, func() (func(), error) {
				return runtime.RegisterToolRenderer(contracts.ToolRenderer{
					Tool:  tool,
					ID:    "toolcards." + tool + "-card",
					Order: realCardOrder,
					Build: build,
				})
			}); err != nil {
				return err
			}
		}
		return nil
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		WithName("ZeoKeel tool cards")
}

// demoBuild is the demo card used by the host's capability probe. It keeps its
// historical content (title, raw status badge, summary) through the shared
// complete card: a custom title and raw status label, and the summary as the
// card's always-visible content, so the Home demo looks native.
func demoBuild(title string) func(c *ui.Context, call contracts.ToolCall) {
	return func(c *ui.Context, call contracts.ToolCall) {
		key := ""
		if call.ID != "" {
			key = "toolcards.demo." + call.ID
		}
		uikit.ToolCallCard(c, uikit.ToolCallCardOptions{
			Key:         key,
			Call:        call,
			Title:       title + ": " + call.Name,
			StatusLabel: call.Status,
			Content: func(cc *ui.Context) {
				if summary := strings.TrimSpace(call.Summary); summary != "" {
					design.Apply(ui.Text(cc, summary), design.Of(cc).TextMuted(design.TypeXS))
				}
			},
		})
	}
}

// realBuild builds one real tool row. The transcript is mostly prose, so the
// registered cards use the shared compact disclosure row (internal/uikit): a
// muted category icon, the friendly localized tool name (the protocol id is the
// row's tooltip), the status with the official spinner while running, a one-line
// summary preview and the chevron. Expanded, the panel shows the tool's input
// (command / path / before-after / JSON) and its output, each copyable, plus the
// team cross-navigation footer for team-related tools.
func realBuild(state *rendererState, runtime contracts.UIRuntime) func(c *ui.Context, call contracts.ToolCall) {
	return func(c *ui.Context, call contracts.ToolCall) {
		t := design.Of(c)
		tool := strings.TrimSpace(call.Name)
		key := call.ID + "\x00" + tool
		summary := strings.TrimSpace(call.Summary)
		// Record this card as rendered before reading its fold state: the fold
		// map is bounded and a live card must outrank cards that left screen.
		state.touch(key)
		open, seen := state.loadSeen(key)
		if !seen {
			open = uikit.SummaryDefaultOpen(call.Status, summary)
		}
		toggled := false
		sections := PanelSections(state.locale, call)
		statusKind := uikit.ParseToolStatus(call.Status).Kind()
		row := uikit.ToolRowOptions{
			Key:         "toolcards." + key,
			Icon:        uikit.ToolIcon(tool),
			Name:        uikit.ToolDisplayName(state.locale, tool),
			RawName:     tool,
			Status:      uikit.ToolStatusLabel(state.locale, call.Status),
			StatusColor: uikit.StatusColor(t, statusKind),
			Running:     statusKind == uikit.StatusRunning,
			Preview:     uikit.ToolPreview(summary, 96),
			Open:        open,
			Toggle: func(next bool) {
				toggled = true
				state.store(key, next)
				runtime.Invalidate()
			},
			Sections: sections,
		}
		uikit.ToolRow(c, row)
		if !seen && !toggled {
			state.store(key, open)
		}
	}
}

// maxToolInputRunes bounds one rendered input section (JSON, command, content
// preview). It mirrors the agent's read-model argument bound so the UI never
// shows less than the read model kept, and keeps a huge write_file content from
// bloating the panel.
const maxToolInputRunes = 4000

// maxToolContentPreviewRunes bounds the write_file content preview.
const maxToolContentPreviewRunes = 2000

// assumedToolSummaryRunes mirrors agent.maxToolSummaryRunes: a read-model
// summary at this length that ends with the truncation ellipsis was cut by the
// backend. Keep the two in sync (both have tests asserting the bound).
const assumedToolSummaryRunes = 4000

// PanelSections builds the expanded panel of one tool call: the tool's input
// (bash command, write path+content, edit path+before/after, or pretty JSON),
// then its bounded output. Labels are localized through locale; colors are
// applied by the shared uikit section renderer from the design tokens.
func PanelSections(locale contracts.LocaleService, call contracts.ToolCall) []uikit.ToolPanelSection {
	tr := func(zh, en string) string { return uikit.T(locale, zh, en) }
	name := strings.ToLower(strings.TrimSpace(call.Name))
	arguments := strings.TrimSpace(call.Arguments)
	sections := make([]uikit.ToolPanelSection, 0, 4)

	switch {
	case name == "bash", name == "pwsh":
		if command := toolStringArg(arguments, "command"); command != "" {
			sections = append(sections, uikit.ToolPanelSection{
				Label:     tr("命令", "Command"),
				Text:      command,
				Mono:      true,
				Bounded:   true,
				CopyLabel: tr("复制命令", "Copy command"),
			})
		} else if generic, ok := genericInputSection(tr, arguments); ok {
			sections = append(sections, generic)
		}
	case name == "write_file":
		if path := toolStringArg(arguments, "path"); path != "" {
			sections = append(sections, uikit.ToolPanelSection{
				Label:     tr("路径", "Path"),
				Text:      path,
				Mono:      true,
				CopyLabel: tr("复制路径", "Copy path"),
			})
		}
		if content := toolStringArg(arguments, "content"); content != "" {
			text, truncated := boundRunes(content, maxToolContentPreviewRunes)
			section := uikit.ToolPanelSection{
				Label:     tr("内容", "Content"),
				Text:      text,
				Mono:      true,
				Bounded:   true,
				CopyLabel: tr("复制内容", "Copy content"),
			}
			if truncated {
				section.Hint = tr("内容已截断，仅显示前部分", "Content truncated; only the beginning is shown")
			}
			sections = append(sections, section)
		}
	case name == "edit_file", name == "str_replace_editor":
		if path := toolStringArg(arguments, "path"); path != "" {
			sections = append(sections, uikit.ToolPanelSection{
				Label:     tr("路径", "Path"),
				Text:      path,
				Mono:      true,
				CopyLabel: tr("复制路径", "Copy path"),
			})
		}
		oldText := firstToolStringArg(arguments, "old_string", "old_str")
		newText := firstToolStringArg(arguments, "new_string", "new_str")
		if oldText != "" {
			sections = append(sections, uikit.ToolPanelSection{
				Label:     tr("替换前", "Before"),
				Text:      oldText,
				Mono:      true,
				Bounded:   true,
				Tone:      uikit.ToolSectionRemoved,
				CopyLabel: tr("复制替换前", "Copy before"),
			})
		}
		if newText != "" {
			sections = append(sections, uikit.ToolPanelSection{
				Label:     tr("替换后", "After"),
				Text:      newText,
				Mono:      true,
				Bounded:   true,
				Tone:      uikit.ToolSectionAdded,
				CopyLabel: tr("复制替换后", "Copy after"),
			})
		}
		// str_replace_editor's create command carries the new file body instead
		// of an old/new pair.
		if oldText == "" && newText == "" {
			if content := firstToolStringArg(arguments, "file_text", "content"); content != "" {
				text, truncated := boundRunes(content, maxToolContentPreviewRunes)
				section := uikit.ToolPanelSection{
					Label:     tr("内容", "Content"),
					Text:      text,
					Mono:      true,
					Bounded:   true,
					CopyLabel: tr("复制内容", "Copy content"),
				}
				if truncated {
					section.Hint = tr("内容已截断，仅显示前部分", "Content truncated; only the beginning is shown")
				}
				sections = append(sections, section)
			}
		}
	default:
		if generic, ok := genericInputSection(tr, arguments); ok {
			sections = append(sections, generic)
		}
	}

	if summary := strings.TrimSpace(call.Summary); summary != "" {
		section := uikit.ToolPanelSection{
			Label:     tr("输出", "Output"),
			Text:      summary,
			Mono:      true,
			Bounded:   true,
			CopyLabel: tr("复制输出", "Copy output"),
		}
		if toolOutputTruncated(summary) {
			section.Hint = tr("输出已截断，仅显示前部分", "Output truncated; only the beginning is shown")
		}
		sections = append(sections, section)
	}
	return sections
}

// genericInputSection renders the tool arguments as indented JSON, bounded and
// copyable. It reports false when there is nothing readable to show.
func genericInputSection(tr func(zh, en string) string, arguments string) (uikit.ToolPanelSection, bool) {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return uikit.ToolPanelSection{}, false
	}
	text := trimmed
	if pretty, ok := prettyJSON(trimmed); ok {
		text = pretty
	}
	bounded, truncated := boundRunes(text, maxToolInputRunes)
	section := uikit.ToolPanelSection{
		Label:     tr("输入", "Input"),
		Text:      bounded,
		Mono:      true,
		Bounded:   true,
		CopyLabel: tr("复制输入", "Copy input"),
	}
	if truncated {
		section.Hint = tr("输入已截断，仅显示前部分", "Input truncated; only the beginning is shown")
	}
	return section, true
}

// toolStringArg reads one string field of the tool's JSON arguments ("" when
// the arguments are not JSON or the field is absent/non-string).
func toolStringArg(arguments, key string) string {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return ""
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(trimmed), &values); err != nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

// firstToolStringArg returns the first present string field among keys.
func firstToolStringArg(arguments string, keys ...string) string {
	for _, key := range keys {
		if value := toolStringArg(arguments, key); value != "" {
			return value
		}
	}
	return ""
}

// prettyJSON indents one JSON document for the input section.
func prettyJSON(raw string) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(raw), "{") && !strings.HasPrefix(strings.TrimSpace(raw), "[") {
		return "", false
	}
	var buffer bytes.Buffer
	if err := json.Indent(&buffer, []byte(raw), "", "  "); err != nil {
		return "", false
	}
	return buffer.String(), true
}

// boundRunes cuts text to max runes (UTF-8 safe) and reports whether it cut.
func boundRunes(text string, max int) (string, bool) {
	runes := []rune(text)
	if max <= 0 || len(runes) <= max {
		return text, false
	}
	return string(runes[:max]) + "…", true
}

// toolOutputTruncated reports whether the backend cut the read-model summary:
// the agent appends the truncation ellipsis when the output exceeds its bound.
func toolOutputTruncated(summary string) bool {
	return strings.HasSuffix(strings.TrimSpace(summary), "…") &&
		len([]rune(strings.TrimSpace(summary))) >= assumedToolSummaryRunes
}

// toolIcon maps a tool name to its brand glyph: shell/process tools use the
// terminal, file and web reads the globe, searches the magnifier, task tools// truncateSummary bounds the expanded excerpt (UTF-8 safe) and marks the cut.
func truncateSummary(summary string) string {
	if len(summary) <= maxSummaryChars {
		return summary
	}
	cut := maxSummaryChars
	for cut > 0 && !utf8.RuneStart(summary[cut]) {
		cut--
	}
	return summary[:cut] + "…"
}

// planArgs is the plan_review tool argument shape.
type planArgs struct {
	Plan    string `json:"plan"`
	Content string `json:"content"`
}

// planMarkdownFromSummary extracts the plan/content Markdown when the call
// summary carries the tool arguments as JSON. The contracts.ToolCall read model
// has no Arguments field, so the summary is the only available carrier; a
// non-JSON or field-less summary falls back to the existing raw preview.
func planMarkdownFromSummary(summary string) (string, bool) {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return "", false
	}
	var args planArgs
	if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
		return "", false
	}
	if markdownText := strings.TrimSpace(args.Plan); markdownText != "" {
		return markdownText, true
	}
	if markdownText := strings.TrimSpace(args.Content); markdownText != "" {
		return markdownText, true
	}
	return "", false
}

// planReviewBuild renders the transcript plan card through the shared complete
// card: the plan's title, the optional PlanReviewer state as the extra header
// badge, and either the plan Markdown as rich content or a plain summary in the
// official detail disclosure. A call with arguments but no readable content
// shows the explicit "no inline content" copy instead of an empty panel.
func planReviewBuild(state *rendererState) func(c *ui.Context, call contracts.ToolCall) {
	return func(c *ui.Context, call contracts.ToolCall) {
		summary := strings.TrimSpace(call.Summary)
		options := uikit.ToolCallCardOptions{
			Key:        "toolcards." + call.ID + "\x00" + call.Name,
			Call:       call,
			Locale:     state.locale,
			Title:      state.tr("计划审阅", "Plan review"),
			HideStatus: true,
			Header: func(c *ui.Context) {
				if label, kind, ok := planReviewStateBadge(state); ok {
					uikit.StatusBadge(c, design.Of(c), kind, label)
				}
			},
		}
		var open bool
		var seen, toggled bool
		switch {
		case summary == "":
			options.Content = func(c *ui.Context) {
				design.Apply(ui.Text(c, state.tr("计划没有内联内容。", "The plan has no inline content.")),
					design.Of(c).TextMuted(design.TypeXS))
			}
		default:
			if markdownText, ok := planMarkdownFromSummary(summary); ok {
				options.Content = func(c *ui.Context) {
					markdown.View(c, markdownText, nil)
				}
				break
			}
			// A summary that is not recoverable Markdown stays a plain detail
			// disclosure, with the same H15 default and per-card fold memory as
			// the compact row.
			key := call.ID + "\x00" + call.Name
			state.touch(key)
			open, seen = state.loadSeen(key)
			if !seen {
				open = uikit.SummaryDefaultOpen(call.Status, summary)
			}
			options.Sections = []uikit.ToolPanelSection{{Text: truncateSummary(summary)}}
			options.Open = &open
			options.OnToggle = func(next bool) {
				toggled = true
				state.store(key, next)
			}
		}
		uikit.ToolCallCard(c, options)
		if options.Open != nil && !seen && !toggled {
			state.store(call.ID+"\x00"+call.Name, open)
		}
	}
}

// planReviewStateBadge maps the optional PlanReviewer state to its label and its
// place in the shared status vocabulary: a reviewing plan is running (warning),
// an approved one succeeded, a cancelled one is neutral (a user stop is not a
// failure, DESIGN.md §5.2) and an idle one waits neutrally.
func planReviewStateBadge(state *rendererState) (string, uikit.StatusKind, bool) {
	reviewer, ok := state.ctx.Get(contracts.KeyPlanReviewer)
	if !ok || reviewer == nil {
		return "", uikit.StatusUnknown, false
	}
	switch strings.ToLower(strings.TrimSpace(reviewer.State())) {
	case "reviewing":
		return state.tr("审阅中", "Reviewing"), uikit.StatusRunning, true
	case "approved":
		return state.tr("已批准", "Approved"), uikit.StatusSuccess, true
	case "cancelled", "canceled":
		return state.tr("已取消", "Cancelled"), uikit.StatusCancelled, true
	case "idle":
		return state.tr("待审阅", "Pending review"), uikit.StatusIdle, true
	default:
		return "", uikit.StatusUnknown, false
	}
}

// own registers one contribution and ties its unregistration to ctx's effect
// lifetime, so activation failure and module disposal leave no residue.
func own(ctx cordis.Context, register func() (func(), error)) error {
	unregister, err := register()
	if err != nil {
		return err
	}
	if _, err := ctx.Effect(func() cordis.Effect {
		return cordis.Effect{cordis.CleanupOf(unregister)}
	}); err != nil {
		unregister()
		return err
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
