// Package toolcards contributes the tool-card renderers (the UI for one tool
// call). It is the RegisterToolRenderer injection point in practice: the module
// that owns a tool ships its card, and any screen that calls
// UIRuntime.RenderTool gets it without knowing which module drew it.
//
// The module registers one native renderer per common Rig tool name (bash,
// read_file, grep, web_search, subagent, ...) plus the original "demo" card the
// host uses as a capability probe. All real cards share one native composition:
// a status badge (theme-colored: running=warning, ok=success, error=danger), the
// tool name and, when the read model carries a summary, a
// ui.Collapsible("摘要") with a truncated result/argument excerpt. No Canvas or
// hand-drawn widget is involved.
//
// MCP tools are named dynamically (mcp__<server>__<tool>) and the host matches
// renderers by exact tool name, so a wildcard renderer is impossible here: MCP
// calls keep the session screen's native badge+text fallback. A product that
// wants a dedicated card for one MCP tool registers a renderer for that exact
// name itself.
package toolcards

import (
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
	"github.com/zeokeel/zeokeel/internal/icons"
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

// rendererState keeps the per-card disclosure state (expanded "摘要" sections)
// for the lifetime of one module instance. Builders run on the UI thread; the
// mutex only keeps the map safe if a host renders from more than one place.
type rendererState struct {
	mu   sync.Mutex
	open map[string]bool
	// locale localizes the real cards' copy per frame (zh is the default).
	locale contracts.LocaleService
	// ctx resolves the optional plan reviewer per frame.
	ctx cordis.Context
}

// tr returns the localized copy for the current frame's language.
func (s *rendererState) tr(zh, en string) string { return uikit.T(s.locale, zh, en) }

func newRendererState() *rendererState {
	return &rendererState{open: make(map[string]bool)}
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

func (s *rendererState) store(key string, open bool) {
	s.mu.Lock()
	s.open[key] = open
	s.mu.Unlock()
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
// historical content (title, raw status badge, summary) but uses the
// same brand panel as the real tool cards so the Home demo looks native.
func demoBuild(title string) func(c *ui.Context, call contracts.ToolCall) {
	return func(c *ui.Context, call contracts.ToolCall) {
		t := design.Of(c)
		card := ui.Column(c).FillWidth().Gap(design.Space2)
		design.Apply(card, t.Panel()).Padding(design.Space3).Children(func() {
			ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.Message()).Size(design.Space4, design.Space4).
					TextColor(t.LabelSecondary)
				design.Apply(ui.Text(c, title+": "+call.Name), t.Font(design.TypeS)).FontWeight(600).
					TextColor(t.LabelPrimary)
				design.Apply(ui.Badge(c, call.Status), t.Badge())
			})
			if call.Summary != "" {
				design.Apply(ui.Text(c, call.Summary), t.TextMuted(design.TypeXS))
			}
		})
	}
}

// realBuild builds one real tool row. The transcript is mostly prose, so the
// registered cards use the shared compact row (internal/uikit): a muted category
// icon, the tool name, a small status label, a one-line summary preview and the
// small 详情 disclosure. The trajectory pane keeps its detailed card.
func realBuild(state *rendererState, runtime contracts.UIRuntime) func(c *ui.Context, call contracts.ToolCall) {
	return func(c *ui.Context, call contracts.ToolCall) {
		t := design.Of(c)
		tool := strings.TrimSpace(call.Name)
		key := call.ID + "\x00" + tool
		summary := strings.TrimSpace(call.Summary)
		open, seen := state.loadSeen(key)
		if !seen {
			open = SummaryDefaultOpen(call.Status, summary)
		}
		toggled := false
		row := uikit.ToolRowOptions{
			Key:         "toolcards." + key,
			Icon:        uikit.ToolIcon(tool),
			Name:        tool,
			Status:      statusLabel(state, call.Status),
			StatusColor: toolStatusTextColor(t, call.Status),
			Preview:     uikit.ToolPreview(summary, 96),
			DetailLabel: state.tr("详情", "Details"),
			Open:        open,
			Toggle: func(next bool) {
				toggled = true
				state.store(key, next)
				runtime.Invalidate()
			},
		}
		if summary != "" {
			row.Detail = func(cc *ui.Context) {
				design.Apply(ui.Text(cc, truncateSummary(summary)), t.TextMuted(design.TypeXXS))
			}
		}
		uikit.ToolRow(c, row)
		if !seen && !toggled {
			state.store(key, open)
		}
	}
}

// toolIcon maps a tool name to its brand glyph: shell/process tools use the
// terminal, file and web reads the globe, searches the magnifier, task tools// toolStatusTextColor is the badge's WCAG-AA foreground of a status (the
// status color itself is a fill/icon color, too light for small text).
func toolStatusTextColor(t *design.Tokens, status string) ui.Color {
	switch normalizeStatus(status) {
	case "running":
		return t.WarningText
	case "error":
		return t.DangerText
	case "ok":
		return t.SuccessText
	default:
		return t.LabelTertiary
	}
}

// statusLabel maps a tool status to the localized card label.
func statusLabel(state *rendererState, status string) string {
	switch normalizeStatus(status) {
	case "running":
		return state.tr("运行中", "Running")
	case "ok":
		return state.tr("完成", "Completed")
	case "error":
		return state.tr("失败", "Failed")
	default:
		if trimmed := strings.TrimSpace(status); trimmed != "" {
			return trimmed
		}
		return state.tr("未知", "Unknown")
	}
}

// normalizeStatus folds the status spellings Rig uses into running/ok/error.
func normalizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "in_progress", "in-progress", "active", "started":
		return "running"
	case "ok", "success", "succeeded", "done", "complete", "completed":
		return "ok"
	case "error", "failed", "failure", "canceled", "cancelled":
		return "error"
	default:
		return ""
	}
}

// truncateSummary bounds the expanded excerpt (UTF-8 safe) and marks the cut.
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

// SummaryDefaultOpen reports whether a card's detail section starts expanded
// (H15): failures first, and very short results are cheap to show. The session
// transcript's fallback card uses the same rule, so a tool call never gets two
// different disclosure defaults.
func SummaryDefaultOpen(status, summary string) bool {
	if normalizeStatus(status) == "error" {
		return true
	}
	return len([]rune(strings.TrimSpace(summary))) <= 160
}

// planReviewBuild renders the transcript plan card: a titled panel with the
// optional PlanReviewer state badge and the plan Markdown when the arguments
// can be recovered, otherwise the existing raw summary disclosure.
func planReviewBuild(state *rendererState) func(c *ui.Context, call contracts.ToolCall) {
	return func(c *ui.Context, call contracts.ToolCall) {
		t := design.Of(c)
		card := ui.Column(c).FillWidth().Gap(design.Space2)
		design.Apply(card, t.Panel()).Padding(design.Space3).Children(func() {
			ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.Sparkles()).Size(design.Space4, design.Space4).
					TextColor(t.LabelSecondary)
				design.Apply(ui.Text(c, state.tr("计划审阅", "Plan review")), t.Font(design.TypeS)).FontWeight(600).
					TextColor(t.LabelPrimary)
				if label, fill, text, tint, ok := planReviewStateBadge(state, t); ok {
					design.Apply(ui.Badge(c, label), t.Badge(), func(el ui.Element) ui.Element {
						return el.Background(fill.Alpha(tint)).TextColor(text)
					})
				}
				ui.Spacer(c).Grow(1)
			})
			summary := strings.TrimSpace(call.Summary)
			if markdownText, ok := planMarkdownFromSummary(summary); ok {
				markdown.View(c, markdownText, nil)
				return
			}
			if summary == "" {
				design.Apply(ui.Text(c, state.tr("计划没有内联内容。", "The plan has no inline content.")),
					t.TextMuted(design.TypeXS))
				return
			}
			key := call.ID + "\x00" + call.Name
			open, seen := state.loadSeen(key)
			if !seen {
				open = SummaryDefaultOpen(call.Status, summary)
			}
			collapsible := ui.Collapsible(c, state.tr("详情", "Details"), &open, func() {
				design.Apply(ui.Text(c, truncateSummary(summary)), t.TextMuted(design.TypeXS))
			})
			design.Apply(collapsible, t.Font(design.TypeXS))
			state.store(key, open)
		})
	}
}

// planReviewStateBadge maps the optional PlanReviewer state to a label, the
// badge's fill tint and its WCAG-AA text variant.
func planReviewStateBadge(state *rendererState, t *design.Tokens) (string, ui.Color, ui.Color, float32, bool) {
	reviewer, ok := state.ctx.Get(contracts.KeyPlanReviewer)
	if !ok || reviewer == nil {
		return "", ui.Transparent, ui.Transparent, 0, false
	}
	switch strings.ToLower(strings.TrimSpace(reviewer.State())) {
	case "reviewing":
		return state.tr("审阅中", "Reviewing"), t.Warning, t.WarningText, 0.12, true
	case "approved":
		return state.tr("已批准", "Approved"), t.Success, t.SuccessText, 0.08, true
	case "cancelled", "canceled":
		return state.tr("已取消", "Cancelled"), t.Danger, t.DangerText, 0.12, true
	case "idle":
		return state.tr("待审阅", "Pending review"), t.LabelTertiary, t.LabelSecondary, 0.12, true
	default:
		return "", ui.Transparent, ui.Transparent, 0, false
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
