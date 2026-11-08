package regression

// Adversarial audit: spilling oversized tool results into the persisted history.
// >32KiB tool results are saved to <workspace>/.zeokeel/tool-results/ and the
// persisted message keeps a 2KiB head + the file path; the live run still sends
// the full text to the model; without a workspace the full text is persisted;
// the transform is idempotent and prunes the directory to 100 files.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
	_ "unsafe"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
	agentmod "github.com/zeokeel/zeokeel/internal/modules/agent"
)

// hugeTool returns a fixed oversized tool result.
type hugeTool struct {
	name    string
	content string
	calls   *atomic.Int32
}

func (t hugeTool) Name() string           { return t.name }
func (t hugeTool) Description() string    { return "audit huge-result tool" }
func (t hugeTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t hugeTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	if t.calls != nil {
		t.calls.Add(1)
	}
	return rig.ToolResult{Content: t.content}, nil
}

// spillPathFromText extracts the path from a persisted spill pointer.
func spillPathFromText(t *testing.T, text string) string {
	t.Helper()
	const marker = "[完整结果: "
	start := strings.Index(text, marker)
	if start < 0 {
		t.Fatalf("persisted message lacks the spill pointer: %q", text[:minInt(len(text), 120)])
	}
	rest := text[start+len(marker):]
	end := strings.Index(rest, "]")
	if end < 0 {
		t.Fatalf("malformed spill pointer in %q", text)
	}
	return rest[:end]
}

// minInt is a small helper for bounded slices.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestToolSpillRoundTrip drives a real agent run whose tool returns 40KiB:
// the persisted history carries the head + path, the file holds the full text,
// and the live run's next provider request still carries the full result.
func TestToolSpillRoundTrip(t *testing.T) {
	h := newHarness(t)
	workspaceRoot := t.TempDir()
	workspace := &auditWorkspace{}
	if _, err := workspace.Create(context.Background(), "spill", workspaceRoot); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	provide(t, h.ctx, contracts.KeyWorkspace, contracts.WorkspaceService(workspace))

	payload := strings.Repeat("A", 40<<10)
	var calls atomic.Int32
	model := newFakeModel(t, func(body []byte) (int, string) {
		if calls.Add(1) == 1 {
			return 0, sseToolCall("huge_result", "{}")
		}
		return 0, sseAnswer("done")
	})
	tool := hugeTool{name: "huge_result", content: payload}
	harness := startAgentOn(t, h, agentmod.Config{}, model, "auto", tool)
	sessionID := harness.newChatSession(t, "Spill session")
	harness.sendAndWait(t, sessionID, "run the huge tool")

	store := requireService(t, h.ctx, contracts.KeyStore)
	messages, err := store.History().Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("load persisted history: %v", err)
	}
	var persisted string
	for _, message := range messages {
		if message.Role == rig.RoleTool {
			persisted = message.Text()
		}
	}
	if persisted == "" {
		t.Fatalf("persisted history has no tool message: %+v", messages)
	}
	if len(persisted) >= len(payload) {
		t.Fatalf("persisted tool text was not spilled (%d bytes)", len(persisted))
	}
	if !strings.HasPrefix(persisted, payload[:2<<10]) {
		t.Fatalf("persisted head is not the first 2KiB of the result")
	}
	path := spillPathFromText(t, persisted)
	if !strings.HasPrefix(path, filepath.Join(workspaceRoot, ".zeokeel", "tool-results")) &&
		!strings.HasPrefix(path, filepath.Join(workspaceRoot, ".zeokeel", "tool-results")+string(filepath.Separator)) {
		t.Fatalf("spill file %q is outside the workspace tool-results dir", path)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != payload {
		t.Fatalf("spill file content = %d bytes (err %v), want the full %d-byte payload", len(content), err, len(payload))
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("spill file mode = %v (err %v)", info.Mode(), err)
	}
	// The live run's follow-up provider request still carries the full result.
	bodies := harness.bodiesAfter(0)
	if len(bodies) < 2 {
		t.Fatalf("provider requests = %d, want at least 2", len(bodies))
	}
	if !strings.Contains(string(bodies[1].Body), payload) {
		t.Fatalf("the live run did not send the full tool result to the model")
	}
}

// TestToolSpillNoWorkspaceFallback covers a harness without a workspace backend:
// the persisted history keeps the full text and no spill pointer is written.
func TestToolSpillNoWorkspaceFallback(t *testing.T) {
	payload := strings.Repeat("B", 40<<10)
	var calls atomic.Int32
	model := newFakeModel(t, func([]byte) (int, string) {
		if calls.Add(1) == 1 {
			return 0, sseToolCall("huge_result", "{}")
		}
		return 0, sseAnswer("done")
	})
	harness := startAgentWith(t, agentmod.Config{}, model,
		hugeTool{name: "huge_result", content: payload})
	sessionID := harness.newChatSession(t, "No workspace spill")
	harness.sendAndWait(t, sessionID, "run the huge tool")

	store := requireService(t, harness.h.ctx, contracts.KeyStore)
	messages, err := store.History().Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("load persisted history: %v", err)
	}
	var persisted string
	for _, message := range messages {
		if message.Role == rig.RoleTool {
			persisted = message.Text()
		}
	}
	if persisted != payload {
		t.Fatalf("persisted tool text = %d bytes, want the full %d bytes without a workspace", len(persisted), len(payload))
	}
	if strings.Contains(persisted, "[完整结果: ") {
		t.Fatalf("no-workspace fallback still wrote a spill pointer")
	}
}

// The spill transform itself is unexported; the audit reaches it through
// test-only linknames to cover the idempotent and prune paths cheaply.

//go:linkname auditNewSpillHistory github.com/zeokeel/zeokeel/internal/modules/agent.newSpillHistory
func auditNewSpillHistory(inner rig.History, dir func(context.Context) string, logger contracts.Logger) unsafe.Pointer

//go:linkname auditSpillAppend github.com/zeokeel/zeokeel/internal/modules/agent.(*spillHistory).Append
func auditSpillAppend(history unsafe.Pointer, ctx context.Context, sessionID string, messages ...rig.Message) error

// auditSpillLogger records the fallback warning.
type auditSpillLogger struct {
	mu       sync.Mutex
	messages []string
}

func (l *auditSpillLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	l.messages = append(l.messages, format)
	l.mu.Unlock()
}

// auditHistory is a capturing rig.History.
type auditHistory struct{ appended []rig.Message }

func (h *auditHistory) Load(context.Context, string) ([]rig.Message, error) { return nil, nil }
func (h *auditHistory) Append(_ context.Context, _ string, messages ...rig.Message) error {
	h.appended = append(h.appended, messages...)
	return nil
}

// TestToolSpillIdempotentAndFallback covers the transform seams: already-spilled
// messages and small/non-tool messages are untouched, and a missing workspace
// directory falls back to the full text with a warning.
func TestToolSpillIdempotentAndFallback(t *testing.T) {
	payload := strings.Repeat("C", 40<<10)
	dir := t.TempDir()
	inner := &auditHistory{}
	logger := &auditSpillLogger{}
	history := auditNewSpillHistory(inner, func(context.Context) string { return dir }, logger)
	if history == nil {
		t.Fatal("newSpillHistory returned nil")
	}
	ctx := context.Background()

	// First append spills; the second append of the same pointer is untouched.
	original := rig.NewMessage(rig.RoleTool, payload)
	if err := auditSpillAppend(history, ctx, "s1", original); err != nil {
		t.Fatalf("append spill: %v", err)
	}
	spilled := inner.appended[0].Text()
	if len(spilled) >= len(payload) || !strings.Contains(spilled, "[完整结果: ") {
		t.Fatalf("first append did not spill: %d bytes", len(spilled))
	}
	if err := auditSpillAppend(history, ctx, "s1", rig.NewMessage(rig.RoleTool, spilled)); err != nil {
		t.Fatalf("append already spilled: %v", err)
	}
	if again := inner.appended[1].Text(); again != spilled {
		t.Fatalf("already-spilled message was transformed again")
	}

	// Small and non-tool messages pass through unchanged.
	small := rig.NewMessage(rig.RoleTool, "small result")
	user := rig.NewMessage(rig.RoleUser, payload)
	if err := auditSpillAppend(history, ctx, "s1", small, user); err != nil {
		t.Fatalf("append pass-through: %v", err)
	}
	if inner.appended[2].Text() != "small result" || inner.appended[3].Text() != payload {
		t.Fatalf("pass-through messages were transformed")
	}

	// No workspace directory: full text + warning.
	fallback := &auditHistory{}
	fallbackLogger := &auditSpillLogger{}
	plain := auditNewSpillHistory(fallback, func(context.Context) string { return "" }, fallbackLogger)
	if err := auditSpillAppend(plain, ctx, "s2", rig.NewMessage(rig.RoleTool, payload)); err != nil {
		t.Fatalf("append fallback: %v", err)
	}
	if fallback.appended[0].Text() != payload {
		t.Fatalf("fallback did not keep the full text")
	}
	if len(fallbackLogger.messages) == 0 || !strings.Contains(fallbackLogger.messages[0], "no current workspace") {
		t.Fatalf("fallback warning = %v, want a no-workspace warning", fallbackLogger.messages)
	}
}

// TestToolSpillPruneCapsFiles covers the 100-file retention: 105 distinct large
// results leave at most 100 files behind.
func TestToolSpillPruneCapsFiles(t *testing.T) {
	dir := t.TempDir()
	inner := &auditHistory{}
	history := auditNewSpillHistory(inner, func(context.Context) string { return dir }, &auditSpillLogger{})
	ctx := context.Background()
	for index := 0; index < 105; index++ {
		payload := strings.Repeat("D", 34<<10) + itoa(int64(index))
		if err := auditSpillAppend(history, ctx, "s-prune", rig.NewMessage(rig.RoleTool, payload)); err != nil {
			t.Fatalf("append %d: %v", index, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read spill dir: %v", err)
	}
	files := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".txt") {
			files++
		}
	}
	if files > 100 {
		t.Fatalf("spill dir kept %d files, want at most 100", files)
	}
}
