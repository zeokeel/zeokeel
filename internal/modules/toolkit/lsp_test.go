package toolkit

// TDD tests (RED first) for the optional single LSP server: contracts parsing,
// settings-driven startup, tool registration with name-conflict skipping and
// ownership cleanup on module unload.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/tools/lsp"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// errFakeLSPStart is the scripted start failure.
var errFakeLSPStart = errors.New("lsp: fake start failure")

// lspSettings is a map-backed settings store with one LSP row.
type lspSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func newLSPSettings(raw string) *lspSettings {
	values := map[string]string{}
	if raw != "" {
		values[contracts.SettingLSPServer] = raw
	}
	return &lspSettings{values: values}
}

func (s *lspSettings) Get(key, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *lspSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *lspSettings) All() map[string]string { return nil }

// fakeLSPSession is the lspSession seam for tests.
type fakeLSPSession struct {
	tools    []rig.Tool
	uriFn    func(string) (string, error)
	closed   atomic.Bool
	closeErr error
}

func (s *fakeLSPSession) Tools(fn func(string) (string, error)) []rig.Tool {
	s.uriFn = fn
	return s.tools
}

func (s *fakeLSPSession) Close() error {
	s.closed.Store(true)
	return s.closeErr
}

// fakeLSPTool is one minimal rig.Tool contributed by the fake session.
type fakeLSPTool struct{ name string }

func (t fakeLSPTool) Name() string           { return t.name }
func (t fakeLSPTool) Description() string    { return "fake LSP tool" }
func (t fakeLSPTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t fakeLSPTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{Content: "ok"}, nil
}

func withLSPStart(t *testing.T, start func(context.Context, lsp.Config) (lspSession, error)) {
	t.Helper()
	previous := lspStartFunc
	lspStartFunc = start
	t.Cleanup(func() { lspStartFunc = previous })
}

// loadToolkit activates the toolkit with the given settings store.
func loadToolkit(t *testing.T, settings contracts.SettingsService, withSettings bool) (cordis.Context, *module.Loader, *captureLogger) {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	logger := &captureLogger{}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); err != nil {
		t.Fatalf("provide logger: %v", err)
	}
	if withSettings {
		if _, err := root.Provide(contracts.KeySettings, settings); err != nil {
			t.Fatalf("provide settings: %v", err)
		}
	}
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register module: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "toolkit", Module: "zeokeel/toolkit", Config: map[string]any{"root": "."},
	}})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	return root, loader, logger
}

func lspToolNames(t *testing.T, root cordis.Context) map[string]bool {
	t.Helper()
	registry, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	names := map[string]bool{}
	for _, spec := range registry.Specs() {
		names[spec.Name] = true
	}
	return names
}

// --- contracts parsing ------------------------------------------------------

func TestParseLSPServerConfigTable(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
		check   func(t *testing.T, cfg contracts.LSPServerConfig)
	}{
		{name: "empty", raw: "", check: func(t *testing.T, cfg contracts.LSPServerConfig) {
			if cfg.Command != "" || cfg.Name != "" || cfg.Dir != "" ||
				cfg.RootURI != "" || cfg.LanguageID != "" || len(cfg.Args) != 0 || len(cfg.Env) != 0 {
				t.Fatalf("empty config = %+v, want zero", cfg)
			}
		}},
		{name: "whitespace", raw: "   ", check: func(t *testing.T, cfg contracts.LSPServerConfig) {
			if cfg.Command != "" || len(cfg.Args) != 0 {
				t.Fatalf("whitespace config = %+v, want zero", cfg)
			}
		}},
		{name: "full", raw: `{"name":" go ","command":" gopls ","args":[" serve "," ","-rpc.trace"],"dir":" /tmp ","rootUri":" file:///tmp ","languageId":" go ","env":[" A=1 "," "]}`, check: func(t *testing.T, cfg contracts.LSPServerConfig) {
			want := contracts.LSPServerConfig{
				Name: "go", Command: "gopls", Args: []string{"serve", "-rpc.trace"},
				Dir: "/tmp", RootURI: "file:///tmp", LanguageID: "go", Env: []string{"A=1"},
			}
			if cfg.Command != want.Command || cfg.Name != want.Name || cfg.Dir != want.Dir ||
				cfg.RootURI != want.RootURI || cfg.LanguageID != want.LanguageID ||
				len(cfg.Args) != 2 || cfg.Args[0] != "serve" || cfg.Args[1] != "-rpc.trace" ||
				len(cfg.Env) != 1 || cfg.Env[0] != "A=1" {
				t.Fatalf("parsed config = %+v, want %+v", cfg, want)
			}
		}},
		{name: "missing_command", raw: `{"args":["serve"]}`, wantErr: true},
		{name: "blank_command", raw: `{"command":"   "}`, wantErr: true},
		{name: "bad_json", raw: `{not-json`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := contracts.ParseLSPServerConfig(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) = nil error, want error", tc.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.raw, err)
			}
			tc.check(t, cfg)
		})
	}
}

func TestEncodeLSPServerConfigRoundTrip(t *testing.T) {
	cfg := contracts.LSPServerConfig{Command: " gopls ", Args: []string{" serve ", ""}, LanguageID: " go "}
	encoded, err := contracts.EncodeLSPServerConfig(cfg)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	parsed, err := contracts.ParseLSPServerConfig(encoded)
	if err != nil {
		t.Fatalf("Parse encoded: %v", err)
	}
	if parsed.Command != "gopls" || parsed.LanguageID != "go" ||
		len(parsed.Args) != 1 || parsed.Args[0] != "serve" {
		t.Fatalf("round trip = %+v", parsed)
	}
}

// --- toolkit activation -----------------------------------------------------

func TestToolkitStartsLSPAndRegistersTools(t *testing.T) {
	session := &fakeLSPSession{tools: []rig.Tool{
		fakeLSPTool{name: "lsp_hover"},
		fakeLSPTool{name: "read_file"}, // conflicts with the builtin tool
	}}
	var captured lsp.Config
	withLSPStart(t, func(_ context.Context, cfg lsp.Config) (lspSession, error) {
		captured = cfg
		return session, nil
	})
	settings := newLSPSettings(`{"command":" gopls ","args":["serve"],"dir":" /tmp/project ","languageId":"go"}`)
	root, loader, logger := loadToolkit(t, settings, true)
	defer func() { _ = loader.Close() }()

	if captured.Command != "gopls" || captured.Dir != "/tmp/project" ||
		captured.LanguageID != "go" || len(captured.Args) != 1 || captured.Args[0] != "serve" {
		t.Fatalf("started LSP config = %+v", captured)
	}
	names := lspToolNames(t, root)
	if !names["lsp_hover"] {
		t.Fatalf("registry lacks lsp_hover: %v", names)
	}
	if !names["read_file"] {
		t.Fatal("registry lost the builtin read_file")
	}
	if !logger.contains("read_file") {
		t.Fatalf("conflict warning = %v", logger.lines)
	}
	if session.uriFn == nil {
		t.Fatal("LSP tools were registered without a file path to URI helper")
	}
	uri, err := session.uriFn("/tmp/project/main.go")
	if err != nil || uri != "file:///tmp/project/main.go" {
		t.Fatalf("filePathToURI = %q, %v", uri, err)
	}
}

func TestToolkitLSPStartFailureDoesNotBreakActivation(t *testing.T) {
	withLSPStart(t, func(context.Context, lsp.Config) (lspSession, error) {
		return nil, errFakeLSPStart
	})
	settings := newLSPSettings(`{"command":"missing-binary"}`)
	root, loader, logger := loadToolkit(t, settings, true)
	defer func() { _ = loader.Close() }()

	if names := lspToolNames(t, root); names["lsp_hover"] {
		t.Fatalf("failed server still registered LSP tools: %v", names)
	}
	if !logger.contains("missing-binary") && !logger.contains("lsp") {
		t.Fatalf("start failure did not log a warning: %v", logger.lines)
	}
}

func TestToolkitLSPDisabledWithoutSettings(t *testing.T) {
	called := atomic.Bool{}
	withLSPStart(t, func(context.Context, lsp.Config) (lspSession, error) {
		called.Store(true)
		return &fakeLSPSession{}, nil
	})
	root, loader, _ := loadToolkit(t, nil, false)
	defer func() { _ = loader.Close() }()
	if called.Load() {
		t.Fatal("LSP started without a settings store")
	}
	if names := lspToolNames(t, root); names["lsp_hover"] {
		t.Fatalf("LSP tools registered without settings: %v", names)
	}
}

func TestToolkitLSPEmptyOrMalformedSettings(t *testing.T) {
	called := atomic.Bool{}
	withLSPStart(t, func(context.Context, lsp.Config) (lspSession, error) {
		called.Store(true)
		return &fakeLSPSession{}, nil
	})
	for _, raw := range []string{"", "   ", "{not-json", `{"command":"   "}`} {
		root, loader, logger := loadToolkit(t, newLSPSettings(raw), true)
		if called.Load() {
			t.Fatalf("LSP started for raw %q", raw)
		}
		if names := lspToolNames(t, root); names["lsp_hover"] {
			t.Fatalf("LSP tools registered for raw %q: %v", raw, names)
		}
		if raw == "{not-json" || raw == `{"command":"   "}` {
			if !logger.contains("lsp") {
				t.Fatalf("raw %q did not log a warning: %v", raw, logger.lines)
			}
		}
		_ = loader.Close()
	}
}

func TestToolkitLSPClosesOnUnload(t *testing.T) {
	session := &fakeLSPSession{tools: []rig.Tool{fakeLSPTool{name: "lsp_hover"}}}
	withLSPStart(t, func(context.Context, lsp.Config) (lspSession, error) { return session, nil })
	_, loader, _ := loadToolkit(t, newLSPSettings(`{"command":"gopls"}`), true)
	if err := loader.Close(); err != nil {
		t.Fatalf("close loader: %v", err)
	}
	if !session.closed.Load() {
		t.Fatal("LSP session was not closed on unload")
	}
}
