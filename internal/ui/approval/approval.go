// Package approval contributes the pending tool-call approval dialog of the
// product: one native mygo Modal (inside the host's overlay.dialogs slot) for
// the oldest request the approvals service is waiting on.
//
// It is a kernel UI module like ui/sidebar: activation requires the host UI
// runtime and the approvals service, subscribes to pending-set changes (the
// callback hops to the UI thread with Post/Invalidate), and registers one
// SlotItem whose unregister function is owned by a kernel effect. The dialog
// itself is built only from mygo's native controls (Modal, Row/Column, Text,
// Badge, Divider, Scroll, Button/PrimaryButton); the module draws no widget.
//
// Resolution: the three buttons resolve the request as allow / allow-always /
// deny. Pressing Escape or clicking the backdrop dismisses the native Modal,
// which is treated as deny (the agent reports a tool error) so a pending call
// can never be left waiting on a hidden dialog.
package approval

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// ModuleID is the compile-time module identity.
const ModuleID = "zeokeel-ui/approval"

// SlotID identifies this module's contribution to overlay.dialogs.
const SlotID = "approval.dialog"

// maxArgumentsRunes bounds the raw arguments shown in the dialog; the request
// may belong to a huge tool call and the modal must stay readable.
const maxArgumentsRunes = 4000

// Config is the module's typed assembly config (all fields have defaults).
type Config struct {
	// Title is the dialog heading (default "需要审批").
	Title string `json:"title"`
	// ToolLabel labels the tool name row (default "工具").
	ToolLabel string `json:"toolLabel"`
	// ArgumentsLabel labels the raw arguments block (default "参数").
	ArgumentsLabel string `json:"argumentsLabel"`
	// AllowLabel permits this one call (default "允许").
	AllowLabel string `json:"allowLabel"`
	// AllowAlwaysLabel permits this tool from now on (default "总是允许").
	AllowAlwaysLabel string `json:"allowAlwaysLabel"`
	// DenyLabel rejects the call (default "拒绝").
	DenyLabel string `json:"denyLabel"`
}

// resolved fills every empty field with its default.
func (c Config) resolved() Config {
	return Config{
		Title:            firstNonEmpty(c.Title, "需要审批"),
		ToolLabel:        firstNonEmpty(c.ToolLabel, "工具"),
		ArgumentsLabel:   firstNonEmpty(c.ArgumentsLabel, "参数"),
		AllowLabel:       firstNonEmpty(c.AllowLabel, "允许"),
		AllowAlwaysLabel: firstNonEmpty(c.AllowAlwaysLabel, "本次运行内始终允许"),
		DenyLabel:        firstNonEmpty(c.DenyLabel, "拒绝"),
	}
}

// state is the module instance's dialog state. It lives in the activation
// closure (one per loaded module) and is touched only on the UI thread.
type state struct {
	// locale localizes the dialog copy per frame (zh is the default).
	locale contracts.LocaleService
	// open is the native Modal's open flag; Escape/backdrop clicks set it to
	// false and are treated as deny.
	open bool
	// shownID is the request currently displayed ("" when none).
	shownID string
	// resolvedID is the request just answered; its row may still be in
	// Pending for a frame or two while the backend settles, and the dialog
	// must not reopen for it.
	resolvedID string
	// argsOpen keeps the collapsed argument details across frames.
	argsOpen bool
}

// Module registers the approval dialog contribution.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		approvals, err := ctx.Require(contracts.KeyApprovals)
		if err != nil {
			return err
		}
		resolved := cfg.resolved()
		locale, _ := ctx.Get(contracts.KeyLocale)
		st := &state{locale: locale}

		// The approvals service notifies synchronously from whichever
		// goroutine resolves or enqueues a request. Hop to the UI thread and
		// let the host schedule a frame; nothing here touches frame state.
		unsubscribe, err := approvals.Subscribe(func() {
			runtime.Post(func() { runtime.Invalidate() })
		})
		if err != nil {
			return fmt.Errorf("approval: subscribe approvals: %w", err)
		}
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(unsubscribe)}
		}); err != nil {
			unsubscribe()
			return err
		}

		item := contracts.SlotItem{
			Slot:  contracts.SlotOverlayDialogs,
			ID:    SlotID,
			Order: 10,
			Build: func(c *ui.Context) {
				buildDialog(c, runtime, approvals, resolved, st)
			},
		}
		unregister, err := runtime.RegisterSlotItem(item)
		if err != nil {
			return fmt.Errorf("approval: register slot: %w", err)
		}
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(unregister)}
		}); err != nil {
			unregister()
			return err
		}
		return nil
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyApprovals)).
		WithName("ZeoKeel approval dialog")
}

// tr returns the localized copy for the current frame's language.
func (st *state) tr(zh, en string) string { return uikit.T(st.locale, zh, en) }

// localized translates a value that still carries its built-in Chinese default
// (an assembly-configured custom label is kept verbatim).
func (st *state) localized(value, zhDefault, enDefault string) string {
	if strings.TrimSpace(value) == "" || value == zhDefault {
		return st.tr(zhDefault, enDefault)
	}
	return value
}

// buildDialog renders the oldest pending request in a native Modal, or nothing
// while no request is waiting. It is a frame builder: it only builds controls,
// except for the click/dismiss handlers that answer the pending request (the
// same immediate-mode pattern the other UI modules use for button clicks).
func buildDialog(c *ui.Context, runtime contracts.UIRuntime, approvals contracts.Approvals, cfg Config, st *state) {
	pending := approvals.Pending()
	if len(pending) == 0 {
		st.open = false
		st.shownID = ""
		st.resolvedID = ""
		return
	}
	request := pending[0]
	if request.ID == "" || request.ID == st.resolvedID {
		// Empty IDs are not resolvable; a just-answered request is still
		// listed while the backend settles, so wait for the next change.
		return
	}
	if request.ID != st.shownID {
		st.shownID = request.ID
		st.open = true
		// Safe default: the footer's Deny action is its Default (focused as
		// the request opens); Enter then presses it (the native button
		// behaviour), so a high-risk tool is never allowed with one stray
		// Enter.
		st.argsOpen = false
	}
	ui.Modal(c, &st.open, func() {
		requestPanel(c, runtime, approvals, cfg, st, request)
	})
	if !st.open {
		// Escape or a backdrop click closed the native Modal. Treat it as
		// deny so the waiting tool call fails clearly instead of hanging.
		id := st.shownID
		st.shownID = ""
		st.resolvedID = id
		if id != "" {
			_ = approvals.Resolve(id, contracts.ApprovalDeny)
			runtime.Invalidate()
		}
	}
}

// requestPanel is the Modal body: a brand title row with an approval icon, the
// tool, a one-line impact statement, the summary first (a command for shell
// tools), the argument details collapsed and the decision footer.
//
// Safety default (H4): there is no dialog-wide Enter shortcut. The Deny button
// is focused as the request opens, so Enter presses the focused button — a
// high-risk tool is never allowed by a stray Enter; allowing needs Tab-to-Allow
// (or a click). The footer order follows the product convention: danger first,
// secondary, accent primary last.
func requestPanel(c *ui.Context, runtime contracts.UIRuntime, approvals contracts.Approvals, cfg Config, st *state, request contracts.ApprovalRequest) {
	t := design.Of(c)
	title := st.localized(cfg.Title, "需要审批", "Approval required")
	toolLabel := st.localized(cfg.ToolLabel, "工具", "Tool")
	argumentsLabel := st.localized(cfg.ArgumentsLabel, "参数", "Arguments")
	allowLabel := st.localized(cfg.AllowLabel, "允许", "Allow")
	allowAlwaysLabel := st.localized(cfg.AllowAlwaysLabel, "本次运行内始终允许", "Always allow this run")
	denyLabel := st.localized(cfg.DenyLabel, "拒绝", "Deny")
	summary := strings.TrimSpace(request.Summary)
	arguments := strings.TrimSpace(request.Arguments)
	emptyArguments := arguments == "" || arguments == "{}" || arguments == "[]" || arguments == "null"

	// The native Modal sizes its panel around this content; the brand spacing
	// keeps the dialog on the same 4 DIP grid as the settings pages.
	design.Apply(ui.Column(c).Width(560).Gap(design.Space4), func(el ui.Element) ui.Element {
		// The dialog body keeps the brand's Space6 side margin inside the
		// native Modal panel, so the content and its buttons breathe.
		return el.PaddingX(design.Space6).PaddingY(design.Space2)
	}).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Icon(c, approvalIcon(request.Tool)).Size(design.Space5, design.Space5).
				TextColor(t.Accent)
			design.Apply(ui.Text(c, title), t.Font(design.TypeL)).FontWeight(600).
				TextColor(t.LabelPrimary)
		})
		// The tool is named once: label + badge, with the impact in one line.
		ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, toolLabel), t.TextMuted(design.TypeXS))
			design.Apply(ui.Badge(c, request.Tool), t.Badge())
		})
		// The approval only lets the call through the gate: the sandbox policy
		// still runs it and may deny it, so the copy must not promise execution
		// (ZK-02: "会立即执行" was false when the sandbox rejected the command).
		design.Apply(ui.Text(c, st.tr("允许后将在沙箱策略限制内执行；策略仍可能拒绝该调用。",
			"It runs within the sandbox policy after approval; the policy may still reject the call.")),
			t.Font(design.TypeXS)).
			TextColor(t.LabelSecondary)
		// What will run comes first: the summary (a command for shell tools) is
		// the thing the user is judging.
		if summary != "" && summary != arguments {
			summaryLabel := st.tr("摘要", "Summary")
			if commandLikeTool(request.Tool) {
				summaryLabel = st.tr("命令", "Command")
			}
			design.Apply(ui.Text(c, summaryLabel), t.TextMuted(design.TypeXS))
			design.Apply(ui.Row(c).FillWidth(), func(el ui.Element) ui.Element {
				return el.Background(t.BgBase).Border(1, t.BorderL2).
					Radius(design.RadiusSM).Padding(design.Space2)
			}).Children(func() {
				design.Apply(ui.Text(c, truncate(summary, maxArgumentsRunes)),
					t.Font(design.TypeXS), t.MonoFamily()).TextColor(t.LabelPrimary)
			})
		}
		// The raw arguments are details: collapsed, and "no arguments" instead
		// of an empty box when the call carries {} / [] / null.
		ui.Divider(c)
		design.Apply(ui.Text(c, argumentsLabel), t.TextMuted(design.TypeXS))
		if emptyArguments {
			design.Apply(ui.Text(c, st.tr("无参数", "No arguments")),
				t.TextMuted(design.TypeXS))
		} else {
			// Two layers only: the configurable section label (参数/Arguments)
			// above the disclosure itself.
			ui.Collapsible(c.Key("approval.arguments."+request.ID),
				st.tr("显示原始参数", "Show raw arguments"), &st.argsOpen, func() {
					scroll := ui.Scroll(c).Height(140).FillWidth()
					design.Apply(scroll, func(el ui.Element) ui.Element {
						return el.Background(t.BgBase).Border(1, t.BorderL2).
							Radius(design.RadiusSM).Padding(design.Space2)
					}).Children(func() {
						design.Apply(ui.Text(c, truncate(arguments, maxArgumentsRunes)),
							t.TextMuted(design.TypeXS), t.MonoFamily())
					})
				})
		}
		ui.Divider(c)
		// The product's one dialog action bar: the destructive Deny first, the
		// run-scoped allow, the accent Allow last (rightmost). Deny is the
		// footer's Default action, focused as the request opens, so Enter
		// denies and allowing needs a deliberate Tab or click. The footer is
		// keyed by request so a following request builds fresh buttons and its
		// Default takes the focus again.
		c.Key("approval.footer/" + request.ID)
		uikit.DialogFooter(c, []uikit.DialogAction{
			{Label: denyLabel, Danger: true, Default: true, OnClick: func() {
				resolve(runtime, approvals, st, contracts.ApprovalDeny)
			}},
			{Label: allowAlwaysLabel, OnClick: func() {
				resolve(runtime, approvals, st, contracts.ApprovalAllowAlways)
			}},
			{Label: allowLabel, Primary: true, OnClick: func() {
				resolve(runtime, approvals, st, contracts.ApprovalAllow)
			}},
		})
		// The key semantics are visible: Esc denies, and Enter presses whatever
		// control has the focus (Deny by default) — never a dialog-wide Allow.
		design.Apply(ui.Text(c, st.tr("Esc = 拒绝本次调用 · Enter 执行当前聚焦的按钮",
			"Esc = deny this call · Enter presses the focused button")),
			t.TextMuted(design.TypeXXS))
	})
}

// commandLikeTool reports whether a tool's summary is a command line, so the
// approval dialog can show it monospaced first.
func commandLikeTool(tool string) bool {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "bash", "terminal_send", "job_start", "web_fetch", "web_search",
		"read_file", "write_file", "edit_file", "str_replace_editor":
		return true
	default:
		return false
	}
}

// approvalIcon picks the header glyph for the tool being approved: destructive
// shell/file tools get the alert triangle, everything else the shield.
func approvalIcon(tool string) *ui.SVG {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "bash", "write_file", "edit_file", "str_replace_editor",
		"terminal_send", "job_start", "enter_worktree":
		return icons.AlertTriangle()
	default:
		return icons.Shield()
	}
}

// resolve answers the displayed request once and closes the dialog. The
// request ID is remembered so a Pending list that still lists it during the
// backend's settle cannot reopen the dialog for the same decision.
func resolve(runtime contracts.UIRuntime, approvals contracts.Approvals, st *state, decision contracts.ApprovalDecision) {
	id := st.shownID
	st.open = false
	st.shownID = ""
	st.resolvedID = id
	if id != "" {
		_ = approvals.Resolve(id, decision)
	}
	runtime.Invalidate()
}

// truncate shortens s to at most max runes, marking the cut.
func truncate(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
