// Package sidebar contributes the runtime sidebar of the product: the primary
// "new chat" action, the chat session list and the settings entry.
//
// It is a UI module like ui/home: a kernel Descriptor that requires the host UI
// runtime and the chat read model, registers three SlotItems on activation and
// hands the unregister functions (and the chat subscription) to kernel effects
// so disposal unwinds them. The host owns the native Sidebar/SidebarSection
// shell; this module only injects native ui.SidebarItem / ui.PrimaryButton /
// ui.Button / ui.Text / ui.Badge controls into the reserved slots, never a
// hand-drawn widget.
package sidebar

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
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
				button := ui.PrimaryButton(c, newChatLabel).FillWidth()
				if button.Clicked() {
					// NewSession marks the session active; navigate to the
					// session screen afterwards. The screen may not be
					// registered yet when this click happens (a headless or
					// partial assembly), so a failed push must not break the
					// frame.
					if _, err := chat.NewSession(""); err == nil {
						_ = nav.Push("session")
						runtime.Invalidate()
					}
				}
			},
		}

		navSlot := contracts.SlotItem{
			Slot:  contracts.SlotSidebarNav,
			ID:    "sidebar.nav.sessions",
			Order: 10,
			Build: func(c *ui.Context) {
				sessions := chat.Sessions()
				active := chat.ActiveSession()
				ui.Column(c).FillWidth().Gap(2).Children(func() {
					ui.Text(c, sessionsTitle).FontSize(12).TextColor(c.Theme().TextMuted)
					if len(sessions) == 0 {
						ui.Text(c, "还没有会话").FontSize(12).TextColor(c.Theme().TextMuted)
						return
					}
					for _, info := range sessions {
						session := info
						label := strings.TrimSpace(session.Title)
						if label == "" {
							label = "未命名会话"
						}
						item := ui.SidebarItem(c, session.ID, nil, label)
						item.OnClick(func() {
							_ = chat.SetActiveSession(session.ID)
							_ = nav.Push("session")
							runtime.Invalidate()
						})
						if session.ID == active {
							item.Children(func() { ui.Badge(c, "当前") })
						}
						if session.Running {
							item.Children(func() { ui.Badge(c, "运行中") })
						}
					}
				})
			},
		}

		footer := contracts.SlotItem{
			Slot:  contracts.SlotSidebarFooter,
			ID:    "sidebar.footer.actions",
			Order: 10,
			Build: func(c *ui.Context) {
				ui.Row(c).FillWidth().Gap(6).Children(func() {
					if ui.Button(c, settingsLabel).Clicked() {
						nav.ToggleSettings()
					}
				})
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
