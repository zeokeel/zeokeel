// Package home contributes the Home screen. It is a UI module: a kernel
// Descriptor that requires the host UI runtime (and any read models it needs),
// registers its contributions on activation and hands the unregister functions
// to the kernel effect so disposal unwinds them in reverse order.
//
// Home is a composition screen. It owns only the page skeleton and the empty
// states; the content of its three regions comes from other modules through the
// injection points home.hero, home.composer and home.recent. It also renders
// one example tool call through UIRuntime.RenderTool, so a tool-card module
// (ui/toolcards) can decorate it without home changing.
package home

import (
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// Config is the module's typed assembly config.
type Config struct {
	Title               string `json:"title"`
	Greeting            string `json:"greeting"`
	ComposerPlaceholder string `json:"composerPlaceholder"`
	RecentTitle         string `json:"recentTitle"`
	RecentEmpty         string `json:"recentEmpty"`
}

// demoCall is the example tool call home renders through RenderTool. The full
// tool read model arrives with the session module; this is enough to prove the
// tool-card injection point without home knowing any renderer.
func demoCall() contracts.ToolCall {
	return contracts.ToolCall{
		ID:      "demo-1",
		Name:    "demo",
		Status:  "ok",
		Summary: "示例工具调用：由 ui/toolcards 模块渲染",
	}
}

// Module registers the Home screen and its command.
func Module() *module.Descriptor[Config] {
	return module.Define("zeokeel-ui/home", func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		nav, err := ctx.Require(contracts.KeyNavigator)
		if err != nil {
			return err
		}
		info, err := ctx.Require(contracts.KeyAppInfo)
		if err != nil {
			return err
		}
		// Chat is optional so the Home screen still loads in products without
		// the agent backend; with it the recent-session list becomes real.
		chat, _ := ctx.Get(contracts.KeyChat)
		if chat != nil {
			unsubscribe, err := chat.Subscribe(func(string) {
				runtime.Post(func() { runtime.Invalidate() })
			})
			if err != nil {
				return err
			}
			if _, err := ctx.Effect(func() cordis.Effect {
				return cordis.Effect{cordis.CleanupOf(unsubscribe)}
			}); err != nil {
				unsubscribe()
				return err
			}
		}
		title := firstNonEmpty(cfg.Title, "首页")
		greeting := firstNonEmpty(cfg.Greeting, "编译期模块化的桌面 AI agent。")
		composerPlaceholder := firstNonEmpty(cfg.ComposerPlaceholder, "向 ZeoKeel 提问…")
		recentTitle := firstNonEmpty(cfg.RecentTitle, "最近会话")
		recentEmpty := firstNonEmpty(cfg.RecentEmpty, "还没有最近会话。")

		screen := contracts.ScreenSpec{
			ID:    "home",
			Title: title,
			Order: 10,
			Build: func(c *ui.Context) {
				ui.Column(c).FillWidth().Gap(16).Children(func() {
					uikit.PageHeader(c, title, greeting)
					region(c, runtime, contracts.SlotHomeHero, func(c *ui.Context) {
						// Default hero, replaced by module contributions when
						// another module registers home.hero.
						ui.Column(c).FillWidth().Gap(8).Children(func() {
							ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Text(c, "ZeoKeel").Bold().FontSize(30)
								ui.Badge(c, "v"+info.Version)
								ui.Badge(c, info.GoVersion)
							})
							ui.Text(c, greeting).FontSize(14)
							ui.Text(c, "这是 home 的默认 Hero 空态。").FontSize(12).TextColor(c.Theme().TextMuted)
						})
					})
					region(c, runtime, contracts.SlotHomeComposer, func(c *ui.Context) {
						ui.Row(c).FillWidth().Padding(12).Background(c.Theme().Surface).Radius(8).
							AlignItems(ui.Center).Gap(10).Children(func() {
							ui.Text(c, composerPlaceholder).FontSize(13).TextColor(c.Theme().TextMuted)
							ui.Spacer(c).Grow(1)
							if chat != nil && ui.PrimaryButton(c, "开始新对话").Clicked() {
								if _, err := chat.NewSession(""); err == nil {
									_ = nav.Push("session")
									runtime.Invalidate()
								}
							}
						})
					})
					uikit.Section(c, recentTitle, func(c *ui.Context) {
						region(c, runtime, contracts.SlotHomeRecent, func(c *ui.Context) {
							buildRecent(c, chat, nav, runtime, recentEmpty)
						})
					})
					uikit.Section(c, "工具卡", func(c *ui.Context) {
						if !runtime.RenderTool(c, demoCall()) {
							uikit.EmptyState(c, "还没有 demo 工具卡渲染器（由 ui/toolcards 模块提供）。")
						}
					})
					if ui.PrimaryButton(c, "打开设置").Clicked() {
						nav.ToggleSettings()
					}
				})
			},
		}
		if err := own(ctx, func() (func(), error) { return runtime.RegisterScreen(screen) }); err != nil {
			return err
		}
		return own(ctx, func() (func(), error) {
			return runtime.RegisterCommand(contracts.CommandSpec{
				ID:    "home.settings",
				Title: "设置",
				Hint:  "打开设置",
				Order: 10,
				Run:   func() error { nav.ToggleSettings(); return nil },
			})
		})
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyNavigator)).
		AddRequires(module.Requires(contracts.KeyAppInfo)).
		WithName("ZeoKeel Home screen")
}

// buildRecent renders the recent-session list with native buttons; without the
// chat service (or with no sessions yet) it shows the configured empty state.
func buildRecent(c *ui.Context, chat contracts.ChatService, nav contracts.Navigator,
	runtime contracts.UIRuntime, empty string) {
	if chat == nil {
		uikit.EmptyState(c, empty)
		return
	}
	sessions := chat.Sessions()
	if len(sessions) == 0 {
		uikit.EmptyState(c, empty)
		return
	}
	if len(sessions) > 5 {
		sessions = sessions[:5]
	}
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		for _, info := range sessions {
			info := info
			label := strings.TrimSpace(info.Title)
			if label == "" {
				label = "未命名会话"
			}
			button := ui.Button(c, label).FillWidth()
			button.OnClick(func() {
				_ = chat.SetActiveSession(info.ID)
				_ = nav.Push("session")
				runtime.Invalidate()
			})
		}
	})
}

// region renders one injection point. When nothing is registered yet it draws
// the module's default empty state instead of an empty hole.
func region(c *ui.Context, runtime contracts.UIRuntime, slot string, fallback contracts.BuildFunc) {
	for _, name := range runtime.Slots() {
		if name == slot {
			runtime.RenderSlot(c, slot)
			return
		}
	}
	if fallback != nil {
		fallback(c)
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
		if value != "" {
			return value
		}
	}
	return ""
}
