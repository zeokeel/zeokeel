package settings

// The Data & Privacy settings page: where the product keeps its data, bulk
// session export/clear and a diagnostic report export. The page only calls the
// chat/settings contracts and two test seams (open-in-file-manager and the save
// dialog), so it works headlessly in tests without a desktop backend.

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// Data & Privacy page constants. Section "高级" matches Diagnostics; Order 106
// places it after the other advanced pages.
const (
	DataPrivacyPageID      = "data-privacy"
	DataPrivacyPageSection = "高级"
	DataPrivacyPageOrder   = 80
	DataPrivacyPageTitle   = "数据与隐私"
)

// Test seams. The defaults use the platform file manager and the native mygo
// save dialog; tests replace them (same pattern as the session export's
// ExportSaver) so no desktop backend is needed.
var (
	// OpenPathFunc opens one path in the platform file manager. The platform
	// behavior is the framework's (mygo.Shell.OpenPath); tests replace this
	// seam to stay headless.
	OpenPathFunc = func(path string) error {
		return mygo.Shell.OpenPath(strings.TrimSpace(path))
	}

	// ExportSessionsSaver is the sessions-export seam: the default opens the
	// native save dialog with the localized title and writes the Markdown
	// content. It returns the written path, "" when the user canceled, or an
	// error. The title is a parameter (not package state) so frames in another
	// language cannot race a dialog.
	ExportSessionsSaver = func(title, defaultName, content string) (string, error) {
		return saveWithDialog(defaultName, content, title, "Markdown", "md")
	}

	// ExportReportSaver is the diagnostic-report seam (same contract).
	ExportReportSaver = func(title, defaultName, content string) (string, error) {
		return saveWithDialog(defaultName, content, title, "Text", "txt")
	}
)

// openPathFunc is the seam the page invokes; OpenPathFunc is its exported
// handle (tests in both packages can replace the behavior).
var openPathFunc = func(path string) error { return OpenPathFunc(path) }

// exportSessionsSaver is the seam alias the archive page invokes.
var exportSessionsSaver = func(title, defaultName, content string) (string, error) {
	return ExportSessionsSaver(title, defaultName, content)
}

// saveWithDialog is the shared native save-dialog implementation behind the two
// export seams.
func saveWithDialog(defaultName, content, title, filterName, extension string) (string, error) {
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Title:       title,
		DefaultPath: defaultName,
		Filters:     []mygo.FileFilter{{Name: filterName, Extensions: []string{extension}}},
	})
	if err != nil || path == "" {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// dataPrivacyState is the page's UI-local state: the copied badge, the last
// action notice/detail (success badge or danger text) and the clear-all
// confirmation modal.
type dataPrivacyState struct {
	copied       bool
	notice       string
	noticeDetail string
	noticeErr    bool
	confirmClear bool
}

func newDataPrivacyState() *dataPrivacyState { return &dataPrivacyState{} }

// buildDataPrivacyPage renders the data directory, bulk session actions and the
// diagnostic report export. The page has one primary action (open the data
// directory); the exports are secondary and the bulk clear is a danger action
// behind the native confirmation dialog.
func buildDataPrivacyPage(c *ui.Context, t *design.Tokens, kctx cordis.Context,
	chat contracts.ChatService, store contracts.SettingsService, st *dataPrivacyState,
	locale contracts.LocaleService) {
	dataDir, _ := kctx.Get(contracts.KeyDataDir)
	dataDir = strings.TrimSpace(dataDir)
	sessions, hasSessionList := dataSessionList(chat)

	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		uikit.PageHeader(c, localized(locale, DataPrivacyPageTitle, "Data & privacy"), "")

		if st.notice != "" {
			if st.noticeErr {
				uikit.ErrorLine(c, st.notice)
			} else {
				statusBadgesRow(c, t, st.notice, false)
			}
		}
		if st.noticeDetail != "" {
			design.Apply(ui.Text(c, st.noticeDetail), t.Font(design.TypeXXS), t.MonoFamily()).
				TextColor(t.LabelTertiary).MaxLines(2)
		}

		uikit.Section(c, localized(locale, "数据目录", "Data directory"), func(*ui.Context) {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				value := firstNonEmpty(dataDir, localized(locale, "未设置", "Not set"))
				design.Apply(ui.Text(c, value), t.Font(design.TypeXXS), t.MonoFamily()).
					TextColor(t.LabelPrimary).Grow(1).MaxLines(2)
				copyButton := design.Apply(ui.Button(c, localized(locale, "复制", "Copy")),
					t.SecondaryButton()).
					Tooltip(localized(locale, "复制数据目录路径", "Copy the data directory path"))
				copyButton.Disabled(dataDir == "")
				if copyButton.Clicked() && dataDir != "" {
					c.WriteClipboard(dataDir)
					st.copied = true
					c.Invalidate()
				}
				if st.copied && dataDir != "" {
					design.Apply(ui.Badge(c, localized(locale, "已复制", "Copied")), t.Badge()).
						TextColor(t.SuccessText)
				}
				// Opening the directory is a convenience next to the path, not the
				// page's primary action: a row-level secondary control keeps the
				// export/clear/report actions as the page's aligned actions.
				openButton := design.Apply(ui.Button(c, localized(locale,
					"打开数据目录", "Open data directory")), t.SecondaryButton()).
					Tooltip(localized(locale, "在文件管理器中打开数据目录",
						"Open the data directory in the file manager"))
				openButton.Disabled(dataDir == "")
				if openButton.Clicked() && dataDir != "" {
					if err := openPathFunc(dataDir); err != nil {
						st.notice = localized(locale, "打开数据目录失败：",
							"Failed to open data directory: ") + err.Error()
						st.noticeErr = true
						st.noticeDetail = ""
					} else {
						st.notice = ""
						st.noticeErr = false
						st.noticeDetail = ""
					}
					c.Invalidate()
				}
			})
		})

		uikit.Section(c, localized(locale, "会话数据", "Session data"), func(*ui.Context) {
			switch {
			case !hasSessionList:
				uikit.EmptyState(c, localized(locale,
					"会话列表不可用，无法导出或清除。",
					"The session list is unavailable; exporting and clearing are disabled."))
			case len(sessions) == 0:
				uikit.EmptyState(c, localized(locale,
					"没有可导出或清除的会话",
					"No sessions to export or clear."))
			default:
				design.Apply(ui.Text(c, fmt.Sprintf(localized(locale,
					"共 %d 个会话", "%d sessions"), len(sessions))),
					t.Font(design.TypeXS)).TextColor(t.LabelSecondary)
				// The page has no accent primary action: each section's action sits
				// at its right end (bulk export here, the destructive clear on its
				// own row below), so the three data actions line up.
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Justify(ui.End).Children(func() {
					if design.Apply(ui.Button(c, localized(locale,
						"导出全部会话", "Export all sessions")),
						t.SecondaryButton()).Clicked() {
						exportDataSessions(chat, st, locale)
						c.Invalidate()
					}
				})
				ui.Divider(c)
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					design.Apply(ui.Text(c, localized(locale,
						"删除全部会话后无法恢复。",
						"Deleting every session cannot be undone.")),
						t.TextMuted(design.TypeXXS)).Grow(1).MaxLines(2)
					if design.Apply(ui.Button(c, localized(locale,
						"清除全部会话", "Clear all sessions")),
						t.DangerButton()).Clicked() {
						st.confirmClear = true
						c.Invalidate()
					}
				})
			}
		})

		uikit.SectionAction(c, localized(locale, "诊断报告", "Diagnostic report"), func(c *ui.Context) {
			if design.Apply(ui.Button(c, localized(locale,
				"导出诊断报告", "Export diagnostic report")),
				t.SecondaryButton()).Clicked() {
				exportDiagnosticReport(kctx, store, chat, st, locale)
				c.Invalidate()
			}
		}, func(*ui.Context) {
			design.Apply(ui.Text(c, localized(locale,
				"API key 不会写入。",
				"The API key is never written.")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
		})

		if st.confirmClear && hasSessionList && len(sessions) > 0 {
			message := fmt.Sprintf(localized(locale,
				"将删除全部 %d 个会话，此操作无法撤销。",
				"This deletes all %d sessions and cannot be undone."), len(sessions))
			uikit.ConfirmDanger(c, &st.confirmClear, uikit.ConfirmDangerOptions{
				Key:     "data.clear-sessions",
				Title:   localized(locale, "清除全部会话", "Clear all sessions"),
				Message: message,
				Confirm: localized(locale, "确认清除", "Confirm clear"),
				Cancel:  localized(locale, "取消", "Cancel"),
				OnConfirm: func() {
					clearDataSessions(chat, st, locale)
					c.Invalidate()
				},
			})
		}
	})
}

// dataSessionList returns the scripted session list; ok=false when the chat
// service is unavailable (the page then hides the bulk actions).
func dataSessionList(chat contracts.ChatService) ([]contracts.ChatSessionInfo, bool) {
	if chat == nil {
		return nil, false
	}
	return chat.Sessions(), true
}

// exportDataSessions renders all sessions as Markdown and calls the save seam.
func exportDataSessions(chat contracts.ChatService, st *dataPrivacyState, locale contracts.LocaleService) {
	sessions, ok := dataSessionList(chat)
	if !ok {
		return
	}
	content, count := sessionsMarkdown(chat, sessions, locale)
	defaultName := fmt.Sprintf("zeokeel-sessions-%s.md", time.Now().Format("2006-01-02"))
	path, err := exportSessionsSaver(
		localized(locale, "导出全部会话", "Export all sessions"), defaultName, content)
	switch {
	case err != nil:
		st.notice = localized(locale, "导出会话失败：", "Failed to export sessions: ") + err.Error()
		st.noticeErr = true
		st.noticeDetail = ""
	case path == "":
		// Canceled: keep the page quiet.
		st.notice = ""
		st.noticeErr = false
		st.noticeDetail = ""
	default:
		st.notice = fmt.Sprintf(localized(locale, "已导出 %d 个会话", "Exported %d sessions"), count)
		st.noticeErr = false
		st.noticeDetail = path
	}
}

// sessionsMarkdown renders every session as `# title` followed by one
// `**role**: text` line per message. It returns the exported session count.
func sessionsMarkdown(chat contracts.ChatService, sessions []contracts.ChatSessionInfo, locale contracts.LocaleService) (string, int) {
	var builder strings.Builder
	count := 0
	for _, info := range sessions {
		session, ok := chat.Session(info.ID)
		if !ok {
			continue
		}
		count++
		title := firstNonEmpty(session.Title, info.Title, localized(locale, "未命名会话", "Untitled session"))
		fmt.Fprintf(&builder, "# %s\n\n", title)
		for _, message := range session.Messages {
			role := firstNonEmpty(strings.TrimSpace(message.Role), "unknown")
			fmt.Fprintf(&builder, "**%s**: %s\n\n", role, message.Text)
		}
	}
	return strings.TrimRight(builder.String(), "\n") + "\n", count
}

// clearDataSessions deletes every listed session and reports the counts.
func clearDataSessions(chat contracts.ChatService, st *dataPrivacyState, locale contracts.LocaleService) {
	sessions, ok := dataSessionList(chat)
	if !ok {
		st.confirmClear = false
		return
	}
	deleted, failed := 0, 0
	for _, info := range sessions {
		if err := chat.DeleteSession(info.ID); err != nil {
			failed++
			continue
		}
		deleted++
	}
	if failed > 0 {
		st.notice = fmt.Sprintf(localized(locale,
			"已清除 %d 个会话，%d 个失败", "Cleared %d sessions, %d failed"), deleted, failed)
		st.noticeErr = true
	} else {
		st.notice = fmt.Sprintf(localized(locale, "已清除 %d 个会话", "Cleared %d sessions"), deleted)
		st.noticeErr = false
	}
	st.noticeDetail = ""
	st.confirmClear = false
}

// exportDiagnosticReport builds the text report and calls the save seam.
func exportDiagnosticReport(kctx cordis.Context, store contracts.SettingsService, chat contracts.ChatService, st *dataPrivacyState, locale contracts.LocaleService) {
	defaultName := fmt.Sprintf("zeokeel-diagnostics-%s.txt", time.Now().Format("2006-01-02"))
	path, err := ExportReportSaver(
		localized(locale, "诊断报告", "Diagnostic report"), defaultName,
		diagnosticReportText(kctx, store, chat, locale))
	switch {
	case err != nil:
		st.notice = localized(locale, "导出诊断报告失败：", "Failed to export diagnostic report: ") + err.Error()
		st.noticeErr = true
		st.noticeDetail = ""
	case path == "":
		st.notice = ""
		st.noticeErr = false
		st.noticeDetail = ""
	default:
		st.notice = localized(locale, "已导出诊断报告", "Exported diagnostic report")
		st.noticeErr = false
		st.noticeDetail = path
	}
}

// diagnosticReportText renders the plain-text report: app identity/runtime/
// data directory/session count, then every setting key through the shared
// credential mask (see redact.go).
func diagnosticReportText(kctx cordis.Context, store contracts.SettingsService, chat contracts.ChatService, locale contracts.LocaleService) string {
	info, _ := kctx.Get(contracts.KeyAppInfo)
	dataDir, _ := kctx.Get(contracts.KeyDataDir)
	sessionCount := 0
	if sessions, ok := dataSessionList(chat); ok {
		sessionCount = len(sessions)
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s\n\n", localized(locale, "ZeoKeel 诊断报告", "ZeoKeel diagnostic report"))
	fmt.Fprintf(&builder, "%s: %s\n", localized(locale, "生成时间", "Generated at"), time.Now().Format(time.RFC3339))
	fmt.Fprintf(&builder, "%s: %s v%s\n", localized(locale, "应用", "Application"),
		firstNonEmpty(info.Name, "ZeoKeel"), firstNonEmpty(info.Version, "dev"))
	fmt.Fprintf(&builder, "Commit: %s\n", firstNonEmpty(info.Commit, "unknown"))
	fmt.Fprintf(&builder, "%s: %s\n", localized(locale, "构建时间", "Build time"), firstNonEmpty(info.BuildTime, "unknown"))
	fmt.Fprintf(&builder, "Go: %s\n", firstNonEmpty(info.GoVersion, runtime.Version()))
	fmt.Fprintf(&builder, "%s: %s/%s\n", localized(locale, "平台", "Platform"), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&builder, "%s: %s\n", localized(locale, "数据目录", "Data directory"),
		firstNonEmpty(strings.TrimSpace(dataDir), localized(locale, "未设置", "not set")))
	fmt.Fprintf(&builder, "%s: %d\n", localized(locale, "会话数", "Sessions"), sessionCount)
	fmt.Fprintf(&builder, "\n%s:\n", localized(locale, "设置项", "Settings"))
	values := store.All()
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(&builder, "  %s=%s\n", key, maskedSettingValue(locale, key, values[key]))
	}
	return builder.String()
}
