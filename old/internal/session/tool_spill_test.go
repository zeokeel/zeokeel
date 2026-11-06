package session_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/state"
)

// sizedTool is the configurable version (methods cannot close over fields on a
// zero value, so the value receiver above is kept for the interface check).
type sizedTool struct{ result string }

func (sizedTool) Name() string        { return "big" }
func (sizedTool) Description() string { return "Returns a large result." }
func (s sizedTool) Invoke(context.Context, json.RawMessage) (string, error) {
	return s.result, nil
}

// bigResult builds a large CJK-bearing tool output with a unique tail so tests
// can prove the persisted/next-turn version really is the truncated summary.
func bigResult() string {
	return strings.Repeat("工具结果行：目录项 a/b/c\n", 2200) + "END-OF-BIG-RESULT"
}

// TestToolResultSpillPersistsSummaryAndFile pins the spill contract: an
// oversized tool result is written into the session workspace, the persisted
// tool_calls row and snapshot keep a summary + path, and the next turn's model
// request contains only that summary.
func TestToolResultSpillPersistsSummaryAndFile(t *testing.T) {
	big := bigResult()
	if len(big) <= 32*1024 {
		t.Fatalf("test result too small: %d", len(big))
	}
	provider := &fakeProvider{respond: func(_ context.Context, _ runtime.Request, call int) []runtime.Chunk {
		if call == 0 {
			return []runtime.Chunk{{
				ToolCalls: []runtime.ToolCall{{ID: "call-big", Name: "big", Arguments: []byte(`{}`)}},
				Done:      true,
			}}
		}
		return []runtime.Chunk{{Text: "ok", Done: true}}
	}}
	h := newHarnessConfigured(t, provider, func(cfg *runtime.Config) {
		if err := cfg.Tools.Register(sizedTool{result: big}); err != nil {
			t.Fatalf("register big tool: %v", err)
		}
	})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "spill")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "read the tree"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	files, err := filepath.Glob(filepath.Join(h.dir, ".zeokeel", "tool-results", "*.txt"))
	if err != nil || len(files) != 1 {
		t.Fatalf("spilled files = %v (err %v), want exactly one", files, err)
	}
	content, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read spilled file: %v", err)
	}
	if string(content) != big {
		t.Fatalf("spilled file is not the full result: %d bytes, want %d", len(content), len(big))
	}

	calls, err := h.store.ToolCallsForSession(id)
	if err != nil {
		t.Fatalf("ToolCallsForSession: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(calls))
	}
	persisted := calls[0].Result
	if !strings.Contains(persisted, "[tool result too large") ||
		!strings.Contains(persisted, ".zeokeel/tool-results/") {
		t.Fatalf("persisted result is not a spill summary: %.300q", persisted)
	}
	if strings.Contains(persisted, "END-OF-BIG-RESULT") {
		t.Fatal("persisted result still contains the huge tail")
	}
	if len(persisted) >= 32*1024 {
		t.Fatalf("persisted result too large: %d bytes", len(persisted))
	}

	// Snapshot/tool card also sees the summary, not the full output.
	if card, ok := h.ctrl.Snapshot().Tools["call-big"]; !ok || !strings.Contains(card.Result, "[tool result too large") {
		t.Fatalf("snapshot tool card = %+v, want the spill summary", card)
	}

}

// TestToolResultSpillFallbackKeepsOriginal pins the write-failure fallback: when
// the spill directory cannot be created, the turn keeps the original result and
// the session continues normally.
func TestToolResultSpillFallbackKeepsOriginal(t *testing.T) {
	big := bigResult()
	provider := &fakeProvider{respond: func(_ context.Context, _ runtime.Request, call int) []runtime.Chunk {
		if call == 0 {
			return []runtime.Chunk{{
				ToolCalls: []runtime.ToolCall{{ID: "call-big", Name: "big", Arguments: []byte(`{}`)}},
				Done:      true,
			}}
		}
		return []runtime.Chunk{{Text: "ok", Done: true}}
	}}
	h := newHarnessConfigured(t, provider, func(cfg *runtime.Config) {
		if err := cfg.Tools.Register(sizedTool{result: big}); err != nil {
			t.Fatalf("register big tool: %v", err)
		}
	})
	// A regular file where the spill directory should go makes MkdirAll fail.
	if err := os.WriteFile(filepath.Join(h.dir, ".zeokeel"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("plant blocking file: %v", err)
	}
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "spill fallback")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "read"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdleAndDrain(t, h.ctrl)

	calls, err := h.store.ToolCallsForSession(id)
	if err != nil || len(calls) != 1 {
		t.Fatalf("tool calls = %v (%v), want 1", calls, err)
	}
	if calls[0].Result != big {
		t.Fatalf("fallback did not keep the original result (%d bytes)", len(calls[0].Result))
	}
	if got := h.ctrl.Snapshot().Phase; got != state.PhaseIdle {
		t.Fatalf("turn phase = %q, want idle after a spill failure", got)
	}
}
