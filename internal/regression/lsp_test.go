package regression

// Adversarial audit: the optional LSP integration. Three layers:
//   1. contracts.Parse/EncodeLSPServerConfig boundaries (bad JSON, missing/blank
//      command, args/env trimming and "no shell splitting", empty = disabled).
//   2. toolkit activation with a bad/nonexistent/empty lsp.server row: the module
//      stays Active, registers no lsp_* tools and never fails because of LSP.
//   3. module unload closes the started server exactly once (through the
//      package-level start seam, reached with a test-only //go:linkname).

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	_ "unsafe"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"
	"github.com/zeokeel/zeokeel-rig/tools/lsp"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/toolkit"
)

// lspSessionAlias mirrors toolkit.lspSession.
type lspSessionAlias interface {
	Tools(filePathToURI func(string) (string, error)) []rig.Tool
	Close() error
}

// toolkitLSPStartFunc is toolkit.lspStartFunc reached by linkname so the
// registration/close ownership path can be exercised without a real process.
//
//go:linkname toolkitLSPStartFunc github.com/zeokeel/zeokeel/internal/modules/toolkit.lspStartFunc
var toolkitLSPStartFunc func(ctx context.Context, cfg lsp.Config) (lspSessionAlias, error)

// lspFakeSession is the fake started server.
type lspFakeSession struct {
	tools    []rig.Tool
	closed   atomic.Int32
	closeErr error
}

func (s *lspFakeSession) Tools(func(string) (string, error)) []rig.Tool { return s.tools }
func (s *lspFakeSession) Close() error {
	s.closed.Add(1)
	return s.closeErr
}

// lspFakeTool is one tool contributed by the fake session.
type lspFakeTool struct{ name string }

func (t lspFakeTool) Name() string           { return t.name }
func (t lspFakeTool) Description() string    { return "fake LSP tool" }
func (t lspFakeTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t lspFakeTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{}, nil
}

// toolNames lists the registry's tool names.
func toolNames(registry *rigtools.Registry) []string {
	names := make([]string, 0)
	for _, tool := range registry.Tools() {
		if tool != nil {
			names = append(names, tool.Name())
		}
	}
	return names
}

// TestLSPConfigParseBoundaries covers ParseLSPServerConfig: empty rows disable
// LSP, malformed/wrongly-typed JSON is rejected, a blank or missing command is
// an error, and scalar/arg/env values are trimmed and blanks dropped.
func TestLSPConfigParseBoundaries(t *testing.T) {
	for _, raw := range []string{"", "   ", "\n\t "} {
		cfg, err := contracts.ParseLSPServerConfig(raw)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", raw, err)
		}
		if !reflect.DeepEqual(cfg, contracts.LSPServerConfig{}) {
			t.Fatalf("Parse(%q) = %+v, want the zero config", raw, cfg)
		}
	}

	cfg, err := contracts.ParseLSPServerConfig(`{
		"name": "  go tools  ",
		"command": "  gopls  ",
		"args": ["-remote=auto", "  ", "serve"],
		"dir": " /tmp/ws ",
		"rootUri": " file:///tmp/ws ",
		"languageId": " go ",
		"env": ["GOFLAGS=-mod=mod", "", "  "],
		"unknownField": 42
	}`)
	if err != nil {
		t.Fatalf("Parse(valid): %v", err)
	}
	want := contracts.LSPServerConfig{
		Name: "go tools", Command: "gopls", Args: []string{"-remote=auto", "serve"},
		Dir: "/tmp/ws", RootURI: "file:///tmp/ws", LanguageID: "go",
		Env: []string{"GOFLAGS=-mod=mod"},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("Parse(valid) = %+v, want %+v", cfg, want)
	}

	// A single arg keeps embedded spaces: args are passed verbatim, not split.
	cfg, err = contracts.ParseLSPServerConfig(`{"command":"x","args":["--flag=a b","two"]}`)
	if err != nil || !reflect.DeepEqual(cfg.Args, []string{"--flag=a b", "two"}) {
		t.Fatalf("args = %+v (err %v), want verbatim entries (no shell splitting)", cfg.Args, err)
	}

	reject := []string{
		"{",                             // bad JSON
		`{"args":["x"]}`,                // missing command
		`{"command":"   "}`,             // blank command
		`{"command":123}`,               // command must be a string
		`{"command":"x","args":"nope"}`, // args must be an array
		`{"command":"x","env":{}}`,      // env must be an array
		`[]`,                            // wrong document type
		`null`,                          // null document -> missing command
	}
	for _, raw := range reject {
		if cfg, err := contracts.ParseLSPServerConfig(raw); err == nil {
			t.Fatalf("Parse(%q) = %+v, want an error", raw, cfg)
		}
	}
}

// TestLSPConfigEncodeRoundTrip covers EncodeLSPServerConfig: values are
// normalized before encoding, Encode->Parse round-trips, an empty row encodes
// without validation (then Parse rejects it as disabled-with-command), and nil
// args/env are omitted.
func TestLSPConfigEncodeRoundTrip(t *testing.T) {
	original := contracts.LSPServerConfig{
		Name: "  ", Command: "  gopls  ", Args: []string{" serve ", " "},
		Dir: " /ws ", LanguageID: " go ", Env: []string{" A=1 ", ""},
	}
	encoded, err := contracts.EncodeLSPServerConfig(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	decoded, err := contracts.ParseLSPServerConfig(encoded)
	if err != nil {
		t.Fatalf("Parse(Encode): %v (encoded %q)", err, encoded)
	}
	want := contracts.LSPServerConfig{Command: "gopls", Args: []string{"serve"},
		Dir: "/ws", LanguageID: "go", Env: []string{"A=1"}}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("round trip = %+v, want %+v (encoded %q)", decoded, want, encoded)
	}
	if strings.Contains(encoded, `"args":[]`) || strings.Contains(encoded, `"env":[]`) {
		t.Fatalf("empty args/env were encoded instead of omitted: %q", encoded)
	}

	// Zero config encodes ("" command) without validation; Parse then rejects it.
	encoded, err = contracts.EncodeLSPServerConfig(contracts.LSPServerConfig{})
	if err != nil {
		t.Fatalf("Encode(zero): %v", err)
	}
	if _, err := contracts.ParseLSPServerConfig(encoded); err == nil {
		t.Fatalf("Parse(Encode(zero)) = %q succeeded, want the command-required error", encoded)
	}
}

// TestToolkitLSPBadConfigDoesNotBlockActivation covers the toolkit resilience:
// malformed, nonexistent-command and empty LSP rows all activate the module,
// register no lsp_* tools and keep the normal tools available.
func TestToolkitLSPBadConfigDoesNotBlockActivation(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		log  string
	}{
		{"malformed", `{"command":`, "toolkit: lsp: ignoring"},
		{"missing command", `{"args":["x"]}`, "toolkit: lsp: ignoring"},
		{"nonexistent command", `{"command":"zeokeel-audit-nonexistent-lsp-binary-xyz"}`, "toolkit: lsp: start"},
		{"empty disables", "", ""},
		{"whitespace disables", "   ", ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			h := newBareHarness(t)
			if test.raw != "" {
				if err := h.settings.Set(contracts.SettingLSPServer, test.raw); err != nil {
					t.Fatalf("set lsp.server: %v", err)
				}
			}
			activate(t, h.ctx, toolkit.Module(), toolkit.Config{})
			registry := requireService(t, h.ctx, contracts.KeyToolkit)
			names := toolNames(registry)
			for _, name := range names {
				if strings.HasPrefix(name, "lsp_") {
					t.Fatalf("LSP tool %q was registered for %s", name, test.raw)
				}
			}
			foundBuiltin := false
			for _, name := range names {
				if name == "read_file" || name == "read" {
					foundBuiltin = true
				}
			}
			if !foundBuiltin {
				t.Fatalf("normal tools are missing after an LSP problem: %v", names)
			}
			if test.log != "" && !h.logger.contains(test.log) {
				t.Fatalf("logger lacks %q; logs = %v", test.log, h.logger.lines)
			}
		})
	}
}

// TestToolkitLSPToolsCloseOnUnload covers the ownership path through the start
// seam: a started server contributes its lsp_* tools, a duplicate name is
// skipped, and module disposal closes the session exactly once.
func TestToolkitLSPToolsCloseOnUnload(t *testing.T) {
	previous := toolkitLSPStartFunc
	t.Cleanup(func() { toolkitLSPStartFunc = previous })

	h := newBareHarness(t)
	if err := h.settings.Set(contracts.SettingLSPServer, `{"command":"fake-lsp"}`); err != nil {
		t.Fatalf("set lsp.server: %v", err)
	}
	session := &lspFakeSession{tools: []rig.Tool{
		lspFakeTool{name: "lsp_hover"},
		lspFakeTool{name: "lsp_definition"},
		lspFakeTool{name: "read_file"}, // conflicts with the built-in tool
	}}
	var starts atomic.Int32
	toolkitLSPStartFunc = func(_ context.Context, cfg lsp.Config) (lspSessionAlias, error) {
		if cfg.Command != "fake-lsp" {
			t.Fatalf("lsp start command = %q, want fake-lsp", cfg.Command)
		}
		starts.Add(1)
		return session, nil
	}
	activate(t, h.ctx, toolkit.Module(), toolkit.Config{})
	registry := requireService(t, h.ctx, contracts.KeyToolkit)
	names := toolNames(registry)
	counts := map[string]int{}
	for _, name := range names {
		counts[name]++
	}
	if counts["lsp_hover"] != 1 || counts["lsp_definition"] != 1 {
		t.Fatalf("LSP tools not registered exactly once: %v", names)
	}
	if counts["read_file"] != 1 {
		t.Fatalf("conflicting read_file registered %d times, want 1 (skip the LSP copy)", counts["read_file"])
	}
	if !h.logger.contains("skipping") {
		t.Fatalf("duplicate LSP tool was not logged; logs = %v", h.logger.lines)
	}
	if starts.Load() != 1 {
		t.Fatalf("start seam calls = %d, want 1", starts.Load())
	}

	if err := h.ctx.DisposeAll(); err != nil {
		t.Fatalf("DisposeAll: %v", err)
	}
	if got := session.closed.Load(); got != 1 {
		t.Fatalf("LSP session Close calls = %d after unload, want 1", got)
	}
}
