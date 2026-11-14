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
	// unavailable marks a build whose update backend reports no release feed
	// configured: the page then explains the state instead of promising a
	// download/install it cannot perform.
	unavailable bool
	info        contracts.UpdateInfo
	err         string
	copied      bool

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
	// installRunning marks that the host's immediate-install command ran and the
	// app is about to quit; the page shows the honest installing state instead
	// of a "restart to upgrade" promise it cannot keep by itself.
	installRunning bool
	// installRunErr is a failure running the host install command.
	installRunErr string

	// transient detects a new visit to the page, clearing the one-shot copy
	// confirmation ("已复制") that belongs to the previous visit.
	transient transientStatus
}

func newUpdateState() *updateState { return &updateState{} }

// updateInstallCommandID is the host's immediate-install command: the shell
// registers it when an update is ready to install, and running it launches the
// platform installer and quits the app. A settings UI module may not import the
// host, so the stable command ID is the cross-module contract (the same way the
// settings module consumes other host command IDs).
const updateInstallCommandID = "uiroot.install-update"

// updateInstallCommand returns the host's immediate-install command when it is
// registered. Presence is the availability contract: the host keeps the command
// registered but may report Enabled=false until a pending plan exists (that hook
// is for the command palette), while this page schedules the plan first and only
// then runs the command. Every desktop build ships the command; a build without
// it (headless, an embedder) falls back to the schedule-on-next-launch behavior.
func updateInstallCommand(runtime contracts.UIRuntime) (contracts.CommandSpec, bool) {
	if runtime == nil {
		return contracts.CommandSpec{}, false
	}
	for _, command := range runtime.Commands() {
		if command.ID == updateInstallCommandID {
			return command, true
		}
	}
	return contracts.CommandSpec{}, false
}

// updateInstallBehaviorCopy explains what installing actually does on the
// running platform: the Windows setup runs, macOS replaces the app from the dmg
// (opening the dmg for a manual drag when the location is not writable), Linux
// installs the AppImage. The copy never describes another platform's installer.
func updateInstallBehaviorCopy(locale contracts.LocaleService) string {
	switch runtime.GOOS {
	case "windows":
		return localized(locale,
			"Windows：会运行已下载的安装程序完成升级，随后应用自动退出。",
			"Windows: runs the downloaded setup to upgrade, then the app quits.")
	case "darwin":
		return localized(locale,
			"macOS：会解压下载的应用包并替换当前 App，然后重启。",
			"macOS: extracts the downloaded app bundle over the current app and restarts.")
	case "linux":
		return localized(locale,
			"Linux：会安装 AppImage 并重启应用。",
			"Linux: installs the AppImage and restarts the app.")
	default:
		return localized(locale,
			"会运行平台的安装步骤并重启应用。",
			"Runs the platform install steps and restarts the app.")
	}
}

// updateInstallNextLaunchCopy is the honest fallback when the host offers no
// immediate-install command: the scheduled update is applied at the next start.
func updateInstallNextLaunchCopy(locale contracts.LocaleService) string {
	return localized(locale,
		"下次启动应用时会自动安装。",
		"The update installs automatically the next time the app starts.")
}

// updateInstallingCopy is the in-flight state shown once the host install
// command ran: the installer is running and the app is exiting.
func updateInstallingCopy(locale contracts.LocaleService) string {
	return localized(locale,
		"正在安装，应用即将退出…",
		"Installing; the app will quit shortly…")
}

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
		if st.unavailable {
			// A build without a release feed cannot check, download or install:
			// say so instead of offering an action that always fails.
			uikit.PageHeader(c, localized(locale, "更新", "Update"), "")
		} else {
			// The page's only primary action (check now) sits at the header's
			// right edge; the page writes nothing, so there is no footer save.
			uikit.PageHeaderAction(c,
				localized(locale, "更新", "Update"),
				"",
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
						// Checking is a utility action; the contextual download/install
						// action is the page's one primary.
						if design.Apply(ui.Button(c, checkLabel), t.SecondaryButton()).
							Disabled(st.checking || st.downloading).Clicked() {
							if !st.checking && !st.downloading {
								startUpdateCheck(updateService, runtime, st)
								c.Invalidate()
							}
						}
					})
				})
		}

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

		if st.unavailable {
			uikit.Section(c, localized(locale, "更新未启用", "Updates not enabled"), func(*ui.Context) {
				design.Apply(ui.Text(c, localized(locale,
					"当前构建未配置更新源，无法检查、下载或安装新版本。",
					"This build has no release feed configured, so new versions cannot be checked, downloaded or installed.")),
					t.Font(design.TypeXS)).TextColor(t.LabelSecondary).MaxLines(4)
			})
			return
		}

		// Install half: the backend may also implement contracts.UpdateInstaller
		// (schedule the verified download for install-on-next-launch). The
		// pending plan is read once when the page opens, so a plan scheduled in
		// an earlier run is visible before any check.
		installer, canInstall := updateService.(contracts.UpdateInstaller)
		installCommand, hasInstallCommand := updateInstallCommand(runtime)
		if canInstall && !st.pendingLoaded {
			st.pendingLoaded = true
			if plan, ok := installer.PendingInstall(); ok {
				pending := plan
				st.pending = &pending
			}
		}
		if st.pending != nil {
			uikit.Section(c, localized(locale, "待安装", "Pending install"), func(*ui.Context) {
				buildPendingInstall(c, t, *st.pending, locale, runtime, st,
					installCommand, hasInstallCommand)
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
					// An available update needs attention: the shared status
					// anatomy's warning tint, with the label carrying the meaning.
					uikit.StatusBadge(c, t, uikit.StatusInProgress,
						localized(locale, "可更新", "Update available"))
				} else {
					uikit.StatusBadge(c, t, uikit.StatusIdle,
						localized(locale, "已是最新", "Up to date"))
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
					buildUpdateDownloadResult(c, t, *st.download, locale, hasInstallCommand)
				})
			}
		}

		// Install action: only after a verified download, and only while no
		// install is already pending/scheduled. Scheduling runs off the UI thread
		// like the download; when the host registers its immediate-install
		// command the page also runs it (on the UI thread, as CommandSpec
		// requires) and shows the installing state while the app exits.
		if canInstall {
			if st.installErr != "" {
				uikit.ErrorLine(c, localized(locale,
					"安装计划失败：", "Failed to schedule install: ")+st.installErr)
			}
			if st.installRunErr != "" && st.pending == nil {
				uikit.ErrorLine(c, localized(locale,
					"安装失败：", "Install failed: ")+st.installRunErr)
			}
			if st.installRunning && st.pending == nil {
				// The host command launched the installer and asked the app to
				// quit: say so instead of promising the old restart-to-upgrade
				// behavior (the bug this page had).
				buildInstallingState(c, t, locale)
			}
			if st.download != nil && st.pending == nil && st.install == nil && !st.installRunning {
				installLabel := localized(locale,
					"安装并在下次启动生效", "Install and apply on next launch")
				busyLabel := localized(locale, "安装中…", "Installing…")
				if hasInstallCommand {
					installLabel = localized(locale, "安装并重启", "Install and restart")
				}
				if st.installing {
					installLabel = busyLabel
				}
				var command *contracts.CommandSpec
				if hasInstallCommand {
					selected := installCommand
					command = &selected
				}
				disabled := st.downloading || st.installing
				buildBusyPrimaryAction(c, t, st.installing, installLabel, busyLabel,
					disabled, func() {
						if st.downloading || st.installing {
							return
						}
						startUpdateInstall(installer, runtime, st, *st.download, command)
						c.Invalidate()
					})
				if hasInstallCommand {
					design.Apply(ui.Text(c, updateInstallBehaviorCopy(locale)),
						t.TextMuted(design.TypeXXS)).MaxLines(3)
				}
			}
			if st.install != nil && !st.installRunning {
				uikit.Section(c, localized(locale, "安装计划", "Install plan"), func(*ui.Context) {
					buildScheduledInstall(c, t, *st.install, locale, runtime, st,
						installCommand, hasInstallCommand)
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
	st.installRunErr = ""
	// A new check discards the failed/scheduled plan state: after checking and
	// downloading again the main install action must be available (F4).
	st.install = nil
	st.installRunning = false
	runtime.Invalidate()
	go func() {
		info, err := service.Check(context.Background())
		runtime.Post(func() {
			st.checking = false
			if err != nil {
				st.err = err.Error()
				st.checked = false
				if updateFeedUnconfigured(err) {
					// The backend's "no feed configured" error names an internal
					// assembly key; the page explains the state in product terms
					// and never echoes that key.
					st.unavailable = true
					st.err = ""
				}
			} else {
				st.info = info
				st.checked = true
				st.err = ""
			}
			runtime.Invalidate()
		})
	}()
}

// updateFeedUnconfigured reports whether an update check failed because the
// backend has no release feed configured (the update module's ErrDisabled). The
// UI module may not import the backend module, so the documented error text is
// the cross-module marker; matching is deliberately narrow to that state.
func updateFeedUnconfigured(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "更新源未配置") ||
		strings.Contains(message, "update.feedurl")
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

// startUpdateInstall schedules the verified download for install, off the UI
// thread, and publishes the plan/error through UIRuntime.Post. When the host's
// immediate-install command is available it runs from the post callback (UI
// thread), after the plan is recorded, so the installer has a plan to apply.
func startUpdateInstall(installer contracts.UpdateInstaller, runtime contracts.UIRuntime,
	st *updateState, download contracts.UpdateDownload, command *contracts.CommandSpec) {
	if st.installing {
		return
	}
	st.installing = true
	st.installErr = ""
	st.installRunErr = ""
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
				if command != nil {
					runUpdateInstallCommand(runtime, st, *command)
				}
			}
			runtime.Invalidate()
		})
	}()
}

// runUpdateInstallCommand runs the host's immediate-install command on the UI
// thread (contracts.CommandSpec.Run may be called there only): it launches the
// platform installer and asks the app to quit. While it runs the page shows the
// installing state; a failure is reported instead of pretending the update was
// applied.
func runUpdateInstallCommand(runtime contracts.UIRuntime, st *updateState, command contracts.CommandSpec) {
	st.installRunErr = ""
	st.installRunning = true
	if err := command.Run(); err != nil {
		st.installRunning = false
		st.installRunErr = err.Error()
	}
	runtime.Invalidate()
}

// buildInstallingState renders the in-flight row after the host install command
// ran: the app is about to exit.
func buildInstallingState(c *ui.Context, t *design.Tokens, locale contracts.LocaleService) {
	label := updateInstallingCopy(locale)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		buildBusySpinner(c, true, label)
		design.Apply(ui.Text(c, label), t.Font(design.TypeS)).TextColor(t.LabelPrimary)
	})
}

// buildPendingInstall shows an already scheduled install (from an earlier run)
// before any check. When the host registers its immediate-install command the
// plan can be applied now (install and restart); otherwise the page keeps the
// honest next-launch explanation.
func buildPendingInstall(c *ui.Context, t *design.Tokens, plan contracts.UpdateInstallPlan,
	locale contracts.LocaleService, runtime contracts.UIRuntime, st *updateState,
	installCommand contracts.CommandSpec, hasInstallCommand bool) {
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			uikit.StatusBadge(c, t, uikit.StatusInProgress,
				localized(locale, "待安装：", "Pending install: v")+strings.TrimSpace(plan.Version))
			if staged := strings.TrimSpace(plan.Path); staged != "" {
				design.Apply(ui.Text(c, staged), t.Font(design.TypeXXS), t.MonoFamily()).
					TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
			}
		})
		if !hasInstallCommand {
			design.Apply(ui.Text(c, updateInstallNextLaunchCopy(locale)),
				t.Font(design.TypeXS)).TextColor(t.LabelSecondary).MaxLines(2)
			return
		}
		if st.installRunErr != "" {
			uikit.ErrorLine(c, localized(locale,
				"安装失败：", "Install failed: ")+st.installRunErr)
		}
		if st.installRunning {
			label := updateInstallingCopy(locale)
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				buildBusySpinner(c, true, label)
				design.Apply(ui.Text(c, label), t.Font(design.TypeS)).TextColor(t.LabelPrimary)
			})
			return
		}
		design.Apply(ui.Text(c, updateInstallBehaviorCopy(locale)),
			t.TextMuted(design.TypeXXS)).MaxLines(3)
		installLabel := localized(locale, "安装并重启", "Install and restart")
		if st.installRunErr != "" {
			installLabel = localized(locale, "重试安装并重启", "Retry install and restart")
		}
		buildBusyPrimaryAction(c, t, false, installLabel,
			localized(locale, "安装中…", "Installing…"), false, func() {
				runUpdateInstallCommand(runtime, st, installCommand)
			})
	})
}

// buildScheduledInstall renders the install plan scheduled in this session: the
// badge plus the version and staged path. With the host install command it offers
// an in-page retry that re-runs the command from the existing plan (no
// re-scheduling), so a failed immediate install is not a dead end (F4); without
// the command it keeps the honest next-launch explanation.
func buildScheduledInstall(c *ui.Context, t *design.Tokens, plan contracts.UpdateInstallPlan,
	locale contracts.LocaleService, runtime contracts.UIRuntime, st *updateState,
	installCommand contracts.CommandSpec, hasInstallCommand bool) {
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			uikit.StatusBadge(c, t, uikit.StatusSuccess,
				localized(locale, "已计划安装", "Install scheduled"))
			design.Apply(ui.Text(c, localized(locale, "版本：", "Version: ")+strings.TrimSpace(plan.Version)),
				t.Font(design.TypeXS)).TextColor(t.LabelPrimary)
		})
		if staged := strings.TrimSpace(plan.Path); staged != "" {
			design.Apply(ui.Text(c, localized(locale, "暂存路径", "Staged file")), t.Font(design.TypeXXS)).
				TextColor(t.LabelTertiary)
			design.Apply(ui.Text(c, staged), t.Font(design.TypeXXS), t.MonoFamily()).
				TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…")
		}
		if !hasInstallCommand {
			design.Apply(ui.Text(c, updateInstallNextLaunchCopy(locale)),
				t.Font(design.TypeXS)).TextColor(t.LabelSecondary).MaxLines(2)
			return
		}
		if st.installRunErr != "" {
			design.Apply(ui.Text(c, localized(locale,
				"安装未完成，可直接重试。",
				"The install did not finish; retry it here.")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
		}
		buildBusyPrimaryAction(c, t, false,
			localized(locale, "重试安装并重启", "Retry install and restart"),
			localized(locale, "安装中…", "Installing…"), false, func() {
				runUpdateInstallCommand(runtime, st, installCommand)
			})
	})
}

// buildUpdateDownloadResult renders the staged-file summary of a verified
// download: version/size/checksum, the staging path and the manual-install
// note (replacing the running binary is a separate host step).
func buildUpdateDownloadResult(c *ui.Context, t *design.Tokens, download contracts.UpdateDownload,
	locale contracts.LocaleService, canInstallNow bool) {
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			uikit.StatusBadge(c, t, uikit.StatusSuccess, localized(locale, "已下载", "Downloaded"))
			downloadCopy := localized(locale,
				"下载完成，可安排在下次启动安装。",
				"Download complete; schedule it for the next launch.")
			if canInstallNow {
				downloadCopy = localized(locale,
					"下载完成，可立即安装并重启。",
					"Download complete; install and restart now.")
			}
			design.Apply(ui.Text(c, downloadCopy),
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
