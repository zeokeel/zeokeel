package uimodtest

// Real-backend cold-resume E2E: a teammate that finished before an app restart
// comes back as a Historical+Resumable row; the 智能体团队 overlay offers
// 「继续此队友」, the child view opens its composer, and a follow-up message
// cold-resumes the saved child session and returns a real answer.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestColdResumeHistoricalTeamMemberE2E(t *testing.T) {
	server := fakeChatServer(t)
	t.Cleanup(server.Close)
	dataDir := t.TempDir()
	settings := &memSettings{values: map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	}}

	root, _, loader := bootDesktop(t, dataDir, settings)
	chat := requireChat(t, root)
	sessionID, err := chat.NewSession("冷恢复会话")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	toolkit := requireService(t, root, contracts.KeyToolkit)
	team := requireService(t, root, contracts.KeyTeam)
	spawnCtx := contracts.WithAgentSession(context.Background(), sessionID)
	result, err := toolkit.Invoke(spawnCtx, rig.ToolCall{
		ID: "spawn-cold", Name: "spawn_teammate",
		Arguments: `{"name":"冷恢复队友","prompt":"先检查一次发布说明"}`,
	})
	if err != nil || result.IsError {
		t.Fatalf("spawn_teammate = %+v (err %v)", result, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	finished := contracts.TeamMember{}
	for time.Now().Before(deadline) {
		for _, member := range team.Members() {
			if member.Name == "冷恢复队友" && member.Last != "" {
				finished = member
			}
		}
		if finished.ID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if finished.ID == "" {
		t.Fatalf("teammate did not finish; members = %+v", team.Members())
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("close loader: %v", err)
	}
	if err := root.DisposeAll(); err != nil {
		t.Fatalf("dispose root: %v", err)
	}

	// Restart on the same data dir: the durable child history restores the
	// teammate as a finished record the backend can cold-resume.
	root2, uiRoot2, loader2 := bootDesktop(t, dataDir, settings)
	t.Cleanup(func() {
		_ = loader2.Close()
		_ = root2.DisposeAll()
	})
	chat2 := requireChat(t, root2)
	if err := chat2.SetActiveSession(sessionID); err != nil {
		t.Fatalf("activate restored session: %v", err)
	}
	team2 := requireService(t, root2, contracts.KeyTeam)
	var historical contracts.TeamMember
	for time.Now().Before(deadline) {
		for _, member := range team2.Members() {
			if member.Name == "冷恢复队友" && member.Historical && member.Resumable {
				historical = member
			}
		}
		if historical.ID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if historical.ID == "" {
		t.Fatalf("restored teammate = %+v, want Historical+Resumable", team2.Members())
	}
	if err := uiRoot2.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	tester := ui.NewTester(uiRoot2.View, 1280, 800)
	tester.Frame()
	tester.Frame()

	if err := tester.Click("智能体团队"); err != nil {
		t.Fatalf("open team overlay: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.HasText("历史 · 可继续") {
		t.Fatalf("restored member lacks the continuable history marker; texts = %v", tester.Texts())
	}
	continueLabel := "继续此队友 冷恢复队友"
	if _, ok := tester.Find(continueLabel); !ok {
		t.Fatalf("restored member has no continue action; texts = %v", tester.Texts())
	}
	if err := tester.Click(continueLabel); err != nil {
		t.Fatalf("open cold-resume child view: %v", err)
	}
	tester.Frame()
	for _, want := range []string{"发送给 冷恢复队友", "发送消息给 冷恢复队友"} {
		if !tester.HasText(want) {
			t.Fatalf("cold-resume composer missing %q; texts = %v", want, tester.Texts())
		}
	}
	input, ok := tester.Find("发送给 冷恢复队友")
	if !ok {
		t.Fatalf("cold-resume input missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(input.X+input.W/2, input.Y+input.H/2)
	tester.Frame()
	tester.Type("继续检查剩余章节")
	tester.Frame()
	if err := tester.Click("发送消息给 冷恢复队友"); err != nil {
		t.Fatalf("send cold-resume message: %v; texts = %v", err, tester.Texts())
	}

	// The real cold resume runs a provider call and returns the scripted answer.
	deadline = time.Now().Add(20 * time.Second)
	answered := false
	for time.Now().Before(deadline) {
		tester.Frame()
		for _, text := range tester.Texts() {
			if strings.Contains(text, "你好，我是 ZeoKeel。") {
				answered = true
			}
		}
		if answered {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !answered {
		t.Fatalf("cold-resume answer missing; texts = %v", tester.Texts())
	}
	for _, member := range team2.Members() {
		if member.Name != "冷恢复队友" {
			continue
		}
		if !member.Historical || !member.Resumable {
			t.Fatalf("member after cold resume = %+v, want Historical+Resumable again", member)
		}
	}
}
