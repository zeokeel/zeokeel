package agent

// Tests for large tool-result spilling: only the persisted history is rewritten
// (head + file path); the live run keeps the full text. The seam is the
// rig.History wrapper installed on the agent run, with a workspace-rooted
// directory, atomic content-addressed files, mtime pruning and full-text
// fallbacks on missing/unwritable directories.

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

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// recordingHistory is a rig.History that records the messages the agent loop
// appends, so tests can inspect the spilled persistence result.
type recordingHistory struct {
	mu      sync.Mutex
	loaded  []rig.Message
	appends [][]rig.Message
}

func (h *recordingHistory) Load(context.Context, string) ([]rig.Message, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]rig.Message(nil), h.loaded...), nil
}

func (h *recordingHistory) Append(_ context.Context, _ string, messages ...rig.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.appends = append(h.appends, append([]rig.Message(nil), messages...))
	return nil
}

func (h *recordingHistory) persisted() []rig.Message {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []rig.Message
	for _, batch := range h.appends {
		out = append(out, batch...)
	}
	return out
}

// toolMessage builds one tool result message with the given result text.
func toolMessage(text string) rig.Message {
	return rig.Message{
		Role:       rig.RoleTool,
		Content:    []rig.ContentPart{rig.TextPart(text)},
		ToolCallID: "call-1",
		Name:       "read_file",
	}
}

// spillHarness builds a spill history over dir with a recording inner history.
func spillHarness(t *testing.T, dir string) (*spillHistory, *recordingHistory, *fakeLogger) {
	t.Helper()
	inner := &recordingHistory{}
	logger := &fakeLogger{}
	history := newSpillHistory(inner, func(context.Context) string { return dir }, logger)
	return history, inner, logger
}

func TestSpillWritesLargeToolResult(t *testing.T) {
	dir := t.TempDir()
	history, inner, _ := spillHarness(t, dir)
	full := strings.Repeat("tool output line\n", 3000) // ~48KiB
	if len([]byte(full)) <= spillThresholdBytes {
		t.Fatalf("test payload = %d bytes, want > %d", len([]byte(full)), spillThresholdBytes)
	}
	original := toolMessage(full)
	if err := history.Append(context.Background(), "sess-1", original); err != nil {
		t.Fatalf("Append: %v", err)
	}

	persisted := inner.persisted()
	if len(persisted) != 1 {
		t.Fatalf("persisted messages = %d, want 1", len(persisted))
	}
	content := persisted[0].Text()
	if strings.Contains(content, full) {
		t.Fatal("persisted history contains the full tool result")
	}
	if !strings.Contains(content, "[完整结果: ") {
		t.Fatalf("persisted history lacks the spill marker: %q", content)
	}
	if len([]byte(content)) > spillHeadBytes+256 {
		t.Fatalf("persisted content = %d bytes, want head+marker", len([]byte(content)))
	}
	// The live run's own copy is untouched: the message value is not mutated.
	if original.Text() != full {
		t.Fatal("spill mutated the live run message")
	}

	// The file holds the complete original text and is user-readable.
	path := extractSpillPath(t, content)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("spill file: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("spill file mode = %o, want 0644", info.Mode().Perm())
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spill file: %v", err)
	}
	if string(written) != full {
		t.Fatalf("spill file = %d bytes, want the full %d bytes", len(written), len(full))
	}
	if persisted[0].ToolCallID != "call-1" || persisted[0].Name != "read_file" {
		t.Fatalf("persisted tool metadata lost: %+v", persisted[0])
	}
}

func TestSpillSmallAndNonToolMessagesUnchanged(t *testing.T) {
	history, inner, _ := spillHarness(t, t.TempDir())
	small := toolMessage("short result")
	exact := toolMessage(strings.Repeat("x", spillThresholdBytes))
	largeUser := rig.NewMessage(rig.RoleUser, strings.Repeat("y", spillThresholdBytes*2))
	largeAssistant := rig.NewMessage(rig.RoleAssistant, strings.Repeat("z", spillThresholdBytes*2))
	if err := history.Append(context.Background(), "sess-1", small, exact, largeUser, largeAssistant); err != nil {
		t.Fatalf("Append: %v", err)
	}
	persisted := inner.persisted()
	if len(persisted) != 4 {
		t.Fatalf("persisted = %d messages, want 4", len(persisted))
	}
	if persisted[0].Text() != "short result" {
		t.Fatalf("small tool result changed: %q", persisted[0].Text())
	}
	if persisted[1].Text() != exact.Text() {
		t.Fatalf("exactly %d-byte tool result changed", spillThresholdBytes)
	}
	if persisted[2].Text() != largeUser.Text() || persisted[3].Text() != largeAssistant.Text() {
		t.Fatal("non-tool messages were spilled")
	}
}

func TestSpillWithoutWorkspaceFallsBackToFullText(t *testing.T) {
	history, inner, logger := spillHarness(t, "")
	full := strings.Repeat("result", spillThresholdBytes)
	if err := history.Append(context.Background(), "sess-1", toolMessage(full)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if got := inner.persisted()[0].Text(); got != full {
		t.Fatalf("persisted text = %d bytes, want the full fallback", len(got))
	}
	if !logger.contains("spill") || !logger.contains("workspace") {
		t.Fatalf("logger lines = %v, want a spill/workspace warning", logger.lines)
	}
}

func TestSpillWriteFailureFallsBackToFullText(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	history, inner, logger := spillHarness(t, blocked)
	full := strings.Repeat("result", spillThresholdBytes)
	if err := history.Append(context.Background(), "sess-1", toolMessage(full)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if got := inner.persisted()[0].Text(); got != full {
		t.Fatalf("persisted text = %d bytes, want the full fallback", len(got))
	}
	if !logger.contains("spill") {
		t.Fatalf("logger lines = %v, want a spill warning", logger.lines)
	}
}

func TestSpillIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	history, inner, _ := spillHarness(t, dir)
	full := strings.Repeat("result", spillThresholdBytes)
	if err := history.Append(context.Background(), "sess-1", toolMessage(full)); err != nil {
		t.Fatalf("first Append: %v", err)
	}
	spilled := inner.persisted()[0]
	if err := history.Append(context.Background(), "sess-1", spilled); err != nil {
		t.Fatalf("second Append: %v", err)
	}
	if got := inner.persisted()[1].Text(); got != spilled.Text() {
		t.Fatalf("already-spilled message was rewritten: %q", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read spill dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("spill files = %d, want the idempotent single file", len(entries))
	}
}

func TestPruneSpillDirKeepsLatest100(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	for index := 0; index < 105; index++ {
		path := filepath.Join(dir, "result-"+strings.Repeat("a", 16)+"-"+fmt.Sprintf("%03d", index)+".txt")
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %d: %v", index, err)
		}
		at := base.Add(time.Duration(index) * time.Minute)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatalf("chtimes %d: %v", index, err)
		}
	}
	pruneSpillDir(dir, spillKeepFiles)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read spill dir: %v", err)
	}
	if len(entries) != spillKeepFiles {
		t.Fatalf("files after prune = %d, want %d", len(entries), spillKeepFiles)
	}
	if _, err := os.Stat(filepath.Join(dir, "result-"+strings.Repeat("a", 16)+"-000.txt")); !os.IsNotExist(err) {
		t.Fatalf("oldest file was not pruned: %v", err)
	}
}

func TestSpilledHistoryLoadDelegates(t *testing.T) {
	inner := &recordingHistory{loaded: []rig.Message{rig.NewMessage(rig.RoleUser, "hi")}}
	history := newSpillHistory(inner, func(context.Context) string { return "" }, &fakeLogger{})
	loaded, err := history.Load(context.Background(), "sess-1")
	if err != nil || len(loaded) != 1 || loaded[0].Text() != "hi" {
		t.Fatalf("Load = %+v/%v, want the inner history", loaded, err)
	}
}

func TestServiceSpillHistoryUsesWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	workspace := &fakeWorkspace{}
	workspace.setCurrent(contracts.Workspace{ID: "ws-1", Root: root}, true)
	svc := newTestService(t, Config{}, &fakeSettings{values: map[string]string{}}, &fakeLogger{})
	svc.workspace = workspace
	inner := &recordingHistory{}
	history := svc.spilledHistory(inner)
	full := strings.Repeat("result", spillThresholdBytes)
	if err := history.Append(context.Background(), "sess-1", toolMessage(full)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, spillDirName))
	if err != nil || len(entries) != 1 {
		t.Fatalf("workspace spill dir entries = %v/%v, want one file", entries, err)
	}
	if got := inner.persisted()[0].Text(); strings.Contains(got, full) {
		t.Fatal("workspace-rooted spill kept the full text")
	}
}

// extractSpillPath returns the path embedded in one persisted marker.
func extractSpillPath(t *testing.T, content string) string {
	t.Helper()
	const prefix = "[完整结果: "
	start := strings.Index(content, prefix)
	if start < 0 {
		t.Fatalf("no spill marker in %q", content)
	}
	rest := content[start+len(prefix):]
	end := strings.Index(rest, "]")
	if end < 0 {
		t.Fatalf("unterminated spill marker in %q", content)
	}
	return rest[:end]
}
