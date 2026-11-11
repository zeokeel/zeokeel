package toolkit

// The agent scopes a run's tool calls to the session's workspace root with
// contracts.WithWorkspaceRoot; the root-scoped proxies must dispatch there
// instead of the process-wide current workspace (concurrent sessions may belong
// to different projects).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestBoundToolDispatchesToContextWorkspaceRoot(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootA, "note.txt"), []byte("hello-A"), 0o644); err != nil {
		t.Fatalf("write rootA note: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootB, "note.txt"), []byte("hello-B"), 0o644); err != nil {
		t.Fatalf("write rootB note: %v", err)
	}
	group := newRebindGroup(Config{}, 5*time.Second, func() string { return "" }, nil, nil)
	t.Cleanup(group.close)
	proxies := group.proxies(group.initial())
	var readFile rig.Tool
	for _, proxy := range proxies {
		if proxy.Name() == "read_file" {
			readFile = proxy
			break
		}
	}
	if readFile == nil {
		t.Fatal("read_file proxy missing")
	}

	read := func(root string) string {
		t.Helper()
		ctx := context.Background()
		if root != "" {
			ctx = contracts.WithWorkspaceRoot(ctx, root)
		}
		result, err := readFile.Invoke(ctx, rig.ToolCall{
			ID: "read", Name: "read_file", Arguments: `{"path":"note.txt"}`,
		})
		if err != nil {
			t.Fatalf("read_file(%q): %v", root, err)
		}
		return result.Content
	}

	if got := read(rootA); !strings.Contains(got, "hello-A") || strings.Contains(got, "hello-B") {
		t.Fatalf("read with rootA override = %q, want hello-A only", got)
	}
	if got := read(rootB); !strings.Contains(got, "hello-B") || strings.Contains(got, "hello-A") {
		t.Fatalf("read with rootB override = %q, want hello-B only", got)
	}
	// Without an override (no current workspace) the call stays fail-closed.
	if result, err := readFile.Invoke(context.Background(), rig.ToolCall{
		ID: "read", Name: "read_file", Arguments: `{"path":"note.txt"}`,
	}); err == nil && !result.IsError {
		t.Fatalf("read without a workspace root unexpectedly succeeded: %+v", result)
	}
}
