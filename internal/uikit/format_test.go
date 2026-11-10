package uikit_test

// Tests for the shared compact-number formatter used by the usage footer and
// the session status strip.

import (
	"runtime"
	"testing"

	"github.com/zeokeel/zeokeel/internal/uikit"
)

// TestFormatCompactCount covers the DSH-style compact units: exact small
// counts, one decimal at most with K/M/B, trimmed ".0", rounding carry into the
// next unit and the signed/zero edge cases.
func TestFormatCompactCount(t *testing.T) {
	cases := []struct {
		count int
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{460, "460"},
		{999, "999"},
		{1000, "1K"},
		{1001, "1K"},
		{1234, "1.2K"},
		{1999, "2K"},        // 1.999 rounds to 2.0 and trims the decimal
		{173635, "173.6K"},  // the user's example from the usage footer
		{999949, "999.9K"},  // still under the next unit after rounding
		{999950, "1M"},      // rounding carries into the next unit
		{8_400_000, "8.4M"}, // the user's DSH example
		{1_200_000_000, "1.2B"},
		{1_500_000_000_000, "1500B"}, // capped at the last unit
		{-1234, "-1.2K"},
		{-999, "-999"},
		{-1000, "-1K"},
	}
	for _, test := range cases {
		if got := uikit.FormatCompactCount(test.count); got != test.want {
			t.Errorf("FormatCompactCount(%d) = %q, want %q", test.count, got, test.want)
		}
	}
}

// fakeLocale is a minimal contracts.LocaleService for the copy helpers.
type fakeLocale struct{ language string }

func (f *fakeLocale) Language() string        { return f.language }
func (f *fakeLocale) SetLanguage(lang string) { f.language = lang }
func (f *fakeLocale) T(key string) string     { return key }

// TestToolDisplayName covers the friendly approval-dialog tool names: known
// tools localize both ways, prefix families share a name and unknown/empty
// names are preserved.
func TestToolDisplayName(t *testing.T) {
	zh := &fakeLocale{language: "zh"}
	en := &fakeLocale{language: "en"}
	cases := []struct {
		tool   string
		wantZh string
		wantEn string
	}{
		{"bash", "运行命令", "Run command"},
		{"read_file", "读取文件", "Read file"},
		{"write_file", "写入文件", "Write file"},
		{"edit_file", "编辑文件", "Edit file"},
		{"list_dir", "列出目录", "List directory"},
		{"grep", "搜索文件内容", "Search file contents"},
		{"terminal_send", "发送终端命令", "Send terminal command"},
		{"terminal_read", "发送终端命令", "Send terminal command"},
		{"job_kill", "后台任务", "Background job"},
		{"web_fetch", "抓取网页", "Fetch web page"},
		{"web_search", "搜索网页", "Search the web"},
		{"memory_search", "记忆", "Memory"},
		{"team_task_create", "团队任务", "Team task"},
		{"working_directory", "查看工作目录", "Show working directory"},
		{"custom_tool", "custom_tool", "custom_tool"},
	}
	for _, test := range cases {
		if got := uikit.ToolDisplayName(zh, test.tool); got != test.wantZh {
			t.Errorf("ToolDisplayName(zh, %q) = %q, want %q", test.tool, got, test.wantZh)
		}
		if got := uikit.ToolDisplayName(en, test.tool); got != test.wantEn {
			t.Errorf("ToolDisplayName(en, %q) = %q, want %q", test.tool, got, test.wantEn)
		}
	}
	if got := uikit.ToolDisplayName(nil, "bash"); got != "运行命令" {
		t.Errorf("nil locale ToolDisplayName(bash) = %q, want the zh default", got)
	}
	if got := uikit.ToolDisplayName(zh, "  "); got != "" {
		t.Errorf("blank tool name = %q, want empty", got)
	}
}

func TestFormatShortcutNormalizesEveryNotation(t *testing.T) {
	primary := "Ctrl"
	if runtime.GOOS == "darwin" {
		primary = "Cmd"
	}
	cases := []struct {
		raw, want string
	}{
		{"CmdOrCtrl+Shift+Space", primary + " + Shift + Space"},
		{"Cmd/Ctrl + N", primary + " + N"},
		{"Ctrl+Alt+P", "Ctrl + Alt + P"},
		{"cmd+k", primary + " + K"},
		{" cmd + alt + i ", primary + " + Alt + I"},
		{"Ctrl+,", "Ctrl + ,"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := uikit.FormatShortcut(tc.raw); got != tc.want {
			t.Fatalf("FormatShortcut(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}
