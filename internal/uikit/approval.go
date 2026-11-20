package uikit

// The approval entry component (AGENT-SURFACES.md S4): the product shows one
// pending tool-call request before it runs and asks for the safety decision.
//
// Two presentations share one anatomy and one decision contract:
//   - Inline is the product path: a card in the session's banner slot, directly
//     above the composer (ApprovalDialogOptions.Inline). It is deliberately not
//     a dialog: the user keeps reading and typing while a call waits and the card
//     stays visible in the conversation flow.
//   - The native Modal is kept for compatibility (tests and hosts that still
//     want a blocking gate); Inline=false renders it as a dialog.
//
// Visual language (B-spec-v2 §3.3.2, "Precision Neutral"): the card is a raised
// surface with a 1 DIP BorderL3 edge and RadiusLG, and its one semantic mark is
// a 2 DIP Warning rail at x=0. There is no full-width warning banner and no
// warning-colored frame: a tinted block behind the whole card read as a sticky
// note (2026-11 audit VD-08), and in the dark theme a 22% warning wash over the
// raised surface collapsed into a muddy brown (VD-22). Warning now travels in
// exactly three small places — the rail, the StatusBadge and the one-line impact
// statement — so the card is unmistakably a decision without shouting.
//
// Safety contract (locked here, never re-composed by a caller):
//   - the footer is Deny (secondary) · Allow (the screen's only accent fill) ·
//     a scope menu; Allow's label and the scope sentence beside it follow the
//     selected scope (one call / this session), so the button never promises a
//     wider permission than it sends;
//   - NO action is the Default and none is auto-focused, so a stray Enter
//     (the user often presses it right after sending a prompt) decides
//     nothing: neither an accidental allow nor an accidental deny. Only the
//     explicit CmdOrCtrl+Enter chord allows, and only while this card is built;
//   - in the Inline path the session screen owns Escape (Escape resolves the
//     oldest pending request as deny, see ui/session), so the card is never
//     dismissed without an answer; the footer states that chord with key caps.
//
// The card draws only native controls (Row/Column, Text, Badge, Scroll,
// Collapsible, Button, PopoverMenuButton) styled with internal/design.

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

// approvalRailWidth is the card's semantic edge: 2 DIP at x=0, one of the three
// places the product allows a 2 DIP rule (B-spec-v2 §2: the selection bar, the
// focus ring and an approval/plan card's status rail). It reads as the card's
// edge, never as a second border, so the card itself keeps its 1 DIP BorderL3.
const approvalRailWidth float32 = design.Space1 / 2

// The two scopes the primary action can send. "Always allow in this session"
// is the widest permission the backend persists (the session's allowed-tool
// set); there is no global allowlist to write, so the card never offers one.
const (
	approvalScopeOnce    = "once"
	approvalScopeSession = "session"
)

// approvalArgumentsMaxHeight is the fixed height of the scrollable raw
// arguments block (140 DIP, the historical dialog height kept on the 4 DIP
// token grid).
const approvalArgumentsMaxHeight float32 = design.Space8*4 + design.Space3

// approvalInlineBodyMaxHeight bounds the inline card's middle section (target,
// change, raw arguments): the header and the decision footer stay fixed while a
// long payload scrolls inside, so the card can never push the composer out of
// the window (2026-11 audit UX-02). The budget fits the target block, the diff's
// readable floor and the collapsed arguments in one view — the room the old
// warning banner took went into the content the user actually judges.
const approvalInlineBodyMaxHeight float32 = design.Space8 * 8

// approvalDiffMinHeight floors the inline card's change block so the diff — the
// thing being judged — can never be squeezed into a half-line by the summary
// above it (2026-11 UI review P1). 128 DIP on the 4-DIP token grid keeps a full
// first hunk readable; the body's total budget (approvalInlineBodyMaxHeight) is
// unchanged, and the body scroll carries the rest (UX-02).
const approvalDiffMinHeight float32 = design.Space8 * 4

// ApprovalLabels is the configurable copy of one approval card. Empty fields
// fall back to the localized product defaults, so a host that only overrides
// one label keeps the rest. The caller localizes the fields it passes (the
// approval module keeps its assembly-config labels verbatim and localizes the
// untouched defaults); the fallbacks use Locale.
type ApprovalLabels struct {
	// Title is the card heading, drawn as the header's status badge (default
	// "需要审批"/"Approval required").
	Title string
	// ToolLabel labels the tool row of the Modal presentation (default
	// "工具"/"Tool"); the inline card names the tool in its header.
	ToolLabel string
	// ArgumentsLabel labels the raw arguments disclosure (default
	// "原始参数"/"Raw arguments"). It is the one label of the disclosure: the
	// target block above already names what the call does.
	ArgumentsLabel string
	// ChangeLabel labels the "what will change" block of a file write/edit
	// (default "将改动"/"Changes"). The block sits between the target block and
	// the raw arguments: the diff is the thing being judged.
	ChangeLabel string
	// AllowLabel is the primary action while the scope is one call (default
	// "允许一次"/"Allow once").
	AllowLabel string
	// AllowAlwaysLabel is the primary action — and the scope menu's choice —
	// while the scope is this session (default "本会话内始终允许"/"Always allow
	// in this session").
	AllowAlwaysLabel string
	// DenyLabel rejects the call (default "拒绝"/"Deny").
	DenyLabel string
	// ScopeOnceLabel and ScopeSessionLabel name the two scopes in the sentence
	// beside the primary action (defaults "仅此一次"/"One call" and
	// "本会话"/"This session").
	ScopeOnceLabel    string
	ScopeSessionLabel string
	// MoreLabel names the scope menu button (default "更多"/"More").
	MoreLabel string
	// RulesLabel labels the in-card rules disclosure, the menu's "what does
	// always-allow mean and where is it revoked" entry (default
	// "查看规则"/"Show rules").
	RulesLabel string
}

// resolved fills every empty field with its localized default.
func (l ApprovalLabels) resolved(locale contracts.LocaleService) ApprovalLabels {
	return ApprovalLabels{
		Title:             approvalFirstNonEmpty(l.Title, T(locale, "需要审批", "Approval required")),
		ToolLabel:         approvalFirstNonEmpty(l.ToolLabel, T(locale, "工具", "Tool")),
		ArgumentsLabel:    approvalFirstNonEmpty(l.ArgumentsLabel, T(locale, "原始参数", "Raw arguments")),
		ChangeLabel:       approvalFirstNonEmpty(l.ChangeLabel, T(locale, "将改动", "Changes")),
		AllowLabel:        approvalFirstNonEmpty(l.AllowLabel, T(locale, "允许一次", "Allow once")),
		AllowAlwaysLabel:  approvalFirstNonEmpty(l.AllowAlwaysLabel, T(locale, "本会话内始终允许", "Always allow in this session")),
		DenyLabel:         approvalFirstNonEmpty(l.DenyLabel, T(locale, "拒绝", "Deny")),
		ScopeOnceLabel:    approvalFirstNonEmpty(l.ScopeOnceLabel, T(locale, "仅此一次", "One call")),
		ScopeSessionLabel: approvalFirstNonEmpty(l.ScopeSessionLabel, T(locale, "本会话", "This session")),
		MoreLabel:         approvalFirstNonEmpty(l.MoreLabel, T(locale, "更多", "More")),
		RulesLabel:        approvalFirstNonEmpty(l.RulesLabel, T(locale, "查看规则", "Show rules")),
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
	// Origin is the localized provenance line of the requesting conversation
	// ("来自会话：修复登录 · 自动化"); empty hides the row (backends without
	// session provenance keep the previous card).
	Origin string
	// OpenSessionLabel names the action that opens the requesting conversation;
	// OpenSession runs it. OpenSession nil hides the action (the request belongs
	// to the conversation on screen, or the backend cannot switch sessions).
	OpenSessionLabel string
	OpenSession      func()
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

// approvalInlineCard is the product's inline approval card (B-spec-v2 §3.3.2):
// a raised surface whose one semantic mark is the 2 DIP warning rail at its
// left edge. It is built in the session's banner slot, above the composer, and
// reads top-down in the order the decision needs: what is asking (badge + tool),
// what it will do (impact line, target block), what it will change (diff), the
// raw payload, then the decision.
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

	root := design.Apply(ui.Row(c.Key("uikit.approval/inline."+request.ID)).FillWidth().
		AlignItems(ui.Stretch), func(el ui.Element) ui.Element {
		// The card is a neutral raised surface with the interactive edge
		// (BorderL3) and the card radius; the warning never fills it. In the
		// dark theme a tinted card body collapsed into mud (VD-22), and a
		// full-width tint read as a sticky note (VD-08).
		return el.Background(t.BgRaised).Border(1, t.BorderL3).
			Radius(design.RadiusLG).Clip()
	})
	root.Role(ui.RoleRegion).Label(labels.Title + " · " + toolName)
	// The card's own visual state, keyed by the request through root: a new
	// request starts collapsed, with the narrowest scope, and with the rules
	// explanation hidden.
	argsOpen := ui.Local(root, "argsOpen", func() bool { return false })
	bodyScroll := ui.Local(root, "bodyScroll", func() ui.ScrollState { return ui.ScrollState{} })
	scope := ui.Local(root, "scope", func() string { return approvalScopeOnce })
	rulesOpen := ui.Local(root, "rulesOpen", func() bool { return false })
	root.Children(func() {
		// The status rail: 2 DIP of Warning at x=0, square, full height. It is
		// the card's only strong warning mark beside the header badge.
		design.Apply(ui.Box(c), func(el ui.Element) ui.Element {
			return el.Width(approvalRailWidth).Shrink(0).Background(t.Warning)
		})
		ui.Column(c).FillWidth().Grow(1).Padding(design.Space3).Gap(design.Space2).
			Children(func() {
				approvalInlineHeader(c, t, o, labels, toolName)
				// The impact statement: what approving actually means. It sits
				// outside the scrolling body so it can never scroll away.
				approvalImpactLine(c, t, o.Locale, approvalImpactWarning)
				// The middle section scrolls inside a fixed budget (UX-02): a long
				// target/diff/argument payload can grow the card only up to the
				// budget, so the header above and the decision footer below stay
				// reachable at the supported minimum window.
				ui.Scroll(c.Key("uikit.approval/inline.body." + request.ID)).FillWidth().
					MaxHeight(approvalInlineBodyMaxHeight).TrackScroll(bodyScroll).Children(func() {
					ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
						approvalSummarySection(c, t, o, summary, arguments)
						// The change itself, before the raw payload: a file write/edit is
						// judged by what it will do to the file, not by its JSON. The diff
						// body keeps a readable floor (approvalDiffMinHeight), and the
						// middle section starts at its top: the reader sees the target,
						// then the first hunk, and scrolls the rest. Scrolling the body
						// to the diff on the first frame skipped the target block above it.
						approvalChangeSection(c, t, o, labels, arguments, approvalDiffMinHeight)
						approvalArgumentsSection(c, t, o, labels, request, arguments, argsOpen)
						approvalRulesSection(c, t, o, labels, toolName, *rulesOpen)
					})
				})
				approvalInlineFooter(c, t, o, labels, scope, rulesOpen, resolve)
			})
	})
}

// approvalInlineHeader is the card's first line: the status badge that names
// the gate, the tool's friendly name, and — right-aligned in the metadata step —
// the queue depth and where the request comes from. The raw protocol id sits
// beside the friendly name in the mono face instead of in a tooltip: this
// surface owns Escape, and a tooltip is an overlay that takes the key (A4-I-01).
func approvalInlineHeader(c *ui.Context, t *design.Tokens, o ApprovalDialogOptions,
	labels ApprovalLabels, toolName string) {
	request := o.Request
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		StatusBadge(c, t, StatusNeedsAction, labels.Title)
		design.Apply(ui.Text(c, toolName), t.Font(design.TypeS)).
			TextColor(t.LabelPrimary).NoWrap()
		if raw := strings.TrimSpace(request.Tool); raw != "" && raw != toolName {
			// The protocol id stays visible: it is what the backend reports back
			// and what the allowlist remembers, so a reviewer can trace the call.
			design.Apply(ui.Text(c, raw), t.MonoText(design.TypeXXXS)).
				TextColor(t.LabelTertiary).NoWrap().MaxWidth(design.DialogWidthXS / 2).
				Ellipsis("…")
		}
		ui.Spacer(c).Grow(1)
		if o.Pending > 1 {
			// The rest wait behind this one; the header counts them instead of
			// rendering more cards.
			design.Apply(ui.Text(c, fmt.Sprintf(T(o.Locale,
				"还有 %d 条", "%d more"), o.Pending-1)), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).NoWrap()
		}
		approvalOriginInline(c, t, o)
	})
}

// approvalOriginInline is the header's provenance tail (FR-29): the requesting
// conversation and its source in the metadata step, plus the action that opens
// that conversation when the request belongs to another one. A request without
// provenance adds nothing.
func approvalOriginInline(c *ui.Context, t *design.Tokens, o ApprovalDialogOptions) {
	origin := strings.TrimSpace(o.Origin)
	if origin == "" {
		return
	}
	design.Apply(ui.Text(c, origin), t.Font(design.TypeXXS)).
		TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…").MaxWidth(design.DialogWidthXS)
	if o.OpenSession == nil {
		return
	}
	label := strings.TrimSpace(o.OpenSessionLabel)
	if label == "" {
		label = T(o.Locale, "打开该会话", "Open conversation")
	}
	// A quiet text action, not a filled button: the card already has exactly one
	// accent fill and this is a way out of the card, not a decision on it.
	link := design.Apply(ui.Button(c, label), t.Font(design.TypeXXS)).
		TextColor(t.AccentText).Height(design.ControlCompactHeight).Shrink(0)
	if link.Clicked() {
		o.OpenSession()
	}
}

// approvalImpactTone is how the one-line impact statement is colored.
type approvalImpactTone int

const (
	// approvalImpactWarning states a consequence of the decision (the approval
	// card's line): WarningText, the AA foreground of the warning role.
	approvalImpactWarning approvalImpactTone = iota
	// approvalImpactQuiet is the neutral metadata line of a card that has no
	// consequence to warn about.
	approvalImpactQuiet
)

// approvalImpactLine renders the copy that keeps the gate honest: approval only
// lets the call through the gate; the sandbox policy still runs it and may deny
// it, so the copy must not promise execution.
func approvalImpactLine(c *ui.Context, t *design.Tokens, locale contracts.LocaleService,
	tone approvalImpactTone) {
	text := T(locale,
		"允许后将在沙箱策略限制内执行；策略仍可能拒绝该调用。",
		"It runs within the sandbox policy after approval; the policy may still reject the call.")
	el := design.Apply(ui.Text(c, text), t.Font(design.TypeXXS))
	if tone == approvalImpactQuiet {
		el = el.TextColor(t.LabelSecondary)
	} else {
		el = el.TextColor(t.WarningText)
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
		// The badge shows the friendly localized name; the raw protocol id is a
		// visible mono note beside it, never a tooltip (a tooltip is an overlay
		// that takes Escape from this surface, A4-I-01).
		ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, labels.ToolLabel), t.TextMuted(design.TypeXS))
			design.Apply(ui.Badge(c, toolName), t.Badge())
			if raw := strings.TrimSpace(request.Tool); raw != "" && raw != toolName {
				design.Apply(ui.Text(c, raw), t.MonoText(design.TypeXXXS)).
					TextColor(t.LabelTertiary).NoWrap()
			}
		})
		approvalOriginRow(c, t, o)
		approvalImpactLine(c, t, o.Locale, approvalImpactWarning)
		approvalSummarySection(c, t, o, summary, arguments)
		// The change itself, before the raw payload: a file write/edit is
		// judged by what it will do to the file, not by its JSON. The Modal keeps
		// its natural diff height (no inline floor).
		approvalChangeSection(c, t, o, labels, arguments, 0)
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
		// quiet copy, including when it expires.
		design.Apply(ui.Text(c, T(o.Locale,
			"「本会话内始终允许」在本会话内不再询问该工具，会话结束后失效。",
			"\"Always allow in this session\" stops this tool's prompts for this conversation; it expires when the conversation ends.")),
			t.TextMuted(design.TypeXXS)).MaxLines(2)
		// The key semantics are visible: Esc denies explicitly, and there is no
		// default action, so a stray Enter decides nothing.
		design.Apply(ui.Text(c, T(o.Locale,
			"Esc = 拒绝本次调用 · 点击或 Tab 选择操作（无默认操作）",
			"Esc = deny this call · click or Tab to choose an action (no default action)")),
			t.TextMuted(design.TypeXXS))
	})
}

// approvalOriginRow names where the request comes from (FR-29): the requesting
// conversation's title and its provenance, plus the action that opens that
// conversation when the request belongs to another one. A request without
// provenance keeps the previous card (no row).
func approvalOriginRow(c *ui.Context, t *design.Tokens, o ApprovalDialogOptions) {
	origin := strings.TrimSpace(o.Origin)
	if origin == "" {
		return
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		ui.Icon(c, icons.Message()).Size(design.Space3, design.Space3).
			TextColor(t.LabelTertiary)
		design.Apply(ui.Text(c, origin), t.Font(design.TypeXXS)).
			TextColor(t.LabelSecondary).Grow(1).MaxLines(1).Ellipsis("…")
		if o.OpenSession == nil {
			return
		}
		label := strings.TrimSpace(o.OpenSessionLabel)
		if label == "" {
			label = T(o.Locale, "打开该会话", "Open conversation")
		}
		button := design.Apply(ui.Button(c, label), t.SecondaryButton()).
			Height(design.RowCompactHeight).Shrink(0)
		if button.Clicked() {
			o.OpenSession()
		}
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

// approvalInlineFooter is the card's decision bar: the key chords on the left,
// then the scope sentence, Deny, the one accent fill and the scope menu. The
// primary action's label follows the selected scope, so the button never sends a
// wider permission than its own text promises; the accent budget stays at one
// fill per screen because the scope choice lives in a menu, not in a second
// button.
func approvalInlineFooter(c *ui.Context, t *design.Tokens, o ApprovalDialogOptions,
	labels ApprovalLabels, scope *string, rulesOpen *bool,
	resolve func(contracts.ApprovalDecision)) {
	session := *scope == approvalScopeSession
	primaryLabel, scopeLabel := labels.AllowLabel, labels.ScopeOnceLabel
	if session {
		primaryLabel, scopeLabel = labels.AllowAlwaysLabel, labels.ScopeSessionLabel
	}
	primary := func() {
		if session {
			resolve(contracts.ApprovalAllowAlways)
			return
		}
		resolve(contracts.ApprovalAllow)
	}
	// CmdOrCtrl+Enter is the explicit allow chord, stated in the footer beside
	// the buttons. Plain Enter stays unbound: the user presses it right after
	// sending a prompt and must never answer a gate by accident. The chord works
	// inside the card's own focus subtree rather than window-wide, so it cannot
	// fire while the user is typing in the composer with the same chord on their
	// mind.
	c.OnShortcut(ui.Cmd, ui.KeyEnter, primary)
	// One scope list, two ways in: the labelled scope disclosure and the "⋯"
	// button both open it, so a reader who does not recognise "⋯" still finds the
	// session-scoped allow.
	scopeMenu := func(m *ui.Menu) {
		onceLabel := T(o.Locale, "仅此一次（只放行本次调用）", "One call (this call only)")
		if item := m.Item(onceLabel); item.Radio(*scope == approvalScopeOnce).Chosen() {
			*scope = approvalScopeOnce
			c.Invalidate()
		}
		sessionLabel := T(o.Locale, "本会话内始终允许该工具",
			"Always allow this tool in this session")
		if item := m.Item(sessionLabel); item.Radio(*scope == approvalScopeSession).Chosen() {
			*scope = approvalScopeSession
			c.Invalidate()
		}
		m.Separator()
		if item := m.Item(labels.RulesLabel); item.Checked(*rulesOpen).Chosen() {
			*rulesOpen = !*rulesOpen
			c.Invalidate()
		}
	}
	// The bar wraps instead of overflowing: at the narrowest supported window the
	// key hints take a second line and every action stays on screen (an action
	// pushed outside the window would be unreachable).
	ui.Row(c).FillWidth().Wrap().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		// The hint words differ from the buttons beside them on purpose: a
		// duplicate label would give the same frame two elements with one name,
		// and assistive technology (or a test) would find the wrong one.
		approvalKbdHint(c, t, T(o.Locale, "拒绝本次", "denies this call"), FormatShortcut("Esc"))
		approvalKbdHint(c, t, T(o.Locale, "按所选范围允许", "allows at the chosen scope"),
			FormatShortcut("CmdOrCtrl+Enter"))
		ui.Spacer(c).Grow(1)
		// The scope is a labelled sentence, not a bare word: "范围：仅此一次" says
		// what the word is, so the reader knows the primary action's reach. The
		// list itself is the "⋯" menu beside the actions (one accent fill, one
		// menu); a second button-shaped trigger here competed with Deny.
		design.Apply(ui.Text(c, T(o.Locale, "范围：", "Scope: ")+scopeLabel),
			t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).NoWrap()
		deny := design.Apply(ui.Button(c, labels.DenyLabel), t.SecondaryButton())
		if deny.Clicked() {
			resolve(contracts.ApprovalDeny)
		}
		allow := design.Apply(ui.PrimaryButton(c, primaryLabel), t.PrimaryButton())
		if allow.Clicked() {
			primary()
		}
		more := IconButton(c, IconButtonOptions{
			Key:   "uikit.approval/inline.more." + o.Request.ID,
			Label: labels.MoreLabel,
			Icon:  icons.More(),
			Size:  design.IconSizeS,
		})
		if more.Valid() {
			more.PopoverMenuButton(scopeMenu)
		}
	})
}

// approvalKbdHint draws one visible shortcut hint: the key cap and the action it
// performs. The card states its keys inline instead of in a tooltip — a tooltip
// is an overlay that takes Escape from the surface that owns it (A4-I-01).
func approvalKbdHint(c *ui.Context, t *design.Tokens, action, keys string) {
	ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(design.Space1).Children(func() {
		Kbd(c, t, keys)
		design.Apply(ui.Text(c, action), t.Font(design.TypeXXS)).
			TextColor(t.LabelTertiary).NoWrap()
	})
}

// approvalRulesSection is the "what does always-allow mean" explanation, opened
// from the scope menu: the sentence that used to sit in the card body as an
// always-visible footnote. It names the widest scope, when it expires and where
// it is revoked, so the menu's widest choice is never a mystery.
func approvalRulesSection(c *ui.Context, t *design.Tokens, o ApprovalDialogOptions,
	labels ApprovalLabels, toolName string, open bool) {
	if !open {
		return
	}
	rules := []string{
		fmt.Sprintf(T(o.Locale,
			"「%s」在本会话内不再询问 %s，会话结束后失效。",
			"\"%s\" stops asking about %s in this conversation; it expires when the conversation ends."),
			labels.AllowAlwaysLabel, toolName),
		T(o.Locale,
			"允许只放行这一道门：沙箱与权限策略仍然生效，并可能拒绝该调用。",
			"Approval only opens this gate: the sandbox and permission policy still run and may reject the call."),
	}
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		for _, rule := range rules {
			design.Apply(ui.Text(c, rule), t.Font(design.TypeXXS)).
				TextColor(t.LabelSecondary)
		}
	})
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
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		// The semantic label names what the block holds (a command, a path, a
		// URL), so the mono block is never an unlabelled blob of text.
		design.Apply(ui.Text(c, approvalTargetLabel(o.Locale, o.Request.Tool)),
			t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary)
		design.Apply(ui.Row(c).FillWidth(), func(el ui.Element) ui.Element {
			return el.Background(t.BgStrong).Border(1, t.BorderL1).
				Radius(design.RadiusSM).Padding(design.Space2, design.Space3)
		}).Children(func() {
			// The block is drawn at the body step in the mono face and clipped by
			// the layout, not by rune counting: the full command stays available
			// to selection and to assistive technology (A1 matrix: no data-side
			// truncation on a gate's payload).
			design.Apply(ui.Text(c, text), t.MonoText(design.TypeXS)).
				TextColor(t.LabelPrimary).MaxLines(4).Ellipsis("…").Selectable()
		})
	})
}

// approvalSummaryKeys is the readable-key priority of one tool family: the
// fields a reviewer needs to decide are rarely the whole argument object.
// Anything not listed falls back to the object's own key order.
var approvalSummaryKeys = map[string][]string{
	"bash":                   {"command"},
	"pwsh":                   {"command"},
	"terminal_send":          {"command", "input", "text"},
	"job_start":              {"command"},
	"read_file":              {"path", "file_path"},
	"write_file":             {"path", "file_path"},
	"edit_file":              {"path", "file_path"},
	"str_replace_editor":     {"path", "file_path", "command"},
	"list_dir":               {"path"},
	"glob":                   {"pattern", "path"},
	"grep":                   {"pattern", "path"},
	"read_image":             {"path", "file_path"},
	"apply_patch":            {"patch", "path"},
	"search":                 {"query", "mode", "path"},
	"request_sandbox_access": {"path", "reason"},
	"web_fetch":              {"url"},
	"web_search":             {"query"},
	"team_task_create":       {"subject", "owner", "description"},
	"team_task_update":       {"id", "subject", "status", "owner"},
	"todo":                   {"items", "subject", "status"},
	"todo_write":             {"items", "subject", "status"},
	"subagent":               {"prompt", "description"},
	"workflow":               {"steps", "tasks", "max_parallel"},
	"ralph":                  {"prompt", "max_rounds", "name"},
	"skill":                  {"name", "arguments"},
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

// approvalChangeSection renders the "what will change" block of a file
// write/edit between the summary and the raw arguments: the path, the +N −M
// line counts and the official unified diff of the content the arguments
// already carry. It never reads the file: an approval request must decide on
// what the call will write, and the arguments hold exactly that. A call whose
// arguments cannot be turned into an honest diff shows a bounded mono preview
// with the reason instead (uikit.DiffPreview), never a blank or fake diff.
// minHeight floors the diff body (the inline card passes approvalDiffMinHeight
// so the summary can never squeeze the diff into a half-line; the Modal passes
// 0 and keeps its natural height). It returns the change block element so the
// inline card can reveal the first hunk into its bounded body scroll.
func approvalChangeSection(c *ui.Context, t *design.Tokens, o ApprovalDialogOptions,
	labels ApprovalLabels, arguments string, minHeight float32) ui.Element {
	change := approvalChangeFor(o.Locale, o.Request.Tool, arguments)
	if !change.show {
		return ui.Element{}
	}
	design.Apply(ui.Text(c, labels.ChangeLabel), t.TextMuted(design.TypeXS))
	return DiffPreview(c, DiffPreviewOptions{
		Key:          "approval.change." + o.Request.ID,
		Path:         change.path,
		Old:          change.old,
		New:          change.new,
		FallbackText: change.fallback,
		FallbackNote: change.note,
		MinHeight:    minHeight,
		Locale:       o.Locale,
	})
}

// approvalChange is the file change a write/edit approval will make, as far as
// the call's arguments describe it: the path and the texts the official diff
// compares, or the bounded fallback copy when no diff can be built.
type approvalChange struct {
	// show reports that the card has a change block to draw. An unknown tool,
	// a read-only command (str_replace_editor view), or arguments that name no
	// write at all leave it false.
	show bool
	// path is the file the call writes (path/file_path/file).
	path string
	// old and new are the compared texts: write_file's content is the whole
	// new file (old empty), edit_file's old_string/new_string are the replaced
	// region.
	old, new string
	// fallback is a bounded mono preview for a change no diff can show, and
	// note the honest one-line reason above it.
	fallback, note string
}

// approvalChangeTools are the tools whose arguments describe a file write the
// approval card can preview. Unknown tools (including a host-registered
// apply_patch with its own patch format) deliberately get no diff: the card
// shows only what it can read honestly.
var approvalChangeTools = map[string]bool{
	"write_file":         true,
	"edit_file":          true,
	"str_replace_editor": true,
}

// approvalChange resolves the change plan of one write/edit call. Arguments are
// the raw JSON the backend captured (a JSON object, or a JSON string holding
// one); empty and unparseable arguments take the fallback path so the card
// explains itself instead of showing nothing.
func approvalChangeFor(locale contracts.LocaleService, tool, arguments string) approvalChange {
	key := strings.ToLower(strings.TrimSpace(tool))
	if !approvalChangeTools[key] {
		return approvalChange{}
	}
	trimmed := strings.TrimSpace(arguments)
	if approvalEmptyArguments(trimmed) {
		return approvalChange{}
	}
	values, ok := approvalArgumentObject(trimmed)
	if !ok {
		return approvalChange{
			show:     true,
			fallback: approvalTruncate(trimmed, approvalMaxArgumentsRunes),
			note: T(locale, "无法解析参数，无法生成 diff。",
				"The arguments cannot be parsed, so no diff can be shown."),
		}
	}
	path := approvalStringArgument(values, "path", "file_path", "file")
	switch key {
	case "write_file":
		content, contentOK := approvalFirstStringArgument(values, "content")
		if !contentOK {
			return approvalChange{}
		}
		return approvalChange{show: true, path: path, new: content}
	case "edit_file":
		return approvalEditChange(path, values, []string{"old_string"}, []string{"new_string"})
	case "str_replace_editor":
		return approvalEditorChange(locale, path, values)
	}
	return approvalChange{}
}

// approvalEditChange resolves the old/new pair of an edit. A payload missing
// either side is no readable edit (the tool itself rejects it), so the card
// omits the block rather than guessing.
func approvalEditChange(path string, values map[string]any, oldKeys, newKeys []string) approvalChange {
	oldText, oldOK := approvalFirstStringArgument(values, oldKeys...)
	newText, newOK := approvalFirstStringArgument(values, newKeys...)
	if !oldOK || !newOK {
		return approvalChange{}
	}
	return approvalChange{show: true, path: path, old: oldText, new: newText}
}

// approvalEditorChange resolves str_replace_editor's command: create writes a
// whole file, str_replace replaces an exact snippet, insert's position lives in
// the file so no diff can be built from the arguments alone, and view changes
// nothing.
func approvalEditorChange(locale contracts.LocaleService, path string, values map[string]any) approvalChange {
	command := strings.ToLower(strings.TrimSpace(approvalStringArgument(values, "command")))
	switch command {
	case "view":
		return approvalChange{}
	case "create":
		text, ok := approvalFirstStringArgument(values, "file_text")
		if !ok {
			return approvalChange{}
		}
		return approvalChange{show: true, path: path, new: text}
	case "insert":
		text, ok := approvalFirstStringArgument(values, "new_str")
		if !ok {
			return approvalChange{}
		}
		return approvalChange{
			show:     true,
			path:     path,
			fallback: text,
			note: T(locale,
				"插入位置取决于文件现有内容，无法仅凭参数生成 diff；以下是将插入的文本。",
				"The insertion point is in the file, so the arguments alone cannot make a diff; showing the text to insert."),
		}
	case "str_replace", "":
		// An empty command is treated as a replacement: the arguments carry the
		// old/new pair the tool's str_replace uses.
		return approvalEditChange(path, values,
			[]string{"old_str", "old_string"}, []string{"new_str", "new_string"})
	default:
		return approvalChange{}
	}
}

// approvalArgumentObject decodes the argument JSON into an object. The contract
// stores the raw arguments as a string, but a host that handed the card a
// JSON-encoded string is read through once more; anything that is not an object
// reports false so the caller can fall back honestly.
func approvalArgumentObject(arguments string) (map[string]any, bool) {
	decode := func(text string) (any, bool) {
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		return value, true
	}
	value, ok := decode(arguments)
	if !ok {
		return nil, false
	}
	if text, isString := value.(string); isString {
		value, ok = decode(text)
		if !ok {
			return nil, false
		}
	}
	object, isObject := value.(map[string]any)
	if !isObject || len(object) == 0 {
		return nil, false
	}
	return object, true
}

// approvalStringArgument returns the first string value under keys, or "".
func approvalStringArgument(values map[string]any, keys ...string) string {
	text, _ := approvalFirstStringArgument(values, keys...)
	return text
}

// approvalFirstStringArgument returns the first string value under keys and
// reports whether one was found.
func approvalFirstStringArgument(values map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		raw, present := values[key]
		if !present {
			continue
		}
		text, isString := raw.(string)
		if !isString {
			continue
		}
		return text, true
	}
	return "", false
}

// approvalArgumentsSection renders the raw arguments as the card's last
// disclosure: the label sits on the header row, and the body is the bounded mono
// payload. It is the section's only label — the target block above already says
// what the call does — so the card never repeats "参数 / 显示原始参数". The
// disclosure carries no divider of its own: the panel's 1 DIP BorderL1 edge is
// the separation (B-spec-v2 §2: a border is drawn once).
func approvalArgumentsSection(c *ui.Context, t *design.Tokens, o ApprovalDialogOptions,
	labels ApprovalLabels, request contracts.ApprovalRequest, arguments string, argsOpen *bool) {
	if approvalEmptyArguments(arguments) {
		design.Apply(ui.Text(c, T(o.Locale, "无参数", "No arguments")),
			t.TextMuted(design.TypeXS))
		return
	}
	ui.Collapsible(c.Key("approval.arguments."+request.ID),
		labels.ArgumentsLabel, argsOpen, func() {
			scroll := ui.Scroll(c).Height(approvalArgumentsMaxHeight).FillWidth()
			design.Apply(scroll, func(el ui.Element) ui.Element {
				return el.Background(t.BgStrong).Border(1, t.BorderL1).
					Radius(design.RadiusSM).Padding(design.Space2)
			}).Children(func() {
				design.Apply(ui.Text(c, approvalTruncate(arguments, approvalMaxArgumentsRunes)),
					t.MonoText(design.TypeXS)).TextColor(t.LabelSecondary).Selectable()
			})
		})
}

// approvalEmptyArguments reports whether a request carries no meaningful
// arguments ({} / [] / null / empty).
func approvalEmptyArguments(arguments string) bool {
	return arguments == "" || arguments == "{}" || arguments == "[]" || arguments == "null"
}

// approvalTargetLabel names the requested call's target block by what the block
// actually holds. A command line, a file path and a URL are read differently, so
// the block says which one it is instead of labelling everything 摘要; a tool
// with no such shape (a team task, a plan) keeps the neutral 摘要.
func approvalTargetLabel(locale contracts.LocaleService, tool string) string {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "bash", "pwsh", "terminal_send", "job_start", "job_output", "terminal_read":
		return T(locale, "命令", "Command")
	case "read_file", "write_file", "edit_file", "str_replace_editor",
		"list_dir", "read_image", "enter_worktree", "list_worktrees":
		return T(locale, "路径", "Path")
	case "web_fetch":
		return T(locale, "域名", "URL")
	case "web_search", "grep", "glob":
		return T(locale, "查询", "Query")
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
