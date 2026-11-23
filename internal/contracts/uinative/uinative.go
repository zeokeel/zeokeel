// Package uinative holds the contribution model of the native mygo UI: the
// types a UI module uses to contribute screens, slots, settings pages, tool
// renderers and commands, plus the host-owned UI runtime they register with.
//
// It lives in its own package so internal/contracts stays engine-agnostic: the
// gateway and any other transport can depend on contracts without dragging
// github.com/egoist/mygo into their dependency graph. Package uinative is the
// one place allowed to import the engine, and Stage 3 deletes it together with
// internal/ui.
package uinative

import (
	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// KeyUIRuntime is the service key of the host UI runtime. It lives here, next
// to UIRuntime, so internal/contracts can stay free of the native UI model.
var KeyUIRuntime = cordis.NewServiceKey[UIRuntime]("zeokeel/ui")

// BuildFunc builds one contribution inside a mygo frame. The context, and the
// elements built from it, live for this frame only: never store them.
type BuildFunc func(c *ui.Context)

// ScreenSpec is a top-level view contributed by a UI module.
type ScreenSpec struct {
	ID      string
	Title   string
	Order   int
	Visible func() bool
	Build   BuildFunc
}

// SlotItem is a contribution to a named injection point (sidebar, top bar,
// composer, overlays, ...). Order is ascending, ID breaks ties.
type SlotItem struct {
	Slot  string
	ID    string
	Order int
	Build BuildFunc
}

// SettingsPageSpec is one page of the settings surface.
type SettingsPageSpec struct {
	ID      string
	Section string
	Title   string
	Order   int
	Visible func() bool
	Build   BuildFunc
	// Footer, when set, renders the page's pinned action row (its save/apply
	// controls) in the host's fixed footer outside the scrolling body, so the
	// page's primary action is visible without scrolling. The host renders it
	// only for the selected page, on the standard 4 DIP grid, under a divider.
	Footer BuildFunc
	// Dirty reports whether the page holds unsaved edits; the host shows the
	// "unsaved" state in the settings header and asks before closing. Nil means
	// the page has no unsaved state (it applies every change immediately).
	Dirty func() bool
	// Discard, when set, throws the page's drafts away and re-reads the saved
	// values. The host calls it when the user chooses "Discard" in the unsaved
	// close guard, so the next open does not show edits the user abandoned.
	Discard func()
	// Keywords are the page's search aliases in the languages its users search
	// in (usually Chinese and English): the settings navigation matches them in
	// addition to the title and section, so a page is found by the words users
	// know it by ("密钥", "theme", "字号") rather than only its title. They are
	// declared by the page's own module, so there is no host-side alias table
	// that can drift away from the page.
	Keywords []string
	// SearchItems are the page's searchable items: a field, a row or a section
	// the navigation search matches by its label and keywords. A hit on an item
	// selects its page and asks the page to reveal the item's region (see
	// Reveal), so a search for "压缩" or "字号" reaches the control itself
	// instead of only the page that holds it.
	SearchItems []SettingsSearchItem
	// Reveal, when set, is called on the UI thread in the frame the page is
	// built after one of its SearchItems was the search hit: anchorKey is the
	// hit item's AnchorKey. The page expands whatever disclosure holds the
	// region, scrolls the region into view and highlights it briefly; it must
	// only touch its own UI state (the host calls it inside the frame, before
	// Build, so the region it reveals is built by the same frame).
	Reveal func(c *ui.Context, anchorKey string)
}

// SettingsSearchItem is one searchable item (a control, a row or a section) of
// a settings page: the words a user searches for, and the stable key of the
// region the page reveals when this item is the hit.
type SettingsSearchItem struct {
	// ID identifies the item within its page (stable and unique); it is what
	// tests and diagnostics name the item by.
	ID string
	// Label is the item's user-facing name in the current language.
	Label string
	// Keywords are the aliases users search for, in the current language and
	// English ("上下文预算", "compact", "compaction").
	Keywords []string
	// AnchorKey names the region the page reveals for this item: the page maps
	// it to the element it builds (and scrolls/highlights it in Build). Empty
	// means the item has no region to reveal beyond the page itself.
	AnchorKey string
}

// ToolRenderer draws one tool call (a tool card). Several renderers may match
// the same Tool; they are ordered by Order then ID.
type ToolRenderer struct {
	Tool  string
	ID    string
	Order int
	Build func(c *ui.Context, call contracts.ToolCall)
}

// CommandSpec is an action contributed to the command surface. Enabled and Run
// may be called from the UI thread only.
type CommandSpec struct {
	ID      string
	Title   string
	Hint    string
	Order   int
	Enabled func() bool
	Run     func() error
	// Shortcut is the human-readable chord the host also binds for this
	// command ("Cmd/Ctrl + Alt + →"), shown in the command palette. Empty when
	// the command has no keyboard chord; the palette simply omits it.
	Shortcut string
	// Category groups the command in the command palette: the goto and
	// settings categories render under their own headings, and the empty value
	// (also CommandCategoryActions) keeps the default actions group. The host
	// also generates the "前往 …" and "设置：…" commands of those two groups
	// from the registered screens and settings pages.
	Category string
	// Keywords are extra search aliases (Chinese and English synonyms) the
	// palette matches besides Title, ID and Hint, so a command is found by the
	// words users know it by rather than the words the module chose.
	Keywords []string
}

// UIRuntime is the host-owned UI registry and frame scheduler. A UI module
// registers its contributions in Activate and hands the returned unregister
// function to ctx.Effect so kernel disposal unwinds them in reverse order.
//
// Registration is only legal during assembly/startup on the UI thread; runtime
// enable/disable returns an empty build instead of mutating registries.
type UIRuntime interface {
	// Post queues fn to run at the next frame start on the UI thread.
	Post(fn func())
	// Invalidate requests a frame from any goroutine.
	Invalidate()
	// Frame returns the number of frames built so far.
	Frame() uint64
	// Commands returns the registered command surface in sorted order. The
	// host uses it for the palette; a composer menu (slash commands) reads the
	// same list so both share one registry.
	Commands() []CommandSpec

	RegisterScreen(ScreenSpec) (unregister func(), err error)
	RegisterSlotItem(SlotItem) (unregister func(), err error)
	RegisterSettingsPage(SettingsPageSpec) (unregister func(), err error)
	RegisterToolRenderer(ToolRenderer) (unregister func(), err error)
	RegisterCommand(CommandSpec) (unregister func(), err error)

	// RenderSlot builds every contribution of a slot, in order, inside the
	// current frame. It is only legal from a BuildFunc (UI thread, frame
	// scope); it lets a module expose its own extension point to other
	// modules that the host does not need to know about.
	RenderSlot(c *ui.Context, slot string)
	// RenderTool builds the registered tool-card renderers for call, in order.
	// It returns true when at least one renderer drew the call.
	RenderTool(c *ui.Context, call contracts.ToolCall) bool
	// Slots reports the slot names that currently have contributions (for
	// diagnostics and tests; the list is sorted).
	Slots() []string
}
