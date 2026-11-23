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
	"encoding/json"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/lib/textutil"
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
	"todo_write",
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
	runtime uinative.UIRuntime
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
		runtime, err := ctx.Require(uinative.KeyUIRuntime)
		if err != nil {
			return err
		}
		title := textutil.FirstNonEmptyTrimmed(cfg.Title, "工具调用")
		locale, _ := ctx.Get(contracts.KeyLocale)
		state := newRendererState()
		state.runtime = runtime
		state.locale = locale
		state.ctx = ctx

		// The demo card stays exactly as it was: the host's capability probe and
		// its integration test assert its text and raw status.
		if err := own(ctx, func() (func(), error) {
			return runtime.RegisterToolRenderer(uinative.ToolRenderer{
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
				return runtime.RegisterToolRenderer(uinative.ToolRenderer{
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
		AddRequires(module.Requires(uinative.KeyUIRuntime)).
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
// registered cards use the shared compact disclosure row (internal/uikit): the
// call's state glyph, the friendly localized tool name (the protocol id is the
// row's accessible description and a mono line in the panel), the state word, a
// one-line summary preview, the measured duration and the chevron. Expanded, the
// panel shows the tool's input (command / path / before-after / JSON) and its
// output, each copyable, plus the team cross-navigation footer for team-related
// tools.
//
// The chevron stays inline, right after the row's text (ToolRowOptions
// .InlineChevron), because the transcript's process timeline renders reasoning
// rows and registered tool rows one under the other: a far-end chevron on one
// row and an inline chevron on the next read as two different widgets (the
// 2026-11 per-screen review, p2-process-expanded).
func realBuild(state *rendererState, runtime uinative.UIRuntime) func(c *ui.Context, call contracts.ToolCall) {
	return func(c *ui.Context, call contracts.ToolCall) {
		t := design.Of(c)
		tool := strings.TrimSpace(call.Name)
		key := call.ID + "\x00" + tool
		// Record this card as rendered before reading its fold state: the fold
		// map is bounded and a live card must outrank cards that left screen.
		state.touch(key)
		open, seen := state.loadSeen(key)
		if !seen {
			open = uikit.SummaryOpenForCall(call)
		}
		toggled := false
		sections := uikit.ToolPanelSections(state.locale, call)
		// The state is the call's status word *and* its shell exit marker: a command
		// that exited nonzero is a failure here exactly as it is in the background
		// job pane, whatever word the backend attached to the call.
		statusKind := uikit.ToolCallStatusKind(call)
		row := uikit.ToolRowOptions{
			Key:         "toolcards." + key,
			Icon:        uikit.ToolIcon(tool),
			Name:        uikit.ToolDisplayName(state.locale, tool),
			RawName:     tool,
			Status:      uikit.ToolCallStatusLabel(state.locale, call),
			StatusColor: uikit.StatusColor(t, statusKind),
			// The state is the row's leading glyph (F-06): the accent spinner while
			// running, a success check, a danger alert, a neutral dot for a stop.
			State:   statusKind,
			Running: statusKind == uikit.StatusRunning,
			Preview: uikit.ToolCallPreview(state.locale, call),
			// The chevron follows the row's text instead of the column's far end:
			// the process timeline puts reasoning rows (inline chevron) right above
			// tool rows, and one far-end chevron among them read as another widget.
			InlineChevron: true,
			Duration:      uikit.ToolCallElapsed(call),
			Open:          open,
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

// truncateSummary bounds the expanded excerpt (UTF-8 safe) and marks the cut.
// The preview copy and expanded-panel sections live in internal/uikit
// (ToolCallPreview/ToolPanelSections) so any screen renders a call without
// importing this module (2026-11 audit AR-01).
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
				open = uikit.SummaryOpenForCall(call)
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
