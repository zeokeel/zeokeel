package agent

// Tests for tool-result spilling at the agent-run seam.
//
// The product only resolves the spill directory (spill.go); writing, the
// model-visible summary and prune-to-MaxFiles are Rig's rig.SpillPolicy
// (rig/context.go, applied in rig/agent/agent.go before the tool message is
// recorded). These tests therefore pin the policy handed to rig.AgentSpec and
// the observable outcome of a real run: the persisted tool message is Rig's
// summary (rig.IsSpilledToolResult), the next model request sees that summary
// instead of the full result, the file holds the complete payload, and the
// directory is capped at MaxFiles. The tombstone guard on History is covered by
// lifecycle_fixes_test.go (TestDeletedRunningSessionIsNotResurrected).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// hugeResultTool returns a fixed oversized tool result.
type hugeResultTool struct{ payload string }

func (t *hugeResultTool) Name() string           { return "huge_result" }
func (t *hugeResultTool) Description() string    { return "Audit tool returning a huge result." }
func (t *hugeResultTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *hugeResultTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{Content: t.payload}, nil
}

// spillToolProvider requests the huge tool on its first call and answers on the
// following ones, recording every request so tests can assert what the model saw
// after the spill.
type spillToolProvider struct {
	toolName string

	mu       sync.Mutex
	calls    int
	requests []rig.Request
}

func (p *spillToolProvider) Name() string { return "spill-tool" }

func (p *spillToolProvider) Stream(_ context.Context, request rig.Request) (rig.Stream, error) {
	p.mu.Lock()
	p.calls++
	p.requests = append(p.requests, request)
	call := p.calls
	p.mu.Unlock()
	if call == 1 {
		return &scriptedStream{chunks: []rig.Chunk{
			{ToolCalls: []rig.ToolCallDelta{{Index: 0, ID: "call-1", Name: p.toolName, Arguments: `{}`}}},
			{FinishReason: "tool_calls"},
		}}, nil
	}
	return &scriptedStream{chunks: []rig.Chunk{{Delta: "done"}, {FinishReason: "stop"}}}, nil
}

// request returns the n-th captured provider request (1-based); it fails the
// test when the run made fewer calls.
func (p *spillToolProvider) request(t *testing.T, n int) rig.Request {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) < n {
		t.Fatalf("provider requests = %d, want at least %d", len(p.requests), n)
	}
	return p.requests[n-1]
}

// newSpillService builds a service with a durable fake store and a data dir,
// returning the data dir so tests can assert the headless spill fallback.
func newSpillService(t *testing.T, tools ...rig.Tool) (*service, *fakeStore, string) {
	t.Helper()
	store := newFakeStore()
	dataDir := t.TempDir()
	svc := newService(Config{}, rigtools.NewRegistry(tools...), newFakeSettings(nil), &fakeLogger{}, store, fakeApprovals{})
	svc.dataDir = dataDir
	return svc, store, dataDir
}

// runSpillTool drives one complete run whose only tool returns a huge result. It
// returns the session ID and the persisted tool message text.
func runSpillTool(t *testing.T, svc *service, provider *spillToolProvider) (string, string) {
	t.Helper()
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(sessionID, "run the huge tool"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, sessionID)
	messages, err := svc.store.History().Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("load persisted history: %v", err)
	}
	persisted := ""
	for _, message := range messages {
		if message.Role == rig.RoleTool {
			persisted = message.Text()
		}
	}
	return sessionID, persisted
}

// spillSummaryPath extracts the "path: ..." line of Rig's spill summary.
func spillSummaryPath(t *testing.T, summary string) string {
	t.Helper()
	for _, line := range strings.Split(summary, "\n") {
		if path, ok := strings.CutPrefix(line, "path: "); ok {
			return strings.TrimSpace(path)
		}
	}
	t.Fatalf("spill summary has no path line: %q", boundedForTest(summary))
	return ""
}

func TestSpillPolicyUsesWorkspaceDir(t *testing.T) {
	root := t.TempDir()
	workspace := &fakeWorkspace{}
	workspace.setCurrent(contracts.Workspace{ID: "ws-1", Root: root}, true)
	svc, _, _ := newSpillService(t)
	svc.workspace = workspace

	policy := svc.spillPolicy(context.Background())
	if policy == nil {
		t.Fatal("spillPolicy = nil, want a policy")
	}
	wantDir := filepath.Join(root, ".zeokeel", "tool-results")
	if policy.Dir != wantDir {
		t.Fatalf("spill dir = %q, want %q", policy.Dir, wantDir)
	}
	if !filepath.IsAbs(policy.Dir) {
		t.Fatalf("spill dir = %q, want an absolute path (Rig resolves relative dirs against the process cwd)", policy.Dir)
	}
	if policy.ThresholdBytes != 32<<10 {
		t.Fatalf("spill threshold = %d, want 32 KiB", policy.ThresholdBytes)
	}
	if policy.MaxFiles != 100 {
		t.Fatalf("spill MaxFiles = %d, want 100", policy.MaxFiles)
	}
}

// TestRunSpillsLargeToolResult covers the headless/ACP path (no workspace): a
// 40 KiB tool result is written under the product data directory and the model
// sees Rig's summary, not the full result, on its follow-up request.
func TestRunSpillsLargeToolResult(t *testing.T) {
	payload := strings.Repeat("A", 40<<10)
	provider := &spillToolProvider{toolName: "huge_result"}
	svc, _, dataDir := newSpillService(t, &hugeResultTool{payload: payload})

	sessionID, persisted := runSpillTool(t, svc, provider)
	if persisted == "" {
		t.Fatal("persisted history has no tool message")
	}
	if !rig.IsSpilledToolResult(persisted) {
		t.Fatalf("persisted tool message is not Rig's spill summary: %q", boundedForTest(persisted))
	}
	if len(persisted) >= len(payload) {
		t.Fatalf("persisted tool message = %d bytes, want the spilled summary", len(persisted))
	}
	if !strings.Contains(persisted, payload[:240]) {
		t.Fatalf("spill summary does not carry the 240-byte preview: %q", boundedForTest(persisted))
	}
	path := spillSummaryPath(t, persisted)
	spillDir := filepath.Join(dataDir, ".zeokeel", "tool-results")
	if !strings.HasPrefix(path, spillDir+string(filepath.Separator)) {
		t.Fatalf("spill file %q is outside the data-dir fallback %q", path, spillDir)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spill file %q: %v", path, err)
	}
	if string(content) != payload {
		t.Fatalf("spill file = %d bytes, want the full %d-byte payload", len(content), len(payload))
	}

	// The model's follow-up request sees the summary, not the full result: the
	// spill carries over to the conversation Rig sends to the provider.
	followUp := provider.request(t, 2)
	followUpText := rigMessagesText(followUp.Messages)
	if !strings.Contains(followUpText, rig.SpillSummaryPrefix) {
		t.Fatalf("follow-up provider request has no spill summary: %q", boundedForTest(followUpText))
	}
	if strings.Contains(followUpText, payload) {
		t.Fatal("follow-up provider request still carries the full 40 KiB tool result")
	}

	// The transcript read model shows the same summary on the tool card (the
	// spill happens before the tool-result event is emitted).
	session, ok := svc.Session(sessionID)
	if !ok {
		t.Fatalf("session %q missing from the read model", sessionID)
	}
	cardSummary := ""
	for _, message := range session.Messages {
		for _, tool := range message.Tools {
			cardSummary = tool.Summary
		}
	}
	if !rig.IsSpilledToolResult(cardSummary) {
		t.Fatalf("transcript tool card summary = %q, want Rig's spill summary", boundedForTest(cardSummary))
	}
}

// TestRunDoesNotSpillAtThreshold pins the threshold: exactly 32 KiB stays
// inline; one byte over spills.
func TestRunDoesNotSpillAtThreshold(t *testing.T) {
	atThreshold := strings.Repeat("B", spillThresholdBytes)
	provider := &spillToolProvider{toolName: "huge_result"}
	svc, _, _ := newSpillService(t, &hugeResultTool{payload: atThreshold})
	_, persisted := runSpillTool(t, svc, provider)
	if persisted != atThreshold {
		t.Fatalf("exactly %d-byte tool result was spilled (persisted %d bytes)", spillThresholdBytes, len(persisted))
	}

	overThreshold := strings.Repeat("C", spillThresholdBytes+1)
	provider = &spillToolProvider{toolName: "huge_result"}
	svc, _, _ = newSpillService(t, &hugeResultTool{payload: overThreshold})
	_, persisted = runSpillTool(t, svc, provider)
	if !rig.IsSpilledToolResult(persisted) {
		t.Fatalf("one byte over the %d-byte threshold was not spilled (persisted %d bytes)",
			spillThresholdBytes, len(persisted))
	}
}

// TestRunSpillPrunesToMaxFiles pins the MaxFiles cleanup: pre-existing older
// spill files are pruned down to 100 after one new spill.
func TestRunSpillPrunesToMaxFiles(t *testing.T) {
	payload := strings.Repeat("D", 40<<10)
	svc, _, dataDir := newSpillService(t, &hugeResultTool{payload: payload})
	dir := filepath.Join(dataDir, ".zeokeel", "tool-results")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create spill dir: %v", err)
	}
	base := time.Now().Add(-3 * time.Hour)
	for index := 0; index < 105; index++ {
		name := filepath.Join(dir, fmt.Sprintf("old-%03d.txt", index))
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatalf("write old spill file %d: %v", index, err)
		}
		at := base.Add(time.Duration(index) * time.Minute)
		if err := os.Chtimes(name, at, at); err != nil {
			t.Fatalf("chtimes old spill file %d: %v", index, err)
		}
	}

	provider := &spillToolProvider{toolName: "huge_result"}
	if _, persisted := runSpillTool(t, svc, provider); !rig.IsSpilledToolResult(persisted) {
		t.Fatalf("tool result was not spilled: %q", boundedForTest(persisted))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read spill dir: %v", err)
	}
	files := 0
	names := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		files++
		names[entry.Name()] = true
	}
	if files != spillKeepFiles {
		t.Fatalf("spill files after prune = %d, want %d", files, spillKeepFiles)
	}
	if names["old-000.txt"] {
		t.Fatal("oldest spill file survived the prune")
	}
	if !names["old-006.txt"] {
		t.Fatal("the 100 newest pre-existing spill files should survive the prune")
	}
}

// boundedForTest shortens a string for failure messages.
func boundedForTest(text string) string {
	const limit = 160
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}

// rigMessagesText joins every message text of one provider request.
func rigMessagesText(messages []rig.Message) string {
	var builder strings.Builder
	for _, message := range messages {
		builder.WriteString(string(message.Role))
		builder.WriteString(":")
		builder.WriteString(message.Text())
		builder.WriteString("\n")
	}
	return builder.String()
}
