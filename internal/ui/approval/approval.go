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
		AllowAlwaysLabel: firstNonEmpty(c.AllowAlwaysLabel, "总是允许"),
		DenyLabel:        firstNonEmpty(c.DenyLabel, "拒绝"),
	}
}

// state is the module instance's dialog state. It lives in the activation
// closure (one per loaded module) and is touched only on the UI thread.
type state struct {
	// open is the native Modal's open flag; Escape/backdrop clicks set it to
	// false and are treated as deny.
	open bool
	// shownID is the request currently displayed ("" when none).
	shownID string
	// resolvedID is the request just answered; its row may still be in
	// Pending for a frame or two while the backend settles, and the dialog
	// must not reopen for it.
	resolvedID string
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
		st := &state{}

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
// tool badge, the summary, a monospaced arguments block and the three decision
// buttons. All controls are mygo natives styled with internal/design tokens.
func requestPanel(c *ui.Context, runtime contracts.UIRuntime, approvals contracts.Approvals, cfg Config, st *state, request contracts.ApprovalRequest) {
	t := design.Of(c)
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
			design.Apply(ui.Text(c, cfg.Title), t.Font(design.TypeL)).FontWeight(600).
				TextColor(t.LabelPrimary)
			ui.Spacer(c).Grow(1)
			design.Apply(ui.Text(c, request.Tool), t.Font(design.TypeXS)).TextColor(t.LabelSecondary)
		})
		ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, cfg.ToolLabel), t.TextMuted(design.TypeXS))
			design.Apply(ui.Badge(c, request.Tool), t.Badge())
		})
		if summary := strings.TrimSpace(request.Summary); summary != "" {
			design.Apply(ui.Text(c, summary), t.Font(design.TypeBody)).TextColor(t.LabelPrimary)
		}
		if arguments := strings.TrimSpace(request.Arguments); arguments != "" {
			ui.Divider(c)
			design.Apply(ui.Text(c, cfg.ArgumentsLabel), t.TextMuted(design.TypeXS))
			scroll := ui.Scroll(c).Height(140).FillWidth()
			design.Apply(scroll, func(el ui.Element) ui.Element {
				return el.Background(t.BgBase).Border(1, t.BorderL2).
					Radius(design.RadiusSM).Padding(design.Space2)
			}).Children(func() {
				design.Apply(ui.Text(c, truncate(arguments, maxArgumentsRunes)),
					t.TextMuted(design.TypeXS), t.MonoFamily())
			})
		}
		ui.Divider(c)
		ui.Row(c).FillWidth().Justify(ui.End).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			if design.Apply(ui.PrimaryButton(c, cfg.AllowLabel), t.PrimaryButton()).Clicked() {
				resolve(runtime, approvals, st, contracts.ApprovalAllow)
			}
			if design.Apply(ui.Button(c, cfg.AllowAlwaysLabel), secondaryButtonStyle(t)).Clicked() {
				resolve(runtime, approvals, st, contracts.ApprovalAllowAlways)
			}
			deny := design.Apply(ui.Button(c, cfg.DenyLabel), secondaryButtonStyle(t))
			deny.TextColor(t.DangerText)
			if deny.Clicked() {
				resolve(runtime, approvals, st, contracts.ApprovalDeny)
			}
		})
	})
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

// secondaryButtonStyle is the brand's secondary dialog button.
func secondaryButtonStyle(t *design.Tokens) design.Style {
	return func(el ui.Element) ui.Element {
		return el.Height(36).PaddingX(design.Space3).Radius(design.RadiusMD).
			Background(t.BgRaised).Border(1, t.BorderL2).TextColor(t.LabelPrimary)
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
