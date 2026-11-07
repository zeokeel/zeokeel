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
		moduleState := &state{
			thinkOpen: make(map[string]bool),
			toolOpen:  make(map[string]bool),
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

		return own(ctx, func() (func(), error) {
			return runtime.RegisterScreen(contracts.ScreenSpec{
				ID:    ScreenID,
				Title: config.Title,
				Order: 20,
				Build: func(c *ui.Context) {
					buildScreen(c, runtime, navigator, chat, config, moduleState)
				},
			})
		})
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyNavigator)).
		AddRequires(module.Requires(contracts.KeyChat)).
		WithName("ZeoKeel conversation screen")
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
