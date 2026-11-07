// Package session contributes the conversation screen: the main chat surface of
// ZeoKeel. It is a UI module (kernel Descriptor + host UI services) rendered
// with mygo's native controls and the brand design system (internal/design):
// TabsBase, Scroll, TextArea, Collapsible, Badge, Box, Row/Column, Text, buttons
// and icons. It never draws a widget (or Canvas) itself.
//
// The branded layout follows the retired shell: a title header with the
// Chat/Trajectory/Memory underline tabs, a centered transcript column with
// right-aligned raised user bubbles and plain assistant body text (reasoning in
// a collapsible, tool cards through UIRuntime.RenderTool), a raised composer
// card with a borderless multiline input and a hint/stop-send toolbar, and the
// tool timeline on the Trajectory tab.
//
// The module reads the chat read model from contracts.ChatService (owned by the
// agent backend module) and calls its operations on user actions:
//
//   - empty session: "new conversation" button -> NewSession + SetActiveSession
//   - navigator.Push(session);
//   - chat tab: messages, streaming text, collapsible reasoning, tool cards via
//     UIRuntime.RenderTool, errors;
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
	tab       int
	draft     string
	thinkOpen map[string]bool
	toolOpen  map[string]bool

	// Memory tab state: the search query, the add-input draft and the last
	// store error shown in DangerText. The store itself is looked up lazily
	// from the kernel context (contracts.KeyMemory) so the module loads without
	// a memory backend.
	memoryQuery string
	memoryDraft string
	memoryError string

	// Export state: the last export status/error shown under the session header.
	exportStatus string
	exportPath   string
	exportErr    string

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

	// Composer attachment state: files picked and staged for the next message,
	// the last picker/read error, the chip sequence and the per-chip image
	// previews decoded once (keyed by attachment ID).
	pendingAttachments []contracts.Attachment
	attachErr          string
	attachSeq          int
	thumbnails         map[string]*ui.Bitmap

	// md caches the Markdown renderer options for the current appearance (the
	// library keeps its parse state per view root and the options must stay
	// stable across frames).
	md markdownCache

	// ctx is the module's kernel context, used for the optional memory backend
	// lookup (ctx.Get(contracts.KeyMemory)); it is not a Requires dependency.
	ctx cordis.Context

	// locale is the host locale service (optional). Copy is generated per frame
	// with uikit.T so switching the language applies on the next frame.
	locale contracts.LocaleService

	// settings is the optional settings store: the permission command and the
	// composer's permission badge read/write contracts.SettingAgentPermission.
	settings contracts.SettingsService

	// Optional command-family backends: the /plan and /memory dialogs read
	// these bridges (the goal bridge lives above with the header chip).
	planReviewer contracts.PlanReviewer
	memoryStore  contracts.MemoryStore

	// notifier is the optional desktop toast bridge: export actions raise a
	// success/error toast in addition to the header badge.
	notifier contracts.Notifier

	// copiedMessage is the last assistant message whose copy action ran (its
	// 「已复制」badge stays until another message is copied).
	copiedMessage string

	// Memory management state: the tidy/clear notice and the clear-all
	// confirmation dialog.
	memoryNotice    string
	memoryClearOpen bool

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
	memNotice string
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
			locale:       locale,
			settings:     settings,
			planReviewer: planReviewer,
			memoryStore:  memoryStore,
			notifier:     notifier,
			thinkOpen:    make(map[string]bool),
			toolOpen:     make(map[string]bool),
			thumbnails:   make(map[string]*ui.Bitmap),
			ctx:          ctx,
		}

		// Session changes arrive on the agent goroutine; hop to the UI thread
		// and request a frame. The unsubscribe is kernel-effect owned.
		unsubscribe, err := chat.Subscribe(func(string) {
			runtime.Post(func() { runtime.Invalidate() })
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
					} else {
						moduleState.goal = nil
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
		for _, command := range sessionCommands(runtime, navigator, chat, config, moduleState) {
			command := command
			if err := own(ctx, func() (func(), error) { return runtime.RegisterCommand(command) }); err != nil {
				return err
			}
		}
		return nil
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyNavigator)).
		AddRequires(module.Requires(contracts.KeyChat)).
		WithName("ZeoKeel conversation screen")
}

// permissionMode reads the effective agent permission mode from the settings
// store (ask when unset/unknown or without a store).
func permissionMode(settings contracts.SettingsService) string {
	if settings == nil {
		return contracts.PermissionAsk
	}
	switch strings.ToLower(strings.TrimSpace(settings.Get(contracts.SettingAgentPermission, ""))) {
	case contracts.PermissionWorkspace:
		return contracts.PermissionWorkspace
	case contracts.PermissionFull:
		return contracts.PermissionFull
	case contracts.PermissionAsk:
		return contracts.PermissionAsk
	default:
		return contracts.PermissionAsk
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
	default:
		return st.tr("询问", "Ask")
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
	return []contracts.CommandSpec{
		{
			ID:    "zeokeel.session.new",
			Title: "新会话",
			Hint:  "新建并打开一个会话",
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
			Title: "创建副本",
			Hint:  "复制当前会话",
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
			Title: "导出对话",
			Hint:  "导出当前会话为 Markdown",
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
			ID:      "zeokeel.session.goal",
			Title:   "设置目标",
			Hint:    "为当前会话设置/清除长期目标",
			Order:   60,
			Enabled: func() bool { return st.goalTracker != nil && chat.ActiveSession() != "" },
			Run:     func() error { openGoalDialog(st, runtime); return nil },
		},
		{
			ID:      "zeokeel.session.plan",
			Title:   "审阅计划",
			Hint:    "提交一份计划给用户批准",
			Order:   61,
			Enabled: func() bool { return st.planReviewer != nil && chat.ActiveSession() != "" },
			Run:     func() error { openPlanDialog(st, runtime); return nil },
		},
		{
			ID:      "zeokeel.session.memory",
			Title:   "添加记忆",
			Hint:    "保存一条全局记忆",
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
			Title:   "权限模式",
			Hint:    "切换权限模式：询问 → 工作区 → 全部允许",
			Order:   45,
			Enabled: func() bool { return st.settings != nil },
			Run: func() error {
				cyclePermissionMode(st)
				runtime.Invalidate()
				return nil
			},
		},
		{
			ID:    "zeokeel.session.settings",
			Title: "打开设置",
			Hint:  "打开设置面板",
			Order: 50,
			Run: func() error {
				navigator.ToggleSettings()
				return nil
			},
		},
	}
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
