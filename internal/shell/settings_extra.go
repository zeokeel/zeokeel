package shell

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/session"
)

// Settings pages beyond General/Models/Memory/Plugins/Presets/About: the
// notification switches, data & privacy, diagnostics, network, updates and
// desktop lifecycle. Each page reads and writes plain settings keys through
// the controller's store, so the app side (which reads the same keys) and
// this UI cannot drift. The tab values continue the existing enum, which
// grows here because settings_ui.go and models_ui.go already own their part
// of the list.
const (
	settingsTabNotifications settingsTab = settingsTabMemory + 1
	settingsTabDataPrivacy   settingsTab = settingsTabMemory + 2
	settingsTabDiagnostics   settingsTab = settingsTabMemory + 3
	settingsTabNetwork       settingsTab = settingsTabMemory + 4
	settingsTabUpdates       settingsTab = settingsTabMemory + 5
	settingsTabDesktop       settingsTab = settingsTabMemory + 6
)

// Settings keys this file owns. The app reads the same names (contract in
// docs/audit/FIX-PLAN.md wave 2); the shell only renders and persists them.
const (
	settingNotificationEnabled         = "notification.enabled"
	settingNotificationSuppressFocused = "notification.suppress_focused"
	settingNetworkProxy                = "network.proxy"
	settingCloseBehavior               = "ui.close_behavior"
	settingAutostart                   = "app.autostart"
	settingLastSession                 = "ui.last_session"
)

// shellSetting reads one of this file's settings keys, falling back to def
// when it was never written or the store is unavailable.
func (a *App) shellSetting(key, def string) string {
	if a.Ctrl == nil || a.Ctrl.Store() == nil {
		return def
	}
	value, err := a.Ctrl.Store().GetSetting(key)
	if err != nil {
		return def
	}
	if strings.TrimSpace(value) == "" {
		return def
	}
	return value
}

// shellBoolSetting reads a "true"/"false" settings key.
func (a *App) shellBoolSetting(key string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(a.shellSetting(key, ""))) {
	case "true", "1", "on", "yes":
		return true
	case "false", "0", "off", "no":
		return false
	}
	return def
}

// setShellSetting persists one settings key. Errors are reported through the
// caller's toast, never swallowed silently by the row.
func (a *App) setShellSetting(key, value string) error {
	if a.Ctrl == nil || a.Ctrl.Store() == nil {
		return nil
	}
	return a.Ctrl.Store().SetSetting(key, value)
}

// setShellBoolSetting persists a bool as "true"/"false".
func (a *App) setShellBoolSetting(key string, value bool) error {
	if value {
		return a.setShellSetting(key, "true")
	}
	return a.setShellSetting(key, "false")
}

// shellToggleRow is one settings row whose control is the native switch bound
// to a persisted boolean. toggle writes the key and reports the new value on
// the next frame.
func (a *App) shellToggleRow(c *ui.Context, t *design.Tokens, title, desc, key string, def bool, last bool) {
	value := a.shellBoolSetting(key, def)
	next := value
	rowStyle := settingsRowStyle{last: last}
	a.settingsRow(c, t, title, desc, rowStyle, func() {
		sw := ui.Switch(c, &next).Label(title)
		if sw.Changed() {
			if err := a.setShellBoolSetting(key, next); err != nil {
				c.Toast(a.menuText("Could not save the setting: ", "设置保存失败：") + err.Error())
				return
			}
			c.Toast(a.menuText("Saved", "已保存"))
		}
	})
}

// shellActionRow is one settings row whose control is a button.
func (a *App) shellActionRow(c *ui.Context, t *design.Tokens, title, desc, label string, danger, last bool, action func()) {
	a.settingsRow(c, t, title, desc, settingsRowStyle{last: last}, func() {
		button := ui.Button(c, label).Height(t.Theme().Space(8)).Label(label)
		if danger {
			button = button.TextColor(t.Danger)
		}
		button.OnClick(action)
	})
}

// shellInfoRow is one read-only settings row: the value on the right is
// selectable text, so paths and version strings can be copied by hand.
func (a *App) shellInfoRow(c *ui.Context, t *design.Tokens, title, value string, last bool) {
	a.settingsRow(c, t, title, "", settingsRowStyle{last: last}, func() {
		ui.Text(c, value).FontSize(12).TextColor(t.LabelSecondary).
			MaxLines(2).Ellipsis("…")
	})
}

// shellTextRow is one settings row whose control is a text input persisted on
// submit or with the row's Save button. The draft lives in shellUIState so
// typing one character does not write the store per frame.
func (a *App) shellTextRow(c *ui.Context, t *design.Tokens, title, desc, key, placeholder, def string, last bool) {
	st := a.shellUI()
	if st.textDrafts == nil {
		st.textDrafts = make(map[string]string)
	}
	draft, ok := st.textDrafts[key]
	if !ok {
		draft = a.shellSetting(key, def)
	}
	a.settingsRow(c, t, title, desc, settingsRowStyle{last: last}, func() {
		ui.TextInput(c, &draft).Label(title).Placeholder(placeholder).
			MinWidth(t.Theme().Space(40))
	})
	st.textDrafts[key] = draft
	// The draft is the source of truth: persist whenever it differs from the
	// stored value, so typing in the field never needs a separate Save click.
	if stored := a.shellSetting(key, def); strings.TrimSpace(stored) != strings.TrimSpace(draft) {
		if err := a.setShellSetting(key, strings.TrimSpace(draft)); err != nil {
			c.Toast(a.menuText("Could not save the setting: ", "设置保存失败：") + err.Error())
		}
	}
}

// settingsPageHeader is the heading every extra settings page starts with.
func (a *App) settingsPageHeader(c *ui.Context, t *design.Tokens, title, desc string) {
	ui.Column(c).Gap(design.Space1).Children(func() {
		settingsTypeText(c, t, design.TypeL, t.LabelPrimary, title).FontWeight(600)
		settingsTypeText(c, t, design.TypeXS, t.LabelTertiary, desc)
	})
}

// notificationsSettingsPage is Settings > Notifications: the two switches
// app/notify.go reads (notification.enabled, notification.suppress_focused).
func (a *App) notificationsSettingsPage(c *ui.Context, t *design.Tokens) {
	a.settingsPageHeader(c, t,
		a.menuText("Notifications", "通知"),
		a.menuText("What the app tells you when a turn finishes or waits for you.",
			"回合完成或等待你处理时，应用如何提醒。"))
	a.settingsSection(c, t, a.menuText("Desktop notifications", "桌面通知"), func() {
		a.shellToggleRow(c, t,
			a.menuText("Turn notifications", "回合通知"),
			a.menuText("Tell me when a turn finishes, fails, or needs approval.",
				"回合完成、失败或需要审批时提醒我。"),
			settingNotificationEnabled, true, false)
		a.shellToggleRow(c, t,
			a.menuText("Silence while focused", "聚焦时静默"),
			a.menuText("Skip the notification while the ZeoKeel window has the focus.",
				"ZeoKeel 窗口处于焦点时不发通知。"),
			settingNotificationSuppressFocused, true, true)
	})
}

// dataPrivacySettingsPage is Settings > Data & privacy: where the data and
// the API keys live, plus the export and clearing actions. The export is a
// redacted archive of the conversation logs and app logs; keys are never
// written into it because they are not part of those files.
func (a *App) dataPrivacySettingsPage(c *ui.Context, t *design.Tokens) {
	dataDir := a.pluginDataDir()
	a.settingsPageHeader(c, t,
		a.menuText("Data & privacy", "数据与隐私"),
		a.menuText("Where ZeoKeel keeps your data, and how to take it with you or remove it.",
			"ZeoKeel 的数据存放位置，以及如何导出或清除。"))
	a.settingsSection(c, t, a.menuText("Storage", "存储"), func() {
		a.shellInfoRow(c, t, a.menuText("Data folder", "数据目录"), dataDir, false)
		a.shellActionRow(c, t,
			a.menuText("Open data folder", "打开数据目录"),
			a.menuText("Show the folder in the system file manager.", "在系统文件管理器中打开该目录。"),
			a.menuText("Open", "打开"), false, false, func() {
				if err := a.pluginOpener()(dataDir); err != nil {
					c.Toast(a.menuText("Could not open the data folder", "无法打开数据目录"))
				}
			})
		a.shellInfoRow(c, t, a.menuText("API keys", "API 密钥"),
			a.menuText("Stored in the local settings database; never uploaded by the app.",
				"保存在本地设置数据库中；应用不会上传。"), true)
	})
	a.settingsSection(c, t, a.menuText("Export", "导出"), func() {
		a.shellActionRow(c, t,
			a.menuText("Export all data", "导出全部数据"),
			a.menuText("A zip of every conversation (JSONL) and the app logs. API keys are not included.",
				"包含全部会话（JSONL）与应用日志的 zip；不含 API 密钥。"),
			a.menuText("Export…", "导出…"), false, true, func() { a.exportAllData(c) })
	})
	a.settingsSection(c, t, a.menuText("Reset", "重置"), func() {
		a.shellActionRow(c, t,
			a.menuText("Reset settings", "重置设置"),
			a.menuText("Restore every setting on this page and the others to its default.",
				"把本页及其它设置恢复为默认值。"),
			a.menuText("Reset…", "重置…"), true, false, func() {
				a.requestShellConfirm(shellConfirmResetSettings)
			})
		a.shellActionRow(c, t,
			a.menuText("Delete all conversations", "删除全部会话"),
			a.menuText("Delete every session and workspace. Settings and models stay.",
				"删除所有会话与工作区；设置与模型保留。"),
			a.menuText("Delete…", "删除…"), true, true, func() {
				a.requestShellConfirm(shellConfirmClearData)
			})
	})
	a.shellConfirmDialog(c, t)
}

// diagnosticsSettingsPage is Settings > Diagnostics: the build identity, the
// paths, and the two support actions (copy diagnostics, export logs).
func (a *App) diagnosticsSettingsPage(c *ui.Context, t *design.Tokens) {
	dataDir := a.pluginDataDir()
	logDir := filepath.Join(dataDir, "logs")
	build := fmt.Sprintf("Go %s · %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	a.settingsPageHeader(c, t,
		a.menuText("Diagnostics", "诊断"),
		a.menuText("Build information and logs to include in a bug report.",
			"构建信息与日志，便于随问题报告提供。"))
	a.settingsSection(c, t, a.menuText("Build", "构建"), func() {
		a.shellInfoRow(c, t, a.menuText("Version", "版本"), BuildVersionString(), false)
		a.shellInfoRow(c, t, a.menuText("Runtime", "运行时"), build, false)
		a.shellInfoRow(c, t, a.menuText("Logs", "日志"), logDir, true)
	})
	a.settingsSection(c, t, a.menuText("Support", "支持"), func() {
		a.shellActionRow(c, t,
			a.menuText("Copy diagnostics", "复制诊断信息"),
			a.menuText("Version, runtime and paths, ready to paste into a report.",
				"版本、运行时与路径，可直接粘贴到问题报告。"),
			a.menuText("Copy", "复制"), false, false, func() {
				c.WriteClipboard(a.diagnosticsText())
				a.toastDeferred(c, a.menuText("Diagnostics copied", "诊断信息已复制"))
			})
		a.shellActionRow(c, t,
			a.menuText("Export logs", "导出日志"),
			a.menuText("Save a zip of the app logs for support.", "把应用日志打包成 zip。"),
			a.menuText("Export…", "导出…"), false, true, func() { a.exportLogs(c) })
	})
}

// diagnosticsText is what "Copy diagnostics" puts on the clipboard: identity
// and paths only, never a key or a message body.
func (a *App) diagnosticsText() string {
	dataDir := a.pluginDataDir()
	return strings.Join([]string{
		"ZeoKeel " + BuildVersionString(),
		fmt.Sprintf("Runtime: Go %s · %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH),
		"Data folder: " + dataDir,
		"Logs: " + filepath.Join(dataDir, "logs"),
		"Config: " + a.pluginConfigPath(),
	}, "\n")
}

// networkSettingsPage is Settings > Network: the proxy every outbound model,
// search and fetch request uses.
func (a *App) networkSettingsPage(c *ui.Context, t *design.Tokens) {
	a.settingsPageHeader(c, t,
		a.menuText("Network", "网络"),
		a.menuText("How ZeoKeel reaches the model, search and fetch endpoints.",
			"ZeoKeel 访问模型、搜索与抓取端点的方式。"))
	a.settingsSection(c, t, a.menuText("Proxy", "代理"), func() {
		a.shellTextRow(c, t,
			a.menuText("Proxy URL", "代理地址"),
			a.menuText("For example http://127.0.0.1:7890. Leave empty to follow the environment proxy.",
				"例如 http://127.0.0.1:7890。留空则跟随环境变量代理。"),
			settingNetworkProxy, "http://127.0.0.1:7890", "", true)
	})
}

// updatesSettingsPage is Settings > Updates: this build has no update
// channel, so the page states the version and what "Check for updates" means
// instead of pretending to download anything.
func (a *App) updatesSettingsPage(c *ui.Context, t *design.Tokens) {
	a.settingsPageHeader(c, t,
		a.menuText("Updates", "更新"),
		a.menuText("How this build receives updates.", "此版本如何获取更新。"))
	a.settingsSection(c, t, a.menuText("Channel", "通道"), func() {
		a.shellInfoRow(c, t, a.menuText("Current version", "当前版本"), BuildVersionString(), false)
		a.shellInfoRow(c, t, a.menuText("Update channel", "更新通道"),
			a.menuText("Manual only", "仅手动"), false)
		a.shellActionRow(c, t,
			a.menuText("Check for updates", "检查更新"),
			a.menuText("This build does not download updates automatically; install a newer package when one is available.",
				"此版本不会自动下载更新；有新安装包时请手动安装。"),
			a.menuText("Check", "检查"), false, true, func() {
				a.toastDeferred(c, a.menuText(
					"Manual updates only. Current version: "+BuildVersionString(),
					"仅手动更新。当前版本："+BuildVersionString()))
			})
	})
}

// desktopSettingsPage is Settings > Desktop: the close behavior and the
// launch-at-login switch. The app and the platform layer read both keys; the
// page only persists them.
func (a *App) desktopSettingsPage(c *ui.Context, t *design.Tokens) {
	behavior := a.shellSetting(settingCloseBehavior, "hide")
	a.settingsPageHeader(c, t,
		a.menuText("Desktop", "桌面"),
		a.menuText("How the window behaves and how ZeoKeel starts.",
			"窗口行为与启动方式。"))
	a.settingsSection(c, t, a.menuText("Window", "窗口"), func() {
		a.settingsRow(c, t,
			a.menuText("Closing the window", "关闭窗口时"),
			a.menuText("Hide keeps ZeoKeel running in the background; Quit exits the app.",
				"隐藏则让 ZeoKeel 在后台继续运行；退出则结束应用。"),
			settingsRowStyle{}, func() {
				hide := behavior != "quit"
				label := a.menuText("Hide", "隐藏")
				if !hide {
					label = a.menuText("Quit", "退出")
				}
				a.settingsSelectorLiveNamed(c, t,
					a.menuText("Closing the window, current: ", "关闭窗口行为，当前：")+label,
					label, &a.closeBehaviorOpen, []settingsOption{
						{a.menuText("Hide", "隐藏"), hide, func() {
							if err := a.setShellSetting(settingCloseBehavior, "hide"); err != nil {
								c.Toast(a.menuText("Could not save the setting", "设置保存失败"))
							}
						}, a.menuText("Keep running in the background.", "在后台继续运行。")},
						{a.menuText("Quit", "退出"), !hide, func() {
							if err := a.setShellSetting(settingCloseBehavior, "quit"); err != nil {
								c.Toast(a.menuText("Could not save the setting", "设置保存失败"))
							}
						}, a.menuText("Exit the app when the window closes.", "关闭窗口时结束应用。")},
					})
			})
	})
	a.settingsSection(c, t, a.menuText("Startup", "启动"), func() {
		a.shellToggleRow(c, t,
			a.menuText("Launch at login", "登录时启动"),
			a.menuText("Start ZeoKeel when you sign in (supported on this platform when the system allows it).",
				"登录系统时启动 ZeoKeel（平台允许时生效）。"),
			settingAutostart, false, true)
	})
}

// requestShellConfirm opens the shared destructive-confirmation dialog.
func (a *App) requestShellConfirm(kind string) {
	st := a.shellUI()
	st.confirmKind = kind
	st.confirmOpen = true
}

// shellConfirmResetSettings / shellConfirmClearData are the two destructive
// actions the Data & privacy page routes through the confirmation dialog.
const (
	shellConfirmResetSettings = "reset-settings"
	shellConfirmClearData     = "clear-conversations"
)

// shellConfirmDialog is the confirmation for the destructive settings actions:
// Cancel takes the focus, the danger button is never the default, and a
// failure keeps the dialog open with the reason on screen.
func (a *App) shellConfirmDialog(c *ui.Context, t *design.Tokens) {
	st := a.shellUI()
	th := t.Theme()
	ui.Modal(c, &st.confirmOpen, func() {
		title := a.menuText("Reset settings?", "重置设置？")
		body := a.menuText("Every setting returns to its default. Conversations, workspaces and models stay.",
			"所有设置将恢复默认；会话、工作区与模型保留。")
		confirm := a.menuText("Reset settings", "重置设置")
		if st.confirmKind == shellConfirmClearData {
			title = a.menuText("Delete all conversations?", "删除全部会话？")
			body = a.menuText("Every session and workspace is deleted. Settings and models stay. This cannot be undone.",
				"所有会话与工作区将被删除；设置与模型保留。此操作不可撤销。")
			confirm = a.menuText("Delete everything", "全部删除")
		}
		ui.Column(c).Width(th.Space(105)).Gap(th.Space(3)).Children(func() {
			settingsTypeText(c, t, design.TypeM, t.LabelPrimary, title).FontWeight(600)
			settingsTypeText(c, t, design.TypeXS, t.LabelSecondary, body).MaxLines(4)
			ui.Row(c).Gap(th.Space(2)).Justify(ui.End).Children(func() {
				if ui.Button(c, a.menuText("Cancel", "取消")).AutoFocus().Clicked() {
					st.confirmOpen = false
					st.confirmKind = ""
				}
				if ui.Button(c, confirm).TextColor(t.Danger).Clicked() {
					if err := a.runShellConfirm(c); err != nil {
						c.Toast(a.menuText("Could not complete the action: ", "操作失败：") + err.Error())
						return
					}
					st.confirmOpen = false
					st.confirmKind = ""
				}
			})
		})
	})
}

// runShellConfirm performs the confirmed action.
func (a *App) runShellConfirm(c *ui.Context) error {
	st := a.shellUI()
	switch st.confirmKind {
	case shellConfirmResetSettings:
		a.updateSettings(c, func(s *session.Settings) { *s = session.DefaultSettings() })
		c.Toast(a.menuText("Settings reset", "设置已重置"))
		return nil
	case shellConfirmClearData:
		if a.Ctrl == nil {
			return fmt.Errorf("no live session service")
		}
		ctx := context.Background()
		workspaces, err := a.Ctrl.ListWorkspaces(ctx)
		if err != nil {
			return err
		}
		sessions, err := a.Ctrl.Store().ListSessions("")
		if err != nil {
			return err
		}
		for _, sess := range sessions {
			if err := a.Ctrl.DeleteSession(ctx, sess.ID); err != nil {
				return err
			}
		}
		for _, ws := range workspaces {
			if err := a.Ctrl.DeleteWorkspace(ctx, ws.ID); err != nil {
				return err
			}
		}
		a.SetSnapshot(nil)
		a.Screen = Home
		c.Toast(a.menuText("All conversations deleted", "全部会话已删除"))
		return nil
	}
	return nil
}

// chooseSaveFile opens the host's save dialog for an export. A live desktop
// host wires SaveFileDialog; tests inject one; without either, the suggested
// path is used so the action still produces a file at a predictable place.
func (a *App) chooseSaveFile(title, suggested string) string {
	if a.SaveFileDialog != nil {
		chosen, err := a.SaveFileDialog(title, suggested)
		if err != nil || strings.TrimSpace(chosen) == "" {
			return ""
		}
		return chosen
	}
	if a.SaveSessionLog != nil {
		chosen, err := a.SaveSessionLog(suggested)
		if err != nil || strings.TrimSpace(chosen) == "" {
			return ""
		}
		return chosen
	}
	return suggested
}

// exportAllData writes the redacted data archive: every session's JSONL
// export plus the app logs. API keys and message-free settings are not part
// of either source, so the archive carries no credentials.
func (a *App) exportAllData(c *ui.Context) {
	if a.Ctrl == nil {
		a.toastDeferred(c, a.menuText("Export is not available", "导出不可用"))
		return
	}
	suggested := filepath.Join(os.TempDir(),
		"zeokeel-data-"+time.Now().Format("20060102-150405")+".zip")
	target := a.chooseSaveFile(a.menuText("Export ZeoKeel data", "导出 ZeoKeel 数据"), suggested)
	if strings.TrimSpace(target) == "" {
		a.toastDeferred(c, a.menuText("Export cancelled", "已取消导出"))
		return
	}
	tmp, err := os.MkdirTemp("", "zeokeel-export-*")
	if err != nil {
		a.toastDeferred(c, a.menuText("Could not prepare the export: ", "无法准备导出：")+err.Error())
		return
	}
	defer os.RemoveAll(tmp)
	ctx := context.Background()
	sessions, err := a.Ctrl.Store().ListSessions("")
	if err != nil {
		a.toastDeferred(c, a.menuText("Could not read the sessions: ", "无法读取会话：")+err.Error())
		return
	}
	entries := make(map[string]string) // zip name -> source path
	for _, sess := range sessions {
		path := filepath.Join(tmp, sess.ID+".jsonl")
		if err := a.Ctrl.ExportSession(ctx, sess.ID, path); err != nil {
			a.toastDeferred(c, a.menuText("Could not export a session: ", "无法导出会话：")+err.Error())
			return
		}
		entries["sessions/"+filepath.Base(path)] = path
	}
	logDir := filepath.Join(a.pluginDataDir(), "logs")
	_ = filepath.Walk(logDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(logDir, path)
		if relErr != nil {
			return nil
		}
		entries["logs/"+filepath.ToSlash(rel)] = path
		return nil
	})
	readme := filepath.Join(tmp, "README.txt")
	_ = os.WriteFile(readme, []byte(a.exportReadme()), 0o600)
	entries["README.txt"] = readme
	if err := writeZip(target, entries); err != nil {
		a.toastDeferred(c, a.menuText("Could not write the archive: ", "无法写入压缩包：")+err.Error())
		return
	}
	a.toastDeferred(c, a.menuText("Data exported to ", "数据已导出到 ")+target)
}

// exportReadme is the note inside the data archive.
func (a *App) exportReadme() string {
	return strings.Join([]string{
		"ZeoKeel data export",
		"Version: " + BuildVersionString(),
		"Exported: " + time.Now().Format(time.RFC3339),
		"",
		"sessions/ holds one JSONL export per conversation.",
		"logs/ holds the app logs.",
		"API keys, models and the local database are not part of this archive.",
	}, "\n")
}

// exportLogs writes a zip of the app logs for support.
func (a *App) exportLogs(c *ui.Context) {
	logDir := filepath.Join(a.pluginDataDir(), "logs")
	suggested := filepath.Join(os.TempDir(),
		"zeokeel-logs-"+time.Now().Format("20060102-150405")+".zip")
	target := a.chooseSaveFile(a.menuText("Export ZeoKeel logs", "导出 ZeoKeel 日志"), suggested)
	if strings.TrimSpace(target) == "" {
		a.toastDeferred(c, a.menuText("Export cancelled", "已取消导出"))
		return
	}
	entries := make(map[string]string)
	_ = filepath.Walk(logDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(logDir, path)
		if relErr != nil {
			return nil
		}
		entries[filepath.ToSlash(rel)] = path
		return nil
	})
	if len(entries) == 0 {
		a.toastDeferred(c, a.menuText("No log files to export yet", "暂无日志文件可导出"))
		return
	}
	if err := writeZip(target, entries); err != nil {
		a.toastDeferred(c, a.menuText("Could not write the archive: ", "无法写入压缩包：")+err.Error())
		return
	}
	a.toastDeferred(c, a.menuText("Logs exported to ", "日志已导出到 ")+target)
}

// writeZip writes entries (zip name -> source file) into one archive.
func writeZip(target string, entries map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	file, err := os.Create(target)
	if err != nil {
		return err
	}
	defer file.Close()
	zw := zip.NewWriter(file)
	defer zw.Close()
	for name, source := range entries {
		in, err := os.Open(source)
		if err != nil {
			return err
		}
		w, err := zw.Create(name)
		if err != nil {
			in.Close()
			return err
		}
		if _, err := io.Copy(w, in); err != nil {
			in.Close()
			return err
		}
		in.Close()
	}
	return zw.Close()
}

// settingsTabIcon is the nav glyph of one extra settings page.
func (a *App) settingsTabIcon(tab settingsTab) *ui.SVG {
	switch tab {
	case settingsTabNotifications:
		return icons.Bell()
	case settingsTabDataPrivacy:
		return icons.Shield()
	case settingsTabDiagnostics:
		return icons.Activity()
	case settingsTabNetwork:
		return icons.Globe()
	case settingsTabUpdates:
		return icons.Download()
	case settingsTabDesktop:
		return icons.PanelLeft()
	default:
		return icons.Sliders()
	}
}
