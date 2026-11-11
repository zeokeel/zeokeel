package agent

// Regression tests for the agent-loop lifecycle fixes (review FR-08…FR-15):
// manual-compaction races, deleted-session resurrection, tool cancellation
// classification, Regenerate reference expansion and its durable-history base,
// finish persistence ordering, finish_reason=length truncation, the iteration
// budget error kind, and the disabled-tool filter.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/session/compact"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/sessionstore"
)

// --- shared fakes ----------------------------------------------------------

// fixBlockingApprovals keeps an approval request pending until the run context
// ends, so a cancel lands exactly while the gate is waiting.
type fixBlockingApprovals struct {
	started chan struct{}
	once    sync.Once
}

func (a *fixBlockingApprovals) Mode() string { return "ask" }
func (a *fixBlockingApprovals) Request(ctx context.Context, _ contracts.ApprovalRequest) (contracts.ApprovalDecision, error) {
	a.once.Do(func() { close(a.started) })
	<-ctx.Done()
	return "", ctx.Err()
}
func (a *fixBlockingApprovals) Pending() []contracts.ApprovalRequest { return nil }
func (a *fixBlockingApprovals) Resolve(string, contracts.ApprovalDecision) error {
	return nil
}
func (a *fixBlockingApprovals) Subscribe(func()) (func(), error) { return func() {}, nil }

// fixBlockingTool parks in Invoke until released (a slow external tool).
type fixBlockingTool struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (t *fixBlockingTool) Name() string           { return "block_tool" }
func (t *fixBlockingTool) Description() string    { return "Block until released." }
func (t *fixBlockingTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *fixBlockingTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	t.once.Do(func() { close(t.started) })
	<-t.release
	return rig.ToolResult{Content: "released"}, nil
}

// fixFailingTool always fails, so its tool result is a genuine failure.
type fixFailingTool struct{}

func (t *fixFailingTool) Name() string           { return "fail_tool" }
func (t *fixFailingTool) Description() string    { return "Always fail." }
func (t *fixFailingTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *fixFailingTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{}, errors.New("the tool broke")
}

// fixToolCallProvider asks for toolName on its first call and answers on the
// following ones.
type fixToolCallProvider struct {
	toolName string
	mu       sync.Mutex
	calls    int
}

func (p *fixToolCallProvider) Name() string { return "fix-tool-call" }
func (p *fixToolCallProvider) Stream(context.Context, rig.Request) (rig.Stream, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call == 1 {
		return &scriptedStream{chunks: []rig.Chunk{
			{Delta: "working "},
			{ToolCalls: []rig.ToolCallDelta{{Index: 0, ID: "call-1", Name: p.toolName, Arguments: `{}`}}},
			{FinishReason: "tool_calls"},
		}}, nil
	}
	return &scriptedStream{chunks: []rig.Chunk{{Delta: "done"}, {FinishReason: "stop"}}}, nil
}

// fixBlockingCompactor parks inside Compact until released.
type fixBlockingCompactor struct {
	entered chan struct{}
	release chan struct{}
	result  []rig.Message
}

func (c *fixBlockingCompactor) Compact(ctx context.Context, _ string, _ []rig.Message, _ int) ([]rig.Message, error) {
	close(c.entered)
	select {
	case <-c.release:
		return c.result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// fixStallCheckpoints parks inside Clear: finish() has already captured and
// settled the read model but has not finished persisting the interrupted turn.
type fixStallCheckpoints struct {
	inner   rig.CheckpointStore
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *fixStallCheckpoints) Load(ctx context.Context, sessionID string) (*rig.Checkpoint, error) {
	return c.inner.Load(ctx, sessionID)
}
func (c *fixStallCheckpoints) Save(ctx context.Context, sessionID string, checkpoint *rig.Checkpoint) error {
	return c.inner.Save(ctx, sessionID, checkpoint)
}
func (c *fixStallCheckpoints) Clear(ctx context.Context, sessionID string) error {
	c.once.Do(func() { close(c.started) })
	<-c.release
	return c.inner.Clear(ctx, sessionID)
}

type fixStallStore struct {
	*fakeStore
	cp rig.CheckpointStore
}

func (s *fixStallStore) CheckpointStore() rig.CheckpointStore { return s.cp }

// fixPartialThenErrorStream streams one delta and then fails, leaving a
// streamed partial answer that the interrupted-turn persistence must keep.
type fixPartialThenErrorStream struct{ sent bool }

func (s *fixPartialThenErrorStream) Recv() (rig.Chunk, error) {
	if !s.sent {
		s.sent = true
		return rig.Chunk{Delta: "first partial answer"}, nil
	}
	return rig.Chunk{}, errors.New("provider went away")
}

type fixFailThenAnswerProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *fixFailThenAnswerProvider) Name() string { return "fix-fail-then-answer" }
func (p *fixFailThenAnswerProvider) Stream(context.Context, rig.Request) (rig.Stream, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	if call == 1 {
		return &fixPartialThenErrorStream{}, nil
	}
	return &scriptedStream{chunks: []rig.Chunk{{Delta: "second answer"}, {FinishReason: "stop"}}}, nil
}

// fixLengthProvider reports finish_reason "length": the provider cut the answer
// at the output-token limit.
type fixLengthProvider struct{}

func (p *fixLengthProvider) Name() string { return "fix-length" }
func (p *fixLengthProvider) Stream(context.Context, rig.Request) (rig.Stream, error) {
	return &scriptedStream{chunks: []rig.Chunk{
		{Delta: "The answer is cut mid-sen"},
		{FinishReason: "length"},
	}}, nil
}

// fixAlwaysToolCallsProvider never finishes, so the iteration budget fires.
type fixAlwaysToolCallsProvider struct{}

func (p *fixAlwaysToolCallsProvider) Name() string { return "fix-always-tools" }
func (p *fixAlwaysToolCallsProvider) Stream(context.Context, rig.Request) (rig.Stream, error) {
	return &scriptedStream{chunks: []rig.Chunk{
		{ToolCalls: []rig.ToolCallDelta{{Index: 0, ID: "call-1", Name: "echo", Arguments: `{"text":"loop"}`}}},
		{FinishReason: "tool_calls"},
	}}, nil
}

// fixNumberedProvider answers "answer-N" for its Nth call.
type fixNumberedProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *fixNumberedProvider) Name() string { return "fix-numbered" }
func (p *fixNumberedProvider) Stream(context.Context, rig.Request) (rig.Stream, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	return &scriptedStream{chunks: []rig.Chunk{
		{Delta: "answer-" + string(rune('0'+call))},
		{FinishReason: "stop"},
	}}, nil
}

// fixNamedTool is a registry tool with an explicit model-visible name.
type fixNamedTool struct {
	name  string
	calls *int32
}

func (t *fixNamedTool) Name() string           { return t.name }
func (t *fixNamedTool) Description() string    { return "Test tool " + t.name }
func (t *fixNamedTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *fixNamedTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{Content: "ok"}, nil
}

// --- helpers ---------------------------------------------------------------

// fixService builds a service over a fresh in-memory store.
func fixService(t *testing.T, cfg Config, registry *rigtools.Registry, settings *fakeSettings, approvals contracts.Approvals, store contracts.Store) *service {
	t.Helper()
	if settings == nil {
		settings = newFakeSettings(nil)
	}
	return newService(cfg, registry, contracts.SettingsService(settings), &fakeLogger{}, store, approvals)
}

// fixRestart simulates an app restart over the same durable store.
func fixRestart(t *testing.T, store contracts.Store) *service {
	t.Helper()
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(newFakeSettings(nil)),
		&fakeLogger{}, store, fakeApprovals{})
	svc.restore(context.Background())
	return svc
}

// fixMessageTexts joins every rig message text of one history.
func fixMessageTexts(messages []rig.Message) string {
	var builder strings.Builder
	for _, message := range messages {
		builder.WriteString(string(message.Role))
		builder.WriteString(":")
		builder.WriteString(message.Text())
		builder.WriteString("\n")
	}
	return builder.String()
}

// fixRequestTexts joins every message text of one captured provider request.
func fixRequestTexts(request rig.Request) string {
	var builder strings.Builder
	for _, message := range request.Messages {
		builder.WriteString(string(message.Role))
		builder.WriteString(":")
		builder.WriteString(message.Text())
		builder.WriteString("\n")
	}
	return builder.String()
}

// fixToolNames lists the model-visible tool names of one captured request.
func fixToolNames(request rig.Request) []string {
	names := make([]string, 0, len(request.Tools))
	for _, tool := range request.Tools {
		names = append(names, tool.Name)
	}
	return names
}

// fixContainsName reports whether names contains want.
func fixContainsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// --- FR-15: disabled tools are filtered out of the model tool surface -------

func TestDisabledToolsFilteredFromRun(t *testing.T) {
	var unused int32
	registry := rigtools.NewRegistry(
		&fixNamedTool{name: "echo", calls: &unused},
		&fixNamedTool{name: "goal", calls: &unused},
		&fixNamedTool{name: "subagent", calls: &unused},
		&fixNamedTool{name: "session_search", calls: &unused},
	)
	settings := newFakeSettings(map[string]string{
		contracts.SettingToolsDisabled:    `["goal","subagent"]`,
		contracts.SettingMCPDisabledTools: `["session_search"]`,
	})
	provider := &captureProvider{}
	svc := fixService(t, Config{}, registry, settings, fakeApprovals{}, newFakeStore())
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)

	names := fixToolNames(provider.lastRequest())
	t.Logf("model tool surface = %v", names)
	for _, disabled := range []string{"goal", "subagent", "session_search"} {
		if fixContainsName(names, disabled) {
			t.Fatalf("BUG: disabled tool %q is still on the model tool surface: %v", disabled, names)
		}
	}
	if !fixContainsName(names, "echo") {
		t.Fatalf("enabled tool \"echo\" was filtered out: %v", names)
	}
}

func TestDisabledToolsBadJSONIsIgnored(t *testing.T) {
	var unused int32
	registry := rigtools.NewRegistry(&fixNamedTool{name: "goal", calls: &unused})
	settings := newFakeSettings(map[string]string{contracts.SettingToolsDisabled: "{not json"})
	provider := &captureProvider{}
	svc := fixService(t, Config{}, registry, settings, fakeApprovals{}, newFakeStore())
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)
	if names := fixToolNames(provider.lastRequest()); !fixContainsName(names, "goal") {
		t.Fatalf("a malformed tools.disabled row must not filter tools: %v", names)
	}
}

func TestDisabledToolsFilteredFromChildSurfaces(t *testing.T) {
	var unused int32
	registry := rigtools.NewRegistry(
		&fixNamedTool{name: "goal", calls: &unused},
		&fixNamedTool{name: "subagent", calls: &unused},
	)
	settings := newFakeSettings(map[string]string{
		contracts.SettingToolsDisabled:    `["goal"]`,
		contracts.SettingMCPDisabledTools: `["subagent"]`,
	})
	svc := fixService(t, Config{}, registry, settings, fakeApprovals{}, newFakeStore())
	// Delegated child runs (subagent/team members) hand enabledRegistryTools to
	// the model, so a disabled tool must be absent there too.
	names := make([]string, 0, 2)
	for _, tool := range svc.enabledRegistryTools() {
		names = append(names, tool.Name())
	}
	if len(names) != 0 {
		t.Fatalf("enabledRegistryTools = %v, want the disabled tools filtered out", names)
	}
}

// --- FR-08: /compact vs an in-flight turn ----------------------------------

func TestCompactRejectsSendAndRegenerate(t *testing.T) {
	compactor := &fixBlockingCompactor{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		result:  compactTestMessages(2),
	}
	store := newFakeStore()
	provider := &captureProvider{}
	svc := fixService(t, Config{ContextBudget: 100}, rigtools.NewRegistry(), nil, fakeApprovals{}, store)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	svc.compactorFactory = func(contracts.ChatConfig) (rig.Compactor, error) { return compactor, nil }

	sessionID, err := svc.NewSession("compact race")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := store.ReplaceHistory(context.Background(), sessionID, compactTestMessages(4)); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, compactErr := svc.CompactNow(context.Background(), sessionID)
		done <- compactErr
	}()
	select {
	case <-compactor.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("compaction never started")
	}

	if err := svc.Send(sessionID, "during compaction"); !errors.Is(err, contracts.ErrChatCompacting) {
		t.Fatalf("Send during compaction = %v, want contracts.ErrChatCompacting", err)
	}
	if err := svc.Regenerate(sessionID); !errors.Is(err, contracts.ErrChatCompacting) {
		t.Fatalf("Regenerate during compaction = %v, want contracts.ErrChatCompacting", err)
	}

	close(compactor.release)
	if err := <-done; err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	if err := svc.Send(sessionID, "after compaction"); err != nil {
		t.Fatalf("Send after compaction: %v", err)
	}
	waitIdle(t, svc, sessionID)
}

func TestCompactPreservesMessagesAppendedDuringCompaction(t *testing.T) {
	compactor := &fixBlockingCompactor{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		result:  compactTestMessages(2),
	}
	store := newFakeStore()
	svc := fixService(t, Config{ContextBudget: 100}, rigtools.NewRegistry(), nil, fakeApprovals{}, store)
	svc.compactorFactory = func(contracts.ChatConfig) (rig.Compactor, error) { return compactor, nil }
	sessionID, err := svc.NewSession("compact growth")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := store.ReplaceHistory(context.Background(), sessionID, compactTestMessages(4)); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, compactErr := svc.CompactNow(context.Background(), sessionID)
		done <- compactErr
	}()
	<-compactor.entered

	// A late writer that bypasses the chat service (e.g. a run that was already
	// in flight) appends a turn while the compaction model call is running.
	late := rig.NewMessage(rig.RoleUser, "late writer message")
	if err := store.History().Append(context.Background(), sessionID, late); err != nil {
		t.Fatalf("late append: %v", err)
	}
	close(compactor.release)
	if err := <-done; err != nil {
		t.Fatalf("CompactNow: %v", err)
	}

	loaded, err := store.History().Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	texts := fixMessageTexts(loaded)
	if !strings.Contains(texts, "late writer message") {
		t.Fatalf("BUG: compaction erased the message appended while it was running: %q", texts)
	}
	if len(loaded) != 3 {
		t.Fatalf("history = %d messages (%q), want compacted 2 + late 1", len(loaded), texts)
	}
}

// --- FR-09: deleting a running session must not resurrect it ---------------

func TestDeletedRunningSessionIsNotResurrected(t *testing.T) {
	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()
	logger := &fakeLogger{}
	provide(t, root, contracts.KeyDataDir, t.TempDir())
	provide(t, root, contracts.KeyLogger, contracts.Logger(logger))
	if err := sessionstore.Module().Factory(root, sessionstore.Config{}); err != nil {
		t.Fatalf("sessionstore: %v", err)
	}
	store, err := root.Require(contracts.KeyStore)
	if err != nil {
		t.Fatalf("require store: %v", err)
	}

	tool := &fixBlockingTool{started: make(chan struct{}), release: make(chan struct{})}
	provider := &fixToolCallProvider{toolName: "block_tool"}
	svc := newService(Config{}, rigtools.NewRegistry(tool), contracts.SettingsService(newFakeSettings(nil)),
		logger, store, fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	sessionID, err := svc.NewSession("delete while running")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "run the blocking tool"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case <-tool.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the tool never started")
	}
	if err := svc.DeleteSession(sessionID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	close(tool.release)
	// Let the cancelled run finish its late write attempts.
	time.Sleep(200 * time.Millisecond)

	metas, err := store.Sessions(context.Background())
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	for _, meta := range metas {
		if meta.ID == sessionID {
			t.Fatalf("BUG: the deleted session was resurrected in the durable store: %+v", meta)
		}
	}
	restored := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(newFakeSettings(nil)),
		logger, store, fakeApprovals{})
	restored.restore(context.Background())
	if _, ok := restored.Session(sessionID); ok {
		t.Fatalf("BUG: the deleted session reappears after restart: %+v", restored.Sessions())
	}
}

// --- FR-10: a user stop before the tool executes is a cancellation, not a
// failure ----------------------------------------------------------------

func TestCancelBeforeToolApprovalMarksToolCancelled(t *testing.T) {
	approvals := &fixBlockingApprovals{started: make(chan struct{})}
	store := newFakeStore()
	provider := &scriptedProvider{} // asks for the echo tool, then answers
	svc := fixService(t, Config{}, rigtools.NewRegistry(&echoTool{}), nil, approvals, store)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "run the echo tool"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case <-approvals.started:
	case <-time.After(3 * time.Second):
		t.Fatal("the approval gate was never reached")
	}
	if err := svc.Cancel(sessionID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	last := session.Messages[len(session.Messages)-1]
	if len(last.Tools) != 1 {
		t.Fatalf("tool rows = %d, want 1 in %+v", len(last.Tools), last)
	}
	tool := last.Tools[0]
	if tool.Status != "cancelled" {
		t.Fatalf("BUG: user stop shows tool status %q (summary %q), want the neutral cancelled status",
			tool.Status, tool.Summary)
	}
	if tool.Summary != "" {
		t.Fatalf("BUG: the raw cancellation text leaked into the transcript: %q", tool.Summary)
	}
	if last.ErrorKind != contracts.MessageErrorKindCancelled {
		t.Fatalf("message error kind = %q, want cancelled", last.ErrorKind)
	}

	// The durable tool_result event must record the same neutral status.
	events, err := store.Events(context.Background(), sessionID, 0, 0)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	recorded := ""
	for _, event := range events {
		if event.Type == "tool_result" {
			recorded = string(event.Payload)
		}
	}
	if !strings.Contains(recorded, `"status":"cancelled"`) {
		t.Fatalf("durable tool_result = %q, want the cancelled status", recorded)
	}

	// And a restart must keep it neutral too.
	restarted := fixRestart(t, store)
	restoredSession, ok := restarted.Session(sessionID)
	if !ok {
		t.Fatal("restore lost the session")
	}
	restoredLast := restoredSession.Messages[len(restoredSession.Messages)-1]
	if len(restoredLast.Tools) != 1 || restoredLast.Tools[0].Status != "cancelled" {
		t.Fatalf("restored tool row = %+v, want cancelled", restoredLast.Tools)
	}
}

func TestGenuineToolErrorStaysError(t *testing.T) {
	store := newFakeStore()
	provider := &fixToolCallProvider{toolName: "fail_tool"}
	svc := fixService(t, Config{}, rigtools.NewRegistry(&fixFailingTool{}), nil, fakeApprovals{}, store)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "run the failing tool"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	last := session.Messages[len(session.Messages)-1]
	if len(last.Tools) != 1 || last.Tools[0].Status != "error" {
		t.Fatalf("genuine tool failure = %+v, want status error", last.Tools)
	}
}

// --- FR-11: Regenerate re-expands @session references ----------------------

func TestRegenerateReExpandsSessionReferences(t *testing.T) {
	store := newFakeStore()
	provider := &captureProvider{}
	svc := fixService(t, Config{}, rigtools.NewRegistry(), nil, fakeApprovals{}, store)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	sourceID, err := svc.NewSession("source")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sourceID, "the source conversation secret-token-42"); err != nil {
		t.Fatalf("Send source: %v", err)
	}
	waitIdle(t, svc, sourceID)

	targetID, err := svc.NewSession("target")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	prompt := "@session:" + sourceID + " please summarize the referenced conversation"
	if err := svc.Send(targetID, prompt); err != nil {
		t.Fatalf("Send target: %v", err)
	}
	waitIdle(t, svc, targetID)
	if first := fixRequestTexts(provider.lastRequest()); !strings.Contains(first, "[引用的会话") {
		t.Fatalf("setup: the first send did not expand the reference: %q", first)
	}

	if err := svc.Regenerate(targetID); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	waitIdle(t, svc, targetID)
	second := fixRequestTexts(provider.lastRequest())
	if !strings.Contains(second, "[引用的会话") || !strings.Contains(second, "secret-token-42") {
		t.Fatalf("BUG: regenerate dropped the @session reference expansion: %q", second)
	}

	// The durable turn keeps the user's original prompt (the expansion is only
	// for the model), exactly like the first Send.
	loaded, err := store.History().Load(context.Background(), targetID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if text := fixMessageTexts(loaded); strings.Contains(text, "[引用的会话") {
		t.Fatalf("the reference block leaked into the durable history: %q", text)
	}
	users := 0
	for _, message := range loaded {
		if message.Role == rig.RoleUser && strings.Contains(message.Text(), "@session:") {
			users++
		}
	}
	if users != 1 {
		t.Fatalf("durable user messages with @session: = %d (%q), want exactly 1", users, fixMessageTexts(loaded))
	}
	events, err := store.Events(context.Background(), targetID, 0, 0)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	foundRefs := false
	for _, event := range events {
		if event.Type == "session_refs" && strings.Contains(string(event.Payload), sourceID) {
			foundRefs = true
		}
	}
	if !foundRefs {
		t.Fatalf("no session_refs event recorded for the regenerate retry: %+v", events)
	}
}

// --- FR-12: Regenerate must continue from the durable (possibly compacted)
// history instead of resurrecting compacted-away turns from the read model ----

func TestRegenerateUsesCompactedDurableHistory(t *testing.T) {
	store := newFakeStore()
	provider := &fixNumberedProvider{}
	svc := fixService(t, Config{ContextBudget: 100}, rigtools.NewRegistry(), nil, fakeApprovals{}, store)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	compacted := []rig.Message{
		rig.NewMessage(rig.RoleSystem, compact.CompactionSummaryContent(2, "earlier turns summarized")),
		rig.NewMessage(rig.RoleUser, "second"),
	}
	svc.compactorFactory = func(contracts.ChatConfig) (rig.Compactor, error) {
		return &fakeCompactor{messages: compacted}, nil
	}

	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for _, prompt := range []string{"first", "second"} {
		if err := svc.Send(sessionID, prompt); err != nil {
			t.Fatalf("Send %q: %v", prompt, err)
		}
		waitIdle(t, svc, sessionID)
	}
	if result, err := svc.CompactNow(context.Background(), sessionID); err != nil {
		t.Fatalf("CompactNow: %v", err)
	} else if result.Replaced != 4 || result.Saved != 2 {
		t.Fatalf("compaction result = %+v, want 4 -> 2", result)
	}
	// The transcript read model still shows the whole conversation.
	session, _ := svc.Session(sessionID)
	if len(session.Messages) != 4 {
		t.Fatalf("read model messages = %d, want the untouched 4-message transcript", len(session.Messages))
	}

	if err := svc.Regenerate(sessionID); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	waitIdle(t, svc, sessionID)

	loaded, err := store.History().Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	texts := fixMessageTexts(loaded)
	t.Logf("durable history after compact+regenerate: %q", texts)
	if strings.Contains(texts, "answer-1") {
		t.Fatalf("BUG: regenerate resurrected the compacted-away first turn: %q", texts)
	}
	if !strings.Contains(texts, "second") || !strings.Contains(texts, "answer-3") {
		t.Fatalf("regenerate lost the retried turn: %q", texts)
	}
	if len(loaded) != 3 {
		t.Fatalf("durable history = %d messages (%q), want summary + user + answer", len(loaded), texts)
	}
}

// --- LOOP-P3-1: finish() keeps running until the interrupted turn is durable --

func TestFinishKeepsRunningUntilInterruptedTurnPersisted(t *testing.T) {
	base := newFakeStore()
	checkpoints := &fixStallCheckpoints{
		inner:   base.CheckpointStore(),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	store := &fixStallStore{fakeStore: base, cp: checkpoints}
	provider := &fixFailThenAnswerProvider{}
	svc := fixService(t, Config{}, rigtools.NewRegistry(), nil, fakeApprovals{}, store)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "first prompt"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case <-checkpoints.started:
	case <-time.After(3 * time.Second):
		t.Fatal("finish never reached the interrupted-turn persistence")
	}
	session, _ := svc.Session(sessionID)
	if !session.Running {
		t.Fatalf("BUG: the session stopped running while its interrupted turn was still being persisted")
	}
	if err := svc.Send(sessionID, "second prompt"); !errors.Is(err, ErrSessionRunning) {
		t.Fatalf("Send during the finish persistence window = %v, want ErrSessionRunning", err)
	}
	close(checkpoints.release)
	waitIdle(t, svc, sessionID)

	loaded, err := store.History().Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if text := fixMessageTexts(loaded); strings.Contains(text, "ZeoKeel exited before it finished") {
		t.Fatalf("BUG: a normal failure injected the crash-recovery marker: %q", text)
	}
	if len(loaded) != 2 {
		t.Fatalf("durable history = %q, want user + the persisted partial answer", fixMessageTexts(loaded))
	}

	if err := svc.Send(sessionID, "second prompt"); err != nil {
		t.Fatalf("Send after the interrupted turn settled: %v", err)
	}
	waitIdle(t, svc, sessionID)
	loaded, err = store.History().Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	texts := fixMessageTexts(loaded)
	t.Logf("durable history after the retried turn: %q", texts)
	if count := strings.Count(texts, "second answer"); count != 1 {
		t.Fatalf("the next turn's answer appears %d times (%q), want exactly 1", count, texts)
	}
	// The partial answer of the failed turn is kept exactly once, in its place.
	if count := strings.Count(texts, "first partial answer"); count != 1 {
		t.Fatalf("the interrupted partial answer appears %d times (%q), want exactly 1", count, texts)
	}
}

// --- LOOP-P3-2: finish_reason=length is a recognized truncation ------------

func TestFinishReasonLengthMarksTruncated(t *testing.T) {
	store := newFakeStore()
	svc := fixService(t, Config{}, rigtools.NewRegistry(), nil, fakeApprovals{}, store)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return &fixLengthProvider{}, nil }
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "answer"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	last := session.Messages[len(session.Messages)-1]
	if last.Text != "The answer is cut mid-sen" {
		t.Fatalf("assistant text = %q", last.Text)
	}
	if strings.TrimSpace(last.Error) != "" || last.ErrorKind != "" {
		t.Fatalf("a length-truncated turn must stay error-free: %+v", last)
	}
	if !last.Truncated {
		t.Fatalf("BUG: finish_reason=length was not recorded on the message (Truncated=false)")
	}

	events, err := store.Events(context.Background(), sessionID, 0, 0)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	runEnd := ""
	for _, event := range events {
		if event.Type == "run_end" {
			runEnd = string(event.Payload)
		}
	}
	if !strings.Contains(runEnd, `"finishReason":"length"`) {
		t.Fatalf("durable run_end = %q, want the finish reason", runEnd)
	}

	restarted := fixRestart(t, store)
	restored, ok := restarted.Session(sessionID)
	if !ok {
		t.Fatal("restore lost the session")
	}
	restoredLast := restored.Messages[len(restored.Messages)-1]
	if !restoredLast.Truncated {
		t.Fatalf("BUG: the truncation hint was lost after restart")
	}
}

func TestFinishReasonStopIsNotTruncated(t *testing.T) {
	store := newFakeStore()
	svc := fixService(t, Config{}, rigtools.NewRegistry(), nil, fakeApprovals{}, newFakeStore())
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return &captureProvider{}, nil }
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	if last := session.Messages[len(session.Messages)-1]; last.Truncated {
		t.Fatalf("a normal answer was marked truncated: %+v", last)
	}
	_ = store
}

// --- LOOP-P3-3: the iteration budget has a dedicated error kind ------------

func TestIterationCapUsesDedicatedErrorKind(t *testing.T) {
	settings := newFakeSettings(map[string]string{contracts.SettingsChatMaxIterations: "1"})
	store := newFakeStore()
	svc := fixService(t, Config{}, rigtools.NewRegistry(&echoTool{}), settings, fakeApprovals{}, store)
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return &fixAlwaysToolCallsProvider{}, nil }
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "loop forever"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, sessionID)
	last := session.Messages[len(session.Messages)-1]
	if last.ErrorKind != contracts.MessageErrorKindIterations {
		t.Fatalf("BUG: iteration-cap error kind = %q (error %q), want %q",
			last.ErrorKind, last.Error, contracts.MessageErrorKindIterations)
	}
	if last.Error == "" {
		t.Fatal("the diagnostic error text was dropped")
	}
	if len(last.Tools) != 1 {
		t.Fatalf("the last allowed round's tool call = %+v, want 1", last.Tools)
	}

	restarted := fixRestart(t, store)
	restored, ok := restarted.Session(sessionID)
	if !ok {
		t.Fatal("restore lost the session")
	}
	if kind := restored.Messages[len(restored.Messages)-1].ErrorKind; kind != contracts.MessageErrorKindIterations {
		t.Fatalf("restored iteration-cap error kind = %q, want %q", kind, contracts.MessageErrorKindIterations)
	}
}
