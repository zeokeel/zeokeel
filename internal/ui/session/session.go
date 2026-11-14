// Package session contributes the conversation screen: the main chat surface of
// ZeoKeel. It is a UI module (kernel Descriptor + host UI services) rendered
// with mygo's native controls and the brand design system (internal/design):
// TabsBase, Scroll, TextArea, Collapsible, CollapsibleBase, Spinner, List,
// markdown, Badge, Box, Row/Column, Text, buttons and icons. It never draws a
// widget (or Canvas) itself.
//
// The branded layout follows the retired shell: a title header with the
// Chat/Trajectory/Memory underline tabs, a centered transcript column with
// right-aligned raised user bubbles and plain assistant body text, one official
// reasoning disclosure per reply (CollapsibleBase with the official spinner and
// the Markdown view), tool rows as official disclosures through
// UIRuntime.RenderTool, a raised composer card with a borderless multiline input
// and a hint/stop-send toolbar, and the tool timeline on the Trajectory tab.
//
// The module reads the chat read model from contracts.ChatService (owned by the
// agent backend module) and calls its operations on user actions:
//
//   - empty session: "new conversation" button -> NewSession + SetActiveSession
//   - navigator.Push(session);
//   - chat tab: messages, streaming text, the reasoning disclosure, tool rows
//     via UIRuntime.RenderTool, errors;
//   - trajectory tab: the session's tool-call timeline; memory stays an empty
//     pane for Phase 1;
//   - composer: native TextArea + send button (Send) or stop button (Cancel)
//     while a run is in flight.
//
// Change notifications arrive on the agent goroutine; the module hops to the UI
// thread with UIRuntime.Post and requests a frame with Invalidate. The
// subscription and screen registration are both owned by kernel effects, so
// disposing the module unwinds them in reverse order.
package session

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// ModuleID is the compile-time module identity.
const ModuleID = "zeokeel-ui/session"

// ScreenID is the screen the module contributes.
const ScreenID = "session"

// Config is the module's typed assembly config (all fields have defaults).
type Config struct {
	Title               string `json:"title"`
	EmptyText           string `json:"emptyText"`
	ComposerPlaceholder string `json:"composerPlaceholder"`
	SendLabel           string `json:"sendLabel"`
	StopLabel           string `json:"stopLabel"`
	TabChat             string `json:"tabChat"`
	TabTrajectory       string `json:"tabTrajectory"`
	TabMemory           string `json:"tabMemory"`
	TabContext          string `json:"tabContext"`
	// ComposerHint is the muted toolbar hint at the bottom-left of the composer
	// card ("Enter sends, Shift+Enter breaks the line" by default).
	ComposerHint string `json:"composerHint"`
}

// copy resolves the configured strings with their defaults.
func (c Config) resolved() Config {
	return Config{
		Title:               firstNonEmpty(c.Title, "对话"),
		EmptyText:           firstNonEmpty(c.EmptyText, "还没有消息，开始一段新对话吧。"),
		ComposerPlaceholder: firstNonEmpty(c.ComposerPlaceholder, "输入消息…"),
		SendLabel:           firstNonEmpty(c.SendLabel, "发送"),
		StopLabel:           firstNonEmpty(c.StopLabel, "停止"),
		TabChat:             firstNonEmpty(c.TabChat, "对话"),
		TabTrajectory:       firstNonEmpty(c.TabTrajectory, "轨迹"),
		TabMemory:           firstNonEmpty(c.TabMemory, "记忆"),
		TabContext:          firstNonEmpty(c.TabContext, "上下文"),
		ComposerHint:        firstNonEmpty(c.ComposerHint, "Enter 发送 · Shift+Enter 换行"),
	}
}

// state is the module's UI state. It lives in the activation closure (one
// instance per loaded module) and is only touched on the UI thread: tab
// selection, the composer draft and the per-message reasoning fold state.
type state struct {
	tab int
	// lastTab is the tab rendered in the previous frame: entering the Memory
	// tab refreshes its cached read model, so entries written by the agent while
	// the user was elsewhere show up.
	lastTab int
	draft   string
	// draftSession is the conversation the live draft/pendingAttachments/
	// attachErr below belong to; composerDrafts keeps the drafts of the other
	// conversations (keyed by session ID) so switching never leaks text or
	// attachments into another conversation.
	draftSession   string
	composerDrafts map[string]composerDraft
	// composerInput is the composer TextArea's persistent identity. The
	// suggestion menu is inserted above the composer, so the native text input
	// needs a stable identity to keep the focus (and the caret) as the tree
	// around it changes.
	composerInput ui.Handle
	// composerCaret is where the composer input's selection ends in runes, read
	// from the last laid-out frame: the @ suggestion menu follows the caret
	// (C-15), so an @ typed in the middle of the message opens the suggestions
	// for the token around it instead of only at the end of the draft.
	composerCaret int
	// composerCaretPending asks the next composer frame to move the input's
	// caret to composerCaret: accepting a suggestion replaces the text through
	// the key hook, and the editor keeps the old caret offset, so the caret must
	// be placed after the inserted reference in the following frame.
	composerCaretPending bool
	// focusComposer asks the next composer frame to focus the TextArea: on
	// opening/switching a conversation and when a run finishes. It is a
	// one-shot edge flag (the frame builder clears it), never a desired-focus
	// value, so it cannot steal focus on every frame.
	focusComposer bool

	// thinkOpen holds the reasoning disclosures the reader toggled themselves,
	// keyed by message ID: a message with no entry uses the default (open while
	// the model is still thinking, collapsed once the turn ends). toolOpen is
	// the same for the transcript tool rows (H15 default when empty).
	thinkOpen map[string]bool
	toolOpen  map[string]bool
	// helpList backs the help dialog's command List; its Label names each row
	// for assistive technology (the list virtualizes rows but they are few).
	helpList ui.ListState

	// Slash/@ suggestion menu state. slashSel and mentionSel are the
	// highlighted rows (keys move them, Enter/Tab accept, Esc closes the menu
	// without clearing the draft); slashDismissed/mentionDismissed hold the
	// draft the user dismissed the menu at, so typing opens it again. The
	// menu rows are BgSelected-tinted and reachable from the keyboard.
	slashSel         int
	slashDismissed   string
	mentionSel       int
	mentionDismissed string

	// processOpen holds the activity-band disclosures the reader expanded: a
	// finished turn with tool calls folds its whole timeline (reasoning and
	// compact tool rows) into one summary row by default. Prune keeps it bounded
	// to the conversation on screen, like thinkOpen/toolOpen.
	processOpen map[string]bool
	// headerWidth is the header row's width from the last laid-out frame: when
	// the conversation column gets narrow (e.g. the right inspector docks at
	// 1024), the title must keep room and the agent entries drop their words.
	headerWidth float32
	// agents is the DSH agent surfaces' state: the header entries' overlays,
	// the child conversation view, the background-jobs panel and the
	// generated-file turn-tail cards (see agentsurface.go).
	agents agentSurface

	// The transcript is the official List: rows are messages keyed by ID, so a
	// streaming reply grows its row in place and re-render/older content keeps
	// the place; FollowEnd follows the end until the user scrolls away.
	chatList ui.ListState
	// chatFollowEnd is the transcript's follow state: true at the end, false
	// while the user reads earlier rows (shows the floating back-to-latest
	// action). chatListSeen waits one laid-out frame before reading AtEnd, so
	// the action does not flash on the first frame.
	chatFollowEnd bool
	chatListSeen  bool
	// chatHoldPlace asks the List to keep its place for the next layout after a
	// fold opened in a message (list.md HoldPlace): the clicked line stays under
	// the pointer instead of being pushed by the growing row.
	chatHoldPlace bool

	// memoryList backs the Memory tab's official List; the pane opens at its
	// top and a new search query resets the list.
	memoryList ui.ListState
	// trajList backs the trajectory pane's official List: the tool cards are a
	// collection, so the List virtualizes and keys them by tool ID.
	trajList      ui.ListState
	scrollSession string

	// Memory tab state: the search query, the add-input draft, the last store
	// error shown in DangerText and the cached read model. The frame builder
	// never reads the store: a query change schedules one debounced background
	// read (memoryPendingQuery/memorySearchDue), the result is applied through
	// UIRuntime.Post only while its sequence is still current, and mutations
	// ask for an immediate re-read (memoryRefresh).
	memoryQuery    string
	memoryDraft    string
	memoryError    string
	memoryEditOpen bool
	memoryEditID   string
	memoryEditText string
	memoryEditErr  string
	memories       []contracts.Memory
	memoriesErr    error
	memoriesQuery  string
	memoriesFresh  bool
	memoryRefresh  bool
	// memoryPendingQuery is the query whose debounced read is scheduled ("" =
	// none); memorySearchDue is when that read may start (frame clock).
	memoryPendingQuery string
	memorySearchDue    time.Time
	// memoryReading is true while a background read is in flight (the pane
	// shows a quiet loading line and never starts a duplicate read).
	memoryReading bool
	// memorySeq drops stale results: a read applies only when its sequence is
	// still the newest one asked for.
	memorySeq int

	// @ suggestion cache: the workspace files the current query resolves to,
	// refreshed only when the query changes. The frame builder never lists or
	// searches the workspace.
	mentionFiles []contracts.FileEntry
	mentionErr   error
	mentionQuery string
	mentionFresh bool
	// mentionNoWorkspace marks the normal "this conversation has no workspace"
	// state (as opposed to a read failure): the @ menu explains it and offers
	// the open/create recovery actions instead of showing a raw English error.
	mentionNoWorkspace bool
	// mentionWorkspaces caches the registered workspaces while no workspace is
	// current, so the menu's 打开工作区 action can list them without a read in
	// the frame.
	mentionWorkspaces []contracts.Workspace
	// mentionActionErr is a failed open/create-workspace action, shown inside
	// the @ menu's recovery block (never a raw read error).
	mentionActionErr string
	// workspaceBusy is true while the native folder picker for 新建工作区 is
	// open; a second click is ignored.
	workspaceBusy bool

	// Export state: the last export error shown under the session header.
	// Successes are transient host toasts, never a permanent header badge.
	exportErr string

	// regenerateErr is the last message-level 「重新生成」 failure shown under
	// the last assistant turn.
	regenerateErr string

	// goalService is the optional per-session durable goal backend
	// (contracts.KeyGoal). The UI caches only the ACTIVE conversation's goal
	// snapshot (goalSessionID + goal): switching conversations re-reads it, so
	// one session's goal is never shown in another.
	goalService   contracts.GoalService
	goalSessionID string
	goal          *contracts.Goal
	// goalActionErr is the last goal-lifecycle failure (DangerText row).
	// Successes go to a transient toast. goalConfirm is the destructive action
	// ("clear") waiting for its ConfirmDanger dialog.
	goalActionErr string
	goalConfirm   string

	// Composer attachment state: files picked and staged for the next message,
	// the last picker/read error, the chip sequence and the per-chip image
	// previews decoded once (keyed by attachment ID).
	pendingAttachments []contracts.Attachment
	attachErr          string
	attachSeq          int
	thumbnails         map[string]*ui.Bitmap
	// attachBusy is true while the native picker runs off the UI thread: the
	// attach actions show 选择中…/Choosing… and stay disabled until the result
	// is posted back, because mygo's dialog blocks its caller.
	attachBusy bool
	// attachPathsOpen/attachPathDraft/attachPathErr drive the manual path-entry
	// fallback for environments without a working native picker (ZK-09).
	attachPathsOpen bool
	attachPathDraft string
	attachPathErr   string

	// md caches the Markdown renderer options for the current appearance (the
	// library keeps its parse state per view root and the options must stay
	// stable across frames). mdReasoning is the muted, slightly smaller theme
	// the thinking panel renders with.
	md          markdownCache
	mdReasoning markdownCache

	// ctx is the module's kernel context, used for the optional memory backend
	// lookup (ctx.Get(contracts.KeyMemory)); it is not a Requires dependency.
	ctx cordis.Context

	// locale is the host locale service (optional). Copy is generated per frame
	// with uikit.T so switching the language applies on the next frame.
	locale contracts.LocaleService

	// navigator is the host navigator; the composer's "open settings" entries
	// deep-link to the page they mention when the host supports it.
	navigator contracts.Navigator
	// runtime is the host UI runtime, kept so an undo action the host invokes
	// later can request the frame that shows the restored state.
	runtime contracts.UIRuntime

	// settings is the optional settings store: the permission command and the
	// composer's permission badge read/write contracts.SettingAgentPermission.
	settings contracts.SettingsService

	// Optional command-family backends: the /plan and /memory dialogs read
	// these bridges (the goal bridge lives above with the header chip).
	planReviewer contracts.PlanReviewer
	memoryStore  contracts.MemoryStore

	// notifier is the optional desktop toast bridge: transient successes and
	// errors (export, stop, compaction, goal lifecycle, copied/footers) go
	// there instead of permanent header badges.
	notifier contracts.Notifier

	// promptQueue is the optional backend inbox half of ChatService (DSH
	// inbox): the queued prompts live in the agent backend, one inbox per
	// session, and the UI only reads/removes them. nil keeps a host without the
	// backend on the old conflict behavior (no queue control).
	promptQueue contracts.PromptQueue
	// queueMenuOpen is the active session's queued-prompts popover
	// (view/remove). queueList backs that popover's official ui.List of queued
	// prompts. queueActionErr is a failed inbox removal (cleared on a session
	// switch); UI thread only.
	queueMenuOpen  bool
	queueList      ui.ListState
	queueActionErr string

	// contextService is the optional context inspector (contracts.KeyContext):
	// the Context tab only exists when the backend provides it. The folded view
	// is cached per active session and refreshed on tab entry, session switch or
	// a chat change (the inspector has no Subscribe), deduplicated by Revision.
	contextService   contracts.ContextInspector
	contextSessionID string
	contextRevision  uint64
	contextData      contracts.SessionContext
	contextOK        bool
	contextLoaded    bool
	contextDirty     bool
	// contextRequests/contextSections/contextEvents back the official
	// Table/List widgets of the pane.
	contextRequests ui.ListState
	contextSections ui.ListState
	contextEvents   ui.ListState

	// interruptedDismissed records the restored-run notices the user dismissed
	// in this app run (ChatSession.Interrupted itself is not durable and clears
	// when the next run starts, so the entries are naturally meaningless after
	// a restart). UI thread only.
	interruptedDismissed map[string]bool

	// Run-settle observation for the composer keyboard handoff. Goal
	// continuation itself lives in the agent backend now; the UI only hands
	// the keyboard back when the visible conversation stops running. UI thread
	// only.
	runSawRunning      bool
	runObservedSession string

	// Composer command feedback: the last error line and the /compact loading
	// state (a success is a transient toast, never a resident badge).
	commandErr string
	// Known backend failures (no model / no workspace roots / unconfigured
	// web_search) are mapped to a localized heading plus a recovery action
	// instead of the raw Go text: commandRecovery is the heading,
	// commandRecoveryKind selects the action (model/workspace/search) and
	// commandDetail keeps the raw text as selectable technical detail.
	commandRecovery     string
	commandRecoveryKind string
	commandDetail       string
	compacting          bool
	helpOpen            bool

	// syncCommandLocale re-registers the slash commands when the host language
	// changes (nil when the module failed to register them, which cannot happen
	// for a valid activation). commandLocale is the language the registry was
	// last built for, so the frame builder only syncs on an actual change.
	syncCommandLocale func()
	commandLocale     string

	// Memory management state: the clear-all confirmation dialog and the
	// destructive action waiting for its confirmation (the ID of the memory
	// to delete, or "tidy" for the duplicate tidy).
	memoryClearOpen bool
	memoryConfirm   string

	// Command-dialog state (native Modals rendered by the session screen).
	// goalCapText is the edit dialog's round-cap field (empty = keep the
	// current cap), not used when creating a goal.
	goalOpen    bool
	goalText    string
	goalCapText string
	goalErr     string
	planOpen    bool
	planDraft   string
	planErr     string
	memOpen     bool
	memDraft    string
	memErr      string
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

// english reports whether the current frame renders English copy.
func (st *state) english() bool { return uikit.Language(st.locale) == "en" }

// openSettingsPage opens the settings surface on one page through the host's
// optional deep-link capability (contracts.Navigator is extended by the
// OpenSettingsPage interface). The fallback toggles the surface as before.
func openSettingsPage(navigator contracts.Navigator, pageID string) {
	if navigator == nil {
		return
	}
	if opener, ok := navigator.(interface{ OpenSettingsPage(string) error }); ok {
		if err := opener.OpenSettingsPage(pageID); err == nil {
			return
		}
	}
	navigator.ToggleSettings()
}

// Settings page identities the conversation links to (the settings module's
// public PageID/BehaviorPageID/MemoryPageID values).
const (
	settingsPageModel    = "model"
	settingsPageBehavior = "behavior"
	settingsPageMemory   = "memory"
	settingsPageSearch   = "search"
)

// toast raises one transient host toast (nil-safe). Transient feedback —
// export/stop/compaction/goal outcomes, copy confirmations — goes here instead
// of a permanent header badge.
func (st *state) toast(level, text string) {
	if st == nil || st.notifier == nil || strings.TrimSpace(text) == "" {
		return
	}
	st.notifier.Notify(level, text)
}

// notifyUndo raises a toast with one undo action when the host's notifier
// implements contracts.ActionNotifier (the product's recoverable-destructive
// pattern), and a plain toast otherwise.
func (st *state) notifyUndo(text, actionLabel string, action func()) {
	if st == nil || st.notifier == nil {
		return
	}
	if actionNotifier, ok := st.notifier.(contracts.ActionNotifier); ok && action != nil {
		actionNotifier.NotifyAction(contracts.NotifySuccess, text, actionLabel, action)
		return
	}
	st.notifier.Notify(contracts.NotifySuccess, text)
}

// Module registers the conversation screen.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		navigator, err := ctx.Require(contracts.KeyNavigator)
		if err != nil {
			return err
		}
		chat, err := ctx.Require(contracts.KeyChat)
		if err != nil {
			return err
		}
		config := cfg.resolved()
		locale, _ := ctx.Get(contracts.KeyLocale)
		settings, _ := ctx.Get(contracts.KeySettings)
		planReviewer, _ := ctx.Get(contracts.KeyPlanReviewer)
		memoryStore, _ := ctx.Get(contracts.KeyMemory)
		notifier, _ := ctx.Get(contracts.KeyNotifier)
		moduleState := &state{
			locale:               locale,
			runtime:              runtime,
			lastTab:              -1,
			navigator:            navigator,
			settings:             settings,
			planReviewer:         planReviewer,
			memoryStore:          memoryStore,
			notifier:             notifier,
			thinkOpen:            make(map[string]bool),
			toolOpen:             make(map[string]bool),
			processOpen:          make(map[string]bool),
			thumbnails:           make(map[string]*ui.Bitmap),
			interruptedDismissed: make(map[string]bool),
			chatFollowEnd:        true,
			ctx:                  ctx,
		}

		// The context inspector is optional: without it the Context tab is not
		// registered at all (a host that records no context data stays exactly as
		// it was). The folded view itself is read lazily on the tab/session.
		if inspector, ok := ctx.Get(contracts.KeyContext); ok && inspector != nil {
			moduleState.contextService = inspector
		}

		// The backend-owned prompt inbox is optional: the composer reads the
		// active session's queue through it, and a host without it keeps the
		// old running-conflict behavior (the composer hides the queue control).
		if queue, ok := chat.(contracts.PromptQueue); ok && queue != nil {
			moduleState.promptQueue = queue
		}

		// Session changes arrive on the agent goroutine; hop to the UI thread
		// and request a frame. The unsubscribe is kernel-effect owned. Draining
		// the queued prompts is the agent backend's job (one prompt per settle,
		// before any automatic goal round); the UI only repaints.
		unsubscribe, err := chat.Subscribe(func(sessionID string) {
			runtime.Post(func() {
				// The context inspector has no change subscription: any chat
				// change (a model call, a settle) can move the folded view, so a
				// visible Context tab re-reads it on the next frame.
				moduleState.contextDirty = true
				if observeRunSettledForFocus(chat, moduleState) {
					// The settle is observed here first (before the next screen
					// build), so request the composer keyboard handoff now: the
					// next composer frame consumes the one-shot flag. Outside the
					// conversation tab the focus stays where the user put it.
					if moduleState.tab == tabChat && strings.TrimSpace(chat.ActiveSession()) == sessionID {
						moduleState.focusComposer = true
					}
				}
				runtime.Invalidate()
			})
		})
		if err != nil {
			return err
		}
		if err := own(ctx, func() (func(), error) { return unsubscribe, nil }); err != nil {
			return err
		}

		// Dropped files (the native window's drop) stage as composer
		// attachments, exactly like the native picker's selection.
		if drop, ok := ctx.Get(contracts.KeyFileDrop); ok {
			drop.SetHandler(func(paths []string) {
				runtime.Post(func() {
					stageAttachmentFiles(moduleState, paths)
					runtime.Invalidate()
				})
			})
			if err := own(ctx, func() (func(), error) {
				return func() { drop.SetHandler(nil) }, nil
			}); err != nil {
				return err
			}
		}

		// The per-session durable goal backend is optional: the composer dock
		// shows the ACTIVE conversation's goal, and every lifecycle action is a
		// direct human GoalService call. A change for the visible conversation
		// refreshes the cached snapshot; changes to other sessions only request
		// a frame (switching conversations re-reads the goal, so no other
		// session's goal is ever cached or displayed).
		if goalService, ok := ctx.Get(contracts.KeyGoal); ok && goalService != nil {
			moduleState.goalService = goalService
			unsubscribeGoals, err := goalService.Subscribe(func(sessionID string) {
				runtime.Post(func() {
					if moduleState.goalService != nil && sessionID == moduleState.goalSessionID {
						moduleState.refreshGoalSnapshot()
					}
					runtime.Invalidate()
				})
			})
			if err != nil {
				return err
			}
			if err := own(ctx, func() (func(), error) {
				return unsubscribeGoals, nil
			}); err != nil {
				return err
			}
		}

		if err := own(ctx, func() (func(), error) {
			return runtime.RegisterScreen(contracts.ScreenSpec{
				ID:    ScreenID,
				Title: config.Title,
				Order: 20,
				Build: func(c *ui.Context) {
					buildScreen(c, runtime, navigator, chat, config, moduleState)
				},
			})
		}); err != nil {
			return err
		}
		// The conversation's chrome lives in the host's B3 top band: the title and
		// workspace (SlotScreenHeader), the running/team/actions entries
		// (SlotScreenActions) and the Chat/Trajectory/Memory tabs (SlotScreenTabs).
		// The item IDs are screen-namespaced so the host keeps them off the other
		// screens. (The goal strip and the approval banner dock above the
		// composer; the banner slot itself is rendered by buildScreen.)
		screenSlots := []contracts.SlotItem{
			{
				Slot: contracts.SlotScreenHeader, ID: "session.title", Order: 10,
				Build: func(c *ui.Context) {
					buildScreenHeaderSlot(c, design.Of(c), moduleState, chat, config)
				},
			},
			{
				Slot: contracts.SlotScreenActions, ID: "session.actions", Order: 10,
				Build: func(c *ui.Context) {
					buildScreenActionsSlot(c, design.Of(c), runtime, moduleState, chat, config)
				},
			},
			{
				Slot: contracts.SlotScreenTabs, ID: "session.tabs", Order: 10,
				Build: func(c *ui.Context) {
					buildScreenTabsSlot(c, design.Of(c), config, moduleState)
				},
			},
		}
		for _, item := range screenSlots {
			item := item
			if err := own(ctx, func() (func(), error) {
				return runtime.RegisterSlotItem(item)
			}); err != nil {
				return err
			}
		}
		commands := newCommandRegistry(runtime, navigator, chat, config, moduleState)
		if err := commands.start(); err != nil {
			commands.close()
			return err
		}
		if err := own(ctx, func() (func(), error) { return commands.close, nil }); err != nil {
			commands.close()
			return err
		}
		moduleState.syncCommandLocale = commands.sync
		return nil
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyNavigator)).
		AddRequires(module.Requires(contracts.KeyChat)).
		WithName("ZeoKeel conversation screen")
}

// workStepsPolicy is the transcript's work-steps presentation for one frame:
// how much of a turn's steps show while it runs and when a finished turn folds.
// It is read from the optional settings store every frame, so a settings change
// applies on the next frame without a restart.
type workStepsPolicy struct {
	// mode is one of contracts.WorkStepsCompact/Standard/Detailed/Verbose.
	mode string
	// collapse is contracts.WorkStepsCollapseCompletion (fold when the turn
	// finishes) or contracts.WorkStepsCollapseNextInput (keep the latest turn
	// open until the next user message).
	collapse string
}

// workStepsPolicyFor resolves the current work-steps policy. Empty, unknown or
// missing settings normalize to the product default: standard mode and folding
// on completion (contracts.NormalizeWorkSteps*).
func (st *state) workStepsPolicyFor() workStepsPolicy {
	policy := workStepsPolicy{
		mode:     contracts.WorkStepsStandard,
		collapse: contracts.WorkStepsCollapseCompletion,
	}
	if st == nil || st.settings == nil {
		return policy
	}
	policy.mode = contracts.NormalizeWorkStepsMode(
		st.settings.Get(contracts.SettingWorkStepsMode, ""))
	policy.collapse = contracts.NormalizeWorkStepsCollapse(
		st.settings.Get(contracts.SettingWorkStepsCollapse, ""))
	return policy
}

// permissionMode reads the effective agent permission mode from the settings
// store: exactly read-only, workspace (the default) or full. Unset, unknown and
// retired "ask"/"never" values normalize through contracts.NormalizePermissionMode
// (both retire to read-only); a missing store resolves to the workspace default.
func permissionMode(settings contracts.SettingsService) string {
	if settings == nil {
		return contracts.PermissionWorkspace
	}
	return contracts.NormalizePermissionMode(settings.Get(contracts.SettingAgentPermission, ""))
}

// nextPermissionMode cycles 只读 → 工作区可写 → 所有权限 → 只读.
func nextPermissionMode(mode string) string {
	switch mode {
	case contracts.PermissionReadOnly:
		return contracts.PermissionWorkspace
	case contracts.PermissionWorkspace:
		return contracts.PermissionFull
	default:
		return contracts.PermissionReadOnly
	}
}

// cyclePermissionMode advances the stored permission mode by one step.
func cyclePermissionMode(st *state) {
	if st.settings == nil {
		return
	}
	_ = st.settings.Set(contracts.SettingAgentPermission,
		nextPermissionMode(permissionMode(st.settings)))
}

// permissionLabel renders one permission mode for the composer menu and badge.
func (st *state) permissionLabel(mode string) string {
	switch mode {
	case contracts.PermissionReadOnly:
		return st.tr("只读", "Read-only")
	case contracts.PermissionFull:
		return st.tr("所有权限", "All permissions")
	default:
		return st.tr("工作区可写", "Workspace-write")
	}
}

// sessionCommands is the conversation screen's slash-command surface: new
// session, fork, export, the permission-mode cycle and settings. The command palette reads these from the
// UIRuntime command registry; Enabled callbacks expose only the actions that
// the current session/backends support.
func sessionCommands(runtime contracts.UIRuntime, navigator contracts.Navigator,
	chat contracts.ChatService, config Config, st *state) []contracts.CommandSpec {
	_, forkerOK := chat.(contracts.SessionForker)
	activeSession := func() (contracts.ChatSession, bool) {
		id := chat.ActiveSession()
		if id == "" {
			return contracts.ChatSession{}, false
		}
		return chat.Session(id)
	}
	commands := []contracts.CommandSpec{
		{
			ID:    "zeokeel.session.new",
			Title: st.tr("新会话", "New conversation"),
			Hint:  st.tr("新建并打开一个会话", "Start and open a new conversation"),
			Order: 20,
			Run: func() error {
				if _, err := chat.NewSession(""); err != nil {
					return err
				}
				_ = navigator.Push(ScreenID)
				runtime.Invalidate()
				return nil
			},
		},
		{
			ID:    "zeokeel.session.fork",
			Title: st.tr("创建副本", "Duplicate conversation"),
			Hint:  st.tr("复制当前会话", "Copy the current conversation"),
			Order: 30,
			Enabled: func() bool {
				if !forkerOK {
					return false
				}
				_, ok := activeSession()
				return ok
			},
			Run: func() error {
				forker, ok := chat.(contracts.SessionForker)
				if !ok {
					return nil
				}
				session, ok := activeSession()
				if !ok {
					return nil
				}
				newID, err := forker.ForkSession(session.ID)
				if err != nil {
					return err
				}
				if strings.TrimSpace(newID) != "" {
					if err := chat.SetActiveSession(newID); err != nil {
						return err
					}
				}
				_ = navigator.Push(ScreenID)
				runtime.Invalidate()
				return nil
			},
		},
		{
			ID:    "zeokeel.session.export",
			Title: st.tr("导出对话", "Export conversation"),
			Hint:  st.tr("导出当前会话为 Markdown", "Export the current conversation as Markdown"),
			Order: 40,
			Enabled: func() bool {
				_, ok := activeSession()
				return ok
			},
			Run: func() error {
				session, ok := activeSession()
				if !ok {
					return nil
				}
				exportSession(st, session, config)
				return nil
			},
		},
		{
			ID:      "zeokeel.session.compact",
			Title:   st.tr("压缩上下文", "Compact context"),
			Hint:    st.tr("压缩当前会话的历史，节省上下文预算", "Compact the conversation history to save context budget"),
			Order:   47,
			Enabled: func() bool { return compactCommandEnabled(chat, st) },
			Run:     func() error { runCompactCommand(chat, st, runtime); return nil },
		},
		{
			ID:      "zeokeel.session.goal",
			Title:   st.tr("设置目标", "Set goal"),
			Hint:    st.tr("为当前会话创建或编辑长期目标", "Create or edit a long-term goal for this conversation"),
			Order:   60,
			Enabled: func() bool { return st.goalService != nil && chat.ActiveSession() != "" },
			Run:     func() error { openGoalDialog(st, runtime); return nil },
		},
		{
			ID:    "zeokeel.session.goal.complete",
			Title: st.tr("完成目标", "Complete goal"),
			Hint:  st.tr("将当前目标标记为已完成", "Mark the current goal as completed"),
			Order: 64,
			Enabled: func() bool {
				return st.goalService != nil && goalNonTerminal(st)
			},
			Run: func() error {
				completeGoalAction(st, runtime)
				return nil
			},
		},
		{
			ID:    "zeokeel.session.goal.clear",
			Title: st.tr("清除目标", "Clear goal"),
			Hint:  st.tr("删除当前目标及其记录（需确认）", "Delete the current goal and its record (asks first)"),
			Order: 65,
			Enabled: func() bool {
				return st.goalService != nil && st.goal != nil
			},
			Run: func() error {
				st.goalConfirm = "clear"
				runtime.Invalidate()
				return nil
			},
		},
		{
			ID:      "zeokeel.session.plan",
			Title:   st.tr("审阅计划", "Review plan"),
			Hint:    st.tr("提交一份计划给用户批准", "Submit a plan for the user to approve"),
			Order:   61,
			Enabled: func() bool { return st.planReviewer != nil && chat.ActiveSession() != "" },
			Run:     func() error { openPlanDialog(st, runtime); return nil },
		},
		{
			ID:      "zeokeel.session.memory",
			Title:   st.tr("添加记忆", "Add memory"),
			Hint:    st.tr("保存一条全局记忆", "Save a global memory"),
			Order:   62,
			Enabled: func() bool { return st.memoryStore != nil },
			Run:     func() error { openMemoryDialog(st, runtime); return nil },
		},
		{
			ID:      "zeokeel.session.permission",
			Title:   st.tr("权限模式", "Permission mode"),
			Hint:    st.tr("切换权限模式：只读 → 工作区可写 → 所有权限", "Cycle permission: read-only → workspace-write → all permissions"),
			Order:   45,
			Enabled: func() bool { return st.settings != nil },
			Run: func() error {
				cyclePermissionMode(st)
				runtime.Invalidate()
				return nil
			},
		},
		{
			ID: "zeokeel.session.settings",
			// The title matches the home module's "设置" command: the palette
			// de-duplicates by Title, so the same action is listed once.
			Title: st.tr("设置", "Settings"),
			Hint:  st.tr("打开设置面板", "Open the settings panel"),
			Order: 50,
			Run: func() error {
				navigator.ToggleSettings()
				return nil
			},
		},
		{
			ID:    "zeokeel.session.stop",
			Title: st.tr("停止", "Stop"),
			Hint:  st.tr("停止当前生成的回复", "Stop the current reply"),
			Order: 44,
			Enabled: func() bool {
				session, ok := activeSession()
				return ok && session.Running
			},
			Run: func() error {
				session, ok := activeSession()
				if !ok {
					return nil
				}
				// A stop must stop: the agent backend disarms the goal when the
				// human stops (the dock then offers 继续目标) and owns the prompt
				// inbox, so a stop never clears the queued prompts; their drain
				// is the backend's decision.
				if err := chat.Cancel(session.ID); err != nil {
					st.commandErr = st.tr("停止失败：", "Stop failed: ") + err.Error()
				} else {
					st.commandErr = ""
					st.toast(contracts.NotifySuccess, st.tr("已停止", "Stopped"))
				}
				runtime.Invalidate()
				return nil
			},
		},
		{
			ID:    "zeokeel.session.attach",
			Title: st.tr("附加文件", "Attach files"),
			Hint:  st.tr("添加文件或图片到输入框", "Add files or images to the input"),
			Order: 46,
			Run: func() error {
				if _, ok := chat.(contracts.AttachmentSender); !ok {
					st.attachErr = st.tr("当前后端不支持附件", "This backend does not support attachments")
					runtime.Invalidate()
					return nil
				}
				startAttachmentPick(runtime, st)
				return nil
			},
		},
		{
			ID:    "zeokeel.session.help",
			Title: st.tr("帮助", "Help"),
			Hint:  st.tr("查看可用命令与快捷键", "Show the available commands and shortcuts"),
			Order: 55,
			Run: func() error {
				st.helpOpen = true
				runtime.Invalidate()
				return nil
			},
		},
	}
	return commands
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// sendWhileRunningEnabled reports whether Enter queues the prompt while the
// active turn still runs. Queueing is a fixed behavior (A4): the old
// ui.sendWhileRunning Settings control was removed, so the stored row is no
// longer read and the composer always offers the queue action during a run. The
// settings parameter is kept for the existing call site.
func sendWhileRunningEnabled(_ contracts.SettingsService) bool {
	return true
}

// composerDraft is one conversation's unsent composer state. Attachments are
// the staged files (their bytes) and attachErr keeps a per-conversation staging
// error with the draft it belongs to.
type composerDraft struct {
	text        string
	attachments []contracts.Attachment
	attachErr   string
}

// saveComposerDraft stores the live composer state under sessionID and clears
// it, so the caller can load another conversation's state without copying the
// previous one over it. An empty draft is dropped instead of cached.
func (st *state) saveComposerDraft(sessionID string) {
	if sessionID == "" {
		return
	}
	if strings.TrimSpace(st.draft) == "" && len(st.pendingAttachments) == 0 && st.attachErr == "" {
		if st.composerDrafts != nil {
			delete(st.composerDrafts, sessionID)
		}
		return
	}
	if st.composerDrafts == nil {
		st.composerDrafts = make(map[string]composerDraft)
	}
	st.composerDrafts[sessionID] = composerDraft{
		text:        st.draft,
		attachments: st.pendingAttachments,
		attachErr:   st.attachErr,
	}
	st.draft = ""
	st.pendingAttachments = nil
	st.attachErr = ""
}

// loadComposerDraft restores sessionID's saved composer state (nothing for a
// fresh conversation) into the live fields.
func (st *state) loadComposerDraft(sessionID string) {
	st.draft = ""
	st.pendingAttachments = nil
	st.attachErr = ""
	if st.composerDrafts == nil {
		return
	}
	saved, ok := st.composerDrafts[sessionID]
	if !ok {
		return
	}
	st.draft = saved.text
	st.pendingAttachments = saved.attachments
	st.attachErr = saved.attachErr
	delete(st.composerDrafts, sessionID)
}

// withComposerSession applies fn to the composer state that belongs to
// sessionID. When that is the active conversation fn runs directly; otherwise
// the live state is stashed, the composer is temporarily reset for fn (an
// attachment picker opened before a conversation switch must not stage its
// result into the newly opened conversation), the updated state is saved back
// and the live state restored.
func (st *state) withComposerSession(sessionID string, fn func()) {
	if fn == nil || sessionID == "" || st.draftSession == sessionID {
		fn()
		return
	}
	currentID := st.draftSession
	if currentID == "" {
		st.draft = ""
		st.pendingAttachments = nil
		st.attachErr = ""
	} else {
		st.saveComposerDraft(currentID)
	}
	fn()
	st.saveComposerDraft(sessionID)
	st.loadComposerDraft(currentID)
}

// syncComposerDraft moves the live composer state onto the conversation it
// belongs to and restores the one being opened: the composer draft and staged
// attachments are per-conversation, so text typed in one conversation can never
// be sent to another. It runs at frame start before the composer builds.
func (st *state) syncComposerDraft(activeID string) {
	if st.draftSession == activeID {
		return
	}
	st.saveComposerDraft(st.draftSession)
	st.loadComposerDraft(activeID)
	st.draftSession = activeID
}

// queuedPromptEntries returns one session's backend inbox in send order (nil
// without a PromptQueue backend or for another session). The UI never caches
// it: every frame reads the backend so a drain (the agent sends the next prompt
// when a run settles) or a removal is visible immediately.
func (st *state) queuedPromptEntries(sessionID string) []contracts.QueuedPrompt {
	if st == nil || st.promptQueue == nil || sessionID == "" {
		return nil
	}
	return st.promptQueue.QueuedPrompts(sessionID)
}

// queuedPromptTexts renders the inbox for the shared queue popover: the prompt
// body plus its local send time, kept in backend order (oldest first).
func (st *state) queuedPromptTexts(sessionID string) []string {
	prompts := st.queuedPromptEntries(sessionID)
	texts := make([]string, 0, len(prompts))
	for _, prompt := range prompts {
		text := strings.TrimSpace(prompt.Text)
		if clock := messageClock(st.locale, prompt.CreatedAt); clock != "" {
			text += " · " + clock
		}
		texts = append(texts, text)
	}
	return texts
}

// sessionRunning reports whether a session currently streams a run (the read
// model the composer uses to decide between sending and enqueueing).
func sessionRunning(chat contracts.ChatService, sessionID string) bool {
	if chat == nil || sessionID == "" {
		return false
	}
	session, ok := chat.Session(sessionID)
	return ok && session.Running
}

// enqueueDraft hands the trimmed composer text to the backend inbox, clears the
// draft (staged attachments stay), and returns false for an empty queue or a
// failed enqueue (the failure stays on the composer). The backend owns the
// queue, so this only requests the append; the badge/popover update on the next
// frame from QueuedPrompts.
func enqueueDraft(st *state, sessionID string) bool {
	if st == nil || st.promptQueue == nil || sessionID == "" {
		return false
	}
	prompt := strings.TrimSpace(st.draft)
	if prompt == "" {
		return false
	}
	if _, err := st.promptQueue.EnqueuePrompt(context.Background(), sessionID, prompt); err != nil {
		st.reportComposerFailure(err)
		return false
	}
	st.clearComposerFailure()
	st.queueActionErr = ""
	st.draft = ""
	st.composerCaret = 0
	st.toast(contracts.NotifySuccess, st.tr("已加入排队", "Added to the queue"))
	return true
}

// removeQueuedPrompt removes one backend inbox entry by index (the popover's
// per-row remove). The index addresses the current backend order; a failure is
// shown in the popover.
func removeQueuedPrompt(st *state, sessionID string, index int) {
	if st == nil || st.promptQueue == nil {
		return
	}
	prompts := st.queuedPromptEntries(sessionID)
	if index < 0 || index >= len(prompts) {
		return
	}
	if err := st.promptQueue.RemoveQueuedPrompt(context.Background(), sessionID, prompts[index].ID); err != nil {
		st.queueActionErr = st.tr("移除排队消息失败：", "Could not remove the queued message: ") + err.Error()
		return
	}
	st.queueActionErr = ""
}

// clearQueuedPrompts removes every entry of one session's backend inbox (the
// popover's clear-all); the backend stays the source of truth.
func clearQueuedPrompts(st *state, sessionID string) {
	if st == nil || st.promptQueue == nil {
		return
	}
	prompts := st.queuedPromptEntries(sessionID)
	for _, prompt := range prompts {
		if err := st.promptQueue.RemoveQueuedPrompt(context.Background(), sessionID, prompt.ID); err != nil {
			st.queueActionErr = st.tr("清空排队消息失败：", "Could not clear the queued messages: ") + err.Error()
			return
		}
	}
	st.queueActionErr = ""
	st.queueMenuOpen = false
}

// deliverPrompt sends one text prompt, falling to the backend inbox when the
// session already runs. A host without contracts.PromptQueue keeps the old
// behavior (Send reports the running conflict to the composer).
func deliverPrompt(chat contracts.ChatService, st *state, sessionID, prompt string) error {
	if st != nil && st.promptQueue != nil && sessionRunning(chat, sessionID) {
		_, err := st.promptQueue.EnqueuePrompt(context.Background(), sessionID, prompt)
		return err
	}
	if err := chat.Send(sessionID, prompt); err != nil {
		// The run may have started between the read-model check and Send (the
		// frame can lag a settle): the backend's running conflict then belongs
		// in its inbox instead of surfacing as a spurious send failure.
		if st != nil && st.promptQueue != nil && errors.Is(err, contracts.ErrSessionRunning) {
			if _, enqueueErr := st.promptQueue.EnqueuePrompt(context.Background(), sessionID, prompt); enqueueErr == nil {
				return nil
			}
		}
		return err
	}
	return nil
}

// observeRunSettledForFocus tracks the active conversation's run phase and
// reports the transition running=true -> running=false exactly once, so the
// composer can take the keyboard back when the reply is done. Goal
// continuation itself runs in the agent backend (DSH goal rounds): the UI no
// longer sends continuation prompts.
func observeRunSettledForFocus(chat contracts.ChatService, st *state) bool {
	if chat == nil || st == nil {
		return false
	}
	activeID := strings.TrimSpace(chat.ActiveSession())
	if activeID != st.runObservedSession {
		st.runObservedSession = activeID
		st.runSawRunning = false
	}
	session, ok := chat.Session(activeID)
	running := ok && session.Running
	if running {
		st.runSawRunning = true
		return false
	}
	if st.runSawRunning {
		st.runSawRunning = false
		return true
	}
	return false
}
