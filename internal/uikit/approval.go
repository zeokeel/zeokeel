package uikit

// The approval dialog entry component (AGENT-SURFACES.md S4): the one native
// mygo Modal the product shows before a tool call runs, with the three-way
// safety decision. The component owns the anatomy, the footer order and the
// keyboard contract; the module passes the request, the queue length and the
// resolved labels, and stores only the queue/data state.
//
// Safety contract (locked here, never re-composed by a caller):
//   - the footer is the product's DialogFooter order: Deny (Danger) first,
//     the session-scoped allow, Allow (Primary) rightmost;
//   - NO action is the Default and none is auto-focused, so a stray Enter
//     (the user often presses it right after sending a prompt) decides
//     nothing: neither an accidental allow nor an accidental deny;
//   - Escape or a click outside the Modal is an explicit Deny, so a waiting
//     tool call can never hang on a dialog the user cannot see.
//
// The dialog draws only native controls (Modal, Row/Column, Text, Badge,
// Divider, Scroll, Collapsible, DialogFooter) styled with internal/design.

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// approvalMaxArgumentsRunes bounds the raw command/argument text one dialog
// renders: a request may belong to a huge tool call and the modal must stay
// readable.
const approvalMaxArgumentsRunes = 4000

// approvalArgumentsMaxHeight is the fixed height of the scrollable raw
// arguments block (140 DIP, the historical dialog height kept on the 4 DIP
// token grid).
const approvalArgumentsMaxHeight float32 = design.Space8*4 + design.Space3

// ApprovalLabels is the configurable copy of one approval dialog. Empty fields
// fall back to the localized product defaults, so a host that only overrides
// one label keeps the rest. The caller localizes the fields it passes (the
// approval module keeps its assembly-config labels verbatim and localizes the
// untouched defaults); the fallbacks use Locale.
type ApprovalLabels struct {
	// Title is the dialog heading (default "需要审批"/"Approval required").
	Title string
	// ToolLabel labels the tool row (default "工具"/"Tool").
	ToolLabel string
	// ArgumentsLabel labels the raw arguments block (default "参数"/"Arguments").
	ArgumentsLabel string
	// AllowLabel permits this one call (default "允许"/"Allow").
	AllowLabel string
	// AllowAlwaysLabel permits this tool for the rest of the session (default
	// "本会话内始终允许"/"Always allow in this session").
	AllowAlwaysLabel string
	// DenyLabel rejects the call (default "拒绝"/"Deny").
	DenyLabel string
}

// resolved fills every empty field with its localized default.
func (l ApprovalLabels) resolved(locale contracts.LocaleService) ApprovalLabels {
	return ApprovalLabels{
		Title:            approvalFirstNonEmpty(l.Title, T(locale, "需要审批", "Approval required")),
		ToolLabel:        approvalFirstNonEmpty(l.ToolLabel, T(locale, "工具", "Tool")),
		ArgumentsLabel:   approvalFirstNonEmpty(l.ArgumentsLabel, T(locale, "参数", "Arguments")),
		AllowLabel:       approvalFirstNonEmpty(l.AllowLabel, T(locale, "允许", "Allow")),
		AllowAlwaysLabel: approvalFirstNonEmpty(l.AllowAlwaysLabel, T(locale, "本会话内始终允许", "Always allow in this session")),
		DenyLabel:        approvalFirstNonEmpty(l.DenyLabel, T(locale, "拒绝", "Deny")),
	}
}

// ApprovalDialogOptions is one pending approval request, rendered as the
// product's approval Modal.
type ApprovalDialogOptions struct {
	// Request is the pending request (the oldest one of the queue).
	Request contracts.ApprovalRequest
	// Pending is the size of the pending queue; more than one shows the quiet
	// "还有 N 个待审批请求" counter behind the title.
	Pending int
	// Labels is the configurable copy; zero fields use the product defaults.
	Labels ApprovalLabels
	// Open is the native Modal's open flag (required). Escape, a backdrop
	// click or any decision sets it to false; the caller reopens it for the
	// next queued request.
	Open *bool
	// OnResolve receives the decision. Escape and a backdrop click resolve as
	// Deny; the component never calls it twice for one opening.
	OnResolve func(contracts.ApprovalDecision)
	// Locale localizes the component's built-in copy (impact statement, hints,
	// queue counter); nil falls back to the product's Chinese copy.
	Locale contracts.LocaleService
}

// ApprovalDialog renders the oldest pending approval request in a native Modal.
// It is a frame builder: it only builds controls, except for the decision and
// dismissal handlers (the same immediate-mode pattern the modules use). The
// component owns the dialog's visual state (the collapsed raw-arguments
// disclosure), keyed by the request ID so the next request starts collapsed.
func ApprovalDialog(c *ui.Context, o ApprovalDialogOptions) {
	if o.Open == nil || !*o.Open {
		return
	}
	labels := o.Labels.resolved(o.Locale)
	el := ui.Modal(c, o.Open, func() {
		approvalRequestBody(c, o, labels)
	})
	if el.Dismissed() {
		// Escape or a click outside the Modal is an explicit deny: the agent
		// reports a tool error instead of a hidden dialog waiting forever.
		if o.OnResolve != nil {
			o.OnResolve(contracts.ApprovalDeny)
		}
	}
}

// approvalRequestBody is the Modal body: a brand title row with an approval
// icon, the tool, a one-line impact statement, the summary first (a command for
// shell tools), the argument details collapsed and the decision footer.
func approvalRequestBody(c *ui.Context, o ApprovalDialogOptions, labels ApprovalLabels) {
	t := design.Of(c)
	request := o.Request
	summary := strings.TrimSpace(request.Summary)
	arguments := strings.TrimSpace(request.Arguments)
	emptyArguments := arguments == "" || arguments == "{}" || arguments == "[]" || arguments == "null"
	// The friendly name is what the user reads; the raw tool ID stays in the
	// badge's tooltip (and is what the backend reports back).
	toolName := ToolDisplayName(o.Locale, request.Tool)
	if toolName == "" {
		toolName = request.Tool
	}
	resolve := func(decision contracts.ApprovalDecision) {
		*o.Open = false
		if o.OnResolve != nil {
			o.OnResolve(decision)
		}
	}

	root := design.Apply(ui.Column(c.Key("uikit.approval/dialog."+request.ID)).
		Width(design.DialogWidthM).Gap(design.Space4), func(el ui.Element) ui.Element {
		// The dialog body keeps the brand's Space6 side margin inside the
		// native Modal panel, so the content and its buttons breathe.
		return el.PaddingX(design.Space6).PaddingY(design.Space2)
	})
	// The raw-arguments disclosure is the dialog's own visual state: it keeps
	// its open flag across frames and starts collapsed for each new request.
	argsOpen := ui.Local(root, "argsOpen", func() bool { return false })
	root.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			ui.Icon(c, approvalToolIcon(request.Tool)).Size(design.Space5, design.Space5).
				TextColor(t.Accent)
			design.Apply(ui.Text(c, labels.Title), t.Font(design.TypeL)).TextColor(t.LabelPrimary)
			if o.Pending > 1 {
				// A quiet counter, not another dialog: the rest are queued
				// behind this one and appear as each is resolved.
				ui.Spacer(c).Grow(1)
				design.Apply(ui.Text(c, fmt.Sprintf(
					T(o.Locale, "还有 %d 个待审批请求", "%d more approval requests waiting"), o.Pending-1)),
					t.TextMuted(design.TypeXXS)).NoWrap()
			}
		})
		// The tool is named once: label + badge, with the impact in one line.
		// The badge shows the friendly localized name; the raw ID is its tooltip.
		ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, labels.ToolLabel), t.TextMuted(design.TypeXS))
			design.Apply(ui.Badge(c, toolName), t.Badge()).Tooltip(request.Tool)
		})
		// The approval only lets the call through the gate: the sandbox policy
		// still runs it and may deny it, so the copy must not promise execution.
		design.Apply(ui.Text(c, T(o.Locale,
			"允许后将在沙箱策略限制内执行；策略仍可能拒绝该调用。",
			"It runs within the sandbox policy after approval; the policy may still reject the call.")),
			t.Font(design.TypeXS)).TextColor(t.LabelSecondary)
		// What will run comes first: the summary is the thing the user is
		// judging, labelled as a command only for shell/terminal tools.
		if summary != "" && summary != arguments {
			summaryLabel := approvalSummaryLabel(o.Locale, request.Tool)
			design.Apply(ui.Text(c, summaryLabel), t.TextMuted(design.TypeXS))
			design.Apply(ui.Row(c).FillWidth(), func(el ui.Element) ui.Element {
				return el.Background(t.BgBase).Border(1, t.BorderL2).
					Radius(design.RadiusSM).Padding(design.Space2)
			}).Children(func() {
				design.Apply(ui.Text(c, approvalTruncate(summary, approvalMaxArgumentsRunes)),
					t.Font(design.TypeXS), t.MonoFamily()).TextColor(t.LabelPrimary)
			})
		}
		// The raw arguments are details: collapsed, and "no arguments" instead
		// of an empty box when the call carries {} / [] / null.
		ui.Divider(c)
		design.Apply(ui.Text(c, labels.ArgumentsLabel), t.TextMuted(design.TypeXS))
		if emptyArguments {
			design.Apply(ui.Text(c, T(o.Locale, "无参数", "No arguments")),
				t.TextMuted(design.TypeXS))
		} else {
			// Two layers only: the configurable section label above the
			// disclosure itself.
			ui.Collapsible(c.Key("approval.arguments."+request.ID),
				T(o.Locale, "显示原始参数", "Show raw arguments"), argsOpen, func() {
					scroll := ui.Scroll(c).Height(approvalArgumentsMaxHeight).FillWidth()
					design.Apply(scroll, func(el ui.Element) ui.Element {
						return el.Background(t.BgBase).Border(1, t.BorderL2).
							Radius(design.RadiusSM).Padding(design.Space2)
					}).Children(func() {
						design.Apply(ui.Text(c, approvalTruncate(arguments, approvalMaxArgumentsRunes)),
							t.TextMuted(design.TypeXS), t.MonoFamily())
					})
				})
		}
		ui.Divider(c)
		// The product's one dialog action bar: the destructive Deny first, the
		// session-scoped allow, the accent Allow last (rightmost). No action is
		// the Default and nothing is auto-focused, so a stray Enter decides
		// nothing. Esc remains the explicit deny shortcut; a decision needs a
		// click or Tab + Enter.
		c.Key("approval.footer/" + request.ID)
		DialogFooter(c, []DialogAction{
			{Label: labels.DenyLabel, Danger: true, OnClick: func() {
				resolve(contracts.ApprovalDeny)
			}},
			{Label: labels.AllowAlwaysLabel, OnClick: func() {
				resolve(contracts.ApprovalAllowAlways)
			}},
			{Label: labels.AllowLabel, Primary: true, OnClick: func() {
				resolve(contracts.ApprovalAllow)
			}},
		})
		// The middle choice is session-scoped, not run-scoped: say so once in
		// quiet copy, including where the user can take it back.
		design.Apply(ui.Text(c, T(o.Locale,
			"「本会话内始终允许」在本会话内不再询问该工具；可在会话设置菜单清除。",
			"\"Always allow in this session\" stops this tool's prompts for this conversation; clear it from the session settings menu.")),
			t.TextMuted(design.TypeXXS)).MaxLines(2)
		// The key semantics are visible: Esc denies explicitly, and there is no
		// default action, so a stray Enter decides nothing.
		design.Apply(ui.Text(c, T(o.Locale,
			"Esc = 拒绝本次调用 · 点击或 Tab 选择操作（无默认操作）",
			"Esc = deny this call · click or Tab to choose an action (no default action)")),
			t.TextMuted(design.TypeXXS))
	})
}

// approvalSummaryLabel names the requested call's summary block. Only the shell
// and terminal tools ask about a command line; a file read, a web fetch or a
// team/task call would read as something to execute when labelled 命令, so they
// keep the neutral 摘要 (the raw arguments stay in the collapsed disclosure
// below).
func approvalSummaryLabel(locale contracts.LocaleService, tool string) string {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "bash", "pwsh", "terminal_send":
		return T(locale, "命令", "Command")
	default:
		return T(locale, "摘要", "Summary")
	}
}

// approvalToolIcon picks the header glyph for the tool being approved:
// destructive shell/file tools get the alert triangle, everything else the
// shield.
func approvalToolIcon(tool string) *ui.SVG {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "bash", "write_file", "edit_file", "str_replace_editor",
		"terminal_send", "job_start", "enter_worktree":
		return icons.AlertTriangle()
	default:
		return icons.Shield()
	}
}

// approvalTruncate shortens s to at most max runes, marking the cut.
func approvalTruncate(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max]) + "…"
}

// approvalFirstNonEmpty returns the first non-empty string.
func approvalFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
