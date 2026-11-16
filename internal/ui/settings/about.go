package settings

// The About hub: build identity, update controls and diagnostics in one settings
// page. The standalone About, Update and Diagnostics pages were merged here so
// the settings sidebar has one version/help destination instead of three.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

type aboutState struct {
	// devOpen is the "development information" disclosure state.
	devOpen bool
}

// aboutValue renders one build value, replacing empty and the assembly's raw
// "unknown" build metadata with a friendly placeholder.
func aboutValue(locale contracts.LocaleService, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.EqualFold(trimmed, "unknown") {
		return localized(locale, "未知", "Unknown")
	}
	return trimmed
}

// buildAboutHubPage renders the merged About page: the version/build identity,
// the development disclosure, the update section and the diagnostics section.
func buildAboutHubPage(c *ui.Context, kctx cordis.Context, info contracts.AppInfo,
	update *updateState, diagnostics *diagnosticsState, about *aboutState,
	locale contracts.LocaleService) {
	t := design.Of(c)
	heading := localized(locale, "关于", "About")
	if name := strings.TrimSpace(info.Name); name != "" {
		heading += " " + name
	}
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		uikit.PageHeader(c, heading, "")

		uikit.Section(c, localized(locale, "构建信息", "Build information"), func(c *ui.Context) {
			uikit.PropertySheet(c, uikit.PropertySheetOptions{
				Rows: []uikit.PropertyRow{
					{Label: localized(locale, "名称", "Name"), Value: aboutValue(locale, info.Name)},
					{Label: localized(locale, "版本", "Version"), Value: aboutValue(locale, info.Version)},
					{Label: localized(locale, "提交", "Commit"), Value: aboutValue(locale, info.Commit)},
					{Label: localized(locale, "构建时间", "Build time"), Value: aboutValue(locale, info.BuildTime)},
					{Label: "Go", Value: aboutValue(locale, info.GoVersion)},
				},
				Key: "about.build", Locale: locale,
			})
		})

		uikit.Section(c, localized(locale, "开发信息", "Development information"), func(c *ui.Context) {
			if about == nil {
				return
			}
			ui.Collapsible(c, localized(locale, "架构说明", "Architecture notes"), &about.devOpen, func() {
				uikit.Bullet(c, localized(locale,
					"UI 是 mygo 原生控件；模块通过注入点贡献界面。",
					"The UI is built from native mygo controls; modules contribute UI through injection points."))
			})
		})

		// Update and diagnostics are sections of this page now; both keep their
		// own state and actions.
		buildUpdatePage(c, t, kctx, uiRuntimeFrom(kctx), update, locale)
		store, _ := kctx.Get(contracts.KeySettings)
		dataDir, _ := kctx.Get(contracts.KeyDataDir)
		toolkit, _ := kctx.Get(contracts.KeyToolkit)
		chat, _ := kctx.Get(contracts.KeyChat)
		rows, keys := diagnosticsRows(info, dataDir, toolkit, chat, store, locale)
		modules := loadedModulesFromContext(kctx)
		buildDiagnosticsPage(c, t, diagnostics, rows, keys, modules, locale, false)
	})
}

// uiRuntimeFrom returns the host UI runtime, or nil in a stripped test
// assembly (the update section then simply renders without async actions).
func uiRuntimeFrom(kctx cordis.Context) contracts.UIRuntime {
	runtime, _ := kctx.Get(contracts.KeyUIRuntime)
	return runtime
}
