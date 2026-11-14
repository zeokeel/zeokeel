package regression

// Adversarial audit: spilling oversized tool results into the persisted history.
//
// The product configures Rig's SpillPolicy (rig/context.go, applied by the Rig
// agent before it records a tool message): results over 32 KiB are written to
// <workspace>/.zeokeel/tool-results/ and the model sees Rig's stable summary
// (prefix, absolute path, byte count and a short preview) instead; without a
// workspace the product data directory is the fallback (headless/ACP sessions).
// The framework owns writing, naming, summarizing and prune-to-100; the product
// only resolves the directory. These tests drive a real agent run end to end.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

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

// spillPathFromText extracts the absolute path from Rig's spill summary:
//
//	[tool result spilled to file]
//	path: <absolute path>
//	bytes: <n>
//	preview: <first bytes with newlines as spaces>
func spillPathFromText(t *testing.T, text string) string {
	t.Helper()
	if !rig.IsSpilledToolResult(text) {
		t.Fatalf("persisted message lacks Rig's spill summary: %q", text[:minInt(len(text), 120)])
	}
	for _, line := range strings.Split(text, "\n") {
		if path, ok := strings.CutPrefix(line, "path: "); ok {
			if path == "" {
				t.Fatalf("spill summary carries an empty path: %q", text)
			}
			return path
		}
	}
	t.Fatalf("malformed spill summary: %q", text)
	return ""
}

// minInt is a small helper for bounded slices.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestToolSpillRoundTrip drives a real agent run whose tool returns 40KiB:
// the persisted history carries Rig's summary and path, the file holds the full
// text, and the follow-up provider request carries the summary, not the payload.
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
	if !strings.Contains(persisted, "bytes: "+itoa(int64(len(payload)))) {
		t.Fatalf("spill summary does not report the spilled byte count: %q", persisted)
	}
	path := spillPathFromText(t, persisted)
	spillDir := filepath.Join(workspaceRoot, ".zeokeel", "tool-results")
	if !strings.HasPrefix(path, spillDir+string(filepath.Separator)) {
		t.Fatalf("spill file %q is outside the workspace tool-results dir %q", path, spillDir)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != payload {
		t.Fatalf("spill file content = %d bytes (err %v), want the full %d-byte payload", len(content), err, len(payload))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat spill file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("spill file mode = %v, want 0600 (Rig creates spill files private)", info.Mode().Perm())
	}
	// The live run's follow-up provider request carries the summary, not the
	// payload: spilling is what keeps an oversized result out of later rounds.
	bodies := harness.bodiesAfter(0)
	if len(bodies) < 2 {
		t.Fatalf("provider requests = %d, want at least 2", len(bodies))
	}
	followUp := string(bodies[1].Body)
	if strings.Contains(followUp, payload) {
		t.Fatalf("the follow-up request still carries the full tool result")
	}
	if !strings.Contains(followUp, rig.SpillSummaryPrefix) {
		t.Fatalf("the follow-up request does not carry Rig's spill summary")
	}
}

// TestToolSpillNoWorkspaceFallback covers a harness without a workspace backend
// (the headless/ACP case): the product data directory is the spill fallback, so
// oversized results are still spilled instead of being resent inline forever.
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
	if persisted == "" {
		t.Fatalf("persisted history has no tool message: %+v", messages)
	}
	if len(persisted) >= len(payload) {
		t.Fatalf("persisted tool text = %d bytes, want it spilled (%d-byte payload)",
			len(persisted), len(payload))
	}
	if !rig.IsSpilledToolResult(persisted) {
		t.Fatalf("data-dir fallback wrote no spill summary: %q", persisted[:minInt(len(persisted), 120)])
	}
	path := spillPathFromText(t, persisted)
	dataSpillDir := filepath.Join(harness.h.dataDir, ".zeokeel", "tool-results")
	if !strings.HasPrefix(path, dataSpillDir+string(filepath.Separator)) {
		t.Fatalf("spill file %q is outside the data-dir fallback %q", path, dataSpillDir)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != payload {
		t.Fatalf("spill file content = %d bytes (err %v), want the full %d-byte payload",
			len(content), err, len(payload))
	}
}
