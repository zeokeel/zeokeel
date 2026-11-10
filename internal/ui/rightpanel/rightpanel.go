// Package rightpanel contributes the DSH-aligned right sidebar of the
// conversation screen (contracts.SlotSessionPanelRight) and the
// contracts.FilePreviewer service the session surface opens files through.
//
// The sidebar has two native tab rows: the fixed view pages 开始 (the guide),
// 文件 (the workspace tree plus the 交付区 deliverables history) and 命令 (the
// one-off command runner) on an underline strip, and, below it, one document
// tab per opened file: clicking a file in the workspace tree opens its
// read-only preview (text/Markdown/image, with an honest fallback for
// everything else), and an opened file that the workspace differ reports as
// changed carries its unified diff. Tab rows switch, file tabs close, and both
// strips scroll sideways when they outgrow the rail.
//
// The retired 8-pane inspector (overview/tools/log/changes/agents/tasks) is
// gone: the team and tasks surfaces moved to the session header, and the log
// and changes surfaces were folded into the file tabs. The host owns the panel
// width and the open/close control: this module only fills the slot and asks
// the host to open it through its stable "uiroot.open-inspector" command when
// a preview request arrives while the sidebar is closed.
//
// Everything is built with mygo's native controls and the brand design tokens
// (internal/design); the module never draws a control itself. UI state lives
// on the module instance and is only touched on the UI thread; backend reads
// (directory listings, file previews, workspace diffs) run off the UI thread
// and land through UIRuntime.Post, so selecting a large file never freezes a
// frame.
package rightpanel

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/lib/openpath"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// ModuleID is the compile-time module identity.
const ModuleID = "zeokeel-ui/rightpanel"

// SlotItemID is the id of the sidebar's contribution to the right panel.
const SlotItemID = "rightpanel.inspector"

// slotOrder places the sidebar first in the right panel.
const slotOrder = 10

// The fixed pages precede the opened-file tabs in the tab strip.
const (
	pageGuide = iota
	pageFiles
	pageTerminal
	// pageFixedCount is the number of fixed pages; a tab index at or above it
	// is an opened file (index - pageFixedCount).
	pageFixedCount
)

// Cross-navigation ids. The host palette and the module's own guide open the
// sidebar through commands, and the host opens the panel through its stable
// command; string literals keep UI modules from importing each other.
const (
	filesOpenCommandID    = "rightpanel.files.open"
	terminalOpenCommandID = "rightpanel.terminal.open"
	// hostOpenCommandID is the host's idempotent "open the inspector" command
	// (never toggles): a preview request from a conversation card opens the
	// sidebar through it.
	hostOpenCommandID = "uiroot.open-inspector"
)

// The guide's shortcut labels. They are the command metadata's shortcuts: the
// guide is the discoverability surface of the same chords, so the two cannot
// drift.
var (
	filesOpenShortcut    = platformShortcut("Ctrl+Alt+P", "Cmd+Alt+P")
	terminalOpenShortcut = platformShortcut("Ctrl+`", "Cmd+`")
)

// Config is the module's typed assembly config (all fields have defaults).
type Config struct {
	// Title is the sidebar's assistive name (default 右侧栏).
	Title string `json:"title"`
	// GuideTab/FilesTab/TerminalTab are the fixed page tab labels; the terminal
	// runner's page is "命令" (Commands): it is a one-off command runner, not an
	// interactive terminal.
	GuideTab    string `json:"guideTab"`
	FilesTab    string `json:"filesTab"`
	TerminalTab string `json:"terminalTab"`
}

// resolved trims the configured strings, keeping explicit overrides only; the
// localized defaults are resolved per frame from the host locale.
func (c Config) resolved() Config {
	return Config{
		Title:       strings.TrimSpace(c.Title),
		GuideTab:    strings.TrimSpace(c.GuideTab),
		FilesTab:    strings.TrimSpace(c.FilesTab),
		TerminalTab: strings.TrimSpace(c.TerminalTab),
	}
}

func (c Config) titleLabel(locale contracts.LocaleService) string {
	return firstNonEmpty(c.Title, uikit.T(locale, "右侧栏", "Right sidebar"))
}

func (c Config) guideLabel(locale contracts.LocaleService) string {
	return firstNonEmpty(c.GuideTab, uikit.T(locale, "开始", "Start"))
}

func (c Config) filesLabel(locale contracts.LocaleService) string {
	return firstNonEmpty(c.FilesTab, uikit.T(locale, "文件", "Files"))
}

func (c Config) terminalLabel(locale contracts.LocaleService) string {
	return firstNonEmpty(c.TerminalTab, uikit.T(locale, "命令", "Commands"))
}

// fileTab is one opened file tab: the workspace-relative path (slash
// separated) that keys its preview cache and the diff lookup.
type fileTab struct {
	path string
}

// dirListing is one lazily loaded directory of the workspace tree.
type dirListing struct {
	loaded  bool
	loading bool
	entries []contracts.FileEntry
	err     error
}

// state is the module's UI state (one instance per loaded module, only touched
// on the UI thread).
type state struct {
	// ctx is the module's kernel context: the optional backends (workspace,
	// terminal, artifact presenter) are looked up with ctx.Get, no Requires.
	ctx cordis.Context
	// locale localizes the sidebar's own copy on every frame.
	locale contracts.LocaleService
	// cfg is the resolved module config.
	cfg Config
	// runtime registers the page commands and requests frames.
	runtime contracts.UIRuntime

	// tab is the active tab index: a fixed page (pageGuide/pageFiles/
	// pageTerminal) or pageFixedCount+fileTab. fileTabs are appended in open
	// order; revealTab asks the next frame to scroll the active tab into view.
	tab       int
	fileTabs  []fileTab
	revealTab bool
	// viewTab and docTab are the native TabsBase selections of the two tab
	// rows (the fixed view pages above, the opened file documents below): the
	// rows are separate lists, so each needs its own bound index, and the frame
	// maps a user choice back to tab.
	viewTab int
	docTab  int

	// filesDirs caches the workspace tree's directory listings by relative
	// path ("" is the root); filesOpen keeps the native tree's fold state per
	// directory (the *bool ui.TreeItem owns). filesWorkspace is the key of the
	// workspace the cache belongs to; a workspace switch drops it.
	filesDirs      map[string]*dirListing
	filesOpen      map[string]*bool
	filesWorkspace string
	// filesGeneration drops posted directory results an explicit refresh (or
	// a workspace switch) invalidated while they were in flight.
	filesGeneration int
	// filesRootErr is the last open-workspace failure ("open folder" dialog or
	// workspace creation), shown on the Files page.
	filesRootErr string

	// activeWorkspace is the workspace identity (ID + root) the path-keyed
	// caches (previews, editors, changes, diffs) belong to. It is synced once
	// per frame; a workspace switch drops those caches while the opened-file
	// tabs stay, so each tab re-reads its path in the new workspace instead of
	// showing the previous workspace's bytes.
	activeWorkspace string

	// previews caches one file preview per path (the cached entry also holds
	// its workspace identity), and editors keeps the official read-only code
	// editor state of a file tab across frames (its scroll and selection
	// survive tab switches). Editor states are keyed by workspace and path: a
	// same-named file in another workspace must never reuse a buffer.
	previews map[string]*filePreview
	editors  map[editorKey]*editor.State

	// Deliverables history: the expanded preview rows (by artifact id), the
	// official List's state and choice, and the last download status.
	artifactsOpen      map[string]bool
	artifactsList      ui.ListState
	artifactsSel       int
	artifactsNotice    string
	artifactsErr       string
	artifactsSavedPath string

	// Workspace changes: cached uncommitted changes (the file tab's diff fold
	// and the Files page read them), the per-file official diff viewers and
	// the fold state.
	changesLoaded    bool
	changesWorkspace string
	changes          []contracts.FileChange
	changesErr       error
	changesLoading   bool
	changesOpen      map[string]bool
	diffs            map[string]*diffView
	// changesGeneration invalidates an in-flight workspace-diff read: a user
	// refresh or a workspace switch bumps it, and a posted result from an
	// older generation is dropped instead of restoring the pre-refresh
	// snapshot.
	changesGeneration int

	// term is the Terminal page's state (its command runner appends output
	// from a background goroutine, so its guard lives in terminalState).
	term *terminalState

	// commands are the registered page commands; commandLang re-registers them
	// with localized titles after a language switch.
	commands    []func()
	commandLang string
}

// tr localizes the sidebar's own copy for the current frame.
func (st *state) tr(zh, en string) string { return uikit.T(st.locale, zh, en) }

// pageCount is the number of tabs (fixed pages plus opened files).
func (st *state) pageCount() int { return pageFixedCount + len(st.fileTabs) }

// clampTab keeps the active tab inside the current tab strip.
func (st *state) clampTab() int {
	if st.tab < 0 || st.tab >= st.pageCount() {
		st.tab = pageGuide
	}
	return st.tab
}

// activeFileIndex reports the opened file the active tab shows.
func (st *state) activeFileIndex() (int, bool) {
	index := st.tab - pageFixedCount
	if index < 0 || index >= len(st.fileTabs) {
		return 0, false
	}
	return index, true
}

// tabLabel is the visible label of tab i: the localized fixed-page name, or
// the opened file's base name (with its parent directory when two open files
// share one).
func (st *state) tabLabel(index int) string {
	switch index {
	case pageGuide:
		return st.cfg.guideLabel(st.locale)
	case pageFiles:
		return st.cfg.filesLabel(st.locale)
	case pageTerminal:
		return st.cfg.terminalLabel(st.locale)
	}
	fileIndex := index - pageFixedCount
	if fileIndex < 0 || fileIndex >= len(st.fileTabs) {
		return ""
	}
	path := st.fileTabs[fileIndex].path
	base := filepath.Base(path)
	for other := range st.fileTabs {
		if other == fileIndex {
			continue
		}
		if filepath.Base(st.fileTabs[other].path) != base {
			continue
		}
		if dir := filepath.Dir(path); dir != "." {
			return base + " · " + dir
		}
	}
	return base
}

// showFixedPage opens one of the fixed pages, asks the host to open the panel
// and requests a frame. It is the one path the guide cards, the page commands
// and the FilePreviewer share.
func (st *state) showFixedPage(page int) {
	if page < 0 || page >= pageFixedCount {
		return
	}
	st.tab = page
	st.revealTab = true
	st.openHostPanel()
	st.invalidate()
}

// openFile focuses (or opens) one workspace file as a tab.
func (st *state) openFile(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	path = filepath.ToSlash(path)
	for index := range st.fileTabs {
		if st.fileTabs[index].path == path {
			st.tab = pageFixedCount + index
			st.revealTab = true
			st.openHostPanel()
			st.invalidate()
			return
		}
	}
	st.fileTabs = append(st.fileTabs, fileTab{path: path})
	st.tab = pageFixedCount + len(st.fileTabs) - 1
	st.revealTab = true
	st.openHostPanel()
	st.invalidate()
}

// closeTab closes the file tab at index (a fixed page is never closable): it
// selects the previous tab, drops the caches that belonged to the file and
// requests a frame.
func (st *state) closeTab(index int) {
	fileIndex := index - pageFixedCount
	if fileIndex < 0 || fileIndex >= len(st.fileTabs) {
		return
	}
	path := st.fileTabs[fileIndex].path
	st.fileTabs = append(st.fileTabs[:fileIndex], st.fileTabs[fileIndex+1:]...)
	st.dropPathCaches(path)
	switch {
	case st.tab > index:
		st.tab--
	case st.tab == index:
		// Closing the active tab selects the neighbour that took its place:
		// the next opened file, else the previous one, else the files page.
		switch {
		case fileIndex < len(st.fileTabs):
			st.tab = pageFixedCount + fileIndex
		case fileIndex > 0:
			st.tab = pageFixedCount + fileIndex - 1
		default:
			st.tab = pageFiles
		}
	}
	if st.tab >= st.pageCount() {
		st.tab = st.pageCount() - 1
	}
	if st.tab < 0 {
		st.tab = pageGuide
	}
	st.revealTab = true
	st.invalidate()
}

// openHostPanel runs the host's idempotent open command when the host
// registered it; a headless/embedded host without it just keeps the local
// state (the sidebar still shows on the next frame that renders the slot).
func (st *state) openHostPanel() {
	if st.runtime == nil {
		return
	}
	for _, command := range st.runtime.Commands() {
		if command.ID != hostOpenCommandID || command.Run == nil {
			continue
		}
		_ = command.Run()
		return
	}
}

// invalidate requests one more frame.
func (st *state) invalidate() {
	if st.runtime != nil {
		st.runtime.Invalidate()
	}
}

// Module registers the right sidebar slot and provides contracts.FilePreviewer.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		config := cfg.resolved()
		locale, _ := ctx.Get(contracts.KeyLocale)
		moduleState := &state{
			ctx:           ctx,
			locale:        locale,
			cfg:           config,
			runtime:       runtime,
			filesDirs:     make(map[string]*dirListing),
			filesOpen:     make(map[string]*bool),
			previews:      make(map[string]*filePreview),
			editors:       make(map[editorKey]*editor.State),
			changesOpen:   make(map[string]bool),
			diffs:         make(map[string]*diffView),
			artifactsOpen: make(map[string]bool),
			artifactsSel:  -1,
			term:          newTerminalState(),
		}
		if err := moduleState.registerCommands(); err != nil {
			return err
		}
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(moduleState.closeCommands)}
		}); err != nil {
			moduleState.closeCommands()
			return err
		}
		// The FilePreviewer is the session surface's entry point into the
		// sidebar: conversation cards preview a file, open the files/terminal
		// page or launch a generated file in the platform's application.
		previewer := &filePreviewService{state: moduleState}
		if _, err := ctx.Provide(contracts.KeyFilePreview, contracts.FilePreviewer(previewer)); err != nil {
			return err
		}
		return own(ctx, func() (func(), error) {
			return runtime.RegisterSlotItem(contracts.SlotItem{
				Slot:  contracts.SlotSessionPanelRight,
				ID:    SlotItemID,
				Order: slotOrder,
				Build: func(c *ui.Context) {
					var workspace contracts.WorkspaceService
					if service, ok := ctx.Get(contracts.KeyWorkspace); ok {
						workspace = service
					}
					var terminal contracts.TerminalService
					if service, ok := ctx.Get(contracts.KeyTerminal); ok {
						terminal = service
					}
					// The deliverables history is the optional lister half of
					// the host artifact presenter; the Files page only reads it.
					var artifacts contracts.ArtifactLister
					if service, ok := ctx.Get(contracts.KeyArtifactPresenter); ok {
						artifacts, _ = service.(contracts.ArtifactLister)
					}
					buildPanel(c, design.Of(c), workspace, terminal, artifacts, config, moduleState)
				},
			})
		})
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddProvides(module.Provides(contracts.KeyFilePreview)).
		WithName("ZeoKeel right sidebar")
}

// registerCommands (re)registers the two page-focus commands: the guide's
// shortcut labels, the host palette and the sidebar's own cards all target the
// same ids.
func (st *state) registerCommands() error {
	if st.runtime == nil {
		return nil
	}
	st.closeCommands()
	files, err := st.runtime.RegisterCommand(contracts.CommandSpec{
		ID:       filesOpenCommandID,
		Title:    st.tr("打开文件页", "Open files page"),
		Hint:     st.tr("在右侧栏浏览工作区文件", "Browse workspace files in the right sidebar"),
		Order:    920,
		Shortcut: filesOpenShortcut,
		Run:      func() error { st.showFixedPage(pageFiles); return nil },
	})
	if err != nil {
		return err
	}
	terminal, err := st.runtime.RegisterCommand(contracts.CommandSpec{
		ID:       terminalOpenCommandID,
		Title:    st.tr("打开命令页", "Open commands page"),
		Hint:     st.tr("在右侧栏运行一次性命令，输出可复制", "Run one-off commands in the right sidebar; the output is copyable"),
		Order:    921,
		Shortcut: terminalOpenShortcut,
		Run:      func() error { st.showFixedPage(pageTerminal); return nil },
	})
	if err != nil {
		files()
		return err
	}
	st.commands = []func(){files, terminal}
	st.commandLang = uikit.Language(st.locale)
	return nil
}

// closeCommands unregisters the page commands.
func (st *state) closeCommands() {
	for _, unregister := range st.commands {
		unregister()
	}
	st.commands = nil
}

// syncCommands re-registers the commands when the host language changed, so
// the palette titles follow the language like every other module's.
func (st *state) syncCommands() {
	if st.commands != nil && uikit.Language(st.locale) == st.commandLang {
		return
	}
	_ = st.registerCommands()
}

// platformShortcut picks the host's actual chord label: mygo's ui.Cmd maps to
// Command on macOS and Control elsewhere, so the guide and palette must not
// advertise "Ctrl" on a Mac.
func platformShortcut(control, command string) string {
	if runtime.GOOS == "darwin" {
		return command
	}
	return control
}

// filePreviewService implements contracts.FilePreviewer over the module state.
type filePreviewService struct {
	state *state
}

// PreviewFile opens (or focuses) one workspace file as a right-sidebar tab and
// reveals the sidebar. The path may be workspace-relative or absolute inside
// the current workspace root; a path outside the root (including a relative
// parent-directory escape) is rejected.
func (s *filePreviewService) PreviewFile(path string) error {
	if s == nil || s.state == nil {
		return errors.New("rightpanel: the right sidebar is not loaded")
	}
	relative, _, err := s.state.resolveWorkspacePath(s.state.workspaceService(), path)
	if err != nil {
		return err
	}
	s.state.openFile(relative)
	return nil
}

// OpenFiles focuses the sidebar's files page.
func (s *filePreviewService) OpenFiles() error {
	if s == nil || s.state == nil {
		return errors.New("rightpanel: the right sidebar is not loaded")
	}
	s.state.showFixedPage(pageFiles)
	return nil
}

// OpenTerminal focuses the sidebar's terminal page.
func (s *filePreviewService) OpenTerminal() error {
	if s == nil || s.state == nil {
		return errors.New("rightpanel: the right sidebar is not loaded")
	}
	s.state.showFixedPage(pageTerminal)
	return nil
}

// OpenNative opens one path in the platform's file manager or default
// application: the secondary action on generated-file cards. It uses the same
// workspace containment as PreviewFile, so a card path can never launch a file
// outside the open workspace: a relative path resolves under the live root, an
// absolute path must be inside it, and without a workspace absolute paths and
// parent-directory escapes are rejected. A contained relative path on a
// workspace-less host is launched as given (there is no root to resolve it
// against).
func (s *filePreviewService) OpenNative(path string) error {
	if s == nil || s.state == nil {
		return errors.New("rightpanel: the right sidebar is not loaded")
	}
	_, native, err := s.state.resolveWorkspacePath(s.state.workspaceService(), path)
	if err != nil {
		return err
	}
	return openpath.Open(native)
}

// workspaceService returns the optional workspace backend (nil when the host
// has none).
func (st *state) workspaceService() contracts.WorkspaceService {
	if st == nil {
		return nil
	}
	raw, ok := st.ctx.Get(contracts.KeyWorkspace)
	if !ok || raw == nil {
		return nil
	}
	workspace, _ := raw.(contracts.WorkspaceService)
	return workspace
}

// resolveWorkspacePath validates one FilePreviewer path against the live
// workspace and returns it in both forms: the workspace-relative slash path
// (the file tab identity) and the native path to hand to the filesystem or the
// OS launcher. The containment is the same for PreviewFile and OpenNative: an
// absolute path must be inside the current workspace root, a relative path must
// stay under it, and without a workspace an absolute path or a
// parent-directory escape is rejected. Symlinks that resolve outside the root
// are rejected when the candidate exists.
func (st *state) resolveWorkspacePath(workspace contracts.WorkspaceService,
	path string) (relative, native string, err error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", "", errors.New("rightpanel: file path is required")
	}
	if strings.ContainsRune(trimmed, 0) {
		return "", "", errors.New("rightpanel: file path contains a NUL byte")
	}
	nativeInput := filepath.FromSlash(trimmed)
	current, hasWorkspace, currentErr := workspaceCurrent(workspace)
	if currentErr != nil {
		return "", "", errors.New("rightpanel: cannot read the current workspace: " + currentErr.Error())
	}
	if !hasWorkspace {
		if filepath.IsAbs(nativeInput) {
			return "", "", errors.New("rightpanel: no workspace is open for an absolute path")
		}
		cleaned := filepath.Clean(nativeInput)
		relative = filepath.ToSlash(cleaned)
		if escapesWorkspace(relative) || relative == "." {
			return "", "", errors.New("rightpanel: file is outside the workspace: " + trimmed)
		}
		return relative, cleaned, nil
	}
	root := filepath.Clean(current.Root)
	if filepath.IsAbs(nativeInput) {
		cleaned := filepath.Clean(nativeInput)
		rel, relErr := filepath.Rel(root, cleaned)
		if relErr != nil {
			return "", "", errors.New("rightpanel: file is outside the workspace: " + trimmed)
		}
		relative = filepath.ToSlash(rel)
		if escapesWorkspace(relative) {
			return "", "", errors.New("rightpanel: file is outside the workspace: " + trimmed)
		}
		if relative == "." {
			return "", "", errors.New("rightpanel: path is the workspace root, not a file")
		}
		if !withinWorkspaceRoot(root, cleaned) {
			return "", "", errors.New("rightpanel: file resolves outside the workspace: " + trimmed)
		}
		return relative, cleaned, nil
	}
	cleaned := filepath.Clean(nativeInput)
	relative = filepath.ToSlash(cleaned)
	if escapesWorkspace(relative) || relative == "." {
		return "", "", errors.New("rightpanel: file is outside the workspace: " + trimmed)
	}
	native = filepath.Join(root, cleaned)
	if !withinWorkspaceRoot(root, native) {
		return "", "", errors.New("rightpanel: file resolves outside the workspace: " + trimmed)
	}
	return relative, native, nil
}

// workspaceCurrent reads the current workspace of the optional backend (false
// when there is no backend or none is open).
func workspaceCurrent(workspace contracts.WorkspaceService) (contracts.Workspace, bool, error) {
	if workspace == nil {
		return contracts.Workspace{}, false, nil
	}
	return workspace.Current(context.Background())
}

// escapesWorkspace reports whether a cleaned relative path climbs out of its
// root.
func escapesWorkspace(relative string) bool {
	return relative == ".." ||
		strings.HasPrefix(relative, "../") ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// withinWorkspaceRoot reports whether candidate (already lexically checked)
// really lives under root once symlinks resolve. A candidate that does not
// exist yet keeps the lexical result: the reader/launcher reports the missing
// file itself.
func withinWorkspaceRoot(root, candidate string) bool {
	resolvedRoot := root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		resolvedRoot = resolved
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return true
	}
	rel, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil {
		return false
	}
	return !escapesWorkspace(filepath.ToSlash(rel))
}

// own registers one contribution and ties its unregistration to ctx's effect
// lifetime, so activation failure and module disposal leave no residue.
func own(ctx cordis.Context, register func() (func(), error)) error {
	unregister, err := register()
	if err != nil {
		return err
	}
	if _, err := ctx.Effect(func() cordis.Effect {
		return cordis.Effect{cordis.CleanupOf(unregister)}
	}); err != nil {
		unregister()
		return err
	}
	return nil
}

// firstNonEmpty returns the first non-empty trimmed string.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
