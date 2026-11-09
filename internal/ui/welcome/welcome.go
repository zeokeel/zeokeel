// Package welcome is the example "third-party" UI module of the skeleton: it
// adds UI through injection points without touching the host or the home
// module. It contributes a top-bar marker and the home hero, and registers a
// command (which copies the app version), which is exactly the pattern later
// product modules follow.
package welcome

import (
	"strings"

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
		// The example module declares the host AppInfo dependency (the product
		// modules all get the build info from it); it deliberately does not
		// surface that build metadata in the page: the About page owns it. The
		// command below copies the version, so it does read the value.
		info, err := ctx.Require(contracts.KeyAppInfo)
		if err != nil {
			return err
		}
		locale, _ := ctx.Get(contracts.KeyLocale)
		title := firstNonEmpty(cfg.Title, "欢迎使用 ZeoKeel")
		message := firstNonEmpty(cfg.Message, "这是一个独立编译的 UI 模块，通过注入点出现在宿主中。")
		// services is this window's frame services, captured while the module's
		// hero builds: a command may run from the palette after a frame, and the
		// clipboard must not be reached through a stored ui.Context.
		var services ui.Services

		// 1. Home hero: replaces home's default hero with the welcome content.
		// The hero build also captures the frame services the command needs.
		if err := own(ctx, func() (func(), error) {
			return runtime.RegisterSlotItem(contracts.SlotItem{
				Slot:  contracts.SlotHomeHero,
				ID:    "welcome.hero",
				Order: 10,
				Build: func(c *ui.Context) {
					services = c.Services()
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

		// 2. A command, rendered by the host's command surface (the palette).
		// It actually copies the version, as its label says.
		return own(ctx, func() (func(), error) {
			return runtime.RegisterCommand(contracts.CommandSpec{
				ID:    "welcome.copy-version",
				Title: uikit.T(locale, "复制版本", "Copy version"),
				Hint:  uikit.T(locale, "复制 ZeoKeel 版本号", "Copy the ZeoKeel version"),
				Order: 20,
				Run: func() error {
					version := strings.TrimSpace(info.Version)
					if version == "" {
						version = strings.TrimSpace(info.Name)
					}
					services.WriteClipboard(version)
					services.Invalidate()
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
