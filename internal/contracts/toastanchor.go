package contracts

// The toast stack's placement contract (2026-11-17 UI review, P2/P1): the
// toasts module paints the notification stack, but only the UI host knows the
// chrome geometry (sidebar, docked inspector, screen header band) and which
// transient surfaces are open. The host anchors the stack so a toast never
// covers the header's actions, the inspector's header row or an open overlay;
// the module keeps its own default placement when no host provides the service.

import (
	cordis "github.com/zeokeel/zeokeel-kernel"
)

// KeyToastAnchor is the optional toast-placement service the UI host
// (internal/uiroot) provides on the kernel root context. The toasts module
// consumes it when present; a minimal host without the service still renders
// toasts at the module's own default anchor.
var KeyToastAnchor = cordis.NewServiceKey[ToastAnchorService]("zeokeel/ui/toast-anchor")

// ToastAnchorService is the host-owned toast stack placement. Anchor is read
// once per frame while the toast viewport builds; ReportOverlay is the hook a
// module calls at its own popover/menu sites so the host can yield the stack
// to surfaces it does not own.
type ToastAnchorService interface {
	// Anchor returns the toast stack's placement for the current frame.
	Anchor() ToastAnchor
	// ReportOverlay reports a module-owned transient overlay (popover, menu)
	// opening (true) or closing (false). While any reported overlay is open
	// the host anchors the stack at the window's bottom-right, clear of the
	// overlay. Calls must pair one open with one close; the host's count
	// floors at zero so a stray close never disables the yield.
	ReportOverlay(open bool)
	// ReportDockTop reports the top of the bottom dock the current screen
	// stacks above its status strip (the conversation's composer plus the
	// approval card, todo plan and goal bar). The stack is placed above that
	// line so a notice never covers an approval's decision buttons — the one
	// control a user must reach while a notice is up. A screen without a dock
	// reports zero, and the host keeps its own inset.
	ReportDockTop(top float32)
}

// ToastAnchor places the toast stack inside the window. Top/Right/Bottom are
// insets from the window edges in DIP. FromBottom grows the stack upward from
// the Bottom inset (a modal or overlay is open and the stack yields to it);
// otherwise the stack hangs downward from the Top inset, below the screen
// header band, at the main content column's right edge.
type ToastAnchor struct {
	Top        float32
	Right      float32
	Bottom     float32
	FromBottom bool
}
