package regression

// Adversarial audit: assistant-turn usage accounting. The agent records provider
// token usage on the in-memory assistant message and mirrors one durable "usage"
// event; a provider that reports no tokens leaves both untouched; usage sums
// across model iterations (cached included); a restart loses the per-message
// Usage by design (rig history has no usage field) while the durable event stays;
// concurrent sessions keep their own accounting.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/agent"
	"github.com/zeokeel/zeokeel/internal/modules/approvals"
)

// usageEvent is one decoded durable "usage" event.
type usageEvent struct {
	PromptTokens     int   `json:"promptTokens"`
	CompletionTokens int   `json:"completionTokens"`
	TotalTokens      int   `json:"totalTokens"`
	CachedTokens     int   `json:"cachedTokens"`
	Iterations       int   `json:"iterations"`
	DurationMS       int64 `json:"durationMs"`
}

// usageEventsOf returns the decoded usage events of one session.
func usageEventsOf(t *testing.T, harness *agentHarness, sessionID string) []usageEvent {
	t.Helper()
	store := requireService(t, harness.h.ctx, contracts.KeyStore)
	events, err := store.Events(context.Background(), sessionID, 0, 0)
	if err != nil {
		t.Fatalf("Events(%q): %v", sessionID, err)
	}
	out := make([]usageEvent, 0)
	for _, event := range events {
		if event.Type != "usage" {
			continue
		}
		var decoded usageEvent
		if err := json.Unmarshal(event.Payload, &decoded); err != nil {
			t.Fatalf("usage payload is not JSON: %v", err)
		}
		out = append(out, decoded)
	}
	return out
}

// lastAssistant returns the newest assistant message of a session.
func lastAssistant(t *testing.T, session contracts.ChatSession) contracts.ChatMessage {
	t.Helper()
	for index := len(session.Messages) - 1; index >= 0; index-- {
		if session.Messages[index].Role == contracts.ChatRoleAssistant {
			return session.Messages[index]
		}
	}
	t.Fatalf("no assistant message in %+v", session.Messages)
	return contracts.ChatMessage{}
}

// TestUsageAbsentWithoutProviderUsage covers a provider that reports no tokens:
// the assistant Usage stays nil and no durable usage event is written.
func TestUsageAbsentWithoutProviderUsage(t *testing.T) {
	harness := startAgent(t, agent.Config{}, func([]byte) (int, string) {
		return 0, sseAnswer("no usage reported")
	})
	sessionID := harness.newChatSession(t, "no-usage")
	harness.sendAndWait(t, sessionID, "hello")

	session, ok := harness.chat.Session(sessionID)
	if !ok {
		t.Fatalf("Session(%q) missing", sessionID)
	}
	assistant := lastAssistant(t, session)
	if assistant.Usage != nil {
		t.Fatalf("assistant Usage = %+v without provider usage, want nil", assistant.Usage)
	}
	if events := usageEventsOf(t, harness, sessionID); len(events) != 0 {
		t.Fatalf("usage events = %+v, want none", events)
	}
	// The user message never carries usage.
	for _, message := range session.Messages {
		if message.Role == contracts.ChatRoleUser && message.Usage != nil {
			t.Fatalf("user message carries usage: %+v", message.Usage)
		}
	}
}

// TestUsageRecordedOnMessageAndEvent covers a provider that reports tokens: the
// assistant message carries the fields and exactly one durable usage event
// mirrors them.
func TestUsageRecordedOnMessageAndEvent(t *testing.T) {
	harness := startAgent(t, agent.Config{}, func([]byte) (int, string) {
		return 0, sseAnswerWithUsage("accounted", usageWire{Prompt: 11, Completion: 7, Total: 18, Cached: 3})
	})
	sessionID := harness.newChatSession(t, "usage")
	harness.sendAndWait(t, sessionID, "hello")

	session, ok := harness.chat.Session(sessionID)
	if !ok {
		t.Fatalf("Session(%q) missing", sessionID)
	}
	assistant := lastAssistant(t, session)
	if assistant.Usage == nil {
		t.Fatal("assistant Usage = nil, want the provider tokens")
	}
	usage := assistant.Usage
	if usage.PromptTokens != 11 || usage.CompletionTokens != 7 ||
		usage.TotalTokens != 18 || usage.CachedTokens != 3 {
		t.Fatalf("assistant Usage = %+v, want prompt 11/completion 7/total 18/cached 3", usage)
	}
	if usage.DurationMS < 0 {
		t.Fatalf("DurationMS = %d, want >= 0", usage.DurationMS)
	}
	if usage.Iterations < 1 {
		t.Fatalf("Iterations = %d, want >= 1", usage.Iterations)
	}
	events := usageEventsOf(t, harness, sessionID)
	if len(events) != 1 {
		t.Fatalf("usage events = %+v, want exactly one", events)
	}
	event := events[0]
	if event.PromptTokens != 11 || event.CompletionTokens != 7 ||
		event.TotalTokens != 18 || event.CachedTokens != 3 || event.Iterations != usage.Iterations {
		t.Fatalf("usage event = %+v, want the same accounting as the message", event)
	}
}

// TestUsageAccumulatesAcrossIterations covers a tool-calling run with two model
// iterations: the recorded usage is the sum, cached tokens included, and the
// audit event reports the iteration count.
func TestUsageAccumulatesAcrossIterations(t *testing.T) {
	iterations := new(counter)
	model := newFakeModel(t, func(body []byte) (int, string) {
		if lastRequestRole(body) == "tool" {
			return 0, sseAnswerWithUsage("final answer", usageWire{Prompt: 20, Completion: 5, Total: 25, Cached: 2})
		}
		return 0, sseToolCallWithUsage("audit_count_tool", "{}",
			usageWire{Prompt: 10, Completion: 2, Total: 12, Cached: 1})
	})
	tool := fakeRigTool{name: "audit_count_tool", calls: iterations}
	harness := startAgentWith(t, agent.Config{}, model, tool)
	sessionID := harness.newChatSession(t, "accumulate")
	harness.sendAndWait(t, sessionID, "use the counting tool")

	if got := iterations.load(); got != 1 {
		t.Fatalf("tool executed %d times, want 1", got)
	}
	session, _ := harness.chat.Session(sessionID)
	assistant := lastAssistant(t, session)
	if assistant.Usage == nil {
		t.Fatal("assistant Usage = nil after a tool-calling run")
	}
	usage := assistant.Usage
	if usage.PromptTokens != 30 || usage.CompletionTokens != 7 ||
		usage.TotalTokens != 37 || usage.CachedTokens != 3 {
		t.Fatalf("accumulated Usage = %+v, want prompt 30/completion 7/total 37/cached 3", usage)
	}
	if usage.Iterations != 2 {
		t.Fatalf("Iterations = %d, want 2 model iterations", usage.Iterations)
	}
	events := usageEventsOf(t, harness, sessionID)
	if len(events) != 1 || events[0].TotalTokens != 37 || events[0].Iterations != 2 {
		t.Fatalf("usage events = %+v, want one cumulative event (total 37, 2 iterations)", events)
	}
}

// TestUsageConcurrentSessions covers two sessions running concurrently with the
// same scripted provider: each keeps its own accounting and event (-race).
func TestUsageConcurrentSessions(t *testing.T) {
	harness := startAgent(t, agent.Config{}, func(body []byte) (int, string) {
		return 0, sseAnswerWithUsage("ack", usageWire{Prompt: 5, Completion: 3, Total: 8, Cached: 1})
	})
	first := harness.newChatSession(t, "usage-a")
	second := harness.newChatSession(t, "usage-b")

	var wg sync.WaitGroup
	for _, sessionID := range []string{first, second} {
		sessionID := sessionID
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := harness.chat.Send(sessionID, "hello"); err != nil {
				t.Errorf("Send(%q): %v", sessionID, err)
			}
		}()
	}
	wg.Wait()
	for _, sessionID := range []string{first, second} {
		session := harness.waitIdle(t, sessionID)
		assistant := lastAssistant(t, session)
		if assistant.Usage == nil || assistant.Usage.TotalTokens != 8 {
			t.Fatalf("%s assistant Usage = %+v, want total 8", sessionID, assistant.Usage)
		}
		if events := usageEventsOf(t, harness, sessionID); len(events) != 1 || events[0].TotalTokens != 8 {
			t.Fatalf("%s usage events = %+v, want one with total 8", sessionID, events)
		}
	}
}

// TestUsageRestoreLosesUsageByDesign covers a restart: the read model is
// restored from rig history, which has no usage field, so the assistant Usage is
// nil; the durable usage event remains for audit.
func TestUsageRestoreLosesUsageByDesign(t *testing.T) {
	first := newHarness(t)
	firstModel := newFakeModel(t, func([]byte) (int, string) {
		return 0, sseAnswerWithUsage("persisted answer", usageWire{Prompt: 9, Completion: 4, Total: 13, Cached: 2})
	})
	firstAgent := startAgentOn(t, first, agent.Config{}, firstModel, approvals.ModeAuto)
	sessionID := firstAgent.newChatSession(t, "restart")
	firstAgent.sendAndWait(t, sessionID, "hello")
	session, _ := firstAgent.chat.Session(sessionID)
	if usage := lastAssistant(t, session).Usage; usage == nil || usage.TotalTokens != 13 {
		t.Fatalf("pre-restart Usage = %+v, want total 13", usage)
	}
	if events := usageEventsOf(t, firstAgent, sessionID); len(events) != 1 {
		t.Fatalf("pre-restart usage events = %d, want 1", len(events))
	}
	if err := first.ctx.DisposeAll(); err != nil {
		t.Fatalf("dispose first root: %v", err)
	}

	// Restart over the same data directory: the session is restored, Usage is
	// lost by design (rig history has no usage), the audit event survives.
	second := newHarnessWithDataDir(t, first.dataDir)
	secondModel := newFakeModel(t, func([]byte) (int, string) { return 0, sseAnswer("unused") })
	restoredAgent := startAgentOn(t, second, agent.Config{}, secondModel, approvals.ModeAuto)
	restored, ok := restoredAgent.chat.Session(sessionID)
	if !ok {
		t.Fatalf("Session(%q) was not restored", sessionID)
	}
	if len(restored.Messages) < 2 {
		t.Fatalf("restored session has %d messages, want the persisted turn", len(restored.Messages))
	}
	for _, message := range restored.Messages {
		if message.Role == contracts.ChatRoleAssistant && message.Usage != nil {
			t.Fatalf("restored assistant Usage = %+v, want nil (usage is in-memory by design)", message.Usage)
		}
	}
	if events := usageEventsOf(t, restoredAgent, sessionID); len(events) != 1 || events[0].TotalTokens != 13 {
		t.Fatalf("post-restart usage events = %+v, want the durable audit event", events)
	}
}

// TestUsageFailedRunHasNoAccounting covers a provider failure: the run errors and
// no zero-value usage is recorded.
func TestUsageFailedRunHasNoAccounting(t *testing.T) {
	harness := startAgent(t, agent.Config{}, func([]byte) (int, string) {
		return 400, ""
	})
	sessionID := harness.newChatSession(t, "failed")
	if err := harness.chat.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := harness.waitIdle(t, sessionID)
	if session.Error == "" {
		t.Fatalf("session Error is empty after a provider failure")
	}
	for _, message := range session.Messages {
		if message.Usage != nil {
			t.Fatalf("failed run recorded usage on %q: %+v", message.Role, message.Usage)
		}
	}
	if events := usageEventsOf(t, harness, sessionID); len(events) != 0 {
		t.Fatalf("failed run usage events = %+v, want none", events)
	}
}
