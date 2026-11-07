package settings

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// Update page constants. Section "general" matches the About page's, and Order
// 92 keeps this page after About (90) instead of becoming the default page.
const (
	UpdatePageID      = "update"
	UpdatePageSection = "general"
	UpdatePageOrder   = 92
	UpdatePageTitle   = "更新"
)

// updateState is the update page's UI state: the last check result, whether a
// check is in flight and whether the download link was just copied.
type updateState struct {
	checking bool
	checked  bool
	info     contracts.UpdateInfo
	err      string
	copied   bool
}

func newUpdateState() *updateState { return &updateState{} }

// buildUpdatePage renders the page: current version, the native check action,
// the version/status result, the release-notes Markdown preview and the copy
// link action. It never downloads or replaces the binary.
func buildUpdatePage(c *ui.Context, t *design.Tokens, kctx cordis.Context, runtime contracts.UIRuntime,
	st *updateState, locale contracts.LocaleService) {
	updateService, hasUpdate := kctx.Get(contracts.KeyUpdate)
	appInfo, _ := kctx.Get(contracts.KeyAppInfo)

	current := firstNonEmpty(appInfo.Version, st.info.CurrentVersion, "dev")

	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		ui.Column(c).Gap(design.Space1).Children(func() {
			design.Apply(ui.Text(c, localized(locale, "更新", "Update")), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, localized(locale,
				"自动替换暂不支持；检查更新后手动下载安装包。",
				"Automatic replacement is not supported; check for updates and install the package manually.")),
				t.TextMuted(design.TypeXS)).MaxLines(2)
		})

		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space3).Children(func() {
			design.Apply(ui.Text(c, localized(locale, "当前版本", "Current version")), t.Font(design.TypeXS)).
				TextColor(t.LabelSecondary)
			design.Apply(ui.Text(c, current), t.MonoFamily()).FontSize(12).
				TextColor(t.LabelPrimary)
		})

		if !hasUpdate {
			design.Apply(ui.Text(c, localized(locale, "更新服务未启用", "Update service is not enabled")), t.TextMuted(design.TypeXS))
			return
		}

		checkLabel := localized(locale, "检查更新", "Check for updates")
		if st.checking {
			checkLabel = localized(locale, "检查中…", "Checking…")
		}
		check := design.Apply(ui.PrimaryButton(c, checkLabel), t.PrimaryButton()).
			Disabled(st.checking)
		if check.Clicked() && !st.checking {
			startUpdateCheck(updateService, runtime, st)
		}

		if st.err != "" {
			buildUpdateError(c, t, localized(locale, "检查更新失败：", "Update check failed: ")+st.err)
		}
		if !st.checked || st.err != "" {
			return
		}

		latest := firstNonEmpty(st.info.LatestVersion, current)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space3).Children(func() {
			design.Apply(ui.Text(c, localized(locale, "最新版本", "Latest version")), t.Font(design.TypeXS)).
				TextColor(t.LabelSecondary)
			design.Apply(ui.Text(c, latest), t.MonoFamily()).FontSize(12).
				TextColor(t.LabelPrimary)
			ui.Spacer(c).Grow(1)
			if st.info.Available {
				design.Apply(ui.Badge(c, localized(locale, "可更新", "Update available")), t.Badge()).TextColor(t.Success)
			} else {
				design.Apply(ui.Badge(c, localized(locale, "已是最新", "Up to date")), t.Badge()).TextColor(t.LabelSecondary)
			}
		})

		if resource := updateResourceLine(st.info, locale); resource != "" {
			design.Apply(ui.Text(c, resource), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		}

		hasURL := strings.TrimSpace(st.info.URL) != ""
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			copyLink := ui.Button(c, localized(locale, "复制下载链接", "Copy download link")).
				Tooltip(firstNonEmpty(st.info.URL, localized(locale, "没有可下载的链接", "No download link")))
			copyLink.Disabled(!hasURL)
			if copyLink.Clicked() && hasURL {
				c.WriteClipboard(st.info.URL)
				st.copied = true
			}
			if st.copied && hasURL {
				design.Apply(ui.Badge(c, localized(locale, "已复制", "Copied")), t.Badge()).TextColor(t.Success)
			}
		})
		if !hasURL {
			design.Apply(ui.Text(c, localized(locale, "没有匹配当前平台的下载资源",
				"No download asset for the current platform")), t.TextMuted(design.TypeXXS))
		}

		if notes := strings.TrimSpace(st.info.Notes); notes != "" {
			design.Apply(ui.Text(c, localized(locale, "更新说明", "Release notes")), t.Font(design.TypeS)).
				FontWeight(600).TextColor(t.LabelPrimary)
			ui.Scroll(c).FillWidth().MaxHeight(design.Space6 * 10).Children(func() {
				markdown.View(c, notes, nil)
			})
		} else {
			design.Apply(ui.Text(c, localized(locale, "暂无更新说明", "No release notes")), t.TextMuted(design.TypeXXS))
		}
	})
}

// startUpdateCheck runs one update check off the UI thread and publishes the
// result through UIRuntime.Post (the settings page has no long-lived frame).
func startUpdateCheck(service contracts.UpdateService, runtime contracts.UIRuntime, st *updateState) {
	st.checking = true
	st.copied = false
	st.err = ""
	runtime.Invalidate()
	go func() {
		info, err := service.Check(context.Background())
		runtime.Post(func() {
			st.checking = false
			if err != nil {
				st.err = err.Error()
				st.checked = false
			} else {
				st.info = info
				st.checked = true
				st.err = ""
			}
			runtime.Invalidate()
		})
	}()
}

// updateResourceLine describes the matching asset for this platform.
func updateResourceLine(info contracts.UpdateInfo, locale contracts.LocaleService) string {
	parts := make([]string, 0, 3)
	platform := runtime.GOOS + "/" + runtime.GOARCH
	if info.Size > 0 || strings.TrimSpace(info.SHA256) != "" {
		parts = append(parts, localized(locale, "资源：", "Asset: ")+platform)
	}
	if info.Size > 0 {
		parts = append(parts, formatUpdateSize(info.Size))
	}
	if sha := strings.TrimSpace(info.SHA256); len(sha) >= 8 {
		parts = append(parts, "SHA256 "+sha[:8])
	}
	return strings.Join(parts, " · ")
}

// formatUpdateSize renders an asset size in B/KB/MB.
func formatUpdateSize(size int64) string {
	switch {
	case size < 1<<10:
		return fmt.Sprintf("%d B", size)
	case size < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	}
}

// buildUpdateError is the page's danger row.
func buildUpdateError(c *ui.Context, t *design.Tokens, text string) {
	th := t.Theme()
	ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Start).Children(func() {
		ui.Icon(c, icons.AlertTriangle()).Size(th.Space(4), th.Space(4)).TextColor(t.Danger)
		design.Apply(ui.Text(c, text), t.Font(design.TypeXS)).TextColor(t.DangerText)
	})
}
