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
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// Update page constants. Section "general" matches the About page's, and Order
// 92 keeps this page after About (90) instead of becoming the default page.
const (
	UpdatePageID      = "update"
	UpdatePageSection = "高级"
	UpdatePageOrder   = 40
	UpdatePageTitle   = "更新"
)

// updateState is the update page's UI state: the last check result, whether a
// check or the asset download is in flight, the verified download (when the
// backend implements contracts.UpdateDownloader) and whether the download link
// was just copied.
type updateState struct {
	checking bool
	checked  bool
	info     contracts.UpdateInfo
	err      string
	copied   bool

	// downloading is true while the asset download runs off the UI thread.
	downloading bool
	// download is the verified staged asset (nil until a download succeeds).
	download *contracts.UpdateDownload
	// downloadErr is the last download error, rendered as the page's danger row.
	downloadErr string

	// Install half (contracts.UpdateInstaller): the install-on-next-launch
	// action after a verified download, its in-flight state/error and the
	// pending plan read once when the page first opens.
	installing    bool
	install       *contracts.UpdateInstallPlan
	installErr    string
	pendingLoaded bool
	pending       *contracts.UpdateInstallPlan
}

func newUpdateState() *updateState { return &updateState{} }

// buildUpdatePage renders the page: current version, the native check action,
// the version/status result, the release-notes Markdown preview and the copy
// link action. Downloading and scheduling install-on-next-launch are offered
// when the backend implements those optional halves.
func buildUpdatePage(c *ui.Context, t *design.Tokens, kctx cordis.Context, runtime contracts.UIRuntime,
	st *updateState, locale contracts.LocaleService) {
	updateService, hasUpdate := kctx.Get(contracts.KeyUpdate)
	appInfo, _ := kctx.Get(contracts.KeyAppInfo)

	current := firstNonEmpty(appInfo.Version, st.info.CurrentVersion, "dev")

	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		// The page's only primary action (check now) sits at the header's right
		// edge; the page writes nothing, so there is no footer save.
		uikit.PageHeaderAction(c,
			localized(locale, "更新", "Update"),
			localized(locale,
				"检查更新并下载安装包；下载后可安排在下次启动安装。",
				"Check for updates and download the package; schedule the install for the next launch."),
			func(c *ui.Context) {
				if !hasUpdate {
					return
				}
				checkLabel := localized(locale, "检查更新", "Check for updates")
				busyLabel := localized(locale, "检查中…", "Checking…")
				if st.checking {
					checkLabel = busyLabel
				}
				// The busy state is the official spinner with an accessible
				// progress name beside the disabled action; no fake percentage.
				ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					buildBusySpinner(c, st.checking, busyLabel)
					if design.Apply(ui.PrimaryButton(c, checkLabel), t.PrimaryButton()).
						Disabled(st.checking || st.downloading).Clicked() {
						if !st.checking && !st.downloading {
							startUpdateCheck(updateService, runtime, st)
							c.Invalidate()
						}
					}
				})
			})

		uikit.Section(c, localized(locale, "当前版本", "Current version"), func(*ui.Context) {
			uikit.KeyValueRows(c, []uikit.KeyValue{
				{Key: localized(locale, "版本", "Version"), Value: current},
			})
		})

		if !hasUpdate {
			uikit.EmptyState(c, localized(locale,
				"更新服务未启用", "Update service is not enabled"))
			return
		}

		// Install half: the backend may also implement contracts.UpdateInstaller
		// (schedule the verified download for install-on-next-launch). The
		// pending plan is read once when the page opens, so a plan scheduled in
		// an earlier run is visible before any check.
		installer, canInstall := updateService.(contracts.UpdateInstaller)
		if canInstall && !st.pendingLoaded {
			st.pendingLoaded = true
			if plan, ok := installer.PendingInstall(); ok {
				pending := plan
				st.pending = &pending
			}
		}
		if st.pending != nil {
			uikit.Section(c, localized(locale, "待安装", "Pending install"), func(*ui.Context) {
				buildPendingInstall(c, t, *st.pending, locale)
			})
		}

		if st.err != "" {
			uikit.ErrorLine(c, localized(locale, "检查更新失败：", "Update check failed: ")+st.err)
		}
		if !st.checked || st.err != "" {
			return
		}

		latest := firstNonEmpty(st.info.LatestVersion, current)
		uikit.Section(c, localized(locale, "更新状态", "Update status"), func(*ui.Context) {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space3).Children(func() {
				design.Apply(ui.Text(c, localized(locale, "最新版本", "Latest version")),
					t.Font(design.TypeXS)).TextColor(t.LabelSecondary)
				design.Apply(ui.Text(c, latest), t.Font(design.TypeXS), t.MonoFamily()).
					TextColor(t.LabelPrimary)
				ui.Spacer(c).Grow(1)
				if st.info.Available {
					design.Apply(ui.Badge(c, localized(locale, "可更新", "Update available")),
						t.Badge()).TextColor(t.SuccessText)
				} else {
					design.Apply(ui.Badge(c, localized(locale, "已是最新", "Up to date")),
						t.Badge()).TextColor(t.LabelSecondary)
				}
			})
			if resource := updateResourceLine(st.info, locale); resource != "" {
				design.Apply(ui.Text(c, resource), t.Font(design.TypeXXS)).
					TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
			}
		})

		// Download half: available only when the update backend also implements
		// contracts.UpdateDownloader. The result replaces the action once a
		// verified file is staged.
		if downloader, canDownload := updateService.(contracts.UpdateDownloader); canDownload {
			if st.download == nil && st.info.Available {
				downloadLabel := localized(locale, "下载更新", "Download update")
				busyLabel := localized(locale, "下载中…", "Downloading…")
				if st.downloading {
					downloadLabel = busyLabel
				}
				buildBusyPrimaryAction(c, t, st.downloading, downloadLabel, busyLabel,
					st.downloading, func() {
						if !st.downloading {
							startUpdateDownload(downloader, runtime, st)
							c.Invalidate()
						}
					})
			}
			if st.downloadErr != "" {
				uikit.ErrorLine(c, localized(locale,
					"下载更新失败：", "Update download failed: ")+st.downloadErr)
			}
			if st.download != nil {
				uikit.Section(c, localized(locale, "已下载", "Downloaded"), func(*ui.Context) {
					buildUpdateDownloadResult(c, t, *st.download, locale)
				})
			}
		}

		// Install action: only after a verified download, and only while no
		// install is already pending/scheduled. It runs off the UI thread like
		// the download; both actions are disabled while either is in flight.
		if canInstall {
			if st.download != nil && st.pending == nil && st.install == nil {
				installLabel := localized(locale, "安装并在下次启动生效", "Install and apply on next launch")
				busyLabel := localized(locale, "安装中…", "Scheduling…")
				if st.installing {
					installLabel = busyLabel
				}
				buildBusyPrimaryAction(c, t, st.installing, installLabel, busyLabel,
					st.downloading || st.installing, func() {
						if !st.downloading && !st.installing {
							startUpdateInstall(installer, runtime, st, *st.download)
							c.Invalidate()
						}
					})
			}
			if st.installErr != "" {
				uikit.ErrorLine(c, localized(locale,
					"安装计划失败：", "Failed to schedule install: ")+st.installErr)
			}
			if st.install != nil {
				uikit.Section(c, localized(locale, "安装计划", "Install plan"), func(*ui.Context) {
					buildScheduledInstall(c, t, *st.install, locale)
				})
			}
		}

		hasURL := strings.TrimSpace(st.info.URL) != ""
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			copyLink := design.Apply(ui.Button(c,
				localized(locale, "复制下载链接", "Copy download link")), t.SecondaryButton()).
				Tooltip(firstNonEmpty(st.info.URL,
					localized(locale, "没有可下载的链接", "No download link")))
			copyLink.Disabled(!hasURL)
			if copyLink.Clicked() && hasURL {
				c.WriteClipboard(st.info.URL)
				st.copied = true
				c.Invalidate()
			}
			if st.copied && hasURL {
				design.Apply(ui.Badge(c, localized(locale, "已复制", "Copied")), t.Badge()).
					TextColor(t.SuccessText)
			}
		})
		if !hasURL {
			design.Apply(ui.Text(c, localized(locale, "没有匹配当前平台的下载资源",
				"No download asset for the current platform")), t.TextMuted(design.TypeXXS))
		}

		// H22: only show the release-notes section when there are notes; an
		// empty "no release notes" line was noise under the result rows.
		if notes := strings.TrimSpace(st.info.Notes); notes != "" {
			uikit.Section(c, localized(locale, "更新说明", "Release notes"), func(*ui.Context) {
				ui.Scroll(c).FillWidth().MaxHeight(design.Space6 * 10).Children(func() {
					markdown.View(c, notes, nil)
				})
			})
		}
	})
}

// startUpdateCheck runs one update check off the UI thread and publishes the
// result through UIRuntime.Post (the settings page has no long-lived frame).
func startUpdateCheck(service contracts.UpdateService, runtime contracts.UIRuntime, st *updateState) {
	if st.checking || st.downloading {
		return
	}
	st.checking = true
	st.copied = false
	st.err = ""
	// A new check invalidates the previous staged result: it may belong to a
	// different version.
	st.download = nil
	st.downloadErr = ""
	st.installErr = ""
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

// startUpdateDownload runs one asset download off the UI thread and publishes
// the verified result through UIRuntime.Post, like startUpdateCheck.
func startUpdateDownload(downloader contracts.UpdateDownloader, runtime contracts.UIRuntime, st *updateState) {
	if st.downloading {
		return
	}
	st.downloading = true
	st.downloadErr = ""
	runtime.Invalidate()
	go func() {
		download, err := downloader.Download(context.Background())
		runtime.Post(func() {
			st.downloading = false
			if err != nil {
				st.downloadErr = err.Error()
			} else {
				st.download = &download
				st.downloadErr = ""
			}
			runtime.Invalidate()
		})
	}()
}

// startUpdateInstall schedules the verified download for install on the next
// launch, off the UI thread, and publishes the plan/error through UIRuntime.Post.
func startUpdateInstall(installer contracts.UpdateInstaller, runtime contracts.UIRuntime, st *updateState, download contracts.UpdateDownload) {
	if st.installing {
		return
	}
	st.installing = true
	st.installErr = ""
	runtime.Invalidate()
	go func() {
		plan, err := installer.ScheduleInstall(download)
		runtime.Post(func() {
			st.installing = false
			if err != nil {
				st.installErr = err.Error()
			} else {
				st.install = &plan
				st.installErr = ""
			}
			runtime.Invalidate()
		})
	}()
}

// buildPendingInstall shows an already scheduled install (from an earlier run)
// before any check: the host applies it at the next start.
func buildPendingInstall(c *ui.Context, t *design.Tokens, plan contracts.UpdateInstallPlan, locale contracts.LocaleService) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		design.Apply(ui.Badge(c,
			localized(locale, "待安装：", "Pending install: v")+strings.TrimSpace(plan.Version)),
			t.Badge()).TextColor(t.WarningText)
		if staged := strings.TrimSpace(plan.Path); staged != "" {
			design.Apply(ui.Text(c, staged), t.Font(design.TypeXXXS), t.MonoFamily()).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		}
	})
}

// buildScheduledInstall renders the install plan scheduled in this session:
// the badge plus the version and staged path the host will install on restart.
func buildScheduledInstall(c *ui.Context, t *design.Tokens, plan contracts.UpdateInstallPlan, locale contracts.LocaleService) {
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Badge(c, localized(locale, "已计划安装", "Install scheduled")), t.Badge()).
				TextColor(t.SuccessText)
			design.Apply(ui.Text(c, localized(locale, "版本：", "Version: ")+strings.TrimSpace(plan.Version)),
				t.Font(design.TypeXS)).TextColor(t.LabelPrimary)
		})
		if staged := strings.TrimSpace(plan.Path); staged != "" {
			design.Apply(ui.Text(c, localized(locale, "暂存路径", "Staged file")), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, staged), t.Font(design.TypeXXS), t.MonoFamily()).
				TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…")
		}
	})
}

// buildUpdateDownloadResult renders the staged-file summary of a verified
// download: version/size/checksum, the staging path and the manual-install
// note (replacing the running binary is a separate host step).
func buildUpdateDownloadResult(c *ui.Context, t *design.Tokens, download contracts.UpdateDownload, locale contracts.LocaleService) {
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Badge(c, localized(locale, "已下载", "Downloaded")), t.Badge()).TextColor(t.SuccessText)
			design.Apply(ui.Text(c, localized(locale,
				"下载完成，可安排在下次启动安装。",
				"Download complete; schedule it for the next launch.")),
				t.Font(design.TypeXS)).TextColor(t.LabelSecondary)
		})
		for _, line := range updateDownloadSummary(download, locale) {
			design.Apply(ui.Text(c, line), t.Font(design.TypeXS)).TextColor(t.LabelPrimary)
		}
		if staged := strings.TrimSpace(download.Path); staged != "" {
			design.Apply(ui.Text(c, localized(locale, "暂存路径", "Staged file")), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, staged), t.Font(design.TypeXXS), t.MonoFamily()).
				TextColor(t.LabelSecondary)
		}
	})
}

// updateDownloadSummary formats the version/size/checksum lines of a staged
// download (size omitted when the manifest did not advertise one).
func updateDownloadSummary(download contracts.UpdateDownload, locale contracts.LocaleService) []string {
	lines := make([]string, 0, 3)
	if version := strings.TrimSpace(download.Version); version != "" {
		lines = append(lines, localized(locale, "版本：", "Version: ")+version)
	}
	if download.Size > 0 {
		lines = append(lines, localized(locale, "大小：", "Size: ")+formatUpdateSize(download.Size))
	}
	if sha := strings.TrimSpace(download.SHA256); len(sha) >= 8 {
		lines = append(lines, "SHA256 "+sha[:8])
	}
	return lines
}
