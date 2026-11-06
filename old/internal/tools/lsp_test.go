package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zeokeel/zeokeel/old/internal/lsp"
	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

// fakeLSPProvider implements lsp.Provider in memory so the tool's argument
// validation, rendering and bounds are tested without a subprocess. The real
// stdio provider is covered by internal/lsp's fake-server tests.
type fakeLSPProvider struct {
	result  *lsp.Result
	err     error
	queries []lsp.Query
}

func (f *fakeLSPProvider) Query(_ context.Context, q lsp.Query) (*lsp.Result, error) {
	f.queries = append(f.queries, q)
	if f.err != nil {
		return nil, f.err
	}
	if f.result != nil {
		return f.result, nil
	}
	return &lsp.Result{}, nil
}

func newLSPToolFixture(t *testing.T, provider lsp.Provider) (runtime.Tool, string) {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
	return NewLSPTool(LSPConfig{WorkspaceRoot: root, Provider: provider}), root
}

func lspArgs(op, file string, line, column int) map[string]any {
	return map[string]any{"operation": op, "file": file, "line": line, "column": column}
}

func TestLSPToolMetadata(t *testing.T) {
	tool := NewLSPTool(LSPConfig{WorkspaceRoot: t.TempDir()})
	if tool.Name() != "lsp" {
		t.Fatalf("Name() = %q, want %q", tool.Name(), "lsp")
	}
	description := tool.Description()
	for _, want := range []string{
		"goToDefinition", "findReferences", "goToImplementation", "hover",
		"UTF-16", "read-only", "lsp.servers", "50",
	} {
		if !strings.Contains(description, want) {
			t.Errorf("Description() does not mention %q:\n%s", want, description)
		}
	}
}

func TestLSPToolDefinitionRendering(t *testing.T) {
	provider := &fakeLSPProvider{}
	tool, root := newLSPToolFixture(t, provider)
	mainPath := filepath.Join(root, "main.go")
	outside := filepath.Join(t.TempDir(), "lib.go")
	provider.result = &lsp.Result{Locations: []lsp.Location{
		{URI: lsp.PathToURI(mainPath), Range: lsp.Range{Start: lsp.Position{Line: 11, Character: 4}}},
		{URI: lsp.PathToURI(outside), Range: lsp.Range{Start: lsp.Position{Line: 2, Character: 0}}},
	}}

	out := mustInvoke(t, tool, lspArgs("goToDefinition", "main.go", 2, 3))
	for _, want := range []string{
		"goToDefinition",
		"2 location(s)",
		"main.go:12:5",
		filepath.ToSlash(outside) + ":3:1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
	if len(provider.queries) != 1 {
		t.Fatalf("provider queries = %d, want 1", len(provider.queries))
	}
	q := provider.queries[0]
	if q.Operation != lsp.OpGoToDefinition || q.LanguageID != "go" {
		t.Errorf("query operation/language = %q/%q", q.Operation, q.LanguageID)
	}
	if q.Path != mainPath {
		t.Errorf("query path = %q, want %q", q.Path, mainPath)
	}
	if q.Line != 2 || q.Column != 3 {
		t.Errorf("query line/column = %d/%d, want 2/3 (one-based passthrough)", q.Line, q.Column)
	}
	if !strings.Contains(q.Text, "package main") {
		t.Errorf("query text = %q, want the file content", q.Text)
	}
}

func TestLSPToolUTF16ColumnPassthrough(t *testing.T) {
	// The tool forwards the model's one-based UTF-16 column unchanged (the
	// provider maps it to the zero-based LSP character). An emoji occupies two
	// UTF-16 units, so column 3 points at the character after it.
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "emoji.go"), "😀x\n")
	provider := &fakeLSPProvider{}
	tool := NewLSPTool(LSPConfig{WorkspaceRoot: root, Provider: provider})
	mustInvoke(t, tool, lspArgs("hover", "emoji.go", 1, 3))
	if len(provider.queries) != 1 {
		t.Fatalf("provider queries = %d, want 1", len(provider.queries))
	}
	q := provider.queries[0]
	if q.Line != 1 || q.Column != 3 {
		t.Fatalf("query line/column = %d/%d, want 1/3", q.Line, q.Column)
	}
	if q.Text != "😀x\n" {
		t.Fatalf("query text = %q, want the emoji file content", q.Text)
	}
}

func TestLSPToolReferencesAreBounded(t *testing.T) {
	provider := &fakeLSPProvider{}
	tool, root := newLSPToolFixture(t, provider)
	uri := lsp.PathToURI(filepath.Join(root, "main.go"))
	locations := make([]lsp.Location, 60)
	for i := range locations {
		locations[i] = lsp.Location{URI: uri, Range: lsp.Range{Start: lsp.Position{Line: i, Character: 0}}}
	}
	provider.result = &lsp.Result{Locations: locations}

	out := mustInvoke(t, tool, lspArgs("findReferences", "main.go", 1, 1))
	if got := strings.Count(out, "\n  main.go:"); got != LSPMaxLocations {
		t.Errorf("rendered %d locations, want the %d-location cap", got, LSPMaxLocations)
	}
	if !strings.Contains(out, "10 more") {
		t.Errorf("output does not report the omitted locations:\n%s", out)
	}
	if len(out) > LSPMaxOutputBytes {
		t.Errorf("output is %d bytes, want <= %d", len(out), LSPMaxOutputBytes)
	}
}

func TestLSPToolHoverRendering(t *testing.T) {
	provider := &fakeLSPProvider{result: &lsp.Result{Hover: "func Foo() int\n\nDocs."}}
	tool, _ := newLSPToolFixture(t, provider)
	out := mustInvoke(t, tool, lspArgs("hover", "main.go", 2, 1))
	if !strings.Contains(out, "hover") || !strings.Contains(out, "func Foo() int") {
		t.Fatalf("hover output = %q", out)
	}
}

func TestLSPToolHoverEmpty(t *testing.T) {
	provider := &fakeLSPProvider{result: &lsp.Result{}}
	tool, _ := newLSPToolFixture(t, provider)
	out := mustInvoke(t, tool, lspArgs("hover", "main.go", 2, 1))
	if !strings.Contains(out, "no hover information") {
		t.Fatalf("empty hover output = %q, want a stable no-information marker", out)
	}
}

func TestLSPToolOutputIsBounded(t *testing.T) {
	provider := &fakeLSPProvider{result: &lsp.Result{Hover: strings.Repeat("日本語", 30000)}}
	tool, _ := newLSPToolFixture(t, provider)
	out := mustInvoke(t, tool, lspArgs("hover", "main.go", 1, 1))
	if len(out) > LSPMaxOutputBytes {
		t.Fatalf("output is %d bytes, want <= %d", len(out), LSPMaxOutputBytes)
	}
	if !utf8.ValidString(out) {
		t.Fatal("truncated output is not valid UTF-8")
	}
	if !strings.Contains(out, "truncated") {
		t.Fatalf("truncated output does not say so:\n%.200s", out)
	}
}

func TestLSPToolValidationErrors(t *testing.T) {
	tool, _ := newLSPToolFixture(t, &fakeLSPProvider{})
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"missing operation", map[string]any{"file": "main.go", "line": 1, "column": 1}, `"operation" is required`},
		{"unknown operation", lspArgs("rename", "main.go", 1, 1), "unknown operation"},
		{"missing file", map[string]any{"operation": "hover", "line": 1, "column": 1}, `"file" is required`},
		{"zero line", lspArgs("hover", "main.go", 0, 1), `"line"`},
		{"negative line", lspArgs("hover", "main.go", -1, 1), `"line"`},
		{"zero column", lspArgs("hover", "main.go", 1, 0), `"column"`},
		{"negative column", lspArgs("hover", "main.go", 1, -2), `"column"`},
		{"parent escape", lspArgs("hover", "../outside.go", 1, 1), ".."},
	}
	for _, tc := range cases {
		_, err := invokeTool(t, tool, tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
	// An absolute path outside the workspace is rejected too.
	outside := filepath.Join(t.TempDir(), "lib.go")
	if _, err := invokeTool(t, tool, lspArgs("hover", outside, 1, 1)); err == nil || !strings.Contains(err.Error(), "outside the workspace") {
		t.Errorf("outside-workspace file error = %v", err)
	}
}

func TestLSPToolUnknownLanguage(t *testing.T) {
	provider := &fakeLSPProvider{}
	tool, root := newLSPToolFixture(t, provider)
	writeTestFile(t, filepath.Join(root, "notes.xyz"), "x")
	_, err := invokeTool(t, tool, lspArgs("hover", "notes.xyz", 1, 1))
	if err == nil || !strings.Contains(err.Error(), ".xyz") || !strings.Contains(err.Error(), "lsp.servers") {
		t.Fatalf("unknown-language error = %v, want the extension and an lsp.servers hint", err)
	}
	if len(provider.queries) != 0 {
		t.Fatalf("provider was called for an unknown language: %v", provider.queries)
	}
}

func TestLSPToolNoServerConfigured(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "main.go"), "package main\n")
	tool := NewLSPTool(LSPConfig{
		WorkspaceRoot: root,
		Provider:      lsp.NewStdioProvider(lsp.ProviderConfig{Root: root}),
	})
	_, err := invokeTool(t, tool, lspArgs("goToDefinition", "main.go", 1, 1))
	if err == nil {
		t.Fatal("query without a configured server returned no error")
	}
	var noServer *lsp.NoServerError
	if !errors.As(err, &noServer) {
		t.Fatalf("error = %v (%T), want *lsp.NoServerError", err, err)
	}
	if !strings.Contains(err.Error(), "no language server is configured") || !strings.Contains(err.Error(), "lsp.servers") {
		t.Errorf("error = %q, want the stable not-configured message", err)
	}
}

func TestLSPToolConfigErrorIsReported(t *testing.T) {
	provider := &fakeLSPProvider{}
	tool := NewLSPTool(LSPConfig{
		WorkspaceRoot: t.TempDir(),
		Provider:      provider,
		ConfigError:   errors.New("lsp.servers is not valid JSON"),
	})
	_, err := invokeTool(t, tool, lspArgs("hover", "main.go", 1, 1))
	if err == nil || !strings.Contains(err.Error(), "lsp.servers is not valid JSON") {
		t.Fatalf("config error = %v, want the configuration error surfaced", err)
	}
	if len(provider.queries) != 0 {
		t.Fatalf("provider was called despite a configuration error: %v", provider.queries)
	}
}

func TestLSPToolProviderFailureIsWrapped(t *testing.T) {
	provider := &fakeLSPProvider{err: errors.New("server exploded")}
	tool, _ := newLSPToolFixture(t, provider)
	_, err := invokeTool(t, tool, lspArgs("hover", "main.go", 1, 1))
	if err == nil || !strings.Contains(err.Error(), "lsp: ") || !strings.Contains(err.Error(), "server exploded") {
		t.Fatalf("provider error = %v, want it stable and lsp-prefixed", err)
	}
}

func TestLSPToolTimeoutIsStable(t *testing.T) {
	provider := &fakeLSPProvider{err: &lsp.TimeoutError{Operation: lsp.OpGoToDefinition, Timeout: 5 * time.Second}}
	tool, _ := newLSPToolFixture(t, provider)
	_, err := invokeTool(t, tool, lspArgs("goToDefinition", "main.go", 1, 1))
	if err == nil || !strings.Contains(err.Error(), "timed out after 5s") {
		t.Fatalf("timeout error = %v, want a stable timed-out message", err)
	}
}

func TestLSPToolFileErrors(t *testing.T) {
	tool, root := newLSPToolFixture(t, &fakeLSPProvider{})
	if _, err := invokeTool(t, tool, lspArgs("hover", "missing.go", 1, 1)); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing file error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeTool(t, tool, lspArgs("hover", "dir.go", 1, 1)); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("directory error = %v", err)
	}
}

func TestLSPToolWithoutProvider(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "main.go"), "package main\n")
	tool := NewLSPTool(LSPConfig{WorkspaceRoot: root})
	_, err := invokeTool(t, tool, lspArgs("hover", "main.go", 1, 1))
	if err == nil || !strings.Contains(err.Error(), "no language server is configured") {
		t.Fatalf("nil provider error = %v", err)
	}
}

func TestLSPToolWithoutWorkspaceRoot(t *testing.T) {
	tool := NewLSPTool(LSPConfig{Provider: &fakeLSPProvider{}})
	_, err := invokeTool(t, tool, lspArgs("hover", "main.go", 1, 1))
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("missing workspace root error = %v", err)
	}
}
