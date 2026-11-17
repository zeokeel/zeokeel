package uiroot

// The host's toast-anchor service (contracts.ToastAnchorService): the shell
// knows the chrome geometry and which transient surfaces are open, so it
// anchors the notification stack for the toasts module (2026-11-17 UI review,
// P2 toast placement / P1 toast yields to overlays).
//
// Rules:
//   - A modal (settings), the command palette, the rail's conversation popover,
//     the inspector's modal drawer, or any module-reported overlay yields the
//     stack to the window's bottom-right corner: the scrim makes the content
//     below uninteractive, and the corner stays clear of the dialog panel.
//   - Otherwise the stack hangs below the screen header band at the main
//     content column's right edge — the docked inspector's pane is not part of
//     that column, so the stack stops before it.
//
// The anchor is computed once per frame in view, after the chrome layout is
// resolved and before the overlay slots build; the toasts module reads the
// cached value while it paints the viewport.

import (
	"log"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// ToastAnchor returns the host toast-placement service
// (contracts.KeyToastAnchor). The Root provides it on the kernel root itself
// (New/SetKernelContext), so the desktop host and embedders need no extra
// wiring; a harness that builds its own kernel root provides it like the other
// host services.
func (r *Root) ToastAnchor() contracts.ToastAnchorService { return r }

// Anchor implements contracts.ToastAnchorService: the placement the last view
// computed. view refreshes it before the overlay slots build, so the toasts
// module always reads the current frame's geometry.
func (r *Root) Anchor() contracts.ToastAnchor {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.toastAnchorCache
}

// ReportOverlay implements contracts.ToastAnchorService: a module's transient
// overlay opened or closed. The count is atomic because a module may report
// from a callback while the UI thread reads it; it floors at zero so an
// unpaired close cannot disable the yield.
func (r *Root) ReportOverlay(open bool) {
	if open {
		r.overlayReports.Add(1)
		return
	}
	for {
		current := r.overlayReports.Load()
		if current <= 0 {
			return
		}
		if r.overlayReports.CompareAndSwap(current, current-1) {
			return
		}
	}
}

// toastAnchorYields reports whether a transient surface the host owns is open:
// the settings modal, the command palette, the rail's conversation popover or
// the inspector's modal drawer. Module-owned popovers join through
// ReportOverlay.
func (r *Root) toastAnchorYields() bool {
	return r.nav.SettingsOpen() || r.paletteOpen || r.railSessionsOpen ||
		r.inspectorDrawerShown || r.overlayReports.Load() > 0
}

// computeToastAnchor resolves the stack's placement for this frame. It reads
// the same layout decisions the chrome just built (effective sidebar, docked
// inspector, header band), so the anchor and the geometry cannot drift.
func (r *Root) computeToastAnchor(c *ui.Context) contracts.ToastAnchor {
	if r.toastAnchorYields() {
		// Bottom-right: clear of a centered modal panel and of a right-anchored
		// popover/menu, above the composer the scrim already disables.
		return contracts.ToastAnchor{
			Right:      design.Space4,
			Bottom:     design.Space4,
			FromBottom: true,
		}
	}
	// Below the screen header band: the 40 DIP header row, the optional 32 DIP
	// tabs row, and one Space3 gap so the stack clears the band's actions. A
	// screen with no band keeps a plain Space4 top inset.
	top := design.Space4
	if r.hasScreenBandSlots() {
		top = design.Space8 + design.Space2
		if r.hasScreenSlot(contracts.SlotScreenTabs) {
			top += design.Space8
		}
		top += design.Space3
	}
	// The main column's right edge: the window edge less the docked inspector
	// pane and the split divider, then the same Space4 gap the stack keeps from
	// the window edge when no inspector docks.
	right := design.Space4
	if r.inspectorDocked(c) {
		right += r.inspectorLayoutWidth(c) + design.Space1
	}
	return contracts.ToastAnchor{Top: top, Right: right, Bottom: design.Space3}
}

// refreshToastAnchor caches this frame's placement for the toasts module. view
// calls it after the inspector drawer built and before the overlay slots.
func (r *Root) refreshToastAnchor(c *ui.Context) {
	anchor := r.computeToastAnchor(c)
	r.mu.Lock()
	r.toastAnchorCache = anchor
	r.mu.Unlock()
}

// provideToastAnchor registers the anchor service on the kernel root once, so
// the toasts module's optional lookup finds it without the process host or the
// assembly knowing the key. The desktop host binds the kernel context before
// the assembly loads (host.go SetKernelContext); embedders may bind it in
// Config.Kernel instead.
func (r *Root) provideToastAnchor(ctx cordis.Context) {
	if ctx == (cordis.Context{}) {
		return
	}
	r.mu.Lock()
	provided := r.toastAnchorProvided
	r.toastAnchorProvided = true
	r.mu.Unlock()
	if provided {
		return
	}
	if _, err := ctx.Provide(contracts.KeyToastAnchor, contracts.ToastAnchorService(r)); err != nil {
		log.Printf("uiroot: provide toast anchor: %v", err)
	}
}
