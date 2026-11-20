package toolkit

// The SDK search tool ranks workspace content (BM25) and file names, which is
// the gap between glob (exact patterns) and grep (line regex). The product never
// registered it; a coding harness wants all three.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
)

// searchWorkspace writes a small tree with two content files and one hidden one.
func searchWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "alpha.md"), "the sandbox policy confines writes\n")
	writeFile(t, filepath.Join(root, "beta.md"), "unrelated notes about the weather\n")
	writeFile(t, filepath.Join(root, ".env"), "SANDBOX_TOKEN=secret\n")
	return root
}

// TestSearchToolRanksContent pins the text mode: the query finds the file that
// actually talks about it, and hidden paths never come back.
func TestSearchToolRanksContent(t *testing.T) {
	root := searchWorkspace(t)
	_, registry, _, _ := activateMCPToolkitWithRoot(t, map[string]any{
		"root": root, "deniedGlobs": []any{"**/.env"},
	}, newMutableSettings(nil))

	result := invoke(t, registry, "search", `{"query":"sandbox policy","mode":"text"}`)
	if result.IsError {
		t.Fatalf("search failed: %q", result.Content)
	}
	if !strings.Contains(result.Content, "alpha.md") {
		t.Fatalf("search did not rank alpha.md: %q", result.Content)
	}
	if strings.Contains(result.Content, "beta.md") {
		t.Fatalf("search returned an unrelated file: %q", result.Content)
	}
	if strings.Contains(result.Content, ".env") {
		t.Fatalf("search leaked a hidden path: %q", result.Content)
	}
}

// TestSearchFilesModeFindsNames pins the file-name mode and the root scope: a
// name-only query resolves inside the workspace and never outside it.
func TestSearchFilesModeFindsNames(t *testing.T) {
	root := searchWorkspace(t)
	_, registry, _, _ := activateMCPToolkitWithRoot(t, map[string]any{"root": root}, newMutableSettings(nil))

	result := invoke(t, registry, "search", `{"query":"beta","mode":"files"}`)
	if result.IsError {
		t.Fatalf("search(files) failed: %q", result.Content)
	}
	if !strings.Contains(result.Content, "beta.md") {
		t.Fatalf("search(files) did not find beta.md: %q", result.Content)
	}

	escape, err := registry.Invoke(context.Background(), rig.ToolCall{
		Name: "search", Arguments: `{"query":"passwd","mode":"files","path":"/etc"}`,
	})
	if err != nil {
		t.Fatalf("search(/etc): %v", err)
	}
	if !escape.IsError {
		t.Fatalf("search escaped the workspace: %q", escape.Content)
	}
}
