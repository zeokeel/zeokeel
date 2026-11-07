// Package toolcards contributes the tool-card renderers (the UI for one tool
// call). It is the RegisterToolRenderer injection point in practice: the module
// that owns a tool ships its card, and any screen that calls
// UIRuntime.RenderTool gets it without knowing which module drew it.
//
// The module registers one native renderer per common Rig tool name (bash,
// read_file, grep, web_search, subagent, ...) plus the original "demo" card the
// host uses as a capability probe. All real cards share one native composition:
// a status badge (theme-colored: running=warning, ok=success, error=danger), the
// tool name, the call ID and, when the read model carries a summary, a
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
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
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
}

func newRendererState() *rendererState {
	return &rendererState{open: make(map[string]bool)}
}

func (s *rendererState) load(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open[key]
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
		state := newRendererState()

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

		build := realBuild(state)
		for _, name := range realToolNames {
			tool := name
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

// demoBuild is the original demo card, kept byte-compatible with the existing
// host integration test (title, raw status badge, summary, call ID).
func demoBuild(title string) func(c *ui.Context, call contracts.ToolCall) {
	return func(c *ui.Context, call contracts.ToolCall) {
		theme := c.Theme()
		ui.Column(c).FillWidth().Gap(6).Padding(10).Radius(8).
			Background(theme.Surface).Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, title+": "+call.Name).Bold().FontSize(13)
				ui.Badge(c, call.Status)
				ui.Text(c, call.ID).FontSize(11).TextColor(theme.TextMuted)
			})
			if call.Summary != "" {
				ui.Text(c, call.Summary).FontSize(13)
			}
		})
	}
}

// realBuild builds one real tool card: status badge + name + call ID + a native
// collapsible summary. The same builder serves every registered tool name.
func realBuild(state *rendererState) func(c *ui.Context, call contracts.ToolCall) {
	return func(c *ui.Context, call contracts.ToolCall) {
		theme := c.Theme()
		status := statusLabel(call.Status)
		color := statusColor(theme, call.Status)
		ui.Column(c).FillWidth().Gap(6).Padding(10).Radius(8).
			Background(theme.Surface).Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Badge(c, status).Background(color.Alpha(0.15)).TextColor(color)
				ui.Text(c, call.Name).Bold().FontSize(13)
				if call.ID != "" {
					ui.Text(c, call.ID).FontSize(11).TextColor(theme.TextMuted)
				}
				ui.Spacer(c).Grow(1)
				if category := toolCategory(call.Name); category != "" {
					ui.Text(c, category).FontSize(11).TextColor(theme.TextMuted)
				}
			})
			if summary := strings.TrimSpace(call.Summary); summary != "" {
				key := call.ID + "\x00" + call.Name
				open := state.load(key)
				ui.Collapsible(c, "摘要", &open, func() {
					ui.Text(c, truncateSummary(summary)).FontSize(12).TextColor(theme.TextMuted)
				})
				state.store(key, open)
			}
		})
	}
}

// statusLabel maps a tool status to the Chinese card label.
func statusLabel(status string) string {
	switch normalizeStatus(status) {
	case "running":
		return "运行中"
	case "ok":
		return "完成"
	case "error":
		return "失败"
	default:
		if trimmed := strings.TrimSpace(status); trimmed != "" {
			return trimmed
		}
		return "未知"
	}
}

// statusColor maps a tool status to its theme color.
func statusColor(theme *ui.Theme, status string) ui.Color {
	switch normalizeStatus(status) {
	case "running":
		return theme.Warning
	case "ok":
		return theme.Success
	case "error":
		return theme.Danger
	default:
		return theme.TextMuted
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

// toolCategory is the small inline hint shown at the right of a card header.
func toolCategory(name string) string {
	switch name {
	case "bash":
		return "shell"
	case "read_file", "write_file", "edit_file", "str_replace_editor", "list_dir", "glob", "grep", "read_image":
		return "文件"
	case "web_fetch", "web_search":
		return "网络"
	case "job_start", "job_output", "terminal_send", "terminal_read":
		return "进程"
	case "enter_worktree", "list_worktrees":
		return "工作树"
	case "subagent", "workflow", "ralph":
		return "编排"
	case "team_task_create", "team_task_update", "todo":
		return "任务"
	case "skill":
		return "技能"
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
