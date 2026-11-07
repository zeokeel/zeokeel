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

// aboutPage owns the settings page contribution and re-registers it when the
// host language changes (the host reads page titles from the runtime registry
// every frame, so the navigation label follows the locale).
type aboutPage struct {
	runtime    contracts.UIRuntime
	locale     contracts.LocaleService
	info       contracts.AppInfo
	title      string
	unregister func()
	lang       string
}

// pageTitle resolves the page/navigation title for the current locale.
func (p *aboutPage) pageTitle() string {
	if title := firstNonEmpty(p.title); title != "" {
		return title
	}
	return uikit.T(p.locale, "关于", "About")
}

// register adds (or replaces) the About page with the current localized title.
func (p *aboutPage) register() error {
	if p.unregister != nil {
		p.unregister()
		p.unregister = nil
	}
	unregister, err := p.runtime.RegisterSettingsPage(contracts.SettingsPageSpec{
		ID:      "about",
		Section: "general",
		Title:   p.pageTitle(),
		Order:   90,
		Build: func(c *ui.Context) {
			p.sync()
			buildAboutPage(c, p.info, p.title, p.locale)
		},
	})
	if err != nil {
		return err
	}
	p.unregister = unregister
	return nil
}

// sync re-registers the page after a language switch.
func (p *aboutPage) sync() {
	lang := p.locale.Language()
	if lang == p.lang {
		return
	}
	if err := p.register(); err == nil {
		p.lang = lang
	}
}

// close removes the current page contribution.
func (p *aboutPage) close() {
	if p.unregister != nil {
		p.unregister()
		p.unregister = nil
	}
}

// buildAboutPage renders the localized About copy.
func buildAboutPage(c *ui.Context, info contracts.AppInfo, title string,
	locale contracts.LocaleService) {
	heading := firstNonEmpty(title, uikit.T(locale, "关于", "About")) + " " + info.Name
	ui.Column(c).FillWidth().Gap(14).Children(func() {
		uikit.PageHeader(c, heading, uikit.T(locale,
			"所有模块编译进同一二进制，由 assembly 选择启用。",
			"All modules are compiled into one binary and enabled by the assembly."))
		uikit.Section(c, uikit.T(locale, "构建信息", "Build information"), func(c *ui.Context) {
			uikit.KeyValueRows(c, []uikit.KeyValue{
				{Key: uikit.T(locale, "名称", "Name"), Value: info.Name},
				{Key: uikit.T(locale, "版本", "Version"), Value: info.Version},
				{Key: uikit.T(locale, "提交", "Commit"), Value: info.Commit},
				{Key: uikit.T(locale, "构建时间", "Build time"), Value: info.BuildTime},
				{Key: "Go", Value: info.GoVersion},
			})
		})
		uikit.Section(c, uikit.T(locale, "架构", "Architecture"), func(c *ui.Context) {
			uikit.Bullet(c, uikit.T(locale,
				"UI 是 mygo 原生控件，没有浏览器/webview。",
				"The UI is built from native mygo controls; there is no browser or webview."))
			uikit.Bullet(c, uikit.T(locale,
				"模块通过注入点贡献界面，宿主只负责布局与帧。",
				"Modules contribute UI through injection points; the host owns layout and frames."))
			uikit.Bullet(c, uikit.T(locale,
				"注册随模块激活生效，注销随 kernel effect 逆序撤销。",
				"Registrations activate with the module and are disposed in reverse order by kernel effects."))
		})
	})
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
		locale, _ := ctx.Get(contracts.KeyLocale)
		page := &aboutPage{
			runtime: runtime,
			locale:  locale,
			info:    info,
			title:   firstNonEmpty(cfg.Title),
		}
		if err := page.register(); err != nil {
			return err
		}
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(page.close)}
		}); err != nil {
			page.close()
			return err
		}
		// Invisible slot contribution: re-registers the page title after a
		// language switch. The host builds the sidebar slot before the settings
		// overlay, so navigation follows in the same frame.
		if err := own(ctx, func() (func(), error) {
			return runtime.RegisterSlotItem(contracts.SlotItem{
				Slot:  contracts.SlotSidebarFooter,
				ID:    "about.locale-sync",
				Order: 1001,
				Build: func(*ui.Context) { page.sync() },
			})
		}); err != nil {
			page.close()
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
					page.sync()
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "ZeoKeel v"+info.Version).FontSize(13).TextColor(c.Theme().TextMuted)
						ui.Badge(c, uikit.T(locale, "构建 ", "Build ")+info.BuildTime)
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
