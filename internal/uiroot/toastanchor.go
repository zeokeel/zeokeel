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

// ReportDockTop implements contracts.ToastAnchorService: the conversation
// screen reports the top of its bottom dock (composer plus any approval card,
// todo plan or goal bar above it) so the stack clears an approval's decision
// buttons, which the designed card height alone cannot know about. Zero means
// "no dock this frame" and leaves the host's own inset.
func (r *Root) ReportDockTop(top float32) {
	r.mu.Lock()
	r.dockTop = top
	r.mu.Unlock()
}

// toastAnchorYields reports whether a transient surface that dims the window is
// open. Only one of those moves the stack: a scrim means the composer and the
// status strip below it are not clickable, while a popover or a menu leaves them
// live, so the stack keeps its dock inset under a popover (audit VD-02: the
// team popover's frame had three notices stacked over the composer and the
// status text). Module-reported overlays (ReportOverlay) are popovers and menus
// by that contract, so they do not move the stack either; the count is kept so a
// future dimming overlay has one place to say so.
func (r *Root) toastAnchorYields() bool {
	return r.nav.SettingsOpen()
}

// transcriptMetaRowHeight is the conversation transcript's trailing meta line (a
// compact row) plus the daylight that keeps a card off it.
const transcriptMetaRowHeight = design.ControlCompactHeight + design.Space3

// toastTopInset is how far a top-hanging stack starts below the window's top: the
// band, the tabs row, and the daylight that keeps a card off the band's chips
// (a chip's popover hangs below the band, so the daylight is one Space5 — the
// audit measured 10 DIP between a menu's bottom edge and the card's top, and a
// notice must not read as part of an open surface).
var toastTopInset = design.BandHeight + design.RowCompactHeight + design.Space5

// The room the conversation screen's bottom edge takes, measured from the
// window's bottom. The shell has no handle on a module's composer (the
// conversation module owns it), so the stack reserves the dock's designed
// height plus one Space4 of daylight above the card's top edge: a notice never
// covers the composer, its send control or the status strip, and the gap keeps
// it from reading as part of the card.
const (
	// composerCardHeight is the composer card's anatomy: its quiet attachment
	// row (when the draft has one), the input row, the toolbar row and the
	// gaps and padding between them.
	composerCardHeight = design.ControlCompactHeight + // attachment chips row
		design.Space2 + // attachments-to-input gap
		design.RowDoubleHeight + // input row
		design.Space2 + // input-to-toolbar gap
		design.RowCompactHeight + // toolbar row
		design.Space3*2 // the card's own padding
	// composerDockMargin is the room the dock column keeps above and below the
	// card.
	composerDockMargin = design.Space2 * 2
	// toastComposerDockHeight is that dock plus the status strip, the screen's
	// padding and the stack's own daylight above the card.
	toastComposerDockHeight = composerCardHeight +
		composerDockMargin +
		design.Space8 + // the status strip
		design.Space6 + // the screen's bottom padding
		design.Space4 // daylight between the stack and the composer's top edge
)

// computeToastAnchor resolves the stack's placement for this frame. It reads
// the same layout decisions the chrome just built (effective sidebar, docked
// inspector, composer dock), so the anchor and the geometry cannot drift. The
// stack always sits in the window's bottom right corner and grows upward: a
// notice may only appear where the current task cannot be clicked, never on the
// reading column it interrupted (audit VD-02 — the old top-right popup covered
// the user's own message, the approval card and a settings footer).
func (r *Root) computeToastAnchor(c *ui.Context) contracts.ToastAnchor {
	// The column's right edge: the window edge less the inspector, plus the
	// Space4 the stack keeps from an edge. The inspector counts in BOTH of its
	// presentations — a docked pane and the narrow window's modal drawer — so a
	// notice never lands on the inspector's own content (audit VD-02's narrow
	// frame: three cards over the README preview).
	right := design.Space4
	switch {
	case r.inspectorDocked(c):
		right += r.inspectorLayoutWidth(c) + design.Space1
	case r.rightPanelDrawerOpen(c):
		right += design.InspectorWidth + design.Space1
	}
	// A modal dialog dims the window and centres its panel: its own footer sits
	// inside the panel, so the corner below the panel is the one place the toast
	// may go. A popover or a menu does NOT move the stack — it opens beside its
	// trigger, away from the bottom edge, and the composer and the status strip
	// are still live under the pointer, so the stack keeps its dock inset
	// (the "corner the current task cannot click" rule).
	if r.toastAnchorYields() {
		return contracts.ToastAnchor{
			Right:      design.Space4,
			Bottom:     design.Space4,
			FromBottom: true,
		}
	}
	// The landing screen stacks a setup card right above its composer: the
	// reserved dock inset alone still lands the stack on that card's action row
	// at 1024×720, and the card's button is the one thing a new user has to
	// press. Its top-right corner below the band is empty, so the stack hangs
	// there instead (the same corner rule: only where the task cannot be
	// clicked).
	if r.nav.Current() == homeScreenID || r.activeTranscriptEmpty() {
		return contracts.ToastAnchor{
			Top:    toastTopInset,
			Right:  right,
			Bottom: design.Space4,
		}
	}
	// A pending decision owns the bottom of the screen: the inline approval
	// card sits above the composer and carries the only controls the user may
	// press, so the stack moves to the top-right corner under the band instead
	// of covering them (a notice must never sit on the action a run waits for).
	if r.nav.Current() == sessionScreenID && r.pendingApprovals() > 0 {
		return contracts.ToastAnchor{
			Top:    toastTopInset,
			Right:  right,
			Bottom: design.Space4,
		}
	}
	bottom := design.Space4
	// Above what a screen docks at the bottom: the composer and the status
	// strip. Both the conversation and the landing screen hold a composer and
	// the landing's action row ("配置 API Key") sits exactly where the stack
	// would otherwise land; the toast body takes the pointer (mygo pauses the
	// timer on hover), so a notice over a button is a real block, not a
	// cosmetic overlap.
	screen := r.nav.Current()
	if screen == sessionScreenID || screen == homeScreenID {
		bottom += toastComposerDockHeight
	}
	// A conversation ends its transcript with one meta line right above the
	// composer (time · duration · tokens · the row's own ⋯). A stack that stops at
	// the dock's top edge covers that row's tail, which reads as the app
	// truncating its own status ("03:38 · 用时 2.5s · 4…", audit item 2). The line
	// and its gap are reserved for the screen that has a transcript; the landing
	// screen has none.
	if screen == sessionScreenID {
		bottom += transcriptMetaRowHeight
	}
	return contracts.ToastAnchor{Right: right, Bottom: bottom, FromBottom: true}
}

// activeTranscriptEmpty reports the conversation screen's hero state: an open
// session with no messages. Its setup card ("开始前还需要…") sits right above the
// composer, exactly where the bottom-anchored stack would land, so the stack
// hangs from the top instead — the hero's top-right corner holds no control.
func (r *Root) activeTranscriptEmpty() bool {
	if r.nav.Current() != sessionScreenID || r.kernelContext == (cordis.Context{}) {
		return false
	}
	chat, ok := r.kernelContext.Get(contracts.KeyChat)
	if !ok || chat == nil {
		return false
	}
	id := chat.ActiveSession()
	if id == "" {
		return false
	}
	session, ok := chat.Session(id)
	if !ok {
		return false
	}
	return len(session.Messages) == 0
}

// pendingApprovals counts the requests waiting for a decision, so the stack can
// yield the bottom edge while a card holds it. A host without the approvals
// service (a fixture) reports zero and keeps the dock inset.
func (r *Root) pendingApprovals() int {
	if r.kernelContext == (cordis.Context{}) {
		return 0
	}
	approvals, ok := r.kernelContext.Get(contracts.KeyApprovals)
	if !ok || approvals == nil {
		return 0
	}
	return len(approvals.Pending())
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
