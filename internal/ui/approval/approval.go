// Package approval contributes the pending tool-call approval card of the
// product: the uikit.ApprovalDialog entry component rendered inline in the
// session screen's banner slot (contracts.SlotSessionBanner), directly above the
// composer.
//
// It is a kernel UI module like ui/sidebar: activation requires the host UI
// runtime, the approvals service and the host navigator, subscribes to
// pending-set changes (the callback hops to the UI thread with Post/Invalidate),
// and registers one SlotItem whose unregister function is owned by a kernel
// effect. The card itself is the uikit approval entry component
// (AGENT-SURFACES.md S4): the module keeps the queue/settle state, resolves
// requests and exits; it draws no widget and re-composes no footer.
//
// The approval is deliberately not a blocking Modal: the user keeps reading and
// typing while a call waits, and the card stays in the conversation flow. When a
// request arrives while another screen is showing, the module brings the session
// screen forward once (from the UI-thread post, never from a build callback) so
// the card can be seen; a failed navigation is ignored and the request stays
// pending.
//
// Resolution: the card's three buttons resolve the request as allow /
// allow-always / deny. The component itself handles no keyboard shortcut; the
// session screen owns Escape (deny the oldest pending request) so the card can
// never be dismissed without an answer.
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

// SlotID identifies this module's contribution to session.banner.
const SlotID = "approval.banner"

// sessionScreenID is the session screen's stable ID: the screen that renders the
// banner slot above the composer. The ui/session module owns the same constant;
// a UI module may not import another module's implementation, so the stable
// screen ID is repeated here (it is part of the host's navigation contract, like
// the slot names).
const sessionScreenID = "session"

// Config is the module's typed assembly config (all fields have defaults).
type Config struct {
	// Title is the card heading (default "需要审批").
	Title string `json:"title"`
	// ToolLabel labels the tool name row of the Modal presentation (default
	// "工具"); the inline banner names the tool without a label.
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

// state is the module instance's banner state. It lives in the activation
// closure (one per loaded module) and is touched only on the UI thread.
type state struct {
	// locale localizes the card copy per frame (zh is the default).
	locale contracts.LocaleService
	// resolvedID is the request just answered; its row may still be in Pending
	// for a frame or two while the backend settles, and the card must not
	// reappear for it.
	resolvedID string
}

// Module registers the approval banner contribution.
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
		navigator, err := ctx.Require(contracts.KeyNavigator)
		if err != nil {
			return err
		}
		resolved := cfg.resolved()
		locale, _ := ctx.Get(contracts.KeyLocale)
		st := &state{locale: locale}

		// The approvals service notifies synchronously from whichever
		// goroutine resolves or enqueues a request. Hop to the UI thread: bring
		// the session screen (which renders the banner slot) forward when a
		// request is waiting on another screen, then let the host schedule a
		// frame. Nothing here touches frame state.
		unsubscribe, err := approvals.Subscribe(func() {
			runtime.Post(func() {
				ensureSessionVisible(navigator, approvals)
				runtime.Invalidate()
			})
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
			Slot:  contracts.SlotSessionBanner,
			ID:    SlotID,
			Order: 10,
			Build: func(c *ui.Context) {
				buildBanner(c, runtime, approvals, resolved, st)
			},
		}
		unregister, err := runtime.RegisterSlotItem(item)
		if err != nil {
			return fmt.Errorf("approval: register banner slot: %w", err)
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
		AddRequires(module.Requires(contracts.KeyNavigator)).
		WithName("ZeoKeel approval banner")
}

// ensureSessionVisible brings the session screen forward when an approval is
// waiting and the user is elsewhere, so the inline card cannot be missed. It
// runs on the UI thread from the approvals subscription's post, never from a
// build pass; a failed push (the screen is not registered yet) is ignored and
// the request stays pending. Once the session screen is current the check is a
// no-op, so a navigation happens once per request appearance rather than every
// frame.
func ensureSessionVisible(navigator contracts.Navigator, approvals contracts.Approvals) {
	if navigator == nil || approvals == nil {
		return
	}
	if len(approvals.Pending()) == 0 || navigator.Current() == sessionScreenID {
		return
	}
	_ = navigator.Push(sessionScreenID)
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

// buildBanner shows the oldest pending request as the inline approval card above
// the composer, or nothing while no request is waiting. The module keeps the
// FIFO and settle state; the component owns the card's anatomy, its footer
// order and its no-default decision semantics.
func buildBanner(c *ui.Context, runtime contracts.UIRuntime, approvals contracts.Approvals,
	cfg Config, st *state) {
	pending := approvals.Pending()
	if len(pending) == 0 {
		st.resolvedID = ""
		return
	}
	request := pending[0]
	if request.ID == "" || request.ID == st.resolvedID {
		// Empty IDs are not resolvable; a just-answered request is still listed
		// while the backend settles, so wait for the next change.
		return
	}
	uikit.ApprovalDialog(c, uikit.ApprovalDialogOptions{
		Request: request,
		Pending: len(pending),
		Labels:  st.labels(cfg),
		Inline:  true,
		Locale:  st.locale,
		OnResolve: func(decision contracts.ApprovalDecision) {
			resolve(runtime, approvals, st, request.ID, decision)
		},
	})
}

// labels builds the localizable copy of the card: an assembly-configured custom
// label is kept verbatim, an untouched default follows the host locale.
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

// resolve answers the displayed request once. The request ID is remembered so a
// Pending list that still lists it during the backend's settle cannot reopen the
// card for the same decision.
func resolve(runtime contracts.UIRuntime, approvals contracts.Approvals, st *state,
	id string, decision contracts.ApprovalDecision) {
	if id == "" {
		return
	}
	st.resolvedID = id
	_ = approvals.Resolve(id, decision)
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
