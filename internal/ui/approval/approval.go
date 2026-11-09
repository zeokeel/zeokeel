// Package approval contributes the pending tool-call approval dialog of the
// product: one native mygo Modal (inside the host's overlay.dialogs slot) for
// the oldest request the approvals service is waiting on.
//
// It is a kernel UI module like ui/sidebar: activation requires the host UI
// runtime and the approvals service, subscribes to pending-set changes (the
// callback hops to the UI thread with Post/Invalidate), and registers one
// SlotItem whose unregister function is owned by a kernel effect. The dialog
// itself is the uikit.ApprovalDialog entry component (AGENT-SURFACES.md S4):
// the module keeps the queue/data state, resolves requests and exits; it draws
// no widget and re-composes no footer.
//
// Resolution: the uikit component's three buttons resolve the request as
// allow / allow-always / deny. Pressing Escape or clicking the backdrop
// dismisses the native Modal, which the component treats as deny (the agent
// reports a tool error) so a pending call can never be left waiting on a hidden
// dialog.
package approval

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// ModuleID is the compile-time module identity.
const ModuleID = "zeokeel-ui/approval"

// SlotID identifies this module's contribution to overlay.dialogs.
const SlotID = "approval.dialog"

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
	// AllowAlwaysLabel permits this tool for the rest of the session (default
	// "本会话内始终允许"; the choice can be revoked from the session menu).
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
		AllowAlwaysLabel: firstNonEmpty(c.AllowAlwaysLabel, "本会话内始终允许"),
		DenyLabel:        firstNonEmpty(c.DenyLabel, "拒绝"),
	}
}

// state is the module instance's dialog queue state. It lives in the activation
// closure (one per loaded module) and is touched only on the UI thread.
type state struct {
	// locale localizes the dialog copy per frame (zh is the default).
	locale contracts.LocaleService
	// open is the native Modal's open flag; Escape/backdrop clicks set it to
	// false and are treated as deny by the uikit component.
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

// buildDialog shows the oldest pending request through the uikit approval
// entry component, or nothing while no request is waiting. The module keeps the
// FIFO and settle state; the component owns the dialog's anatomy, its footer
// order and its no-default/Escape-denies safety semantics.
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
	uikit.ApprovalDialog(c, uikit.ApprovalDialogOptions{
		Request: request,
		Pending: len(pending),
		Labels:  st.labels(cfg),
		Open:    &st.open,
		Locale:  st.locale,
		OnResolve: func(decision contracts.ApprovalDecision) {
			resolve(runtime, approvals, st, decision)
		},
	})
}

// labels builds the localizable copy of the dialog: an assembly-configured
// custom label is kept verbatim, an untouched default follows the host locale.
func (st *state) labels(cfg Config) uikit.ApprovalLabels {
	return uikit.ApprovalLabels{
		Title:            st.localized(cfg.Title, "需要审批", "Approval required"),
		ToolLabel:        st.localized(cfg.ToolLabel, "工具", "Tool"),
		ArgumentsLabel:   st.localized(cfg.ArgumentsLabel, "参数", "Arguments"),
		AllowLabel:       st.localized(cfg.AllowLabel, "允许", "Allow"),
		AllowAlwaysLabel: st.localized(cfg.AllowAlwaysLabel, "本会话内始终允许", "Always allow in this session"),
		DenyLabel:        st.localized(cfg.DenyLabel, "拒绝", "Deny"),
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

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
