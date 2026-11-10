package session_test

// Tests for the DSH-aligned work-steps policy: one live activity line while a
// turn runs (standard/compact), the full timeline in detailed/verbose, the
// finished turn's fold, and the completion / next-input collapse timing with the
// reader's explicit toggle always winning.

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// newWorkStepsHarness builds the conversation screen with the given work-steps
// settings; the permission default is untouched unless a test sets it too.
func newWorkStepsHarness(t *testing.T, chat *fakeChat, values map[string]string) (*harness, *permissionSettings) {
	t.Helper()
	settings := newPermissionSettings(values)
	return newPermissionHarness(t, chat, settings), settings
}

// liveStepChat is a conversation with one answered user message and one live
// assistant turn whose tools are the given calls.
func liveStepChat(tools ...contracts.ToolCall) *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "工作步骤", Running: true,
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "开始"},
			{
				ID: "a1", Role: contracts.ChatRoleAssistant,
				Text: "正在分析 **认证模块** 的实现", Reasoning: "先想一想",
				Streaming: true, Tools: tools,
			},
		},
	}, true)
	return chat
}

// replaceSession replaces the harness chat's active conversation (the tests use
// it to age a streaming turn's CreatedAt and to append user messages).
func replaceSession(chat *fakeChat, running bool, messages []contracts.ChatMessage) {
	chat.updateSession(contracts.ChatSession{
		ID: "sess-1", Title: "工作步骤", Running: running, Messages: messages,
	})
}

// TestLiveWorkStepsStandardSingleLine covers the default streaming shape: one
// activity line with the tool's live summary and the elapsed clock, the
// streaming prose under it, and none of the timeline's rows or indicators.
func TestLiveWorkStepsStandardSingleLine(t *testing.T) {
	chat := liveStepChat(contracts.ToolCall{
		ID: "t1", Name: "bash", Status: "running", Summary: "npm test --watch",
	})
	h, _ := newWorkStepsHarness(t, chat, nil)
	tester := h.tester()
	created := tester.Now().Add(-8 * time.Second)
	replaceSession(chat, true, []contracts.ChatMessage{
		{ID: "u1", Role: contracts.ChatRoleUser, Text: "开始"},
		{
			ID: "a1", Role: contracts.ChatRoleAssistant,
			Text: "正在分析 **认证模块** 的实现", Reasoning: "先想一想", Streaming: true,
			CreatedAt: created,
			Tools: []contracts.ToolCall{{
				ID: "t1", Name: "bash", Status: "running", Summary: "npm test --watch",
			}},
		},
	})
	tester.Frame()

	h.assertTexts(tester, "正在运行命令", "npm test --watch", "用时 8.0s", "认证模块")
	h.assertNoTexts(tester, "生成中", "思考中…", "过程 ·", "先想一想")
	h.assertNoExactTexts(tester, "运行命令")
	// The streaming prose still renders through the Markdown view.
	if tester.HasText("**认证模块**") {
		t.Fatalf("streaming markdown markers leaked: %v", tester.Texts())
	}
}

// TestLiveWorkStepsCompactOmitsDetail covers the compact mode: the same live line
// without the tool's live detail.
func TestLiveWorkStepsCompactOmitsDetail(t *testing.T) {
	chat := liveStepChat(contracts.ToolCall{
		ID: "t1", Name: "bash", Status: "running", Summary: "npm test --watch",
	})
	h, _ := newWorkStepsHarness(t, chat, map[string]string{
		contracts.SettingWorkStepsMode: contracts.WorkStepsCompact,
	})
	tester := h.tester()
	h.assertTexts(tester, "正在运行命令")
	h.assertNoTexts(tester, "npm test --watch")
}

// TestLiveWorkStepsDetailedKeepsTimeline covers the modes that keep the classic
// interleaved timeline while the turn runs: the thinking disclosure is open, the
// tool row reads inline and the generating indicator stays.
func TestLiveWorkStepsDetailedKeepsTimeline(t *testing.T) {
	for _, mode := range []string{contracts.WorkStepsDetailed, contracts.WorkStepsVerbose} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			chat := liveStepChat(contracts.ToolCall{
				ID: "t1", Name: "bash", Status: "running", Summary: "go build",
			})
			h, _ := newWorkStepsHarness(t, chat, map[string]string{
				contracts.SettingWorkStepsMode: mode,
			})
			tester := h.tester()
			h.assertTexts(tester, "思考中…", "先想一想", "运行命令", "生成中", "正在分析")
			h.assertNoTexts(tester, "正在运行命令")
		})
	}
}

// TestLiveWorkStepsKeepsDecisionCards covers that the live line replaces only
// the timeline: a decision card the turn waits on stays visible.
func TestLiveWorkStepsKeepsDecisionCards(t *testing.T) {
	chat := liveStepChat(contracts.ToolCall{
		ID: "t1", Name: "ask_user", Status: "running", Summary: "要继续吗？",
	})
	h, _ := newWorkStepsHarness(t, chat, nil)
	tester := h.tester()
	h.assertTexts(tester, "等待你的操作", "向用户提问", "正在分析")
}

// TestLiveWorkStepsElapsedNeedsStart covers the clock: a turn with CreatedAt
// shows 用时, a turn without a recorded start omits the clock entirely.
func TestLiveWorkStepsElapsedNeedsStart(t *testing.T) {
	chat := liveStepChat()
	h, _ := newWorkStepsHarness(t, chat, nil)
	tester := h.tester()
	h.assertNoTexts(tester, "用时")

	tester.Advance(3 * time.Second)
	tester.Frame()
	h.assertNoTexts(tester, "用时")

	created := tester.Now().Add(-1500 * time.Millisecond)
	replaceSession(chat, true, []contracts.ChatMessage{{
		ID: "a1", Role: contracts.ChatRoleAssistant, Streaming: true,
		CreatedAt: created,
	}})
	tester.Frame()
	h.assertTexts(tester, "用时 1.5s")
}

// TestWorkStepsFinishedModes covers the finished-turn fold per mode:
// compact/standard/detailed fold the process into the summary row (timeline one
// click away), while verbose always shows the full timeline under the meta row
// and renders no fold summary at all.
func TestWorkStepsFinishedModes(t *testing.T) {
	folded := []string{contracts.WorkStepsCompact, contracts.WorkStepsStandard, contracts.WorkStepsDetailed}
	for _, mode := range folded {
		mode := mode
		t.Run(mode+"-folds", func(t *testing.T) {
			h, _ := newWorkStepsHarness(t, foldChat(foldMessage()), map[string]string{
				contracts.SettingWorkStepsMode: mode,
			})
			tester := h.tester()
			summary := "过程 · 思考 · 2 个工具调用"
			h.assertTexts(tester, summary, "最终答复", "消息操作")
			h.assertNoTexts(tester, "运行命令", "读取文件", "先想一想，再运行命令。")
			h.click(tester, summary)
			h.assertTexts(tester, "运行命令", "读取文件", "思考", "最终答复")
		})
	}
	t.Run("verbose-never-folds", func(t *testing.T) {
		h, _ := newWorkStepsHarness(t, foldChat(foldMessage()), map[string]string{
			contracts.SettingWorkStepsMode: contracts.WorkStepsVerbose,
		})
		tester := h.tester()
		h.assertTexts(tester, "运行命令", "读取文件", "思考", "最终答复", "消息操作")
		h.assertNoTexts(tester, "过程 ·")
	})
}

// TestWorkStepsPureTextNeverFolds covers the non-foldable shape: a reply with no
// tools and no reasoning stays inline in every mode (nothing to fold).
func TestWorkStepsPureTextNeverFolds(t *testing.T) {
	for _, mode := range []string{contracts.WorkStepsCompact, contracts.WorkStepsStandard,
		contracts.WorkStepsDetailed, contracts.WorkStepsVerbose} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			h, _ := newWorkStepsHarness(t, foldChat(contracts.ChatMessage{
				ID: "a1", Role: contracts.ChatRoleAssistant, Text: "纯文本回答",
			}), map[string]string{contracts.SettingWorkStepsMode: mode})
			tester := h.tester()
			h.assertTexts(tester, "纯文本回答")
			h.assertNoTexts(tester, "过程 ·")
		})
	}
}

// TestWorkStepsNextInputKeepsTheLatestTurnOpen covers collapse=next-input: the
// turn after the last user message opens by default; once a newer user message
// arrives the old turn retracts to the folded default.
func TestWorkStepsNextInputKeepsTheLatestTurnOpen(t *testing.T) {
	message := foldMessage()
	messages := []contracts.ChatMessage{
		{ID: "u1", Role: contracts.ChatRoleUser, Text: "开始"},
		message,
	}
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "工作步骤", Messages: messages}, true)
	h, _ := newWorkStepsHarness(t, chat, map[string]string{
		contracts.SettingWorkStepsCollapse: contracts.WorkStepsCollapseNextInput,
	})
	tester := h.tester()
	summary := "过程 · 思考 · 2 个工具调用"
	// Latest turn: open by default (the summary row still names the process).
	h.assertTexts(tester, summary, "运行命令", "最终答复")

	// A newer user message retracts the finished turn to the folded default.
	messages = append(messages, contracts.ChatMessage{ID: "u2", Role: contracts.ChatRoleUser, Text: "继续"})
	replaceSession(chat, false, messages)
	tester.Frame()
	h.assertTexts(tester, summary, "消息操作")
	h.assertNoTexts(tester, "运行命令", "读取文件", "先想一想，再运行命令。")

	// The reader's explicit expand wins over the retracted default and survives
	// further new user messages.
	h.click(tester, summary)
	h.assertTexts(tester, "运行命令", "读取文件")
	messages = append(messages, contracts.ChatMessage{ID: "u3", Role: contracts.ChatRoleUser, Text: "再继续"})
	replaceSession(chat, false, messages)
	tester.Frame()
	h.assertTexts(tester, "运行命令", "读取文件")

	// An explicit collapse also survives; the turn stays folded for new input.
	h.click(tester, summary)
	h.assertNoTexts(tester, "运行命令")
	messages = append(messages, contracts.ChatMessage{ID: "u4", Role: contracts.ChatRoleUser, Text: "还继续"})
	replaceSession(chat, false, messages)
	tester.Frame()
	h.assertNoTexts(tester, "运行命令")
}

// TestWorkStepsNextInputGroupedTurn covers the grouped shape: the group's
// default follows its last message relative to the newest user message.
func TestWorkStepsNextInputGroupedTurn(t *testing.T) {
	steps := []contracts.ChatMessage{
		{ID: "u1", Role: contracts.ChatRoleUser, Text: "开始"},
		{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "第一步结果",
			Reasoning: "先想一想",
			Tools:     []contracts.ToolCall{{ID: "t1", Name: "bash", Status: "ok", Summary: "ls"}}},
		{ID: "a2", Role: contracts.ChatRoleAssistant, Text: "第二步结果",
			Tools: []contracts.ToolCall{{ID: "t2", Name: "read_file", Status: "ok", Summary: "main.go"}}},
		{ID: "a3", Role: contracts.ChatRoleAssistant, Text: "最终结果"},
	}
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "工作步骤", Messages: steps}, true)
	h, _ := newWorkStepsHarness(t, chat, map[string]string{
		contracts.SettingWorkStepsCollapse: contracts.WorkStepsCollapseNextInput,
	})
	tester := h.tester()
	summary := "过程 · 思考 · 2 个工具调用"
	h.assertTexts(tester, summary, "第一步结果", "第二步结果", "最终结果")

	steps = append(steps, contracts.ChatMessage{ID: "u2", Role: contracts.ChatRoleUser, Text: "继续"})
	replaceSession(chat, false, steps)
	tester.Frame()
	h.assertTexts(tester, summary, "最终结果")
	h.assertNoTexts(tester, "第一步结果", "第二步结果", "运行命令")
}

// TestWorkStepsVerboseDoesNotGroup covers verbose's no-bundling rule: each step
// renders as its own row with its timeline, never one 过程 summary.
func TestWorkStepsVerboseDoesNotGroup(t *testing.T) {
	h, _ := newWorkStepsHarness(t, foldChat(
		contracts.ChatMessage{ID: "u1", Role: contracts.ChatRoleUser, Text: "开始"},
		contracts.ChatMessage{ID: "a1", Role: contracts.ChatRoleAssistant, Text: "第一步结果",
			Reasoning: "先想一想",
			Tools:     []contracts.ToolCall{{ID: "t1", Name: "bash", Status: "ok", Summary: "ls"}}},
		contracts.ChatMessage{ID: "a2", Role: contracts.ChatRoleAssistant, Text: "第二步结果",
			Tools: []contracts.ToolCall{{ID: "t2", Name: "read_file", Status: "ok", Summary: "main.go"}}},
		contracts.ChatMessage{ID: "a3", Role: contracts.ChatRoleAssistant, Text: "最终结果"},
	), map[string]string{contracts.SettingWorkStepsMode: contracts.WorkStepsVerbose})
	tester := h.tester()
	h.assertTexts(tester, "第一步结果", "第二步结果", "最终结果", "运行命令", "读取文件", "思考")
	h.assertNoTexts(tester, "过程 ·")
}

// TestWorkStepsUnknownSettingsNormalize covers the settings contract: unknown or
// empty stored values fall back to standard mode and completion folding.
func TestWorkStepsUnknownSettingsNormalize(t *testing.T) {
	chat := liveStepChat(contracts.ToolCall{
		ID: "t1", Name: "bash", Status: "running", Summary: "npm test",
	})
	h, _ := newWorkStepsHarness(t, chat, map[string]string{
		contracts.SettingWorkStepsMode:     "bogus",
		contracts.SettingWorkStepsCollapse: "bogus",
	})
	tester := h.tester()
	// standard: the live line shows the detail, no timeline.
	h.assertTexts(tester, "正在运行命令", "npm test")
	h.assertNoTexts(tester, "生成中")

	// completion folding: a finished process folds by default.
	h2, _ := newWorkStepsHarness(t, foldChat(foldMessage()), map[string]string{
		contracts.SettingWorkStepsMode:     "bogus",
		contracts.SettingWorkStepsCollapse: "bogus",
	})
	tester2 := h2.tester()
	h2.assertTexts(tester2, "过程 · 思考 · 2 个工具调用")
	h2.assertNoTexts(tester2, "运行命令")
}

// TestWorkStepsPolicyReadEveryFrame covers the per-frame read: a settings change
// applies on the next frame without restarting the screen.
func TestWorkStepsPolicyReadEveryFrame(t *testing.T) {
	chat := liveStepChat(contracts.ToolCall{
		ID: "t1", Name: "bash", Status: "running", Summary: "npm test",
	})
	h, settings := newWorkStepsHarness(t, chat, nil)
	tester := h.tester()
	h.assertTexts(tester, "正在运行命令", "npm test")
	h.assertNoTexts(tester, "生成中")

	settings.set(contracts.SettingWorkStepsMode, contracts.WorkStepsDetailed)
	tester.Frame()
	h.assertTexts(tester, "生成中", "思考中…", "运行命令")
	h.assertNoTexts(tester, "正在运行命令")

	settings.set(contracts.SettingWorkStepsMode, contracts.WorkStepsCompact)
	tester.Frame()
	h.assertTexts(tester, "正在运行命令")
	h.assertNoTexts(tester, "npm test", "生成中")

	settings.set(contracts.SettingWorkStepsMode, contracts.WorkStepsVerbose)
	settings.set(contracts.SettingWorkStepsCollapse, contracts.WorkStepsCollapseNextInput)
	tester.Frame()
	h.assertTexts(tester, "生成中", "运行命令")
}

// TestWorkStepsActivityWords covers the activity vocabulary's tool mapping: one
// live line names what the agent is doing instead of the tool id.
func TestWorkStepsActivityWords(t *testing.T) {
	cases := []struct{ name, want string }{
		{"read_file", "正在读取文件"},
		{"list_dir", "正在读取文件"},
		{"glob", "正在读取文件"},
		{"read_image", "正在读取图片"},
		{"write_file", "正在写入文件"},
		{"edit_file", "正在编辑文件"},
		{"str_replace_editor", "正在编辑文件"},
		{"grep", "正在搜索代码"},
		{"bash", "正在运行命令"},
		{"terminal_send", "正在运行命令"},
		{"run_code", "正在运行代码"},
		{"web_search", "正在搜索网页"},
		{"web_fetch", "正在访问网页"},
		{"subagent", "正在协调子智能体"},
		{"workflow", "正在协调子智能体"},
		{"plan_review", "正在更新计划"},
		{"exit_plan_mode", "正在更新计划"},
		{"ask_user", "等待你的操作"},
		{"mcp__srv__lookup", "正在调用工具"},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			chat := liveStepChat(contracts.ToolCall{
				ID: "t1", Name: testCase.name, Status: "running", Summary: "细节",
			})
			h, _ := newWorkStepsHarness(t, chat, nil)
			tester := h.tester()
			h.assertTexts(tester, testCase.want, "细节")
		})
	}
	// A turn without a tool call is analyzing the request.
	h, _ := newWorkStepsHarness(t, liveStepChat(), nil)
	h.assertTexts(h.tester(), "正在分析请求")
}

// TestWorkStepsEnglishCopy covers the localized live line and the elapsed clock.
func TestWorkStepsEnglishCopy(t *testing.T) {
	chat := liveStepChat(contracts.ToolCall{
		ID: "t1", Name: "bash", Status: "running", Summary: "npm test --watch",
	})
	h, _ := newWorkStepsHarness(t, chat, nil)
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	created := tester.Now().Add(-8 * time.Second)
	replaceSession(chat, true, []contracts.ChatMessage{{
		ID: "a1", Role: contracts.ChatRoleAssistant, Streaming: true, CreatedAt: created,
		Tools: []contracts.ToolCall{{
			ID: "t1", Name: "bash", Status: "running", Summary: "npm test --watch",
		}},
	}})
	tester.Frame()
	h.assertTexts(tester, "Running a command", "npm test --watch", "Took 8.0s")
	h.assertNoTexts(tester, "正在运行命令")
}

// TestWorkStepsStreamingCompactKeepsBody covers that compact mode still streams
// the reply body (only the timeline and the detail are gone).
func TestWorkStepsStreamingCompactKeepsBody(t *testing.T) {
	chat := liveStepChat(contracts.ToolCall{
		ID: "t1", Name: "bash", Status: "running", Summary: "npm test",
	})
	h, _ := newWorkStepsHarness(t, chat, map[string]string{
		contracts.SettingWorkStepsMode: contracts.WorkStepsCompact,
	})
	tester := h.tester()
	h.assertTexts(tester, "认证模块")
	if _, ok := tester.AccessByRole(ui.RoleDisclosure, "思考中…"); ok {
		t.Fatalf("compact streaming rendered the reasoning disclosure; texts = %v", tester.Texts())
	}
}
