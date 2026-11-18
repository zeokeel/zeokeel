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
	"github.com/zeokeel/zeokeel/internal/hosttools"
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
	toolkit := requireService(t, root, hosttools.KeyRegistry)
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
	// The team card's sub-line collapsed the old 历史 chip and the run state into
	// ONE status phrase (visual audit): a restored, resumable member reads
	// 「已结束 · 可继续」 — not 「历史 · 可继续」, and not two competing state words.
	if !tester.HasText("已结束 · 可继续") {
		t.Fatalf("restored member lacks the continuable finished marker; texts = %v", tester.Texts())
	}
	for _, gone := range []string{"历史 · 可继续", "已完成 · 可继续"} {
		if tester.HasText(gone) {
			t.Fatalf("restored member still shows the superseded marker %q beside its status; texts = %v", gone, tester.Texts())
		}
	}
	continueLabel := "继续此队友 冷恢复队友"
	if _, ok := tester.Find(continueLabel); !ok {
		t.Fatalf("restored member has no continue action; texts = %v", tester.Texts())
	}
	if err := tester.Click(continueLabel); err != nil {
		t.Fatalf("open cold-resume child view: %v", err)
	}
	tester.Frame()
	// The child view is the conversation's own composer shell now (p2-team-child
	// audit): the card AND its input carry the unique 发送给 <name> name (the
	// placeholder 继续该队友… is visual copy only, so a screen reader or a driver
	// never anchors on a string that changes with the state), and the one action
	// is the shell's 发送. The old stand-alone 「发送消息给 <name>」 button label is
	// gone; that sentence survives only as the send control's description.
	if input, ok := tester.AccessByRole(ui.RoleTextField, "发送给 冷恢复队友"); !ok {
		t.Fatalf("child composer input is not named 发送给 <name>; texts = %v", tester.Texts())
	} else if input.Bounds.H <= 0 {
		t.Fatalf("child composer input has no height: %+v", input)
	}
	// The child view's status is ONE phrase: 已结束 · 可继续. The retired
	// three-word line (已完成 + 已结束记录 + 可继续 as separate items) must not
	// come back beside it.
	if !tester.HasText("已结束 · 可继续") {
		t.Fatalf("child view lacks the continued-record status phrase; texts = %v", tester.Texts())
	}
	for _, gone := range []string{"已结束记录", "历史 · 可继续"} {
		if tester.HasText(gone) {
			t.Fatalf("child view still shows the superseded status copy %q; texts = %v", gone, tester.Texts())
		}
	}
	input, ok := tester.AccessByRole(ui.RoleTextField, "发送给 冷恢复队友")
	if !ok {
		t.Fatalf("cold-resume input missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(input.Bounds.X+input.Bounds.W/2, input.Bounds.Y+input.Bounds.H/2)
	tester.Frame()
	if !tester.Focused("发送给 冷恢复队友") {
		t.Fatalf("cold-resume input did not take focus; texts = %v", tester.Texts())
	}
	tester.Type("继续检查剩余章节")
	tester.Frame()
	if err := tester.Click("发送"); err != nil {
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
