package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestSpillCleanupCapsFiles pins the retention cap: after more than 100 spills
// only the newest 100 files remain.
func TestSpillCleanupCapsFiles(t *testing.T) {
	dir := t.TempDir()
	payload := strings.Repeat("x", 33*1024)
	for i := 0; i < 105; i++ {
		sum, ok, err := spillToolResultFile(dir, fmt.Sprintf("call-%03d", i), payload)
		if err != nil || !ok || sum == "" {
			t.Fatalf("spill %d: ok=%v err=%v summary=%q", i, ok, err, sum)
		}
	}
	files, err := filepath.Glob(filepath.Join(dir, ".zeokeel", "tool-results", "*.txt"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) != toolResultSpillKeep {
		t.Fatalf("retained files = %d, want %d", len(files), toolResultSpillKeep)
	}
}

// TestSpillSummaryKeepsUTF8Head pins the CJK/UTF-8 boundary behavior: the head
// never splits a rune and the full file keeps the original bytes.
func TestSpillSummaryKeepsUTF8Head(t *testing.T) {
	root := filepath.Join(t.TempDir(), "中文 工作区")
	payload := strings.Repeat("中文目录项/文件.txt\n", 6000) // > 32KB, 3-byte runes
	summary, ok, err := spillToolResultFile(root, "调用-1", payload)
	if err != nil || !ok {
		t.Fatalf("spill: ok=%v err=%v", ok, err)
	}
	if !utf8.ValidString(summary) {
		t.Fatal("summary head split a UTF-8 rune")
	}
	if !strings.Contains(summary, "中文目录项") || !strings.Contains(summary, "tool-results") {
		t.Fatalf("summary = %.200q", summary)
	}
	files, err := filepath.Glob(filepath.Join(root, ".zeokeel", "tool-results", "*.txt"))
	if err != nil || len(files) != 1 {
		t.Fatalf("files = %v (err %v)", files, err)
	}
	content, err := os.ReadFile(files[0])
	if err != nil || string(content) != payload {
		t.Fatalf("written file mismatch (err %v, %d bytes)", err, len(content))
	}
}

// TestSpillWriteFailureReportsError pins the helper's error path used by the
// fallback: a root that cannot host the directory returns ok=false + an error.
func TestSpillWriteFailureReportsError(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("file, not a directory"), 0o600); err != nil {
		t.Fatalf("plant file: %v", err)
	}
	sum, ok, err := spillToolResultFile(blocked, "call-1", strings.Repeat("y", 33*1024))
	if err == nil || ok || sum != "" {
		t.Fatalf("spill into a file root = (%q, %v, %v), want fallback + error", sum, ok, err)
	}
}
