// Package contracts is ZeoKeel's only shared surface between the host and its
// modules. It holds service keys, host service interfaces, and the value types
// a UI module contributes (screens, slots, settings pages, tool renderers,
// commands). It deliberately imports no product package: a module may import
// contracts, uikit, design and lib/*, never another module or the host.
package contracts

import (
	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	rigtool "github.com/zeokeel/zeokeel-rig/tools"
)

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
}

// ToolCall is the read-only summary a tool renderer receives. The full tool
// state belongs to the session read model and is added when that module is
// ported; keep renderers tolerant of zero values.
type ToolCall struct {
	ID      string
	Name    string
	Status  string
	Summary string
}

// ToolRenderer draws one tool call (a tool card). Several renderers may match
// the same Tool; they are ordered by Order then ID.
type ToolRenderer struct {
	Tool  string
	ID    string
	Order int
	Build func(c *ui.Context, call ToolCall)
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
}

// AppInfo is the build identity provided by the appinfo module.
type AppInfo struct {
	Name      string
	Version   string
	Commit    string
	BuildTime string
	GoVersion string
}

// Slot names used by the host and modules.
//
// The model is region -> slot -> item: the host owns the layout container of a
// reserved region and renders its slots in order; a module owns the content it
// contributes. Modules may also register their own slots (use a module prefix,
// e.g. "session.transcript.turn") and render them from their own Build with
// UIRuntime.RenderSlot, which lets one module expose extension points to other
// modules without the host knowing about them.
const (
	// Window top bar.
	SlotTopBarBrand   = "topbar.brand"
	SlotTopBarCenter  = "topbar.center"
	SlotTopBarActions = "topbar.actions"

	// Main sidebar.
	SlotSidebarPrimary = "sidebar.primary"
	SlotSidebarNav     = "sidebar.nav"
	SlotSidebarTree    = "sidebar.tree"
	SlotSidebarFooter  = "sidebar.footer"

	// Screen surface (the screen body itself is registered via RegisterScreen).
	SlotScreenHeader  = "screen.header"
	SlotScreenActions = "screen.actions"
	SlotScreenFooter  = "screen.footer"

	// Home screen (rendered by the ui/home module).
	SlotHomeHero     = "home.hero"
	SlotHomeComposer = "home.composer"
	SlotHomeRecent   = "home.recent"

	// Session / conversation screen (rendered by the ui/session module).
	SlotSessionHeaderBrand   = "session.header.brand"
	SlotSessionHeaderTabs    = "session.header.tabs" // 页头横条：Chat / Trajectory / Memory
	SlotSessionHeaderActions = "session.header.actions"
	SlotSessionBanner        = "session.banner"
	SlotSessionTranscript    = "session.transcript"
	SlotSessionTrajectory    = "session.trajectory"     // 轨迹主体
	SlotSessionTrajectoryBar = "session.trajectory.bar" // 轨迹旁的一横条
	SlotSessionPanelLeft     = "session.panel.left"
	SlotSessionPanelRight    = "session.panel.right"
	SlotSessionRightRail     = "session.right.rail"
	SlotSessionComposerTop   = "session.composer.top"
	SlotSessionComposerInput = "session.composer.input"
	SlotSessionComposerBar   = "session.composer.toolbar"
	SlotSessionStatus        = "session.status"

	// Settings surface (the settings sidebar is host-rendered, pages are
	// contributed via RegisterSettingsPage).
	SlotSettingsSidebar    = "settings.sidebar"
	SlotSettingsHeader     = "settings.header"
	SlotSettingsPageBody   = "settings.page.body"
	SlotSettingsPageFooter = "settings.page.footer"

	// Global overlays.
	SlotOverlayPrefix   = "overlay."
	SlotOverlaySettings = "overlay.settings"
	SlotOverlayPalette  = "overlay.palette"
	SlotOverlayDialogs  = "overlay.dialogs"
	SlotOverlayToasts   = "overlay.toasts"
)

// Service keys: the name is the stable contract, the type is checked by the
// kernel at Provide/Require time and by module.Validate at assembly preflight.
var (
	// KeyUIRuntime is provided by the UI host (internal/uiroot).
	KeyUIRuntime = cordis.NewServiceKey[UIRuntime]("zeokeel/ui")
	// KeyNavigator is provided by the UI host (internal/uiroot).
	KeyNavigator = cordis.NewServiceKey[Navigator]("zeokeel/ui/navigator")
	// KeyTheme is provided by the UI host (internal/uiroot).
	KeyTheme = cordis.NewServiceKey[ThemeService]("zeokeel/ui/theme")
	// KeyLocale is provided by the UI host (internal/uiroot).
	KeyLocale = cordis.NewServiceKey[LocaleService]("zeokeel/ui/locale")
	// KeySettings is provided by the process host (internal/host).
	KeySettings = cordis.NewServiceKey[SettingsService]("zeokeel/settings")
	// KeyAppInfo is provided by the appinfo backend module.
	KeyAppInfo = cordis.NewServiceKey[AppInfo]("zeokeel/appinfo")
	// KeyToolkit is provided by the toolkit module: the ZeoKeel Rig toolset
	// (registry + built-in tools) assembled into the product's kernel tree.
	KeyToolkit = cordis.NewServiceKey[*rigtool.Registry]("zeokeel/toolkit")
	// KeyLogger is provided by the process host; modules log through it.
	KeyLogger = cordis.NewServiceKey[Logger]("zeokeel/log")

	// KeyUIRegistryOfScreens is a read-only view of registered screens for
	// modules that need to navigate (the host provides it with KeyUIRuntime).
	// Kept as an interface so modules never import the runtime implementation.
	KeyScreenLister = cordis.NewServiceKey[ScreenLister]("zeokeel/ui/screens")
)

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
	RenderTool(c *ui.Context, call ToolCall) bool
	// Slots reports the slot names that currently have contributions (for
	// diagnostics and tests; the list is sorted).
	Slots() []string
}

// Navigator is host-owned navigation state. Modules push screens or open
// settings; they never mutate the host's layout directly.
type Navigator interface {
	Current() string
	Push(screenID string) error
	Back() bool
	SettingsOpen() bool
	ToggleSettings()
	CloseSettings()
}

// ThemeService is the host theme switch (System/Light/Dark lives in settings).
type ThemeService interface {
	Dark() bool
	SetDark(dark bool)
	Toggle() bool
}

// LocaleService resolves UI copy. Keys are stable IDs like "app.name".
type LocaleService interface {
	Language() string
	SetLanguage(lang string)
	T(key string) string
}

// SettingsService is the host settings store (file-backed in the process host).
type SettingsService interface {
	Get(key, def string) string
	Set(key, value string) error
	All() map[string]string
}

// Logger is the host log sink exposed to modules.
type Logger interface {
	Printf(format string, args ...any)
}

// ScreenLister exposes the registered screen IDs in render order.
type ScreenLister interface {
	Screens() []string
}
