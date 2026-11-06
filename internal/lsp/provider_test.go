package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeServerConfig re-executes this test binary as a fake language server.
func fakeServerConfig(t *testing.T, mode string, env ...string) ServerConfig {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return ServerConfig{
		Command: exe,
		Env:     append([]string{fakeLSPModeEnv + "=" + mode}, env...),
	}
}

func newFakeProvider(t *testing.T, mode string, env ...string) (*StdioProvider, string) {
	t.Helper()
	root := t.TempDir()
	provider := NewStdioProvider(ProviderConfig{
		Servers: map[string]ServerConfig{"go": fakeServerConfig(t, mode, env...)},
		Root:    root,
	})
	return provider, root
}

func fakeQuery(root string, op Operation) Query {
	return Query{
		Operation:  op,
		LanguageID: "go",
		Path:       filepath.Join(root, "main.go"),
		Text:       "package main\n\nfunc main() {}\n",
		Line:       1,
		Column:     1,
	}
}

func TestStdioProviderDefinitionRoundTrip(t *testing.T) {
	provider, root := newFakeProvider(t, "ok")
	q := fakeQuery(root, OpGoToDefinition)
	result, err := provider.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Locations) != 2 {
		t.Fatalf("locations = %+v, want 2", result.Locations)
	}
	if got, want := result.Locations[0].URI, PathToURI(q.Path); got != want {
		t.Errorf("first location uri = %q, want %q", got, want)
	}
	if got, want := result.Locations[0].Range.Start, (Position{Line: 1, Character: 2}); got != want {
		t.Errorf("first location start = %+v, want %+v", got, want)
	}
	if !strings.Contains(result.Locations[1].URI, "sibling.go") {
		t.Errorf("second location uri = %q, want the sibling file", result.Locations[1].URI)
	}
}

func TestStdioProviderReferences(t *testing.T) {
	provider, root := newFakeProvider(t, "ok")
	result, err := provider.Query(context.Background(), fakeQuery(root, OpFindReferences))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Locations) != 3 {
		t.Fatalf("locations = %+v, want 3", result.Locations)
	}
	for i, loc := range result.Locations {
		if loc.Range.Start.Line != i || loc.Range.Start.Character != i {
			t.Errorf("location %d start = %+v, want line/character %d", i, loc.Range.Start, i)
		}
	}
}

func TestStdioProviderImplementationLink(t *testing.T) {
	provider, root := newFakeProvider(t, "ok")
	result, err := provider.Query(context.Background(), fakeQuery(root, OpGoToImplementation))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Locations) != 1 {
		t.Fatalf("locations = %+v, want 1 LocationLink converted to a location", result.Locations)
	}
	if got, want := result.Locations[0].Range.Start, (Position{Line: 3, Character: 0}); got != want {
		t.Errorf("implementation start = %+v, want %+v", got, want)
	}
}

func TestStdioProviderHover(t *testing.T) {
	provider, root := newFakeProvider(t, "ok")
	result, err := provider.Query(context.Background(), fakeQuery(root, OpHover))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !strings.Contains(result.Hover, "func Foo(x int) int") {
		t.Errorf("hover = %q, want the normalized markdown content", result.Hover)
	}
}

func TestStdioProviderLegacyHover(t *testing.T) {
	provider, root := newFakeProvider(t, "legacy_hover")
	result, err := provider.Query(context.Background(), fakeQuery(root, OpHover))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !strings.Contains(result.Hover, "first line") || !strings.Contains(result.Hover, "func Foo() int") {
		t.Errorf("hover = %q, want both MarkedString entries normalized", result.Hover)
	}
}

func TestStdioProviderUTF16Columns(t *testing.T) {
	// "😀" is one rune but two UTF-16 code units: the 1-based column 3 is the
	// end position, which the client must forward as LSP character 2.
	text := "😀\n中文target\n"
	provider, root := newFakeProvider(t, "ok",
		fakeLSPExpectLine+"=0", fakeLSPExpectChar+"=2", fakeLSPExpectText+"=😀", fakeLSPExpectLang+"=go")
	q := fakeQuery(root, OpGoToDefinition)
	q.Text = text
	q.Line = 1
	q.Column = 3
	if _, err := provider.Query(context.Background(), q); err != nil {
		t.Fatalf("emoji UTF-16 end position rejected or mis-forwarded: %v", err)
	}

	// The second line starts after a multi-byte Chinese run; column 3 is the
	// third UTF-16 code unit, character 2.
	q = fakeQuery(root, OpGoToDefinition)
	q.Text = text
	q.Line = 2
	q.Column = 3
	provider2, root2 := newFakeProvider(t, "ok", fakeLSPExpectLine+"=1", fakeLSPExpectChar+"=2")
	q.Path = filepath.Join(root2, "main.go")
	if _, err := provider2.Query(context.Background(), q); err != nil {
		t.Fatalf("Chinese UTF-16 position rejected or mis-forwarded: %v", err)
	}
}

func TestStdioProviderPositionOutOfRange(t *testing.T) {
	provider, root := newFakeProvider(t, "ok")
	q := fakeQuery(root, OpGoToDefinition)
	q.Line = 99
	if _, err := provider.Query(context.Background(), q); err == nil || !strings.Contains(err.Error(), "outside the file") {
		t.Fatalf("out-of-range line error = %v, want an outside-the-file error", err)
	}
	q = fakeQuery(root, OpGoToDefinition)
	q.Column = 500
	if _, err := provider.Query(context.Background(), q); err == nil || !strings.Contains(err.Error(), "outside line 1") {
		t.Fatalf("out-of-range column error = %v, want an outside-the-line error", err)
	}
}

func TestStdioProviderNoServerConfigured(t *testing.T) {
	provider := NewStdioProvider(ProviderConfig{Servers: map[string]ServerConfig{}})
	_, err := provider.Query(context.Background(), Query{
		Operation: OpGoToDefinition, LanguageID: "go",
		Path: "main.go", Text: "", Line: 1, Column: 1,
	})
	if err == nil {
		t.Fatal("Query without a configured server returned no error")
	}
	var noServer *NoServerError
	if !errors.As(err, &noServer) {
		t.Fatalf("error = %v (%T), want *NoServerError", err, err)
	}
	if !strings.Contains(err.Error(), "lsp.servers") {
		t.Errorf("error = %q, want a hint to configure lsp.servers", err)
	}
}

func TestStdioProviderUnsupportedCapability(t *testing.T) {
	provider, root := newFakeProvider(t, "nocap")
	_, err := provider.Query(context.Background(), fakeQuery(root, OpGoToDefinition))
	if err == nil || !strings.Contains(err.Error(), "does not support goToDefinition") {
		t.Fatalf("definition on a server without definitionProvider: err = %v", err)
	}
	var unsupported *UnsupportedError
	if !errors.As(err, &unsupported) {
		t.Errorf("error = %v (%T), want *UnsupportedError", err, err)
	}
	// Hover is still advertised, so the same server answers it.
	if _, err := provider.Query(context.Background(), fakeQuery(root, OpHover)); err != nil {
		t.Fatalf("hover on the same server: %v", err)
	}
}

func TestStdioProviderRPCError(t *testing.T) {
	provider, root := newFakeProvider(t, "rpc_error")
	_, err := provider.Query(context.Background(), fakeQuery(root, OpGoToDefinition))
	if err == nil || !strings.Contains(err.Error(), "internal explosion") {
		t.Fatalf("server error = %v, want it to carry the server's message", err)
	}
}

func TestStdioProviderServerCrashes(t *testing.T) {
	t.Run("before initialize", func(t *testing.T) {
		provider, root := newFakeProvider(t, "crash")
		_, err := provider.Query(context.Background(), fakeQuery(root, OpGoToDefinition))
		if err == nil || !strings.Contains(err.Error(), "exited") {
			t.Fatalf("error = %v, want a crashed-server error", err)
		}
	})
	t.Run("during request", func(t *testing.T) {
		provider, root := newFakeProvider(t, "crash_mid")
		_, err := provider.Query(context.Background(), fakeQuery(root, OpGoToDefinition))
		if err == nil || !strings.Contains(err.Error(), "exited") {
			t.Fatalf("error = %v, want a crashed-server error", err)
		}
	})
}

func TestStdioProviderTimeout(t *testing.T) {
	root := t.TempDir()
	provider := NewStdioProvider(ProviderConfig{
		Servers: map[string]ServerConfig{"go": fakeServerConfig(t, "hang")},
		Root:    root,
		Timeout: 300 * time.Millisecond,
	})
	start := time.Now()
	_, err := provider.Query(context.Background(), fakeQuery(root, OpGoToDefinition))
	if err == nil {
		t.Fatal("hanging server returned no error")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("Query took %s, want it to be bounded by the timeout", elapsed)
	}
	var timeoutErr *TimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("error = %v (%T), want *TimeoutError", err, err)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want it to mention the timeout", err)
	}
}

func TestStdioProviderAnswersServerRequests(t *testing.T) {
	provider, root := newFakeProvider(t, "server_request")
	result, err := provider.Query(context.Background(), fakeQuery(root, OpGoToDefinition))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Locations) != 2 {
		t.Fatalf("locations = %+v, want 2", result.Locations)
	}
}

func TestStdioProviderNullResult(t *testing.T) {
	provider, root := newFakeProvider(t, "null_result")
	result, err := provider.Query(context.Background(), fakeQuery(root, OpGoToDefinition))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Locations) != 0 {
		t.Fatalf("locations = %+v, want none for a null result", result.Locations)
	}
}

func TestStdioProviderCanceledContext(t *testing.T) {
	provider, root := newFakeProvider(t, "ok")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err := provider.Query(ctx, fakeQuery(root, OpGoToDefinition))
	if err == nil {
		t.Fatal("Query with a canceled context returned no error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Query with a canceled context took %s", elapsed)
	}
}

// TestStdioProviderIsolatesServerFailures proves the failure isolation the
// stdio provider promises: a crashing (or hanging) server for one language
// cannot affect a query for another language, because every query owns its own
// child process.
func TestStdioProviderIsolatesServerFailures(t *testing.T) {
	root := t.TempDir()
	provider := NewStdioProvider(ProviderConfig{
		Servers: map[string]ServerConfig{
			"go":     fakeServerConfig(t, "crash"),
			"python": fakeServerConfig(t, "ok"),
		},
		Root: root,
	})
	goQuery := fakeQuery(root, OpGoToDefinition)
	if _, err := provider.Query(context.Background(), goQuery); err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("crashing go server error = %v, want a crashed-server error", err)
	}
	pythonQuery := fakeQuery(root, OpGoToDefinition)
	pythonQuery.LanguageID = "python"
	pythonQuery.Path = filepath.Join(root, "main.py")
	result, err := provider.Query(context.Background(), pythonQuery)
	if err != nil {
		t.Fatalf("the crashing go server broke the python query: %v", err)
	}
	if len(result.Locations) != 2 {
		t.Fatalf("python locations = %+v, want 2", result.Locations)
	}
}
