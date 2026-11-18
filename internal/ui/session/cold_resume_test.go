package session_test

// Frame tests for cold-resuming a finished teammate/subagent: a Historical row
// with Resumable=true offers the continue action and the follow-up composer, the
// send really calls ChildAgentService.SendChildAgent (the backend cold-resumes
// the saved child session), in-flight/answered states stay truthful, and a
// non-resumable finished record stays read-only with its reason.

import (
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// resumableHistoricalChild is one finished child the backend can resume.
func resumableHistoricalChild() contracts.ChildAgent {
	return contracts.ChildAgent{
		ID: "history-m-1", ParentSessionID: "sess-1", Name: "上次的研究员",
		Kind: contracts.ChildAgentTeammate, Mode: contracts.ChildAgentContinuable,
		Status: contracts.ChildAgentCompleted, Historical: true, Resumable: true,
		Prompt: "研究发布说明", Output: "上次的答案",
	}
}

// resumableHistoricalTeamMember is the team-overlay row for that child.
func resumableHistoricalTeamMember() contracts.TeamMember {
	return contracts.TeamMember{
		ID: "history-m-1", Name: "上次的研究员", RunState: contracts.TeamRunDone,
		SessionID: "sess-1", Historical: true, Resumable: true, Last: "上次的答案",
	}
}

func TestTeamOverlayResumableHistoryOffersContinue(t *testing.T) {
	children := &fakeChildAgents{agents: []contracts.ChildAgent{resumableHistoricalChild()}}
	h := newAgentSurfaceHarness(t, headerChat("历史团队"), agentServices{
		team:     &fakeTeam{members: []contracts.TeamMember{resumableHistoricalTeamMember()}},
		children: children,
	})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "智能体团队")
	h.assertTexts(tester, "上次的研究员", "已结束 · 可继续")
	if _, ok := tester.Find("继续此队友 上次的研究员"); !ok {
		t.Fatalf("resumable historical member has no continue action; texts = %v", tester.Texts())
	}
	h.click(tester, "继续此队友 上次的研究员")
	// The action opens the child conversation with its cold-resume composer.
	h.assertTexts(tester, "上次的研究员", "已结束 · 可继续",
		"历史记录（应用重启后仍保留）：可继续；发送后会接着已保存的会话继续。",
		"发送给 上次的研究员")
}

func TestTeamOverlayNonResumableHistoryIsReadOnly(t *testing.T) {
	children := &fakeChildAgents{agents: []contracts.ChildAgent{{
		ID: "history-m-2", ParentSessionID: "sess-1", Name: "旧的队友",
		Kind: contracts.ChildAgentTeammate, Mode: contracts.ChildAgentContinuable,
		Status: contracts.ChildAgentCompleted, Historical: true,
	}}}
	h := newAgentSurfaceHarness(t, headerChat("历史团队"), agentServices{
		team: &fakeTeam{members: []contracts.TeamMember{{
			ID: "history-m-2", Name: "旧的队友", RunState: contracts.TeamRunDone,
			SessionID: "sess-1", Historical: true,
		}}},
		children: children,
	})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "智能体团队")
	h.assertTexts(tester, "已结束 · 只读")
	if _, ok := tester.Find("继续此队友 旧的队友"); ok {
		t.Fatalf("non-resumable history shows a continue action; texts = %v", tester.Texts())
	}
	if _, ok := tester.Find("打开成员 旧的队友"); !ok {
		t.Fatalf("non-resumable history lost its read-only open action; texts = %v", tester.Texts())
	}
	h.click(tester, "打开成员 旧的队友")
	h.assertTexts(tester, "已结束的记录（应用重启后仍保留）：只读，不能再继续对话。")
	h.assertNoTexts(tester, "发送给 旧的队友")
}

func TestHistoricalChildResumeSendCallsChildAgentService(t *testing.T) {
	chat := headerChat("冷恢复会话")
	children := &fakeChildAgents{agents: []contracts.ChildAgent{resumableHistoricalChild()}}
	children.answer = "冷恢复后的回答"
	gate := make(chan struct{})
	children.sendGate = gate
	h := newAgentSurfaceHarness(t, chat, agentServices{children: children})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "子智能体 1")
	h.assertTexts(tester, "队友", "已结束 · 可继续")
	h.click(tester, "继续此队友 上次的研究员")
	h.assertTexts(tester, "发送给 上次的研究员",
		"发送后会恢复这条历史记录的会话，并等待它的回答。")

	input, ok := tester.Find("发送给 上次的研究员")
	if !ok {
		t.Fatalf("cold-resume composer is missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(input.X+input.W/2, input.Y+input.H/2)
	tester.Frame()
	tester.Type("继续检查剩余文档")
	tester.Frame()
	clickRoleButton(t, tester, "发送")

	// While the backend works the UI says it sent the message and is waiting —
	// no invented progress bar or completion.
	waitFor(t, tester, func() bool { return tester.HasText("已发送，等待该队友的回复。") }, "in-flight send state")

	close(gate)
	waitFor(t, tester, func() bool { return len(children.sent()) == 1 }, "SendChildAgent call")
	sent := children.sent()[0]
	if sent.id != "history-m-1" || sent.message != "继续检查剩余文档" {
		t.Fatalf("SendChildAgent(%q, %q), want the historical id and typed message", sent.id, sent.message)
	}
	waitFor(t, tester, func() bool { return tester.HasText("冷恢复后的回答") }, "cold-resume answer")
	h.assertNoTexts(tester, "已发送，等待该队友的回复。")
}

func TestHistoricalChildResumeSendErrorsAreLocalized(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{errors.New(`agent: child agent "上次的研究员" is already running`),
			"发送失败：该队友正在运行，请等本次运行结束后再发送。"},
		{errors.New("agent: this child session cannot be resumed: the child has no durable session id"),
			"发送失败：这条历史记录无法继续：缺少可恢复的会话数据。"},
	}
	for _, tc := range cases {
		children := &fakeChildAgents{agents: []contracts.ChildAgent{resumableHistoricalChild()}}
		children.sendErr = tc.err
		h := newAgentSurfaceHarness(t, headerChat("冷恢复错误"), agentServices{children: children})
		tester := h.tester()
		tester.Frame()
		h.click(tester, "子智能体 1")
		h.click(tester, "继续此队友 上次的研究员")
		input, ok := tester.Find("发送给 上次的研究员")
		if !ok {
			t.Fatalf("cold-resume composer is missing; texts = %v", tester.Texts())
		}
		tester.ClickAt(input.X+input.W/2, input.Y+input.H/2)
		tester.Frame()
		tester.Type("再试一次")
		tester.Frame()
		clickRoleButton(t, tester, "发送")
		waitFor(t, tester, func() bool { return tester.HasText(tc.want) }, "localized send error")
	}
}

func TestHistoricalSubagentSwitcherOffersContinue(t *testing.T) {
	children := &fakeChildAgents{agents: []contracts.ChildAgent{{
		ID: "history-s-1", ParentSessionID: "sess-1", Name: "上次的子代理",
		Kind: contracts.ChildAgentSubagent, Mode: contracts.ChildAgentOneShot,
		Status: contracts.ChildAgentCompleted, Historical: true, Resumable: true,
		Output: "上次的输出",
	}}}
	h := newAgentSurfaceHarness(t, headerChat("历史会话"), agentServices{children: children})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "子智能体 1")
	h.assertTexts(tester, "子智能体", "已结束 · 可继续")
	if _, ok := tester.Find("继续此子代理 上次的子代理"); !ok {
		t.Fatalf("resumable historical subagent has no continue action; texts = %v", tester.Texts())
	}
	h.click(tester, "继续此子代理 上次的子代理")
	h.assertTexts(tester, "发送给 上次的子代理",
		"发送后会恢复这条历史记录的会话，并等待它的回答。")
}

// TestColdResumeCopyIsPlainLanguage covers US-10: the user-facing copy says
// what happens in plain words (恢复/继续 the saved conversation) — the internal
// term 冷恢复 never reaches the frame.
func TestColdResumeCopyIsPlainLanguage(t *testing.T) {
	children := &fakeChildAgents{agents: []contracts.ChildAgent{resumableHistoricalChild()}}
	h := newAgentSurfaceHarness(t, headerChat("白话文案"), agentServices{children: children})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "子智能体 1")
	h.click(tester, "继续此队友 上次的研究员")
	tester.Frame()
	h.assertTexts(tester, "发送后会恢复这条历史记录的会话，并等待它的回答。")
	for _, text := range tester.Texts() {
		if strings.Contains(text, "冷恢复") {
			t.Fatalf("US-10: internal term 冷恢复 leaked into user copy: %q", text)
		}
	}
}

func TestColdResumeEnglishCopy(t *testing.T) {
	children := &fakeChildAgents{agents: []contracts.ChildAgent{resumableHistoricalChild()}}
	h := newAgentSurfaceHarness(t, headerChat("history"), agentServices{children: children})
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()

	h.click(tester, "Subagents 1")
	h.assertTexts(tester, "Teammate", "Finished · continuable")
	if _, ok := tester.Find("Continue teammate 上次的研究员"); !ok {
		t.Fatalf("English continue action missing; texts = %v", tester.Texts())
	}
	h.click(tester, "Continue teammate 上次的研究员")
	h.assertTexts(tester,
		"Finished record (kept after a restart): continuable; sending picks the saved conversation back up.",
		"Send to 上次的研究员")
	// Sending uses the same localized pipeline.
	input, ok := tester.Find("Send to 上次的研究员")
	if !ok {
		t.Fatalf("English cold-resume composer missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(input.X+input.W/2, input.Y+input.H/2)
	tester.Frame()
	tester.Type("continue")
	tester.Frame()
	// The send control is the composer shell's square: its accessible name is the
	// shell's own "Send" (the description names the recipient).
	if info, ok := tester.AccessByRole(ui.RoleButton, "Send"); !ok {
		t.Fatalf("English send control missing; texts = %v", tester.Texts())
	} else if info.Description == "" {
		t.Fatalf("English send control has no description; %+v", info)
	}
	// The English header/hint never leaks raw Chinese keys.
	for _, text := range tester.Texts() {
		if strings.Contains(text, "发送给") {
			t.Fatalf("English child view leaked Chinese copy: %q", text)
		}
	}
}
