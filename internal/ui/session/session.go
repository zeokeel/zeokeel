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
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
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

	// expandedActions is the message whose rating actions the ⋯ entry unfolded.
	// usageOpen holds the expanded usage details by message ID.
	expandedActions string
	usageOpen       map[string]bool
	// headerWidth is the header row's width from the last laid-out frame: when
	// the conversation column gets narrow (e.g. the right inspector docks at
	// 1024), the title must keep room and the goal chip drops its text.
	headerWidth float32

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
	// error shown in DangerText and the cached read model. The cache is
	// refreshed only when the query changes or a mutation asks for it, so the
	// frame builder never reads the store while nothing changed.
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

	// @ suggestion cache: the workspace files the current query resolves to,
	// refreshed only when the query changes. The frame builder never lists or
	// searches the workspace.
	mentionFiles []contracts.FileEntry
	mentionErr   error
	mentionQuery string
	mentionFresh bool

	// Export state: the last export error shown under the session header.
	// Successes are transient host toasts, never a permanent header badge.
	exportErr string

	// regenerateErr is the last message-level 「重新生成」 failure shown under
	// the last assistant turn.
	regenerateErr string

	// feedbackErr is the last good/bad feedback write failure shown under the
	// assistant action row.
	feedbackErr string

	// /feedback dialog target and state.
	feedbackOpen        bool
	feedbackSessionID   string
	feedbackMessageID   string
	feedbackMessageText string
	feedbackCurrent     string
	feedbackDialogErr   string

	// goal is the active goal shown in the header (nil when none); goalTracker
	// is the optional host bridge used to clear it. Both are UI-thread state.
	goal        *contracts.Goal
	goalTracker contracts.GoalTracker
	// goalActionErr is the last goal-lifecycle failure (DangerText row).
	// Successes go to a transient toast. goalConfirm is the destructive action
	// ("abandon"/"clear") waiting for its AlertDialog confirmation.
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

	// copiedMessage is the assistant message whose copy action just ran; the
	// row shows a check icon while it is the last one copied (transient state,
	// never a resident badge).
	copiedMessage string

	// queuedPrompts holds, per session ID, the prompts submitted while a turn
	// was running (only when ui.sendWhileRunning is on). The queue belongs to
	// its session: switching conversations never drops it, and the module-level
	// chat subscription flushes it when that session's run ends, whichever
	// screen is visible. queuePaused marks a session's queue paused after the
	// user chose 停止 (no automatic send; the queue popover can send or clear
	// it). queueErr is the last delivery failure per session. UI thread only.
	queuedPrompts map[string][]string
	queuePaused   map[string]bool
	queueErr      map[string]string
	// queueMenuOpen is the active session's queued-prompts popover
	// (view/cancel/send/clear). queueList backs that popover's official ui.List
	// of queued prompts.
	queueMenuOpen bool
	queueList     ui.ListState

	// Goal auto-continue state: the per-goal send budget, the goal it belongs
	// to, the last observed run phase and the last delivery error. UI thread
	// only.
	autoContinueCount      int
	lastAutoGoalID         string
	autoContinueErr        string
	autoContinueSuppressed bool
	autoContinueSawRunning bool
	autoContinueSessionID  string

	// Composer command feedback: the last error line and the /compact loading
	// state (a success is a transient toast, never a resident badge).
	commandErr string
	compacting bool
	helpOpen   bool

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
	goalOpen  bool
	goalText  string
	goalErr   string
	planOpen  bool
	planDraft string
	planErr   string
	memOpen   bool
	memDraft  string
	memErr    string

	// /feedback dialog: the message being rated and the rating chosen in the
	// dialog before it is saved (empty = no rating).
	feedbackRating string
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
			locale:        locale,
			runtime:       runtime,
			lastTab:       -1,
			navigator:     navigator,
			settings:      settings,
			planReviewer:  planReviewer,
			memoryStore:   memoryStore,
			notifier:      notifier,
			thinkOpen:     make(map[string]bool),
			toolOpen:      make(map[string]bool),
			usageOpen:     make(map[string]bool),
			thumbnails:    make(map[string]*ui.Bitmap),
			queuedPrompts: make(map[string][]string),
			queuePaused:   make(map[string]bool),
			queueErr:      make(map[string]string),
			chatFollowEnd: true,
			ctx:           ctx,
		}

		// Session changes arrive on the agent goroutine; hop to the UI thread
		// and request a frame. The unsubscribe is kernel-effect owned.
		unsubscribe, err := chat.Subscribe(func(sessionID string) {
			runtime.Post(func() {
				// Queue flush runs before goal auto-continue: a queued prompt
				// starts the next run, so auto-continue waits for its settle.
				processQueuedPrompts(chat, moduleState, sessionID)
				if observeRunSettled(chat, moduleState) {
					// The settle is observed here first (before the next screen
					// build), so request the composer keyboard handoff now: the
					// next composer frame consumes the one-shot flag. Outside the
					// conversation tab the focus stays where the user put it.
					if moduleState.tab == tabChat && strings.TrimSpace(chat.ActiveSession()) == sessionID {
						moduleState.focusComposer = true
					}
					handleRunSettled(chat, moduleState, runtime)
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

		// The host goal tracker is optional: the session header shows the
		// active goal and backs its clear action through this bridge.
		if tracker, ok := ctx.Get(contracts.KeyGoalTracker); ok && tracker != nil {
			moduleState.goalTracker = tracker
			tracker.SetHandler(func(goal contracts.Goal, ok bool) {
				runtime.Post(func() {
					if ok {
						copied := goal
						moduleState.goal = &copied
						if copied.ID != moduleState.lastAutoGoalID {
							moduleState.lastAutoGoalID = copied.ID
							moduleState.autoContinueCount = 0
						}
					} else {
						moduleState.goal = nil
						moduleState.lastAutoGoalID = ""
						moduleState.autoContinueCount = 0
					}
					runtime.Invalidate()
				})
			})
			if err := own(ctx, func() (func(), error) {
				return func() { tracker.SetHandler(nil) }, nil
			}); err != nil {
				return err
			}
			if id, text, status, ok := tracker.Get(); ok {
				goal := contracts.Goal{ID: id, Text: text, Status: status}
				moduleState.goal = &goal
				moduleState.lastAutoGoalID = id
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

// permissionMode reads the effective agent permission mode from the settings
// store. The default is workspace (normal in-workspace editing without asking);
// unset/unknown values and a missing store resolve to it too.
func permissionMode(settings contracts.SettingsService) string {
	if settings == nil {
		return contracts.PermissionWorkspace
	}
	switch strings.ToLower(strings.TrimSpace(settings.Get(contracts.SettingAgentPermission, ""))) {
	case contracts.PermissionWorkspace:
		return contracts.PermissionWorkspace
	case contracts.PermissionFull:
		return contracts.PermissionFull
	case contracts.PermissionNever:
		return contracts.PermissionNever
	case contracts.PermissionAsk:
		return contracts.PermissionAsk
	default:
		return contracts.PermissionWorkspace
	}
}

// nextPermissionMode cycles ask → workspace → full → ask.
func nextPermissionMode(mode string) string {
	switch mode {
	case contracts.PermissionAsk:
		return contracts.PermissionWorkspace
	case contracts.PermissionWorkspace:
		return contracts.PermissionFull
	default:
		return contracts.PermissionAsk
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

// permissionLabel renders one permission mode for the composer badge.
func (st *state) permissionLabel(mode string) string {
	switch mode {
	case contracts.PermissionWorkspace:
		return st.tr("工作区", "Workspace")
	case contracts.PermissionFull:
		return st.tr("全部允许", "Allow all")
	case contracts.PermissionNever:
		return st.tr("从不允许", "Never")
	default:
		return st.tr("询问", "Ask")
	}
}

// goalLifecycle returns the optional goal lifecycle half of the host bridge.
func goalLifecycle(st *state) (contracts.GoalLifecycler, bool) {
	if st == nil || st.goalTracker == nil {
		return nil, false
	}
	lifecycle, ok := st.goalTracker.(contracts.GoalLifecycler)
	if !ok || lifecycle == nil {
		return nil, false
	}
	return lifecycle, true
}

// goalCompletable reports whether the current goal is in an active/paused state
// (the only states complete/abandon accept).
func goalCompletable(st *state) bool {
	if st == nil || st.goalTracker == nil {
		return false
	}
	_, _, status, ok := st.goalTracker.Get()
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case contracts.GoalStatusActive, contracts.GoalStatusPaused:
		return true
	default:
		return false
	}
}

// sessionCommands is the conversation screen's slash-command surface: new
// session, fork, export, the permission-mode cycle and settings. The command palette reads these from the
// UIRuntime command registry; Enabled callbacks expose only the actions that
// the current session/backends support.
func sessionCommands(runtime contracts.UIRuntime, navigator contracts.Navigator,
	chat contracts.ChatService, config Config, st *state) []contracts.CommandSpec {
	_, forkerOK := chat.(contracts.SessionForker)
	_, feedbackerOK := chat.(contracts.SessionFeedbacker)
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
			Hint:    st.tr("为当前会话设置/清除长期目标", "Set or clear a long-term goal for this conversation"),
			Order:   60,
			Enabled: func() bool { return st.goalTracker != nil && chat.ActiveSession() != "" },
			Run:     func() error { openGoalDialog(st, runtime); return nil },
		},
		{
			ID:    "zeokeel.session.goal.complete",
			Title: st.tr("完成目标", "Complete goal"),
			Hint:  st.tr("将当前目标标记为已完成", "Mark the current goal as completed"),
			Order: 64,
			Enabled: func() bool {
				_, ok := goalLifecycle(st)
				return ok && goalCompletable(st)
			},
			Run: func() error {
				lifecycle, ok := goalLifecycle(st)
				if !ok {
					return nil
				}
				if lifecycle.Complete() {
					st.goalActionErr = ""
					st.toast(contracts.NotifySuccess, st.tr("目标已完成", "Goal completed"))
				} else {
					st.goalActionErr = st.tr("完成目标失败：", "Complete goal failed: ") + st.tr("目标不存在", "no active goal")
				}
				runtime.Invalidate()
				return nil
			},
		},
		{
			ID:    "zeokeel.session.goal.abandon",
			Title: st.tr("放弃目标", "Abandon goal"),
			Hint:  st.tr("放弃当前目标", "Abandon the current goal"),
			Order: 65,
			Enabled: func() bool {
				_, ok := goalLifecycle(st)
				return ok && goalCompletable(st)
			},
			Run: func() error {
				lifecycle, ok := goalLifecycle(st)
				if !ok {
					return nil
				}
				if lifecycle.Abandon() {
					st.goalActionErr = ""
					st.toast(contracts.NotifySuccess, st.tr("目标已放弃", "Goal abandoned"))
				} else {
					st.goalActionErr = st.tr("放弃目标失败：", "Abandon goal failed: ") + st.tr("目标不存在", "no active goal")
				}
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
			ID:    "zeokeel.session.feedback",
			Title: st.tr("反馈", "Feedback"),
			Hint:  st.tr("评价最近一条回答", "Rate the latest answer"),
			Order: 63,
			Enabled: func() bool {
				if !feedbackerOK {
					return false
				}
				_, message, ok := latestAssistantTarget(chat)
				return ok && strings.TrimSpace(message.ID) != ""
			},
			Run: func() error {
				openFeedbackDialog(st, runtime, chat)
				return nil
			},
		},
		{
			ID:      "zeokeel.session.permission",
			Title:   st.tr("权限模式", "Permission mode"),
			Hint:    st.tr("切换权限模式：询问 → 工作区 → 全部允许", "Cycle permission: ask → workspace → full"),
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
				st.autoContinueSuppressed = true
				// A stop must stop: pause the queued prompts instead of letting
				// the settle handler start another run automatically.
				pauseQueue(st, session.ID)
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

// sendWhileRunningEnabled normalizes ui.sendWhileRunning: queueing is on unless
// the row explicitly says "false". Missing, empty and malformed values keep the
// default (on) so the composer stays usable while a reply streams; only an
// explicit opt-out turns Enter-to-send off during a run.
func sendWhileRunningEnabled(settings contracts.SettingsService) bool {
	if settings == nil {
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(settings.Get(contracts.SettingSendWhileRunning, "")), "false")
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

// queuedFor returns one session's queued prompts (nil when none). The caller
// treats the slice as read-only.
func (st *state) queuedFor(sessionID string) []string {
	if st == nil {
		return nil
	}
	return st.queuedPrompts[sessionID]
}

// queueErrorFor returns the last queued-delivery failure of one session ("").
func (st *state) queueErrorFor(sessionID string) string {
	if st == nil {
		return ""
	}
	return st.queueErr[sessionID]
}

// setQueueError records/clears one session's queued-delivery failure.
func (st *state) setQueueError(sessionID, text string) {
	if st.queueErr == nil {
		st.queueErr = make(map[string]string)
	}
	if text == "" {
		delete(st.queueErr, sessionID)
		return
	}
	st.queueErr[sessionID] = text
}

// queueDraft appends the trimmed draft to the session's queue, clears the draft
// and returns false for empty text. Attachments are not queued. Queuing again
// resumes a queue the user paused with 停止.
func queueDraft(st *state, activeID string) bool {
	if st == nil || activeID == "" {
		return false
	}
	prompt := strings.TrimSpace(st.draft)
	if prompt == "" {
		return false
	}
	if st.queuedPrompts == nil {
		st.queuedPrompts = make(map[string][]string)
	}
	st.queuedPrompts[activeID] = append(st.queuedPrompts[activeID], prompt)
	delete(st.queuePaused, activeID)
	st.setQueueError(activeID, "")
	st.draft = ""
	return true
}

// pauseQueue marks a session's queued prompts paused after the user chose 停止:
// a stop must stop, so the queued prompts are kept for review but never start
// another run automatically. The queue popover offers an explicit send.
func pauseQueue(st *state, sessionID string) {
	if st == nil || sessionID == "" || len(st.queuedPrompts[sessionID]) == 0 {
		return
	}
	if st.queuePaused == nil {
		st.queuePaused = make(map[string]bool)
	}
	st.queuePaused[sessionID] = true
}

// resumeQueue clears the paused flag and delivers the next queued prompt; only
// the user's explicit "send queued" action calls it.
func resumeQueue(chat contracts.ChatService, st *state, sessionID string) {
	if st == nil {
		return
	}
	delete(st.queuePaused, sessionID)
	processQueuedPrompts(chat, st, sessionID)
}

// cancelQueuedPrompt removes one queued prompt of sessionID.
func cancelQueuedPrompt(st *state, sessionID string, index int) {
	if st == nil {
		return
	}
	queue := st.queuedPrompts[sessionID]
	if index < 0 || index >= len(queue) {
		return
	}
	queue = append(queue[:index], queue[index+1:]...)
	if len(queue) == 0 {
		delete(st.queuedPrompts, sessionID)
		delete(st.queuePaused, sessionID)
		st.queueMenuOpen = false
		return
	}
	st.queuedPrompts[sessionID] = queue
}

// clearQueuedPrompts drops one session's whole queue (the popover's 清空全部).
func clearQueuedPrompts(st *state, sessionID string) {
	if st == nil {
		return
	}
	delete(st.queuedPrompts, sessionID)
	delete(st.queuePaused, sessionID)
	st.setQueueError(sessionID, "")
	st.queueMenuOpen = false
}

// processQueuedPrompts sends the first queued prompt once the session it
// belongs to is no longer running. It runs from the module-level chat
// subscription (and the screen's fallback check), so it works whichever screen
// is visible, and a queue paused by 停止 waits for the user. A failed Send keeps
// the prompt and surfaces the danger line; the next notification retries it.
func processQueuedPrompts(chat contracts.ChatService, st *state, sessionID string) {
	if st == nil || chat == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || st.queuePaused[sessionID] {
		return
	}
	queue := st.queuedPrompts[sessionID]
	if len(queue) == 0 {
		return
	}
	session, ok := chat.Session(sessionID)
	if !ok || session.Running {
		return
	}
	prompt := queue[0]
	if err := chat.Send(sessionID, prompt); err != nil {
		st.setQueueError(sessionID, st.tr("排队发送失败：", "Queued send failed: ")+err.Error())
		return
	}
	st.setQueueError(sessionID, "")
	queue = queue[1:]
	if len(queue) == 0 {
		delete(st.queuedPrompts, sessionID)
		if strings.TrimSpace(chat.ActiveSession()) == sessionID {
			st.queueMenuOpen = false
		}
		return
	}
	st.queuedPrompts[sessionID] = queue
}

// maxGoalAutoContinues bounds the automatic continuation prompts for one goal.
const maxGoalAutoContinues = 5

// goalAutoContinueEnabled normalizes goal.autoContinue: only "true" enables it.
func goalAutoContinueEnabled(settings contracts.SettingsService) bool {
	if settings == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(settings.Get(contracts.SettingGoalAutoContinue, "")), "true")
}

// observeRunSettled tracks the active session's run phase and reports the
// transition running=true -> running=false exactly once. A new run clears the
// user-cancel suppression flag.
func observeRunSettled(chat contracts.ChatService, st *state) bool {
	if chat == nil || st == nil {
		return false
	}
	activeID := strings.TrimSpace(chat.ActiveSession())
	if activeID != st.autoContinueSessionID {
		st.autoContinueSessionID = activeID
		st.autoContinueSawRunning = false
	}
	session, ok := chat.Session(activeID)
	running := ok && session.Running
	if running {
		if !st.autoContinueSawRunning {
			st.autoContinueSuppressed = false
		}
		st.autoContinueSawRunning = true
		return false
	}
	if st.autoContinueSawRunning {
		st.autoContinueSawRunning = false
		return true
	}
	return false
}

// handleRunSettled flushes queued prompts before considering goal
// auto-continue. When a queued prompt was sent it starts the next run, so
// auto-continue waits for that run to end instead of racing it.
func handleRunSettled(chat contracts.ChatService, st *state, runtime contracts.UIRuntime) {
	if st == nil {
		return
	}
	if activeID := strings.TrimSpace(chat.ActiveSession()); activeID != "" && len(st.queuedFor(activeID)) > 0 {
		before := len(st.queuedFor(activeID))
		processQueuedPrompts(chat, st, activeID)
		if len(st.queuedFor(activeID)) < before {
			runtime.Invalidate()
			return
		}
		if st.queueErrorFor(activeID) != "" {
			runtime.Invalidate()
			return
		}
	}
	maybeAutoContinue(chat, st, runtime)
}

// maybeAutoContinue sends one bounded continuation prompt for the active goal
// after a run settles. The count is per goal ID, reset on a goal change and
// capped at maxGoalAutoContinues; failures count too so a broken backend cannot
// loop forever.
func maybeAutoContinue(chat contracts.ChatService, st *state, runtime contracts.UIRuntime) {
	if chat == nil || st == nil || st.autoContinueSuppressed {
		return
	}
	if !goalAutoContinueEnabled(st.settings) {
		return
	}
	tracker := st.goalTracker
	if tracker == nil {
		if fromContext, ok := st.ctx.Get(contracts.KeyGoalTracker); ok {
			tracker = fromContext
		}
	}
	if tracker == nil {
		return
	}
	id, text, status, ok := tracker.Get()
	text = strings.TrimSpace(text)
	if !ok || text == "" {
		return
	}
	if status != "" && !strings.EqualFold(strings.TrimSpace(status), "active") {
		return
	}
	activeID := strings.TrimSpace(chat.ActiveSession())
	if activeID == "" {
		return
	}
	if id != st.lastAutoGoalID {
		st.lastAutoGoalID = id
		st.autoContinueCount = 0
	}
	if st.autoContinueCount >= maxGoalAutoContinues {
		return
	}
	st.autoContinueCount++
	message := st.tr(
		"请继续推进目标："+text+"\n\n完成后请更新或清除目标。",
		"Continue working toward the goal: "+text+"\n\nUpdate or clear the goal when it is done.")
	if err := chat.Send(activeID, message); err != nil {
		st.autoContinueErr = st.tr("自动续跑发送失败：", "Auto-continue send failed: ") + err.Error()
		runtime.Invalidate()
		return
	}
	st.autoContinueErr = ""
	runtime.Invalidate()
}
