package uikit

// The approval entry component (AGENT-SURFACES.md S4): the product shows one
// pending tool-call request before it runs and asks for the three-way safety
// decision.
//
// Two presentations share one anatomy and one decision contract:
//   - Inline is the product path: a card in the session's banner slot, directly
//     above the composer (ApprovalDialogOptions.Inline). It is deliberately not
//     a dialog: the user keeps reading and typing while a call waits, the card
//     stays visible in the conversation flow, and its emphasis comes from the V3
//     treatment — a warning banner, a neutral raised body and a warning border —
//     so it can never be mistaken for an ordinary tool card.
//   - The native Modal is kept for compatibility (tests and hosts that still
//     want a blocking gate); Inline=false renders it exactly as before.
//
// Safety contract (locked here, never re-composed by a caller):
//   - the footer is the product's DialogFooter order: Deny first, the
//     session-scoped allow, Allow (Primary) rightmost;
//   - NO action is the Default and none is auto-focused, so a stray Enter
//     (the user often presses it right after sending a prompt) decides
//     nothing: neither an accidental allow nor an accidental deny;
//   - the component itself handles no keyboard shortcut. In the Modal path
//     Escape/backdrop are an explicit Deny; in the Inline path the session screen
//     owns Escape (Escape resolves the oldest pending request as deny, see
//     ui/session), so the card is never dismissed without an answer.
//
// The card draws only native controls (Row/Column, Text, Badge, Divider, Scroll,
// Collapsible, DialogFooter) styled with internal/design.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// approvalMaxArgumentsRunes bounds the raw command/argument text one card
// renders: a request may belong to a huge tool call and the card must stay
// readable.
const approvalMaxArgumentsRunes = 4000

// approvalArgumentsMaxHeight is the fixed height of the scrollable raw
// arguments block (140 DIP, the historical dialog height kept on the 4 DIP
// token grid).
const approvalArgumentsMaxHeight float32 = design.Space8*4 + design.Space3

// ApprovalLabels is the configurable copy of one approval card. Empty fields
// fall back to the localized product defaults, so a host that only overrides
// one label keeps the rest. The caller localizes the fields it passes (the
// approval module keeps its assembly-config labels verbatim and localizes the
// untouched defaults); the fallbacks use Locale.
type ApprovalLabels struct {
	// Title is the card heading (default "需要审批"/"Approval required").
	Title string
	// ToolLabel labels the tool row of the Modal presentation (default
	// "工具"/"Tool"); the inline banner names the tool without a label.
	ToolLabel string
	// ArgumentsLabel labels the raw arguments disclosure (default
	// "原始参数"/"Raw arguments"). It is the one label of the disclosure: the
	// summary above already names what the call does.
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
		ArgumentsLabel:   approvalFirstNonEmpty(l.ArgumentsLabel, T(locale, "原始参数", "Raw arguments")),
		AllowLabel:       approvalFirstNonEmpty(l.AllowLabel, T(locale, "允许", "Allow")),
		AllowAlwaysLabel: approvalFirstNonEmpty(l.AllowAlwaysLabel, T(locale, "本会话内始终允许", "Always allow in this session")),
		DenyLabel:        approvalFirstNonEmpty(l.DenyLabel, T(locale, "拒绝", "Deny")),
	}
}

// ApprovalDialogOptions is one pending approval request, rendered as the
// product's inline approval card (or, with Inline=false, the legacy Modal).
type ApprovalDialogOptions struct {
	// Request is the pending request (the oldest one of the queue).
	Request contracts.ApprovalRequest
	// Pending is the size of the pending queue; more than one shows the quiet
	// "还有 N 条待审批" counter in the card's banner.
	Pending int
	// Labels is the configurable copy; zero fields use the product defaults.
	Labels ApprovalLabels
	// Inline renders the in-flow card above the composer instead of the
	// centred Modal. Inline mode ignores Open and never handles Escape or a
	// backdrop click: the session screen owns the keyboard and the caller owns
	// the pending queue.
	Inline bool
	// Open is the native Modal's open flag (Modal path only, required there).
	// Escape, a backdrop click or any decision sets it to false; the caller
	// reopens it for the next queued request.
	Open *bool
	// OnResolve receives the decision. In the Modal path Escape and a backdrop
	// click resolve as Deny; the component never calls it twice for one opening.
	OnResolve func(contracts.ApprovalDecision)
	// Locale localizes the component's built-in copy (impact statement, hints,
	// queue counter); nil falls back to the product's Chinese copy.
	Locale contracts.LocaleService
}

// ApprovalDialog renders the oldest pending approval request: the inline card
// (Inline) or the legacy native Modal. It is a frame builder: it only builds
// controls, except for the decision and dismissal handlers (the same
// immediate-mode pattern the modules use). The component owns the card's visual
// state (the collapsed raw-arguments disclosure), keyed by the request ID so the
// next request starts collapsed.
func ApprovalDialog(c *ui.Context, o ApprovalDialogOptions) {
	labels := o.Labels.resolved(o.Locale)
	if o.Inline {
		approvalInlineCard(c, o, labels)
		return
	}
	if o.Open == nil || !*o.Open {
		return
	}
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

// approvalInlineCard is the product's V3 inline approval card: a warning banner
// over a neutral raised body, outlined in the warning color so it stands out in
// the conversation flow. It is built in the session's banner slot, above the
// composer.
func approvalInlineCard(c *ui.Context, o ApprovalDialogOptions, labels ApprovalLabels) {
	t := design.Of(c)
	request := o.Request
	summary := strings.TrimSpace(request.Summary)
	arguments := strings.TrimSpace(request.Arguments)
	toolName := approvalToolName(o.Locale, request.Tool)
	resolve := func(decision contracts.ApprovalDecision) {
		if o.OnResolve != nil {
			o.OnResolve(decision)
		}
	}

	root := design.Apply(ui.Column(c.Key("uikit.approval/inline."+request.ID)).FillWidth(),
		func(el ui.Element) ui.Element {
			// The warning banner carries the emphasis; the card outline is a
			// quiet warning tint (40%), never a full-saturation warning frame
			// around the whole card (DESIGN.md §5.2 A: warning colors do not
			// paint full-strength borders).
			return el.Background(t.BgRaised).Border(1, t.Warning.Alpha(0.4)).Radius(design.RadiusLG)
		})
	root.Role(ui.RoleRegion).Label(labels.Title + " · " + toolName)
	// The raw-arguments disclosure is the card's own visual state: it keeps its
	// open flag across frames and starts collapsed for each new request.
	argsOpen := ui.Local(root, "argsOpen", func() bool { return false })
	root.Children(func() {
		// The warning banner: the card's one strong emphasis, so it reads as a
		// decision to make, not as another tool result.
		design.Apply(ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2),
			func(el ui.Element) ui.Element {
				return el.Background(t.Warning.Alpha(0.22)).
					Padding(design.Space2, design.Space3).
					Radius(design.RadiusLG, design.RadiusLG, design.RadiusXS, design.RadiusXS)
			}).Children(func() {
			ui.Icon(c, icons.Shield()).Size(design.Space3, design.Space3).
				TextColor(t.WarningText)
			design.Apply(ui.Text(c, labels.Title), t.Font(design.TypeS)).
				Bold().TextColor(t.WarningText).NoWrap()
			// The friendly name is what the user reads; the raw tool ID stays in
			// the tooltip (and is what the backend reports back).
			design.Apply(ui.Text(c, toolName), t.Font(design.TypeXXXS)).
				TextColor(t.WarningText).Tooltip(request.Tool).NoWrap()
			ui.Spacer(c).Grow(1)
			if o.Pending > 1 {
				// The rest wait behind this one; the banner counts them instead
				// of rendering more cards.
				design.Apply(ui.Text(c, fmt.Sprintf(T(o.Locale,
					"还有 %d 条待审批", "%d more pending approvals"), o.Pending-1)),
					t.Font(design.TypeXXXS)).TextColor(t.WarningText).NoWrap()
			}
			design.Apply(ui.Text(c, T(o.Locale, "Esc 拒绝", "Esc denies")),
				t.Font(design.TypeXXXS)).TextColor(t.WarningText).NoWrap()
		})
		// The neutral body: what will run, the details and the decision. The
		// body keeps the raised surface so the warning banner is the only
		// colored region.
		design.Apply(ui.Column(c).FillWidth().Gap(design.Space2),
			func(el ui.Element) ui.Element {
				return el.Background(t.BgRaised).Padding(design.Space3).
					Radius(design.RadiusXS, design.RadiusXS, design.RadiusLG, design.RadiusLG)
			}).Children(func() {
			approvalImpactLine(c, t, o.Locale)
			approvalSummarySection(c, t, o, summary, arguments)
			approvalArgumentsSection(c, t, o, labels, request, arguments, argsOpen)
			// The middle choice is session-scoped, not run-scoped: say so once
			// in quiet copy, including where the user can take it back.
			design.Apply(ui.Text(c, T(o.Locale,
				"「本会话内始终允许」在本会话内不再询问该工具；可在会话设置菜单清除。",
				"\"Always allow in this session\" stops this tool's prompts for this conversation; clear it from the session settings menu.")),
				t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary).MaxLines(2)
			// The inline footer keeps the decision order (deny, session-scoped
			// allow, allow) without the Modal's danger emphasis: the warning
			// border and banner already carry the weight, and the deny stays the
			// safest-looking leftmost action. No action is the Default.
			DialogFooter(c, []DialogAction{
				{Label: labels.DenyLabel, OnClick: func() {
					resolve(contracts.ApprovalDeny)
				}},
				{Label: labels.AllowAlwaysLabel, OnClick: func() {
					resolve(contracts.ApprovalAllowAlways)
				}},
				{Label: labels.AllowLabel, Primary: true, OnClick: func() {
					resolve(contracts.ApprovalAllow)
				}},
			})
		})
	})
}

// approvalRequestBody is the Modal body: a brand title row with an approval
// icon, the tool, a one-line impact statement, the summary first (a command for
// shell tools), the argument details collapsed and the decision footer.
func approvalRequestBody(c *ui.Context, o ApprovalDialogOptions, labels ApprovalLabels) {
	t := design.Of(c)
	request := o.Request
	summary := strings.TrimSpace(request.Summary)
	arguments := strings.TrimSpace(request.Arguments)
	toolName := approvalToolName(o.Locale, request.Tool)
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
		// The one dialog header: the tool's alert/shield glyph, the decision
		// title and, when the queue has more requests, their count as the muted
		// subtitle. There is deliberately no close control: the decision needs
		// an explicit answer (Escape denies, it never just dismisses).
		subtitle := ""
		if o.Pending > 1 {
			subtitle = fmt.Sprintf(
				T(o.Locale, "还有 %d 个待审批请求", "%d more approval requests waiting"), o.Pending-1)
		}
		DialogHeader(c, DialogHeaderOptions{
			Key:      "uikit.approval/dialog-header." + request.ID,
			Icon:     approvalToolIcon(request.Tool),
			Title:    labels.Title,
			Subtitle: subtitle,
		})
		// The tool is named once: label + badge, with the impact in one line.
		// The badge shows the friendly localized name; the raw ID is its tooltip.
		ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, labels.ToolLabel), t.TextMuted(design.TypeXS))
			design.Apply(ui.Badge(c, toolName), t.Badge()).Tooltip(request.Tool)
		})
		approvalImpactLine(c, t, o.Locale)
		approvalSummarySection(c, t, o, summary, arguments)
		approvalArgumentsSection(c, t, o, labels, request, arguments, argsOpen)
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

// approvalToolName returns the visible friendly name of one tool; an unknown
// tool keeps its raw ID (the raw ID is always the tooltip/badge tooltip).
func approvalToolName(locale contracts.LocaleService, tool string) string {
	name := ToolDisplayName(locale, tool)
	if name == "" {
		return tool
	}
	return name
}

// approvalImpactLine renders the copy that keeps the gate honest: approval only
// lets the call through the gate; the sandbox policy still runs it and may deny
// it, so the copy must not promise execution.
func approvalImpactLine(c *ui.Context, t *design.Tokens, locale contracts.LocaleService) {
	design.Apply(ui.Text(c, T(locale,
		"允许后将在沙箱策略限制内执行；策略仍可能拒绝该调用。",
		"It runs within the sandbox policy after approval; the policy may still reject the call.")),
		t.Font(design.TypeXS)).TextColor(t.LabelSecondary)
}

// approvalSummarySection renders the summary block first: what will run is the
// thing the user is judging, labelled as a command only for shell/terminal
// tools. A request whose tool reported no summary still gets a readable
// preview: the first one to three key/value pairs of its JSON arguments
// (bash→command, files→path, tasks→subject/owner) instead of a blind "approve"
// with only a collapsed raw JSON below.
func approvalSummarySection(c *ui.Context, t *design.Tokens, o ApprovalDialogOptions,
	summary, arguments string) {
	text := summary
	if text == "" || text == arguments {
		text = approvalArgumentsPreview(o.Request.Tool, arguments)
	}
	if text == "" {
		return
	}
	design.Apply(ui.Text(c, approvalSummaryLabel(o.Locale, o.Request.Tool)),
		t.TextMuted(design.TypeXS))
	design.Apply(ui.Row(c).FillWidth(), func(el ui.Element) ui.Element {
		return el.Background(t.BgBase).Border(1, t.BorderL2).
			Radius(design.RadiusSM).Padding(design.Space2)
	}).Children(func() {
		design.Apply(ui.Text(c, approvalTruncate(text, approvalMaxArgumentsRunes)),
			t.Font(design.TypeXS), t.MonoFamily()).TextColor(t.LabelPrimary)
	})
}

// approvalSummaryKeys is the readable-key priority of one tool family: the
// fields a reviewer needs to decide are rarely the whole argument object.
// Anything not listed falls back to the object's own key order.
var approvalSummaryKeys = map[string][]string{
	"bash":               {"command"},
	"pwsh":               {"command"},
	"terminal_send":      {"command", "input", "text"},
	"job_start":          {"command"},
	"read_file":          {"path", "file_path"},
	"write_file":         {"path", "file_path"},
	"edit_file":          {"path", "file_path"},
	"str_replace_editor": {"path", "file_path", "command"},
	"list_dir":           {"path"},
	"glob":               {"pattern", "path"},
	"grep":               {"pattern", "path"},
	"read_image":         {"path", "file_path"},
	"web_fetch":          {"url"},
	"web_search":         {"query"},
	"team_task_create":   {"subject", "owner", "description"},
	"team_task_update":   {"id", "subject", "status", "owner"},
	"todo":               {"items", "subject", "status"},
	"subagent":           {"prompt", "description"},
	"workflow":           {"prompt", "description"},
	"skill":              {"name", "arguments"},
}

// approvalArgumentsPreview extracts the first one to three readable key/value
// pairs of a JSON argument object for the summary fallback. Unparseable
// arguments keep a bounded raw preview, so the summary area always says what
// will run; empty or null arguments preview nothing.
func approvalArgumentsPreview(tool, arguments string) string {
	trimmed := strings.TrimSpace(arguments)
	if approvalEmptyArguments(trimmed) {
		return ""
	}
	var values map[string]any
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&values); err != nil || len(values) == 0 {
		return approvalTruncate(trimmed, 240)
	}
	keys := approvalPreviewKeys(tool, values)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		value := approvalPreviewValue(values[key])
		if value == "" {
			continue
		}
		lines = append(lines, key+": "+value)
		if len(lines) == 3 {
			break
		}
	}
	if len(lines) == 0 {
		return approvalTruncate(trimmed, 240)
	}
	return strings.Join(lines, "\n")
}

// approvalPreviewKeys orders a tool's arguments for the preview: the tool
// family's priority keys first, then the remaining keys in a stable order.
func approvalPreviewKeys(tool string, values map[string]any) []string {
	seen := make(map[string]bool, len(values))
	keys := make([]string, 0, len(values))
	priority := approvalSummaryKeys[strings.ToLower(strings.TrimSpace(tool))]
	for _, key := range priority {
		if _, ok := values[key]; !ok || seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	rest := make([]string, 0, len(values))
	for key := range values {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

// approvalPreviewValue renders one argument value for the mono preview: a
// string keeps its first line, scalars print directly and composite values
// stay compact JSON.
func approvalPreviewValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return approvalTruncate(approvalFirstLine(typed), 200)
	case json.Number:
		return typed.String()
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return approvalTruncate(string(encoded), 200)
	}
}

// approvalFirstLine keeps a preview to one line.
func approvalFirstLine(text string) string {
	trimmed := strings.TrimSpace(text)
	if index := strings.IndexAny(trimmed, "\r\n"); index >= 0 {
		return strings.TrimSpace(trimmed[:index])
	}
	return trimmed
}

// approvalArgumentsSection renders the raw arguments as details: a divider, then
// either the "no arguments" fallback or the collapsed, bounded disclosure. The
// disclosure is the section's only label (the summary above already says what
// the call does), so the card never repeats "参数 / 显示原始参数".
func approvalArgumentsSection(c *ui.Context, t *design.Tokens, o ApprovalDialogOptions,
	labels ApprovalLabels, request contracts.ApprovalRequest, arguments string, argsOpen *bool) {
	ui.Divider(c)
	if approvalEmptyArguments(arguments) {
		design.Apply(ui.Text(c, T(o.Locale, "无参数", "No arguments")),
			t.TextMuted(design.TypeXS))
		return
	}
	ui.Collapsible(c.Key("approval.arguments."+request.ID),
		labels.ArgumentsLabel, argsOpen, func() {
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

// approvalEmptyArguments reports whether a request carries no meaningful
// arguments ({} / [] / null / empty).
func approvalEmptyArguments(arguments string) bool {
	return arguments == "" || arguments == "{}" || arguments == "[]" || arguments == "null"
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

// approvalToolIcon picks the header glyph for the tool being approved in the
// Modal presentation: destructive shell/file tools get the alert triangle,
// everything else the shield.
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
