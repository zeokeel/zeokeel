package regression

// Adversarial audit: opt-in model-driven memory extraction. Backend gates
// (disabled by default, no store, fewer than four messages, unknown session,
// single-flight busy, Add failure), the tolerant fenced-JSON parser, normalized
// dedupe against existing memories and the 5-item cap. The settings page keeps
// the action disabled until the toggle is on, shows backend presence, renders
// success/failure and ignores repeated clicks while one run is in flight.

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
	"github.com/zeokeel/zeokeel/internal/modules/memory"
	settingsui "github.com/zeokeel/zeokeel/internal/ui/settings"
)

// extractScript scripts the fake model's extraction responses.
type extractScript struct {
	mu          sync.Mutex
	json        string
	block       chan struct{}
	started     chan struct{}
	startOnce   sync.Once
	extractions int
}

// respond returns the scripted extraction payload (blocking when asked).
func (s *extractScript) respond(body []byte) (int, string) {
	if !strings.Contains(string(body), "You extract durable user memories") {
		return 0, sseAnswer("turn answer")
	}
	s.mu.Lock()
	s.extractions++
	block := s.block
	json := s.json
	s.mu.Unlock()
	if s.started != nil {
		s.startOnce.Do(func() { close(s.started) })
	}
	if block != nil {
		<-block
	}
	return 0, sseAnswer(json)
}

// setResponse replaces the scripted extraction JSON.
func (s *extractScript) setResponse(json string) {
	s.mu.Lock()
	s.json = json
	s.mu.Unlock()
}

// faultMemoryAddStore fails every Add.
type faultMemoryAddStore struct{}

func (faultMemoryAddStore) List(context.Context) ([]contracts.Memory, error) {
	return nil, nil
}
func (faultMemoryAddStore) Add(context.Context, contracts.Memory) (contracts.Memory, error) {
	return contracts.Memory{}, errors.New("memory backend read-only")
}
func (faultMemoryAddStore) Remove(context.Context, string) bool { return false }
func (faultMemoryAddStore) Search(context.Context, string) ([]contracts.Memory, error) {
	return nil, nil
}

// startExtractAgent boots memory + agent over a scripted model.
func startExtractAgent(t *testing.T, script *extractScript) (*agentHarness, contracts.MemoryStore) {
	t.Helper()
	h := newHarness(t)
	activate(t, h.ctx, memory.Module(), memory.Config{})
	harness := startAgentOn(t, h, agent.Config{},
		newFakeModel(t, script.respond), "auto")
	store := requireService(t, h.ctx, contracts.KeyMemory)
	return harness, store
}

// seedFourMessages sends two turns so the session has four messages.
func seedFourMessages(t *testing.T, harness *agentHarness, sessionID string) {
	t.Helper()
	harness.sendAndWait(t, sessionID, "first question")
	harness.sendAndWait(t, sessionID, "second question")
}

// TestMemoryExtractBackend covers the extraction service gates, fenced JSON
// parsing, normalized dedupe, cap and failures.
func TestMemoryExtractBackend(t *testing.T) {
	script := &extractScript{json: "```json\n{\"memories\":[\"Likes Go\",\" likes   GO \",\"Uses /data/mygo\",\"Likes Go\"]}\n```"}
	harness, store := startExtractAgent(t, script)
	extractor := requireService(t, harness.h.ctx, contracts.KeyMemoryExtractor)
	ctx := context.Background()
	sessionID := harness.newChatSession(t, "Extract session")
	seedFourMessages(t, harness, sessionID)

	// Disabled by default (only the exact "true" enables it).
	if _, err := extractor.ExtractFromSession(ctx, sessionID, 5); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("extraction while disabled = %v, want a disabled error", err)
	}
	if err := harness.h.settings.Set(contracts.SettingMemoryExtractEnabled, "true"); err != nil {
		t.Fatalf("enable extraction: %v", err)
	}

	// Success: fenced JSON parsed, duplicates (normalized) collapsed, two added.
	memories, err := extractor.ExtractFromSession(ctx, sessionID, 5)
	if err != nil {
		t.Fatalf("ExtractFromSession: %v", err)
	}
	if len(memories) != 2 {
		t.Fatalf("added memories = %d, want 2 after dedupe", len(memories))
	}
	stored, err := store.List(ctx)
	if err != nil || len(stored) != 2 {
		t.Fatalf("stored memories = %d (err %v), want 2", len(stored), err)
	}
	for _, memory := range stored {
		if memory.Scope != "global" {
			t.Fatalf("extracted memory scope = %q, want global", memory.Scope)
		}
	}
	// A second run adds nothing: the same candidates already exist.
	script.setResponse("{\"memories\":[\"Likes Go\",\"Uses /data/mygo\"]}")
	again, err := extractor.ExtractFromSession(ctx, sessionID, 5)
	if err != nil || len(again) != 0 {
		t.Fatalf("second extraction added %d memories (err %v), want 0", len(again), err)
	}

	// Bad JSON is a readable parse error.
	script.setResponse("not json at all")
	if _, err := extractor.ExtractFromSession(ctx, sessionID, 5); err == nil || !strings.Contains(err.Error(), "parse extracted memories") {
		t.Fatalf("bad JSON extraction = %v, want a parse error", err)
	}

	// The cap keeps at most five; unknown/too-short sessions are rejected.
	script.setResponse("{\"memories\":[\"m1\",\"m2\",\"m3\",\"m4\",\"m5\",\"m6\",\"m7\"]}")
	capped, err := extractor.ExtractFromSession(ctx, sessionID, 50)
	if err != nil || len(capped) != 5 {
		t.Fatalf("capped extraction = %d (err %v), want 5", len(capped), err)
	}
	if _, err := extractor.ExtractFromSession(ctx, "missing-session", 5); err == nil {
		t.Fatal("extraction from an unknown session succeeded")
	}
	shortID := harness.newChatSession(t, "Short")
	if _, err := extractor.ExtractFromSession(ctx, shortID, 5); err == nil || !strings.Contains(err.Error(), "at least four") {
		t.Fatalf("short-session extraction = %v, want the four-message gate", err)
	}

	// Add failure surfaces as an error instead of a silent partial success.
	h2 := newHarness(t)
	provide(t, h2.ctx, contracts.KeyMemory, contracts.MemoryStore(faultMemoryAddStore{}))
	harness2 := startAgentOn(t, h2, agent.Config{},
		newFakeModel(t, script.respond), "auto")
	extractor2 := requireService(t, harness2.h.ctx, contracts.KeyMemoryExtractor)
	if err := harness2.h.settings.Set(contracts.SettingMemoryExtractEnabled, "true"); err != nil {
		t.Fatalf("enable extraction (fault store): %v", err)
	}
	faultSession := harness2.newChatSession(t, "Fault add")
	seedFourMessages(t, harness2, faultSession)
	script.setResponse("{\"memories\":[\"one candidate\"]}")
	if _, err := extractor2.ExtractFromSession(ctx, faultSession, 5); err == nil || !strings.Contains(err.Error(), "add memory") {
		t.Fatalf("Add-failure extraction = %v, want an add-memory error", err)
	}

	// Single-flight: a concurrent extraction reports busy while the first runs.
	script.setResponse("{\"memories\":[\"busy candidate\"]}")
	block := make(chan struct{})
	started := make(chan struct{})
	script.mu.Lock()
	script.block, script.started = block, started
	script.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, extractErr := extractor.ExtractFromSession(ctx, sessionID, 5)
		done <- extractErr
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the first extraction never reached the provider")
	}
	if _, err := extractor.ExtractFromSession(ctx, sessionID, 5); !errors.Is(err, agent.ErrMemoryExtractBusy) {
		t.Fatalf("concurrent extraction = %v, want ErrMemoryExtractBusy", err)
	}
	script.mu.Lock()
	script.block, script.started = nil, nil
	script.mu.Unlock()
	close(block)
	if err := <-done; err != nil {
		t.Fatalf("first extraction: %v", err)
	}
}

// scriptedExtractor is a fake contracts.MemoryExtractionService for the page.
type scriptedExtractor struct {
	mu       sync.Mutex
	memories []contracts.Memory
	err      error
	calls    int
	session  string
	max      int
	block    chan struct{}
	started  chan struct{}
	once     sync.Once
}

func (e *scriptedExtractor) ExtractFromSession(_ context.Context, sessionID string, max int) ([]contracts.Memory, error) {
	e.mu.Lock()
	e.calls++
	e.session = sessionID
	e.max = max
	block, started := e.block, e.started
	e.mu.Unlock()
	if started != nil {
		e.once.Do(func() { close(started) })
	}
	if block != nil {
		<-block
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]contracts.Memory(nil), e.memories...), e.err
}

// setScript replaces the extractor result.
func (e *scriptedExtractor) setScript(memories []contracts.Memory, err error) {
	e.mu.Lock()
	e.memories, e.err = memories, err
	e.mu.Unlock()
}

// startExtractionPage opens settings > Memory with an active chat session.
func startExtractionPage(t *testing.T, extractor contracts.MemoryExtractionService) (*harness, *ui.Tester) {
	t.Helper()
	h := newBareHarness(t)
	seedMemoryFile(t, h.dataDir, nil)
	activate(t, h.ctx, memory.Module(), memory.Config{})
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-extract", Title: "Extract",
		Messages: []contracts.ChatMessage{{ID: "m1", Role: contracts.ChatRoleUser, Text: "hello"}}}, true)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(chat))
	if extractor != nil {
		provide(t, h.ctx, contracts.KeyMemoryExtractor, extractor)
	}
	activate(t, h.ctx, settingsui.Module(), settingsui.Config{})
	h.ui.Navigator().ToggleSettings()
	tester := h.tester(1280, 1000)
	tester.Frame()
	if err := tester.Click("记忆"); err != nil {
		t.Fatalf("open memory page: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	return h, tester
}

// pumpExtractionFrame renders until the page shows text.
func pumpExtractionFrame(t *testing.T, tester *ui.Tester, text string) {
	t.Helper()
	waitFor(t, "memory page text "+text, func() bool {
		tester.Frame()
		for _, value := range tester.Texts() {
			if strings.Contains(value, text) {
				return true
			}
		}
		return false
	})
}

// TestMemoryExtractSettingsPage covers the opt-in panel: backend absence,
// toggle gating, success badge, failure line and duplicate-click guard.
func TestMemoryExtractSettingsPage(t *testing.T) {
	extractor := &scriptedExtractor{memories: []contracts.Memory{{ID: "m1", Text: "Likes Go"}, {ID: "m2", Text: "Uses Go"}}}
	h, tester := startExtractionPage(t, extractor)
	requireHasText(t, tester, "模型提取")
	requireHasText(t, tester, "启用模型提取（从最近对话提取记忆，会调用模型）")
	requireHasText(t, tester, "先启用模型提取后再提取")
	// Disabled: clicking the button must not call the extractor.
	if err := tester.Click("从当前会话提取"); err == nil {
		tester.Frame()
	}
	if extractor.calls != 0 {
		t.Fatalf("disabled extraction called the backend %d times", extractor.calls)
	}

	// Enable: the setting is saved and the action runs.
	if err := tester.Click("启用模型提取（从最近对话提取记忆，会调用模型）"); err != nil {
		t.Fatalf("toggle extraction: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := h.settings.Get(contracts.SettingMemoryExtractEnabled, ""); got != "true" {
		t.Fatalf("memory.extract = %q, want true", got)
	}
	if err := tester.Click("从当前会话提取"); err != nil {
		t.Fatalf("extract: %v; texts = %v", err, tester.Texts())
	}
	pumpExtractionFrame(t, tester, "已添加 2 条记忆")
	extractor.mu.Lock()
	session, max, calls := extractor.session, extractor.max, extractor.calls
	extractor.mu.Unlock()
	if calls != 1 || session != "sess-extract" || max != 5 {
		t.Fatalf("extractor calls/session/max = %d/%q/%d", calls, session, max)
	}

	// Failure renders the error line.
	extractor.setScript(nil, errors.New("provider boom"))
	if err := tester.Click("从当前会话提取"); err != nil {
		t.Fatalf("extract failure: %v; texts = %v", err, tester.Texts())
	}
	pumpExtractionFrame(t, tester, "提取失败：provider boom")

	// Duplicate-click guard: while one run is blocked the label shows
	// 「提取中…」 and another click does not start a second extraction.
	block := make(chan struct{})
	started := make(chan struct{})
	extractor.mu.Lock()
	extractor.block, extractor.started = block, started
	extractor.err = nil
	extractor.mu.Unlock()
	extractor.setScript([]contracts.Memory{{ID: "m3", Text: "Third"}}, nil)
	extractor.mu.Lock()
	before := extractor.calls
	extractor.mu.Unlock()
	if err := tester.Click("从当前会话提取"); err != nil {
		t.Fatalf("blocked extract: %v; texts = %v", err, tester.Texts())
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the extraction never started")
	}
	pumpExtractionFrame(t, tester, "提取中…")
	if err := tester.Click("提取中…"); err == nil {
		tester.Frame()
	}
	extractor.mu.Lock()
	during := extractor.calls
	extractor.block, extractor.started = nil, nil
	extractor.mu.Unlock()
	if during != before+1 {
		t.Fatalf("duplicate click started %d extra extractions", during-before-1)
	}
	close(block)
	pumpExtractionFrame(t, tester, "已添加 1 条记忆")

	// Without the optional extractor backend the action is hidden and a note is
	// shown instead.
	_, plainTester := startExtractionPage(t, nil)
	requireHasTexts(t, plainTester, "模型提取", "未启用记忆提取后端")
	requireNoText(t, plainTester, "从当前会话提取")
}
