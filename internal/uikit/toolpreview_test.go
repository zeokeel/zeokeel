package uikit_test

// Unit tests for the tool-call preview and panel-section builders
// (uikit.ToolCallPreview / uikit.ToolPanelSections): the pure functions the
// transcript's tool rows render from one contracts.ToolCall. They moved into
// uikit so the session screen no longer imports the toolcards module
// implementation (2026-11 audit AR-01, the module boundary in AGENTS.md §7);
// the behavior is pinned here — the localized preview templates, the rune
// bounds with their honest truncation hints, and the backend-summary fallbacks.

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

func previewCall(name, arguments, summary string) contracts.ToolCall {
	return contracts.ToolCall{ID: "t1", Name: name, Status: "success", Arguments: arguments, Summary: summary}
}

// TestToolCallPreviewTemplates covers the localized action lines: a registered
// tool with the template's fields renders the product's copy, not the backend's
// raw English narration.
func TestToolCallPreviewTemplates(t *testing.T) {
	en := &entryDialogTestLocale{lang: "en"}
	cases := []struct {
		name   string
		call   contracts.ToolCall
		locale contracts.LocaleService
		want   string
	}{
		{"bash", previewCall("bash", `{"command":"ls -la"}`, "ran ls"), nil, "运行命令：ls -la"},
		{"bash english", previewCall("bash", `{"command":"ls -la"}`, "ran ls"), en, "Run command: ls -la"},
		{"pwsh", previewCall("pwsh", `{"command":"Get-ChildItem"}`, ""), nil, "运行命令：Get-ChildItem"},
		{"read_file", previewCall("read_file", `{"path":"main.go"}`, ""), nil, "读取文件：main.go"},
		{"write_file", previewCall("write_file", `{"path":"out.txt","content":"x"}`, ""), nil, "写入文件：out.txt"},
		{"edit_file", previewCall("edit_file", `{"path":"a.go"}`, ""), nil, "编辑文件：a.go"},
		{"str_replace_editor", previewCall("str_replace_editor", `{"path":"a.go"}`, ""), nil, "编辑文件：a.go"},
		{"list_dir", previewCall("list_dir", `{"path":"src"}`, ""), nil, "列出目录：src"},
		{"glob", previewCall("glob", `{"pattern":"*.go"}`, ""), nil, "查找文件：*.go"},
		{"grep", previewCall("grep", `{"pattern":"TODO"}`, ""), nil, "搜索代码：TODO"},
		{"read_image", previewCall("read_image", `{"path":"a.png"}`, ""), nil, "查看图片：a.png"},
		{"web_fetch", previewCall("web_fetch", `{"url":"https://example.com"}`, ""), nil, "抓取网页：https://example.com"},
		{"web_search query", previewCall("web_search", `{"query":"go generics"}`, ""), nil, "搜索网页：go generics"},
		{"web_search queries array", previewCall("web_search", `{"queries":["first","second"]}`, ""), nil, "搜索网页：first"},
		{"job_start", previewCall("job_start", `{"command":"sleep 2"}`, ""), nil, "启动后台任务：sleep 2"},
		{"job_output", previewCall("job_output", `{"job_id":"j1"}`, ""), nil, "读取后台任务输出：j1"},
		{"terminal_open", previewCall("terminal_open", `{"cwd":"/tmp"}`, ""), nil, "打开终端：/tmp"},
		{"terminal_send", previewCall("terminal_send", `{"session_id":"t1","text":"hello"}`, ""), nil, "向终端 t1 发送：hello"},
		{"terminal_send camelCase", previewCall("terminal_send", `{"sessionId":"t2","input":"hi"}`, ""), nil, "向终端 t2 发送：hi"},
		{"terminal_read", previewCall("terminal_read", `{"session_id":"t1"}`, ""), nil, "读取终端输出：t1"},
		{"terminal_signal", previewCall("terminal_signal", `{"session_id":"t1","signal":"SIGINT"}`, ""), nil, "向终端 t1 发送信号：SIGINT"},
		{"terminal_close", previewCall("terminal_close", `{"sessionId":"t1"}`, ""), nil, "关闭终端：t1"},
		{"subagent", previewCall("subagent", `{"description":"审查代码"}`, ""), nil, "启动子代理：审查代码"},
		{"subagent prompt fallback", previewCall("subagent", `{"prompt":"do it"}`, ""), nil, "启动子代理：do it"},
		{"team_task_create", previewCall("team_task_create", `{"subject":"修复登录"}`, ""), nil, "创建团队任务：修复登录"},
		{"team_task_update action", previewCall("team_task_update", `{"id":"task-1","action":"claim"}`, ""), nil, "更新任务 task-1（认领）"},
		{"team_task_update no action", previewCall("team_task_update", `{"id":"task-1"}`, ""), nil, "更新任务 task-1"},
		{"team_task_update unknown action", previewCall("team_task_update", `{"id":"task-1","action":"frobnicate"}`, ""), nil, "更新任务 task-1（frobnicate）"},
		{"team_task_get", previewCall("team_task_get", `{"id":"task-9"}`, ""), nil, "查看任务：task-9"},
		{"team_task_list plain", previewCall("team_task_list", `{}`, ""), nil, "列出团队任务"},
		{"team_task_list filters", previewCall("team_task_list", `{"status":"in_progress","owner":"alice"}`, ""), nil, "列出团队任务（进行中 · 负责人：alice）"},
		{"name case and space", previewCall(" BASH ", `{"command":"pwd"}`, ""), nil, "运行命令：pwd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := uikit.ToolCallPreview(tc.locale, tc.call); got != tc.want {
				t.Fatalf("ToolCallPreview = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestToolCallPreviewFallbacks covers the honest fallback: an unknown tool, a
// known tool whose arguments lack the template's fields, or unreadable
// arguments keep the backend result summary unchanged.
func TestToolCallPreviewFallbacks(t *testing.T) {
	cases := []struct {
		name string
		call contracts.ToolCall
		want string
	}{
		{"unknown tool", previewCall("mcp__server__tool", `{"x":"y"}`, "backend summary"), "backend summary"},
		{"missing field", previewCall("bash", `{"cwd":"/tmp"}`, "ran something"), "ran something"},
		{"empty field", previewCall("bash", `{"command":"   "}`, "ran something"), "ran something"},
		{"non-string field", previewCall("bash", `{"command":42}`, "ran something"), "ran something"},
		{"invalid json", previewCall("bash", `{command:`, "ran something"), "ran something"},
		{"non-object json", previewCall("bash", `["command"]`, "ran something"), "ran something"},
		{"empty arguments", previewCall("bash", "", "ran something"), "ran something"},
		{"terminal_send missing text", previewCall("terminal_send", `{"session_id":"t1"}`, "sent"), "sent"},
		{"team_task_update missing id", previewCall("team_task_update", `{"action":"claim"}`, "updated"), "updated"},
		{"empty everything", previewCall("unknown", "", ""), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := uikit.ToolCallPreview(nil, tc.call); got != tc.want {
				t.Fatalf("ToolCallPreview = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestToolCallPreviewBounds covers the one-line bound: the preview collapses
// whitespace and cuts at 96 runes with an ellipsis, so a template never makes a
// row wider than the raw summary it replaces.
func TestToolCallPreviewBounds(t *testing.T) {
	long := strings.Repeat("长", 200)
	got := uikit.ToolCallPreview(nil, previewCall("bash", `{"command":"`+long+`"}`, ""))
	want := "运行命令：" + strings.Repeat("长", 96-5) + "…"
	if got != want {
		t.Fatalf("bounded preview = %q (%d runes), want %q", got, len([]rune(got)), want)
	}
	if n := len([]rune(got)); n != 97 { // 96 runes + the ellipsis
		t.Fatalf("bounded preview rune count = %d, want 97", n)
	}
	// Line breaks and whitespace runs collapse to one space (the shared
	// ToolPreview normalization).
	multiline := uikit.ToolCallPreview(nil, previewCall("unknown", "", "first\nsecond\t third"))
	if multiline != "first second third" {
		t.Fatalf("multiline summary = %q, want one collapsed line", multiline)
	}
}

// TestToolPanelSectionsShell covers the bash/pwsh panel: the command section
// (mono, bounded, copyable) then the output section from the summary.
func TestToolPanelSectionsShell(t *testing.T) {
	sections := uikit.ToolPanelSections(nil, previewCall("bash", `{"command":"go test ./..."}`, "ok  \t3 packages"))
	if len(sections) != 2 {
		t.Fatalf("bash sections = %d, want 2 (command + output)", len(sections))
	}
	cmd, out := sections[0], sections[1]
	if cmd.Label != "命令" || cmd.Text != "go test ./..." || !cmd.Mono || !cmd.Bounded || cmd.CopyLabel != "复制命令" {
		t.Fatalf("command section = %+v", cmd)
	}
	if out.Label != "输出" || out.Text != "ok  \t3 packages" || !out.Mono || !out.Bounded || out.CopyLabel != "复制输出" {
		t.Fatalf("output section = %+v", out)
	}
	// English labels follow the locale.
	en := uikit.ToolPanelSections(&entryDialogTestLocale{lang: "en"},
		previewCall("bash", `{"command":"ls"}`, "done"))
	if en[0].Label != "Command" || en[1].Label != "Output" {
		t.Fatalf("english labels = %q / %q", en[0].Label, en[1].Label)
	}
	// Without a command the arguments fall back to the generic JSON input.
	generic := uikit.ToolPanelSections(nil, previewCall("bash", `{"cwd":"/tmp"}`, ""))
	if len(generic) != 1 || generic[0].Label != "输入" || !strings.Contains(generic[0].Text, `"cwd": "/tmp"`) {
		t.Fatalf("bash generic input = %+v", generic)
	}
}

// TestToolPanelSectionsWrite covers write_file: path + bounded content, with
// the honest truncation hint past the content bound.
func TestToolPanelSectionsWrite(t *testing.T) {
	sections := uikit.ToolPanelSections(nil, previewCall("write_file",
		`{"path":"out.txt","content":"hello"}`, ""))
	if len(sections) != 2 {
		t.Fatalf("write_file sections = %d, want 2 (path + content)", len(sections))
	}
	if sections[0].Label != "路径" || sections[0].Text != "out.txt" || sections[0].CopyLabel != "复制路径" {
		t.Fatalf("path section = %+v", sections[0])
	}
	if sections[1].Label != "内容" || sections[1].Text != "hello" || sections[1].Hint != "" {
		t.Fatalf("content section = %+v", sections[1])
	}
	long := strings.Repeat("字", 2500)
	bounded := uikit.ToolPanelSections(nil, previewCall("write_file",
		`{"path":"out.txt","content":"`+long+`"}`, ""))
	content := bounded[1]
	if len([]rune(content.Text)) != 2001 { // 2000 runes + ellipsis
		t.Fatalf("content runes = %d, want 2001", len([]rune(content.Text)))
	}
	if content.Hint != "内容已截断，仅显示前部分" {
		t.Fatalf("truncated content hint = %q", content.Hint)
	}
}

// TestToolPanelSectionsEdit covers edit_file/str_replace_editor: path, the
// removed/added tones, and the create command's file body fallback.
func TestToolPanelSectionsEdit(t *testing.T) {
	sections := uikit.ToolPanelSections(nil, previewCall("edit_file",
		`{"path":"a.go","old_string":"before","new_string":"after"}`, ""))
	if len(sections) != 3 {
		t.Fatalf("edit_file sections = %d, want 3 (path + before + after)", len(sections))
	}
	if sections[1].Label != "替换前" || sections[1].Tone != uikit.ToolSectionRemoved {
		t.Fatalf("before section = %+v", sections[1])
	}
	if sections[2].Label != "替换后" || sections[2].Tone != uikit.ToolSectionAdded {
		t.Fatalf("after section = %+v", sections[2])
	}
	// The snake_case spellings the backend also records.
	snake := uikit.ToolPanelSections(nil, previewCall("str_replace_editor",
		`{"path":"a.go","old_str":"b","new_str":"a"}`, ""))
	if len(snake) != 3 || snake[1].Text != "b" || snake[2].Text != "a" {
		t.Fatalf("snake_case edit sections = %+v", snake)
	}
	// create carries the new file body instead of an old/new pair.
	create := uikit.ToolPanelSections(nil, previewCall("str_replace_editor",
		`{"command":"create","path":"new.go","file_text":"package main"}`, ""))
	if len(create) != 2 || create[1].Label != "内容" || create[1].Text != "package main" {
		t.Fatalf("create sections = %+v", create)
	}
}

// TestToolPanelSectionsGeneric covers the default tool: pretty-printed bounded
// JSON input, the empty-argument shapes that render nothing, and the input
// truncation hint.
func TestToolPanelSectionsGeneric(t *testing.T) {
	sections := uikit.ToolPanelSections(nil, previewCall("mcp__x__y", `{"b":2,"a":1}`, ""))
	if len(sections) != 1 || sections[0].Label != "输入" {
		t.Fatalf("generic sections = %+v", sections)
	}
	if !strings.Contains(sections[0].Text, "\n  ") {
		t.Fatalf("generic input is not indented JSON: %q", sections[0].Text)
	}
	for _, empty := range []string{"", "{}", "null", "   "} {
		if got := uikit.ToolPanelSections(nil, previewCall("mcp__x__y", empty, "")); len(got) != 0 {
			t.Fatalf("empty arguments %q rendered %+v", empty, got)
		}
	}
	long := strings.Repeat("x", 5000)
	bounded := uikit.ToolPanelSections(nil, previewCall("mcp__x__y", `{"data":"`+long+`"}`, ""))
	if len([]rune(bounded[0].Text)) != 4001 {
		t.Fatalf("input runes = %d, want 4001", len([]rune(bounded[0].Text)))
	}
	if bounded[0].Hint != "输入已截断，仅显示前部分" {
		t.Fatalf("input truncation hint = %q", bounded[0].Hint)
	}
}

// TestToolPanelSectionsOutputTruncation covers the backend's own cut: a summary
// at the read-model bound ending with the ellipsis gets the output hint; a
// short summary with an ellipsis does not.
func TestToolPanelSectionsOutputTruncation(t *testing.T) {
	long := strings.Repeat("y", 4000) + "…"
	sections := uikit.ToolPanelSections(nil, previewCall("bash", `{"command":"ls"}`, long))
	out := sections[len(sections)-1]
	if out.Hint != "输出已截断，仅显示前部分" {
		t.Fatalf("backend-truncated output hint = %q", out.Hint)
	}
	short := uikit.ToolPanelSections(nil, previewCall("bash", `{"command":"ls"}`, "done…"))
	if short[len(short)-1].Hint != "" {
		t.Fatalf("short ellipsis summary claimed truncation: %+v", short[len(short)-1])
	}
	// No summary, no output section.
	none := uikit.ToolPanelSections(nil, previewCall("bash", `{"command":"ls"}`, "  "))
	if len(none) != 1 {
		t.Fatalf("empty summary rendered %d sections, want 1", len(none))
	}
}
