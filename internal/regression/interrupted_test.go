package regression

// Adversarial audit: the restored-run interruption notice. ChatSession.Interrupted
// is a restore-only read-model hint; the session screen explains the un-replayed
// run quietly, 继续 only focuses the composer (never replays), 忽略 hides the
// notice for that session in this app run only, the next run clears it, a normal
// session shows nothing, and no sidebar badge is added.

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func interruptedAuditSession(id, title string) contracts.ChatSession {
	return contracts.ChatSession{
		ID: id, Title: title, Interrupted: true,
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "跑一下发布检查"}},
	}
}

func countLabels(tester interface {
	Texts() []string
}, fragment string) int {
	count := 0
	for _, text := range tester.Texts() {
		if strings.Contains(text, fragment) {
			count++
		}
	}
	return count
}

func TestInterruptedNoticeActionsAndClear(t *testing.T) {
	harness := startSessionUI(t, interruptedAuditSession("sess-int", "恢复会话"))
	requireHasText(t, harness.tester, "上次运行被中断")
	requireHasText(t, harness.tester, "未重放；请检查工作区状态，然后在输入框继续。")
	requireHasText(t, harness.tester, "在输入框继续")
	// The notice has exactly one home (above the transcript): no sidebar copy and
	// no duplicate banner.
	if got := countLabels(harness.tester, "上次运行被中断"); got != 1 {
		t.Fatalf("interrupted notice rendered %d times, want once in the session body", got)
	}

	// 在输入框继续 hands the composer the keyboard without replaying anything.
	if err := harness.tester.Click("在输入框继续"); err != nil {
		t.Fatalf("click 在输入框继续: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	if sends := harness.chat.recordedSends(); len(sends) != 0 {
		t.Fatalf("继续 replayed a prompt: %v", sends)
	}
	requireHasText(t, harness.tester, "上次运行被中断")

	// 忽略 hides it for this app run and does not touch the read model.
	if err := harness.tester.Click("忽略上次运行中断提示"); err != nil {
		t.Fatalf("dismiss interrupted notice: %v; texts = %v", err, harness.tester.Texts())
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "上次运行被中断")
	session, _ := harness.chat.Session("sess-int")
	if !session.Interrupted {
		t.Fatal("dismissing the notice changed the backend read model")
	}

	// The next run clears the hint; even if it were restored again in this app
	// run the dismissal stays (it is "this visit" only).
	cleared := interruptedAuditSession("sess-int", "恢复会话")
	cleared.Interrupted = false
	cleared.Messages = append(cleared.Messages, contracts.ChatMessage{
		ID: "m2", Role: contracts.ChatRoleAssistant, Text: "继续处理",
	})
	harness.chat.updateSession(cleared)
	for i := 0; i < 2; i++ {
		harness.tester.Frame()
	}
	requireNoText(t, harness.tester, "上次运行被中断")
}

func TestInterruptedNoticePerSessionAndNormalSession(t *testing.T) {
	harness := startSessionUI(t,
		interruptedAuditSession("sess-a", "会话 A"),
		interruptedAuditSession("sess-b", "会话 B"),
	)
	requireHasText(t, harness.tester, "上次运行被中断")
	if err := harness.tester.Click("忽略上次运行中断提示"); err != nil {
		t.Fatalf("dismiss session A notice: %v", err)
	}
	harness.tester.Frame()
	requireNoText(t, harness.tester, "上次运行被中断")

	if err := harness.chat.SetActiveSession("sess-b"); err != nil {
		t.Fatalf("activate sess-b: %v", err)
	}
	for i := 0; i < 2; i++ {
		harness.tester.Frame()
	}
	requireHasText(t, harness.tester, "上次运行被中断")
	if err := harness.chat.SetActiveSession("sess-a"); err != nil {
		t.Fatalf("activate sess-a: %v", err)
	}
	for i := 0; i < 2; i++ {
		harness.tester.Frame()
	}
	requireNoText(t, harness.tester, "上次运行被中断")

	// A normal conversation never grows the notice.
	normal := startSessionUI(t, contracts.ChatSession{
		ID: "sess-ok", Title: "正常会话",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleAssistant, Text: "完成"}},
	})
	requireNoText(t, normal.tester, "上次运行被中断")
}
