// Package sidebar contributes the runtime sidebar of the product: the primary
// "new chat" action, the chat session list and the settings entry.
//
// It is a UI module like ui/home: a kernel Descriptor that requires the host UI
// runtime and the chat read model, registers three SlotItems on activation and
// hands the unregister functions (and the chat subscription) to kernel effects
// so disposal unwinds them. The host owns the native Sidebar/SidebarSection
// shell; this module only injects native ui.ButtonBase / ui.Button / ui.Icon /
// ui.Text controls into the reserved slots and styles them through
// internal/design (the brand accent, row metrics, type scale and badges). It
// never draws a control by hand.
package sidebar

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// Config is the module's typed assembly config.
type Config struct {
	// NewChatLabel is the primary action label (default "新对话").
	NewChatLabel string `json:"newChatLabel"`
	// SettingsLabel is the footer settings button label (default "设置").
	SettingsLabel string `json:"settingsLabel"`
	// SessionsTitle is the small list heading above the sessions (default "会话").
	SessionsTitle string `json:"sessionsTitle"`
}

// Module registers the sidebar slots.
func Module() *module.Descriptor[Config] {
	return module.Define("zeokeel-ui/sidebar", func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		nav, err := ctx.Require(contracts.KeyNavigator)
		if err != nil {
			return err
		}
		chat, err := ctx.Require(contracts.KeyChat)
		if err != nil {
			return err
		}

		newChatLabel := firstNonEmpty(cfg.NewChatLabel, "新对话")
		settingsLabel := firstNonEmpty(cfg.SettingsLabel, "设置")
		sessionsTitle := firstNonEmpty(cfg.SessionsTitle, "会话")

		// The chat service notifies from the agent goroutine; hop to the UI
		// thread with Post and let the host schedule a frame. The subscription
		// is unwound with the module through the kernel effect.
		unsubscribe, err := chat.Subscribe(func(string) {
			runtime.Post(func() { runtime.Invalidate() })
		})
		if err != nil {
			return fmt.Errorf("sidebar: subscribe chat: %w", err)
		}
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(unsubscribe)}
		}); err != nil {
			unsubscribe()
			return err
		}

		primary := contracts.SlotItem{
			Slot:  contracts.SlotSidebarPrimary,
			ID:    "sidebar.primary.new-chat",
			Order: 10,
			Build: func(c *ui.Context) {
				buildPrimary(c, design.Of(c), chat, nav, runtime, newChatLabel)
			},
		}
		navSlot := contracts.SlotItem{
			Slot:  contracts.SlotSidebarNav,
			ID:    "sidebar.nav.sessions",
			Order: 10,
			Build: func(c *ui.Context) {
				buildSessionList(c, design.Of(c), chat, nav, runtime, sessionsTitle)
			},
		}
		footer := contracts.SlotItem{
			Slot:  contracts.SlotSidebarFooter,
			ID:    "sidebar.footer.actions",
			Order: 10,
			Build: func(c *ui.Context) {
				buildFooter(c, design.Of(c), nav, settingsLabel)
			},
		}

		for _, item := range []contracts.SlotItem{primary, navSlot, footer} {
			if err := own(ctx, func() (func(), error) { return runtime.RegisterSlotItem(item) }); err != nil {
				return err
			}
		}
		return nil
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyNavigator)).
		AddRequires(module.Requires(contracts.KeyChat)).
		WithName("ZeoKeel sidebar")
}

// buildPrimary is the brand "new chat" action: a native ButtonBase on the brand
// accent, 38 DIP tall, with the NewChat glyph and a 500-weight label. Pointer,
// keyboard and pressed/hover states are the native control's.
func buildPrimary(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime contracts.UIRuntime, label string) {
	ui.Row(c).FillWidth().
		Padding(design.Space2, design.Space3, design.Space2, design.Space3).
		Children(func() {
			button := ui.ButtonBase(c).FillWidth().Height(38).Radius(design.RadiusMD).
				Background(t.Accent).Label(label)
			switch {
			case button.Pressed():
				button = button.Background(t.AccentPressed)
			case button.Hovered():
				button = button.Background(t.AccentHover)
			}
			button.Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.Center).
					Gap(design.Space2).Children(func() {
					ui.Icon(c, icons.NewChat()).Size(16, 16).TextColor(t.AccentText)
					design.Apply(ui.Text(c, label), t.Text(design.TypeBody)).
						FontWeight(500).TextColor(t.AccentText)
				})
			})
			if button.Clicked() {
				// NewSession marks the session active; navigate afterwards. A
				// headless or partial assembly may not have the screen yet, so a
				// failed push must not break the frame.
				if _, err := chat.NewSession(""); err == nil {
					_ = nav.Push("session")
					runtime.Invalidate()
				}
			}
		})
}

// buildSessionList renders the brand session list inside the host's sidebar nav
// section: a SectionLabel heading and one native row per session with a message
// glyph, an ellipsized title and the current/running badges.
func buildSessionList(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime contracts.UIRuntime, title string) {
	ui.Column(c).FillWidth().Gap(design.Space1).Padding(design.Space2, 0, 0, 0).
		Children(func() {
			design.Apply(ui.Text(c, title), t.SectionLabel())
			sessions := chat.Sessions()
			if len(sessions) == 0 {
				design.Apply(ui.Text(c, "还没有会话"), t.TextMuted(design.TypeXXS)).
					Padding(design.Space1, design.Space2, 0, design.Space2)
				return
			}
			active := chat.ActiveSession()
			for _, info := range sessions {
				buildSessionRow(c, t, chat, nav, runtime, info, info.ID == active)
			}
		})
}

// buildSessionRow is one brand session row: a native ButtonBase at the brand row
// metrics (36 DIP, 8 DIP radius, hover/selected tint) with the accessible label
// and the native click behavior.
func buildSessionRow(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime contracts.UIRuntime, session contracts.ChatSessionInfo, active bool) {
	label := strings.TrimSpace(session.Title)
	if label == "" {
		label = "未命名会话"
	}
	row := ui.ButtonBase(c).FillWidth().Height(36).PaddingX(design.Space3).
		Radius(design.RadiusSM).Label(label)
	if active || row.Hovered() {
		row = row.Background(t.BgHover)
	}
	row.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			icon := icons.Message()
			iconColor := t.LabelSecondary
			if active {
				icon = icons.NewChat()
				iconColor = t.Accent
			}
			ui.Icon(c, icon).Size(16, 16).TextColor(iconColor)
			design.Apply(ui.Text(c, label), t.Text(design.TypeS)).
				Grow(1).MaxLines(1).Ellipsis("…")
			if active {
				design.Apply(ui.Badge(c, "当前"), t.Badge())
			}
			if session.Running {
				design.Apply(ui.Badge(c, "运行中"), t.Badge())
			}
		})
	})
	row.OnClick(func() {
		_ = chat.SetActiveSession(session.ID)
		_ = nav.Push("session")
		runtime.Invalidate()
	})
}

// buildFooter is the quiet sidebar footer: the native settings button with its
// sliders glyph, so the settings surface stays reachable without competing with
// the primary action.
func buildFooter(c *ui.Context, t *design.Tokens, nav contracts.Navigator, label string) {
	ui.Row(c).FillWidth().
		Padding(design.Space2, design.Space3, design.Space2, design.Space3).
		Children(func() {
			button := ui.Button(c, label)
			button.Children(func() {
				ui.Icon(c, icons.Sliders()).Size(16, 16).TextColor(t.LabelSecondary)
			})
			if button.Clicked() {
				nav.ToggleSettings()
			}
		})
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

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
