// Package welcome is the example "third-party" UI module of the skeleton: it
// adds UI through injection points without touching the host or the home
// module. It contributes to topbar.center and home.hero, and registers a
// command, which is exactly the pattern later product modules follow.
package welcome

import (
	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// Config is the module's typed assembly config.
type Config struct {
	Title   string `json:"title"`
	Message string `json:"message"`
}

// Module registers the welcome contributions.
func Module() *module.Descriptor[Config] {
	return module.Define("zeokeel-ui/welcome", func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		info, err := ctx.Require(contracts.KeyAppInfo)
		if err != nil {
			return err
		}
		title := firstNonEmpty(cfg.Title, "欢迎使用 ZeoKeel")
		message := firstNonEmpty(cfg.Message, "这是一个独立编译的 UI 模块，通过注入点出现在宿主中。")

		// 1. Top bar center: a version badge next to the product name.
		if err := own(ctx, func() (func(), error) {
			return runtime.RegisterSlotItem(contracts.SlotItem{
				Slot:  contracts.SlotTopBarCenter,
				ID:    "welcome.topbar",
				Order: 10,
				Build: func(c *ui.Context) {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Badge(c, "v"+info.Version)
						ui.Text(c, "via ui/welcome").FontSize(12).TextColor(c.Theme().TextMuted)
					})
				},
			})
		}); err != nil {
			return err
		}

		// 2. Home hero: replaces home's default hero with the welcome content.
		if err := own(ctx, func() (func(), error) {
			return runtime.RegisterSlotItem(contracts.SlotItem{
				Slot:  contracts.SlotHomeHero,
				ID:    "welcome.hero",
				Order: 10,
				Build: func(c *ui.Context) {
					ui.Column(c).FillWidth().Gap(8).Children(func() {
						uikit.PageHeader(c, title, "")
						ui.Text(c, message).FontSize(14)
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Badge(c, "v"+info.Version)
							ui.Badge(c, info.GoVersion)
						})
					})
				},
			})
		}); err != nil {
			return err
		}

		// 3. A command, rendered by the host's command surface (top bar).
		return own(ctx, func() (func(), error) {
			return runtime.RegisterCommand(contracts.CommandSpec{
				ID:    "welcome.copy-version",
				Title: "复制版本",
				Hint:  "复制 ZeoKeel 版本号",
				Order: 20,
				Run: func() error {
					runtime.Invalidate()
					return nil
				},
			})
		})
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyAppInfo)).
		WithName("ZeoKeel welcome contributions")
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
