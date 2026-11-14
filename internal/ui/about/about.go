// Package about contributes the About page to the settings surface. It is a UI
// module: it needs the host UI runtime and the appinfo read model, registers a
// settings page on activation and hands its unregister function to the kernel
// effect.
//
// The only settings extension it contributes is the page itself (and an
// invisible locale-sync item): build metadata belongs on this page, not in a
// header above every settings page.
package about

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
	// devOpen is the "development information" disclosure state (H22): the
	// architecture notes are developer material and stay collapsed.
	devOpen bool
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
		Section: "关于",
		Title:   p.pageTitle(),
		Order:   900,
		Build: func(c *ui.Context) {
			p.sync()
			buildAboutPage(c, p.info, p.title, p.locale, &p.devOpen)
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

// aboutValue renders one build value, replacing empty and the assembly's raw
// "unknown" build metadata with a friendly placeholder (H22).
func aboutValue(locale contracts.LocaleService, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.EqualFold(trimmed, "unknown") {
		return uikit.T(locale, "未知", "Unknown")
	}
	return trimmed
}

// buildAboutPage renders the localized About copy: the version/build identity
// for everyone, and the architecture notes folded into a "development
// information" disclosure instead of being the page's main content (H22).
func buildAboutPage(c *ui.Context, info contracts.AppInfo, title string,
	locale contracts.LocaleService, devOpen *bool) {
	heading := firstNonEmpty(title, uikit.T(locale, "关于", "About")) + " " + info.Name
	ui.Column(c).FillWidth().Gap(design.Space3).Children(func() {
		uikit.PageHeader(c, heading, "")
		uikit.Section(c, uikit.T(locale, "构建信息", "Build information"), func(c *ui.Context) {
			buildPropertySheet(c, []uikit.KeyValue{
				{Key: uikit.T(locale, "名称", "Name"), Value: aboutValue(locale, info.Name)},
				{Key: uikit.T(locale, "版本", "Version"), Value: aboutValue(locale, info.Version)},
				{Key: uikit.T(locale, "提交", "Commit"), Value: aboutValue(locale, info.Commit)},
				{Key: uikit.T(locale, "构建时间", "Build time"), Value: aboutValue(locale, info.BuildTime)},
				{Key: "Go", Value: aboutValue(locale, info.GoVersion)},
			})
		})
		uikit.Section(c, uikit.T(locale, "开发信息", "Development information"), func(c *ui.Context) {
			if devOpen == nil {
				return
			}
			ui.Collapsible(c, uikit.T(locale, "架构说明", "Architecture notes"), devOpen, func() {
				uikit.Bullet(c, uikit.T(locale,
					"UI 是 mygo 原生控件；模块通过注入点贡献界面。",
					"The UI is built from native mygo controls; modules contribute UI through injection points."))
			})
		})
	})
}

// Module registers the About settings page and the locale-sync item.
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

		return nil
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyAppInfo)).
		WithName("ZeoKeel About settings page")
}

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

// buildPropertySheet draws the product's property sheet (DESIGN.md §5.2): a
// fixed 160 DIP label column, the value left-aligned on a 32 DIP row and a
// divider between rows; the Diagnostics, Network and About pages share the
// shape.
func buildPropertySheet(c *ui.Context, rows []uikit.KeyValue) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space0).Children(func() {
		for index, row := range rows {
			if index > 0 {
				ui.Divider(c)
			}
			ui.Row(c).FillWidth().Height(design.Space8).
				AlignItems(ui.Center).Gap(design.Space3).Children(func() {
				design.Apply(ui.Text(c, row.Key), t.TextMuted(design.TypeXS)).
					Width(design.Space8 * 5).Shrink(0)
				design.Apply(ui.Text(c, row.Value), t.Font(design.TypeXS)).
					TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…").
					Tooltip(row.Value).Selectable()
			})
		}
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
