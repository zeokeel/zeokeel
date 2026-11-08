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
	"github.com/zeokeel/zeokeel/internal/design"
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
		// The example module still declares the host AppInfo dependency (the
		// product modules all get the build info from it); it deliberately does
		// not surface that build metadata in the UI: the About page owns it.
		if _, err := ctx.Require(contracts.KeyAppInfo); err != nil {
			return err
		}
		title := firstNonEmpty(cfg.Title, "欢迎使用 ZeoKeel")
		message := firstNonEmpty(cfg.Message, "这是一个独立编译的 UI 模块，通过注入点出现在宿主中。")

		// 1. Top bar center: the module marker (the build metadata belongs to
		// About, not to the product chrome).
		if err := own(ctx, func() (func(), error) {
			return runtime.RegisterSlotItem(contracts.SlotItem{
				Slot:  contracts.SlotTopBarCenter,
				ID:    "welcome.topbar",
				Order: 10,
				Build: func(c *ui.Context) {
					t := design.Of(c)
					ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
						design.Apply(ui.Text(c, "ui/welcome"), t.TextMuted(design.TypeXXS))
					})
				},
			})
		}); err != nil {
			return err
		}

		// 2. Home hero: replaces home's default hero with the welcome content.
		// The version is only shown in About, so the example hero keeps the
		// message alone.
		if err := own(ctx, func() (func(), error) {
			return runtime.RegisterSlotItem(contracts.SlotItem{
				Slot:  contracts.SlotHomeHero,
				ID:    "welcome.hero",
				Order: 10,
				Build: func(c *ui.Context) {
					t := design.Of(c)
					ui.Column(c).FillWidth().Gap(design.Space3).Children(func() {
						uikit.PageHeader(c, title, "")
						design.Apply(ui.Text(c, message), t.Text(design.TypeBody))
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
