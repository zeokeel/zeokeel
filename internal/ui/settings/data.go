package settings

// The Data & Privacy settings page: where the product keeps its data, bulk
// session export/clear and a diagnostic report export. The page only calls the
// chat/settings contracts and two test seams (open-in-file-manager and the save
// dialog), so it works headlessly in tests without a desktop backend.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	// OpenPathFunc opens one path in the platform file manager.
	OpenPathFunc = func(path string) error {
		path = strings.TrimSpace(path)
		if path == "" {
			return errors.New("empty path")
		}
		command := "xdg-open"
		switch runtime.GOOS {
		case "darwin":
			command = "open"
		case "windows":
			command = "explorer"
		}
		cmd := exec.Command(command, path)
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
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

// exportSessionsSaver/exportReportSaver are the seam aliases the page invokes.
var (
	exportSessionsSaver = func(title, defaultName, content string) (string, error) {
		return ExportSessionsSaver(title, defaultName, content)
	}
	exportReportSaver = func(title, defaultName, content string) (string, error) {
		return ExportReportSaver(title, defaultName, content)
	}
)

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
		uikit.PageHeader(c, localized(locale, DataPrivacyPageTitle, "Data & privacy"), localized(locale,
			"数据目录、会话导出/清除与诊断报告。",
			"Data directory, session export/clear and a diagnostic report."))

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
			})
			design.Apply(ui.Text(c, localized(locale,
				"设置、会话与记忆都保存在这个目录。",
				"Settings, sessions and memories are stored in this directory.")),
				t.TextMuted(design.TypeXXS))
			uikit.DialogFooter(c, []uikit.DialogAction{{
				Label:    localized(locale, "打开数据目录", "Open data directory"),
				Disabled: dataDir == "",
				OnClick: func() {
					if dataDir == "" {
						return
					}
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
				},
			}})
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
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					if design.Apply(ui.PrimaryButton(c, localized(locale,
						"导出全部会话", "Export all sessions")),
						t.PrimaryButton()).Clicked() {
						exportDataSessions(chat, st, locale)
						c.Invalidate()
					}
					ui.Spacer(c).Grow(1)
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
				"导出应用版本、数据目录、会话数与设置项（API key 不会写入）。",
				"Export the app version, data directory, session count and settings (the API key is never written).")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
		})

		if st.confirmClear && hasSessionList && len(sessions) > 0 {
			message := fmt.Sprintf(localized(locale,
				"将删除全部 %d 个会话，此操作无法撤销。",
				"This deletes all %d sessions and cannot be undone."), len(sessions))
			switch ui.AlertDialog(c, &st.confirmClear,
				localized(locale, "清除全部会话", "Clear all sessions"), message,
				localized(locale, "取消", "Cancel"),
				localized(locale, "确认清除", "Confirm clear")) {
			case 1:
				clearDataSessions(chat, st, locale)
				c.Invalidate()
			default:
				c.Invalidate()
			}
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

// Session feedback kinds: the stored ChatMessage.Feedback values normalized for
// the export/report copy.
const (
	feedbackHelpful = iota + 1
	feedbackUnhelpful
)

// sessionFeedbackKind normalizes one stored feedback value (unknown/empty means
// "not rated").
func sessionFeedbackKind(raw string) int {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case contracts.FeedbackGood:
		return feedbackHelpful
	case contracts.FeedbackBad:
		return feedbackUnhelpful
	default:
		return 0
	}
}

// feedbackCounts returns the helpful/unhelpful counts of one message list.
func feedbackCounts(messages []contracts.ChatMessage) (helpful, unhelpful int) {
	for _, message := range messages {
		switch sessionFeedbackKind(message.Feedback) {
		case feedbackHelpful:
			helpful++
		case feedbackUnhelpful:
			unhelpful++
		}
	}
	return helpful, unhelpful
}

// chatFeedbackTotals counts the feedback ratings across every session the
// diagnostics report can see.
func chatFeedbackTotals(chat contracts.ChatService) (helpful, unhelpful int) {
	sessions, ok := dataSessionList(chat)
	if !ok {
		return 0, 0
	}
	for _, info := range sessions {
		session, ok := chat.Session(info.ID)
		if !ok {
			continue
		}
		good, bad := feedbackCounts(session.Messages)
		helpful += good
		unhelpful += bad
	}
	return helpful, unhelpful
}

// sessionsMarkdown renders every session as `# title` followed by one
// `**role**: text` line per message. Rated messages add a `> 用户反馈` line and a
// session with ratings starts with a feedback summary. It returns the exported
// session count.
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
		if helpful, unhelpful := feedbackCounts(session.Messages); helpful+unhelpful > 0 {
			fmt.Fprintf(&builder, localized(locale,
				"反馈：%d 有帮助 / %d 没帮助", "Feedback: %d helpful / %d not helpful")+"\n\n",
				helpful, unhelpful)
		}
		for _, message := range session.Messages {
			role := firstNonEmpty(strings.TrimSpace(message.Role), "unknown")
			fmt.Fprintf(&builder, "**%s**: %s\n\n", role, message.Text)
			switch sessionFeedbackKind(message.Feedback) {
			case feedbackHelpful:
				builder.WriteString(localized(locale, "> 用户反馈：有帮助", "> User feedback: Helpful") + "\n\n")
			case feedbackUnhelpful:
				builder.WriteString(localized(locale, "> 用户反馈：没帮助", "> User feedback: Not helpful") + "\n\n")
			}
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
// data directory/session count, then every setting key (API key masked).
// maskedSettingValue hides credentials in the exported diagnostic report:
// whole values for API-key/token-ish settings, and Bearer tokens inside JSON
// structured settings (such as mcp.servers headers).
func maskedSettingValue(locale contracts.LocaleService, key, value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	hidden := localized(locale, "已设置(隐藏)", "set (hidden)")
	lower := strings.ToLower(key)
	for _, marker := range []string{"apikey", "api_key", "token", "secret", "password", "authorization"} {
		if strings.Contains(lower, marker) {
			return hidden
		}
	}
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
			encoded, encodeErr := json.Marshal(redactJSONSecrets(decoded))
			if encodeErr == nil {
				return string(encoded)
			}
		}
		return hidden
	}
	if strings.Contains(trimmed, "Bearer ") {
		return hidden
	}
	return value
}

// redactJSONSecrets walks decoded JSON and hides Bearer tokens and values of
// credential-looking object keys.
func redactJSONSecrets(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		redacted := make(map[string]any, len(typed))
		for key, item := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "token") || strings.Contains(lower, "authorization") ||
				strings.Contains(lower, "secret") || strings.Contains(lower, "apikey") ||
				strings.Contains(lower, "password") {
				redacted[key] = "***"
				continue
			}
			redacted[key] = redactJSONSecrets(item)
		}
		return redacted
	case []any:
		redacted := make([]any, len(typed))
		for index, item := range typed {
			redacted[index] = redactJSONSecrets(item)
		}
		return redacted
	case string:
		if strings.Contains(typed, "Bearer ") {
			return "Bearer ***"
		}
		return typed
	default:
		return value
	}
}

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
	helpful, unhelpful := chatFeedbackTotals(chat)
	fmt.Fprintf(&builder, localized(locale, "反馈：有帮助 %d", "Feedback: helpful %d")+"\n", helpful)
	fmt.Fprintf(&builder, localized(locale, "反馈：没帮助 %d", "Feedback: not helpful %d")+"\n", unhelpful)
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
