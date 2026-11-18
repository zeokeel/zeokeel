package settings

// The About hub: build identity, update controls and diagnostics in one settings
// page. The standalone About, Update and Diagnostics pages were merged here so
// the settings sidebar has one version/help destination instead of three.

import (
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/lib/textutil"
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

// buildFactRows is the About page's one build-identity table: the facts the
// old 构建信息 / 当前版本 / 运行时信息 sections each repeated (version, commit,
// build time, Go) live here once. The version keeps the update section's
// fallback chain (build metadata → check result → "dev"); the other build
// metadata renders empty/"unknown" as the one placeholder aboutValue defines.
func buildFactRows(info contracts.AppInfo, updateCurrent string, locale contracts.LocaleService) []diagnosticRow {
	return []diagnosticRow{
		{Label: localized(locale, "名称", "Name"), Value: aboutValue(locale, info.Name)},
		{Label: localized(locale, "版本", "Version"), Value: textutil.FirstNonEmpty(info.Version, updateCurrent, "dev")},
		{Label: "Commit", Value: aboutValue(locale, info.Commit)},
		{Label: localized(locale, "构建时间", "Build time"), Value: aboutValue(locale, info.BuildTime)},
		{Label: "Go", Value: aboutValue(locale, textutil.FirstNonEmpty(info.GoVersion, runtime.Version()))},
		{Label: localized(locale, "平台", "Platform"), Value: runtime.GOOS + "/" + runtime.GOARCH},
	}
}

// buildAboutHubPage renders the merged About page: the one build-facts table,
// the development disclosure, the update section (status + actions only) and
// the diagnostics section (runtime/storage facts only).
func buildAboutHubPage(c *ui.Context, kctx cordis.Context, info contracts.AppInfo,
	update *updateState, diagnostics *diagnosticsState, about *aboutState,
	locale contracts.LocaleService) {
	t := design.Of(c)
	heading := localized(locale, "关于", "About")
	if name := strings.TrimSpace(info.Name); name != "" {
		heading += " " + name
	}
	buildRows := buildFactRows(info, update.info.CurrentVersion, locale)
	ui.Column(c).FillWidth().Gap(design.SpaceScreen).Children(func() {
		uikit.PageHeader(c, heading, "")

		uikit.Section(c, localized(locale, "构建信息", "Build information"), func(c *ui.Context) {
			sheet := make([]uikit.PropertyRow, len(buildRows))
			for index, row := range buildRows {
				sheet[index] = uikit.PropertyRow{Label: row.Label, Value: row.Value}
			}
			uikit.PropertySheet(c, uikit.PropertySheetOptions{
				Rows: sheet,
				Key:  "about.build", Locale: locale,
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
		// The narrow rig-free tool catalog (AR-06): diagnostics only need the tool
		// names and count, not the concrete *rigtool.Registry behind
		// hosttools.KeyRegistry.
		catalog, _ := kctx.Get(contracts.KeyToolCatalog)
		chat, _ := kctx.Get(contracts.KeyChat)
		rows, keys := diagnosticsRows(dataDir, catalog, chat, store, locale)
		modules := loadedModulesFromContext(kctx)
		buildDiagnosticsPage(c, t, diagnostics, buildRows, rows, keys, modules, locale, false)
	})
}

// uiRuntimeFrom returns the host UI runtime, or nil in a stripped test
// assembly (the update section then simply renders without async actions).
func uiRuntimeFrom(kctx cordis.Context) contracts.UIRuntime {
	runtime, _ := kctx.Get(contracts.KeyUIRuntime)
	return runtime
}
