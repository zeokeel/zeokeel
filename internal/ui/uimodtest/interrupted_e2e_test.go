package uimodtest

// Real-backend interruption E2E: a session whose durable checkpoint is still in
// a non-terminal phase restores as ChatSession.Interrupted; the session screen
// explains the un-replayed run and offers 继续 (focus the composer) / 忽略.

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestInterruptedRestoreE2E(t *testing.T) {
	server := fakeChatServer(t)
	t.Cleanup(server.Close)
	dataDir := t.TempDir()
	settings := &memSettings{values: map[string]string{
		contracts.SettingsChatModel:   "test-model",
		contracts.SettingsChatBaseURL: server.URL,
	}}

	root, _, loader := bootDesktop(t, dataDir, settings)
	chat := requireChat(t, root)
	sessionID, err := chat.NewSession("被中断的会话")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := chat.Send(sessionID, "跑一下发布检查"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, chat, sessionID, 10*time.Second)

	// Simulate a crash mid-turn: leave a non-terminal checkpoint behind.
	store, err := root.Require(contracts.KeyStore)
	if err != nil {
		t.Fatalf("require store: %v", err)
	}
	if err := store.CheckpointStore().Save(context.Background(), sessionID, &rig.Checkpoint{
		SessionID: sessionID,
		TurnID:    "turn-interrupted-e2e",
		Phase:     rig.CheckpointPhaseRequested,
	}); err != nil {
		t.Fatalf("save interrupted checkpoint: %v", err)
	}
	if err := loader.Close(); err != nil {
		t.Fatalf("close loader: %v", err)
	}
	if err := root.DisposeAll(); err != nil {
		t.Fatalf("dispose root: %v", err)
	}

	// Restart the product on the same data dir: the agent restore surfaces the
	// unfinished run as a read-model hint.
	root2, uiRoot2, loader2 := bootDesktop(t, dataDir, settings)
	t.Cleanup(func() {
		_ = loader2.Close()
		_ = root2.DisposeAll()
	})
	chat2 := requireChat(t, root2)
	var restored contracts.ChatSession
	found := false
	for _, info := range chat2.Sessions() {
		if info.ID != sessionID {
			continue
		}
		if session, ok := chat2.Session(sessionID); ok {
			restored = session
			found = true
		}
	}
	if !found || !restored.Interrupted {
		t.Fatalf("restored session = %+v (found %v), want Interrupted", restored, found)
	}
	if err := chat2.SetActiveSession(sessionID); err != nil {
		t.Fatalf("activate restored session: %v", err)
	}
	if err := uiRoot2.Navigator().Push("session"); err != nil {
		t.Fatalf("Push(session): %v", err)
	}
	tester := ui.NewTester(uiRoot2.View, 1280, 800)
	tester.Frame()
	tester.Frame()
	for _, want := range []string{"上次运行被中断", "未重放；请检查工作区状态，然后在输入框继续。", "在输入框继续"} {
		if !tester.HasText(want) {
			t.Fatalf("interrupted notice missing %q; texts = %v", want, tester.Texts())
		}
	}

	// 在输入框继续 only hands the composer the keyboard.
	if err := tester.Click("在输入框继续"); err != nil {
		t.Fatalf("click 在输入框继续: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if !tester.Focused("输入消息…") {
		t.Fatalf("在输入框继续 did not focus the composer; texts = %v", tester.Texts())
	}
	session, _ := chat2.Session(sessionID)
	if len(session.Messages) != 2 {
		t.Fatalf("在输入框继续 changed the restored transcript: %+v", session.Messages)
	}

	// 忽略 hides only the notice (the read model hint stays until the next run).
	// The continue toast sits at the same top-right corner and would intercept
	// the click, so close it first as a user would.
	if tester.HasText("关闭通知") {
		if err := tester.Click("关闭通知"); err != nil {
			t.Fatalf("close continue toast: %v; texts = %v", err, tester.Texts())
		}
		tester.Frame()
	}
	if err := tester.Click("忽略上次运行中断提示"); err != nil {
		t.Fatalf("dismiss interrupted notice: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if tester.HasText("上次运行被中断") {
		t.Fatalf("notice survived the dismiss; texts = %v", tester.Texts())
	}
	if session, _ := chat2.Session(sessionID); !session.Interrupted {
		t.Fatal("dismiss changed the backend read-model hint")
	}
}
