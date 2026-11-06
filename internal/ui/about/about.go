// Package about contributes the About page to the settings surface. It is a UI
// module: it needs the host UI runtime and the appinfo read model, registers a
// settings page on activation and hands its unregister function to the kernel
// effect.
//
// It also demonstrates extending the settings surface itself: the header slot
// (settings.header) shows the build identity above every settings page, which
// the host renders without knowing which module contributed it.
package about

import (
	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// Config is the module's typed assembly config.
type Config struct {
	Title string `json:"title"`
}

// Module registers the About settings page and the settings header item.
func Module() *module.Descriptor[Config] {
	return module.Define("zeokeel-ui/about", func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		info, err := ctx.Require(contracts.KeyAppInfo)
		if err != nil {
			return err
		}
		title := firstNonEmpty(cfg.Title, "关于")

		page := contracts.SettingsPageSpec{
			ID:      "about",
			Section: "general",
			Title:   title,
			Order:   90,
			Build: func(c *ui.Context) {
				ui.Column(c).FillWidth().Gap(14).Children(func() {
					uikit.PageHeader(c, title+" "+info.Name, "所有模块编译进同一二进制，由 assembly 选择启用。")
					uikit.Section(c, "构建信息", func(c *ui.Context) {
						uikit.KeyValueRows(c, []uikit.KeyValue{
							{Key: "名称", Value: info.Name},
							{Key: "版本", Value: info.Version},
							{Key: "提交", Value: info.Commit},
							{Key: "构建时间", Value: info.BuildTime},
							{Key: "Go", Value: info.GoVersion},
						})
					})
					uikit.Section(c, "架构", func(c *ui.Context) {
						uikit.Bullet(c, "UI 是 mygo 原生控件，没有浏览器/webview。")
						uikit.Bullet(c, "模块通过注入点贡献界面，宿主只负责布局与帧。")
						uikit.Bullet(c, "注册随模块激活生效，注销随 kernel effect 逆序撤销。")
					})
				})
			},
		}
		if err := own(ctx, func() (func(), error) { return runtime.RegisterSettingsPage(page) }); err != nil {
			return err
		}

		// Extend the settings surface with a header item. The host renders
		// settings.header for every settings page; about's item needs no host
		// change.
		return own(ctx, func() (func(), error) {
			return runtime.RegisterSlotItem(contracts.SlotItem{
				Slot:  contracts.SlotSettingsHeader,
				ID:    "about.header",
				Order: 10,
				Build: func(c *ui.Context) {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "ZeoKeel v"+info.Version).FontSize(13).TextColor(c.Theme().TextMuted)
						ui.Badge(c, "构建 "+info.BuildTime)
					})
				},
			})
		})
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyAppInfo)).
		WithName("ZeoKeel About settings page")
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
