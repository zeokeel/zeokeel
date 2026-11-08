package regression

// Adversarial audit: manual context compaction. The agent's SessionCompactor
// replaces the persisted history with the compacted window (success), rejects
// running sessions / a zero budget / empty history with stable errors, and the
// session screen's /compact command is hidden without a compactor and maps every
// stable error to localized copy.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/agent"
	sessionui "github.com/zeokeel/zeokeel/internal/ui/session"
)

// compactModelScript answers normal turns with text and recognizes the
// compactor's summary request by its system prompt.
func compactModelScript() func([]byte) (int, string) {
	return func(body []byte) (int, string) {
		if strings.Contains(string(body), "conversation summarizer") {
			return 0, sseAnswer("condensed summary")
		}
		return 0, sseAnswer("turn answer")
	}
}

// TestAgentCompactNow covers the agent SessionCompactor: success replaces the
// persisted history, running/zero-budget/empty sessions fail with the stable
// errors.
func TestAgentCompactNow(t *testing.T) {
	harness := startAgent(t, agent.Config{ContextBudget: 2000}, compactModelScript())
	sessionID := harness.newChatSession(t, "Compact session")
	// More than the compactor's keep-last window (8) is required for a real
	// compaction: ten turns give twenty messages.
	for index := 0; index < 10; index++ {
		harness.sendAndWait(t, sessionID, "turn-"+itoa(int64(index)))
	}
	compactor, ok := harness.chat.(contracts.SessionCompactor)
	if !ok {
		t.Fatal("the agent chat service does not implement SessionCompactor")
	}
	store := requireService(t, harness.h.ctx, contracts.KeyStore)
	before, err := store.History().Load(context.Background(), sessionID)
	if err != nil || len(before) == 0 {
		t.Fatalf("load history before compaction = %d (err %v)", len(before), err)
	}
	result, err := compactor.CompactNow(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	if result.Replaced != len(before) || result.Saved <= 0 || result.Saved >= result.Replaced {
		t.Fatalf("compact result = %+v (before %d)", result, len(before))
	}
	if result.BeforeTokens <= 0 || result.AfterTokens <= 0 || result.AfterTokens >= result.BeforeTokens {
		t.Fatalf("token estimates = %d -> %d, want a decrease", result.BeforeTokens, result.AfterTokens)
	}
	after, err := store.History().Load(context.Background(), sessionID)
	if err != nil || len(after) != result.Saved {
		t.Fatalf("stored history after compaction = %d messages (want %d, err %v)", len(after), result.Saved, err)
	}
	if got := harness.chat.ActiveSession(); got != sessionID {
		t.Fatalf("active session changed to %q", got)
	}
	if _, err := compactor.CompactNow(context.Background(), sessionID); err == nil {
		t.Fatal("compacting an already-compacted short history succeeded")
	}

	// Zero budget disables manual compaction.
	noBudget := startAgent(t, agent.Config{}, nil)
	noBudgetSession := noBudget.newChatSession(t, "No budget")
	noBudget.sendAndWait(t, noBudgetSession, "hello")
	noBudgetCompactor := noBudget.chat.(contracts.SessionCompactor)
	if _, err := noBudgetCompactor.CompactNow(context.Background(), noBudgetSession); !errors.Is(err, contracts.ErrCompactUnavailable) {
		t.Fatalf("CompactNow(budget=0) = %v, want ErrCompactUnavailable", err)
	}

	// An empty session has nothing to compact.
	emptySession := harness.newChatSession(t, "Empty")
	if _, err := compactor.CompactNow(context.Background(), emptySession); !errors.Is(err, contracts.ErrCompactNothing) {
		t.Fatalf("CompactNow(empty) = %v, want ErrCompactNothing", err)
	}
	if _, err := compactor.CompactNow(context.Background(), "missing-session"); err == nil {
		t.Fatal("CompactNow(unknown session) succeeded")
	}
}

// TestAgentCompactNowRejectsRunning covers the running guard: a compaction
// request during an in-flight turn fails with ErrCompactRunning and never
// replaces the history.
func TestAgentCompactNowRejectsRunning(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	model := newFakeModel(t, func([]byte) (int, string) {
		once.Do(func() { close(started) })
		<-release
		return 0, sseAnswer("slow answer")
	})
	harness := startAgentWith(t, agent.Config{ContextBudget: 2000}, model)
	sessionID := harness.newChatSession(t, "Running compact")
	compactor := harness.chat.(contracts.SessionCompactor)
	if err := harness.chat.Send(sessionID, "slow prompt"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "run to start", func() bool {
		<-started
		return true
	})
	waitFor(t, "session running", func() bool {
		chatSession, ok := harness.chat.Session(sessionID)
		return ok && chatSession.Running
	})
	if _, err := compactor.CompactNow(context.Background(), sessionID); !errors.Is(err, contracts.ErrCompactRunning) {
		t.Fatalf("CompactNow(running) = %v, want ErrCompactRunning", err)
	}
	close(release)
	harness.waitIdle(t, sessionID)
}

// compactScriptChat is a fake chat that adds the SessionCompactor half.
type compactScriptChat struct {
	*fakeChat
	mu     sync.Mutex
	result contracts.CompactResult
	err    error
	calls  int
	wake   chan struct{}
}

func (c *compactScriptChat) CompactNow(context.Context, string) (contracts.CompactResult, error) {
	c.mu.Lock()
	c.calls++
	result, err := c.result, c.err
	wake := c.wake
	c.mu.Unlock()
	if wake != nil {
		<-wake
	}
	time.Sleep(5 * time.Millisecond)
	return result, err
}

// setCompactResult scripts the next compaction result.
func (c *compactScriptChat) setCompactResult(result contracts.CompactResult, err error) {
	c.mu.Lock()
	c.result, c.err = result, err
	c.mu.Unlock()
}

// startCompactSessionUI boots the session screen over chat.
func startCompactSessionUI(t *testing.T, chat contracts.ChatService) (*harness, *ui.Tester) {
	t.Helper()
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyChat, chat)
	activate(t, h.ctx, sessionui.Module(), sessionui.Config{})
	h.push(sessionui.ScreenID)
	tester := h.tester(1280, 800)
	tester.Frame()
	return h, tester
}

// pumpCompactNotice renders frames until the composer shows text.
func pumpCompactNotice(t *testing.T, h *harness, tester *ui.Tester, text string) {
	t.Helper()
	waitFor(t, "compact notice "+text, func() bool {
		tester.Frame()
		for _, value := range tester.Texts() {
			if strings.Contains(value, text) {
				return true
			}
		}
		return false
	})
}

// TestCompactCommand covers the /compact command: disabled without a compactor,
// while running and with an empty session; success shows the message/token
// counts and every stable failure maps to localized copy.
func TestCompactCommand(t *testing.T) {
	// No SessionCompactor: the command exists but is disabled, and running it is
	// a no-op.
	plainChat := newFakeChat()
	plainChat.addSession(contracts.ChatSession{ID: "s1", Title: "Plain",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "hi"}}}, true)
	plainHarness, _ := startCompactSessionUI(t, contracts.ChatService(plainChat))
	command := sessionCommandByID(t, plainHarness, "zeokeel.session.compact")
	if command.Enabled() {
		t.Fatal("compact command is enabled without a SessionCompactor")
	}

	chat := &compactScriptChat{fakeChat: newFakeChat()}
	chat.addSession(contracts.ChatSession{ID: "s1", Title: "Compactable",
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "question"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "answer"},
			{ID: "m3", Role: contracts.ChatRoleUser, Text: "follow up"},
			{ID: "m4", Role: contracts.ChatRoleAssistant, Text: "final"},
		}}, true)
	h, tester := startCompactSessionUI(t, contracts.ChatService(chat))
	command = sessionCommandByID(t, h, "zeokeel.session.compact")
	if !command.Enabled() {
		t.Fatalf("compact command disabled for a compactable session")
	}
	chat.setCompactResult(contracts.CompactResult{Replaced: 4, Saved: 2, BeforeTokens: 100, AfterTokens: 40}, nil)
	if err := command.Run(); err != nil {
		t.Fatalf("Run /compact: %v", err)
	}
	pumpCompactNotice(t, h, tester, "已压缩：4 条 → 2 条")
	requireHasText(t, tester, "（100 → 40 tokens）")
	if chat.calls != 1 {
		t.Fatalf("compact calls = %d, want 1", chat.calls)
	}

	// While compacting the command is disabled: a second Run is a no-op and
	// never starts a duplicate compaction.
	chat.setCompactResult(contracts.CompactResult{Replaced: 4, Saved: 2}, nil)
	wake := make(chan struct{})
	chat.mu.Lock()
	chat.wake = wake
	chat.mu.Unlock()
	beforeCalls := chat.calls
	if err := command.Run(); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	waitFor(t, "compaction in flight", func() bool {
		chat.mu.Lock()
		defer chat.mu.Unlock()
		return chat.calls > beforeCalls
	})
	tester.Frame()
	if command.Enabled() {
		t.Fatal("compact command stayed enabled while a compaction was in flight")
	}
	if err := command.Run(); err != nil {
		t.Fatalf("duplicate Run: %v", err)
	}
	chat.mu.Lock()
	duringCalls := chat.calls
	chat.wake = nil
	chat.mu.Unlock()
	if duringCalls != beforeCalls+1 {
		t.Fatalf("duplicate Run started %d compactions, want exactly 1", duringCalls-beforeCalls)
	}
	close(wake)
	waitFor(t, "compaction finished", func() bool {
		tester.Frame()
		return command.Enabled()
	})

	// Stable failures map to localized copy.
	cases := []struct {
		name     string
		err      error
		expected string
	}{
		{"running", contracts.ErrCompactRunning, "会话正在运行，无法压缩"},
		{"unavailable", contracts.ErrCompactUnavailable, "压缩不可用（未配置上下文预算或压缩器）"},
		{"nothing", contracts.ErrCompactNothing, "没有可压缩的内容"},
		{"generic", errors.New("boom"), "压缩失败：boom"},
	}
	for _, test := range cases {
		chat.setCompactResult(contracts.CompactResult{}, test.err)
		waitFor(t, "command enabled before "+test.name, func() bool {
			tester.Frame()
			return command.Enabled()
		})
		if err := command.Run(); err != nil {
			t.Fatalf("%s: Run: %v", test.name, err)
		}
		pumpCompactNotice(t, h, tester, test.expected)
	}

	// Running/empty sessions disable the command.
	chat.mu.Lock()
	chat.sessions["s1"] = contracts.ChatSession{ID: "s1", Title: "Running", Running: true,
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "hi"}}}
	chat.mu.Unlock()
	tester.Frame()
	if command.Enabled() {
		t.Fatal("compact command enabled for a running session")
	}
}
