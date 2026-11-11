package toolkit

// TDD tests (RED first) for the optional single LSP server: contracts parsing,
// settings-driven startup, tool registration with name-conflict skipping and
// ownership cleanup on module unload.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/sandbox"
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
	tools       []rig.Tool
	uriFn       func(string) (string, error)
	closed      atomic.Bool
	closeErr    error
	initCalls   atomic.Int32
	initErr     error
	initialized atomic.Bool
}

// Initialize implements lspSession.
func (s *fakeLSPSession) Initialize(context.Context) error {
	s.initCalls.Add(1)
	if s.initErr != nil {
		return s.initErr
	}
	s.initialized.Store(true)
	return nil
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

func withLSPStart(t *testing.T, start func(context.Context, lsp.Config, *sandbox.Policy) (lspSession, error)) {
	t.Helper()
	previous := lspStartFunc
	lspStartFunc = start
	t.Cleanup(func() { lspStartFunc = previous })
}

// loadToolkit activates the toolkit with the given settings store.
func loadToolkit(t *testing.T, settings contracts.SettingsService, withSettings bool) (cordis.Context, *module.Loader, *captureLogger) {
	t.Helper()
	return loadToolkitFull(t, settings, withSettings, nil)
}

// loadToolkitFull activates the toolkit with optional settings and workspace
// services (nil keeps them absent).
func loadToolkitFull(t *testing.T, settings contracts.SettingsService, withSettings bool,
	workspace contracts.WorkspaceService) (cordis.Context, *module.Loader, *captureLogger) {
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
	if workspace != nil {
		if _, err := root.Provide(contracts.KeyWorkspace, workspace); err != nil {
			t.Fatalf("provide workspace: %v", err)
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
	withLSPStart(t, func(_ context.Context, cfg lsp.Config, _ *sandbox.Policy) (lspSession, error) {
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
	withLSPStart(t, func(context.Context, lsp.Config, *sandbox.Policy) (lspSession, error) {
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
	withLSPStart(t, func(context.Context, lsp.Config, *sandbox.Policy) (lspSession, error) {
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
	withLSPStart(t, func(context.Context, lsp.Config, *sandbox.Policy) (lspSession, error) {
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
	withLSPStart(t, func(context.Context, lsp.Config, *sandbox.Policy) (lspSession, error) { return session, nil })
	_, loader, _ := loadToolkit(t, newLSPSettings(`{"command":"gopls"}`), true)
	if err := loader.Close(); err != nil {
		t.Fatalf("close loader: %v", err)
	}
	if !session.closed.Load() {
		t.Fatal("LSP session was not closed on unload")
	}
}

// --- multi-server configuration ---------------------------------------------

// newLSPMultiSettings builds a settings store with explicit LSP rows.
func newLSPMultiSettings(values map[string]string) *lspSettings {
	if values == nil {
		values = map[string]string{}
	}
	return &lspSettings{values: values}
}

func TestParseLSPServerConfigListTable(t *testing.T) {
	if servers, err := contracts.ParseLSPServerConfigList(""); err != nil || len(servers) != 0 {
		t.Fatalf("empty list = %+v, %v", servers, err)
	}
	servers, err := contracts.ParseLSPServerConfigList(`[
		{"name":" go ","command":" gopls ","args":[" serve ",""],"dir":" /tmp "},
		{"name":"rs","command":"rust-analyzer","languageId":" rust "}
	]`)
	if err != nil {
		t.Fatalf("Parse list: %v", err)
	}
	if len(servers) != 2 || servers[0].Name != "go" || servers[0].Command != "gopls" ||
		len(servers[0].Args) != 1 || servers[0].Args[0] != "serve" ||
		servers[1].Name != "rs" || servers[1].LanguageID != "rust" {
		t.Fatalf("parsed list = %+v", servers)
	}

	for _, raw := range []string{
		`[{"command":"gopls"}]`,                                   // name missing
		`[{"name":"a","command":" "}]`,                            // command missing
		`[{"name":"a","command":"x"},{"name":"A","command":"y"}]`, // duplicate name
		`{bad-json`, // bad JSON
	} {
		if _, err := contracts.ParseLSPServerConfigList(raw); err == nil {
			t.Fatalf("Parse list(%q) succeeded, want error", raw)
		}
	}
}

func TestEncodeLSPServerConfigListRoundTrip(t *testing.T) {
	encoded, err := contracts.EncodeLSPServerConfigList([]contracts.LSPServerConfig{
		{Name: " go ", Command: " gopls ", Args: []string{" serve ", ""}},
	})
	if err != nil {
		t.Fatalf("Encode list: %v", err)
	}
	servers, err := contracts.ParseLSPServerConfigList(encoded)
	if err != nil || len(servers) != 1 || servers[0].Name != "go" || servers[0].Command != "gopls" ||
		len(servers[0].Args) != 1 || servers[0].Args[0] != "serve" {
		t.Fatalf("round trip = %+v, %v", servers, err)
	}
	if empty, err := contracts.EncodeLSPServerConfigList(nil); err != nil || empty != "[]" {
		t.Fatalf("Encode nil = %q, %v; want []", empty, err)
	}
}

func TestResolveLSPServerConfigsLegacyFallback(t *testing.T) {
	if servers, err := contracts.ResolveLSPServerConfigs("", ""); err != nil || len(servers) != 0 {
		t.Fatalf("empty resolve = %+v, %v", servers, err)
	}
	legacy, err := contracts.ResolveLSPServerConfigs("", `{"command":"gopls"}`)
	if err != nil || len(legacy) != 1 || legacy[0].Name != "default" || legacy[0].Command != "gopls" {
		t.Fatalf("legacy fallback = %+v, %v", legacy, err)
	}
	list, err := contracts.ResolveLSPServerConfigs(
		`[{"name":"go","command":"gopls"}]`, `{"command":"legacy"}`)
	if err != nil || len(list) != 1 || list[0].Name != "go" {
		t.Fatalf("list should win = %+v, %v", list, err)
	}
	if _, err := contracts.ResolveLSPServerConfigs("{bad", ""); err == nil {
		t.Fatal("bad servers list resolved without error")
	}
}

func TestActiveLSPServerSelection(t *testing.T) {
	servers := []contracts.LSPServerConfig{
		{Name: "go", Command: "gopls"},
		{Name: "rs", Command: "rust-analyzer"},
	}
	if got, ok := contracts.ActiveLSPServer(servers, ""); !ok || got.Name != "go" {
		t.Fatalf("empty active = %+v, %v", got, ok)
	}
	if got, ok := contracts.ActiveLSPServer(servers, "rs"); !ok || got.Name != "rs" {
		t.Fatalf("explicit active = %+v, %v", got, ok)
	}
	if got, ok := contracts.ActiveLSPServer(servers, "missing"); !ok || got.Name != "go" {
		t.Fatalf("unknown active = %+v, %v", got, ok)
	}
}

func TestToolkitStartsOnlyActiveServer(t *testing.T) {
	session := &fakeLSPSession{tools: []rig.Tool{fakeLSPTool{name: "lsp_hover"}}}
	var captured lsp.Config
	withLSPStart(t, func(_ context.Context, cfg lsp.Config, _ *sandbox.Policy) (lspSession, error) {
		captured = cfg
		return session, nil
	})
	settings := newLSPMultiSettings(map[string]string{
		contracts.SettingLSPServers: `[{"name":"go","command":"gopls"},{"name":"rs","command":"rust-analyzer","args":["serve"]}]`,
		contracts.SettingLSPActive:  "rs",
	})
	root, loader, logger := loadToolkit(t, settings, true)
	defer func() { _ = loader.Close() }()

	if captured.Command != "rust-analyzer" || len(captured.Args) != 1 || captured.Args[0] != "serve" {
		t.Fatalf("started LSP config = %+v, want the active rs server", captured)
	}
	if !lspToolNames(t, root)["lsp_hover"] {
		t.Fatalf("active server tools were not registered: %v", lspToolNames(t, root))
	}
	if !logger.contains("rust-analyzer") || !logger.contains("2 configured") {
		t.Fatalf("activation log = %v", logger.lines)
	}
}

func TestToolkitLSPActiveMissingUsesFirstServer(t *testing.T) {
	var captured lsp.Config
	withLSPStart(t, func(_ context.Context, cfg lsp.Config, _ *sandbox.Policy) (lspSession, error) {
		captured = cfg
		return &fakeLSPSession{tools: []rig.Tool{fakeLSPTool{name: "lsp_hover"}}}, nil
	})
	settings := newLSPMultiSettings(map[string]string{
		contracts.SettingLSPServers: `[{"name":"go","command":"gopls"},{"name":"rs","command":"rust-analyzer"}]`,
		contracts.SettingLSPActive:  "unknown",
	})
	_, loader, _ := loadToolkit(t, settings, true)
	defer func() { _ = loader.Close() }()
	if captured.Command != "gopls" {
		t.Fatalf("unknown active started %q, want the first server", captured.Command)
	}
}

func TestToolkitLSPLegacySingleServerStillStarts(t *testing.T) {
	var captured lsp.Config
	withLSPStart(t, func(_ context.Context, cfg lsp.Config, _ *sandbox.Policy) (lspSession, error) {
		captured = cfg
		return &fakeLSPSession{tools: []rig.Tool{fakeLSPTool{name: "lsp_hover"}}}, nil
	})
	settings := newLSPMultiSettings(map[string]string{
		contracts.SettingLSPServer: `{"command":"gopls"}`,
	})
	_, loader, logger := loadToolkit(t, settings, true)
	defer func() { _ = loader.Close() }()
	if captured.Command != "gopls" {
		t.Fatalf("legacy server started %q, want gopls", captured.Command)
	}
	if !logger.contains("default") {
		t.Fatalf("legacy fallback log = %v", logger.lines)
	}
}

func TestToolkitLSPBadServerListSkips(t *testing.T) {
	called := atomic.Bool{}
	withLSPStart(t, func(context.Context, lsp.Config, *sandbox.Policy) (lspSession, error) {
		called.Store(true)
		return &fakeLSPSession{}, nil
	})
	settings := newLSPMultiSettings(map[string]string{
		contracts.SettingLSPServers: "{bad-json",
		contracts.SettingLSPServer:  `{"command":"gopls"}`,
	})
	root, loader, logger := loadToolkit(t, settings, true)
	defer func() { _ = loader.Close() }()
	if called.Load() {
		t.Fatal("bad servers list still started a server")
	}
	if names := lspToolNames(t, root); names["lsp_hover"] {
		t.Fatalf("bad servers list registered tools: %v", names)
	}
	if !logger.contains("lsp") {
		t.Fatalf("bad servers list did not warn: %v", logger.lines)
	}
}

// --- workspace LSP config opt-in --------------------------------------------

// fakeLSPWorkspace implements contracts.WorkspaceService with one current root.
type fakeLSPWorkspace struct {
	current    contracts.Workspace
	has        bool
	currentErr error
}

func (w *fakeLSPWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	return nil, nil
}

func (w *fakeLSPWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	return w.current, w.has, w.currentErr
}

func (w *fakeLSPWorkspace) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}

func (w *fakeLSPWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}

func (w *fakeLSPWorkspace) Delete(context.Context, string) error { return nil }

func (w *fakeLSPWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (w *fakeLSPWorkspace) ReadFile(context.Context, string, int) ([]byte, error) {
	return nil, errFakeLSPStart
}

func (w *fakeLSPWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

// writeWorkspaceLSPConfig writes <root>/.zeokeel/lsp.json and returns the root.
func writeWorkspaceLSPConfig(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".zeokeel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir workspace config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lsp.json"), []byte(content), 0o600); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	return root
}

func TestToolkitLSPWorkspaceNotOptedInDoesNotRead(t *testing.T) {
	root := writeWorkspaceLSPConfig(t, `{bad-json`)
	withLSPStart(t, func(context.Context, lsp.Config, *sandbox.Policy) (lspSession, error) {
		return &fakeLSPSession{tools: []rig.Tool{fakeLSPTool{name: "lsp_hover"}}}, nil
	})
	settings := newLSPMultiSettings(map[string]string{})
	_, loader, logger := loadToolkitFull(t, settings, true, &fakeLSPWorkspace{
		current: contracts.Workspace{ID: "w1", Root: root}, has: true,
	})
	defer func() { _ = loader.Close() }()
	if logger.contains("workspace") {
		t.Fatalf("not opted-in activation touched the workspace config: %v", logger.lines)
	}
}

func TestToolkitLSPWorkspaceOptInUsesWorkspaceServers(t *testing.T) {
	root := writeWorkspaceLSPConfig(t,
		`{"servers":[{"name":"ws","command":"ws-lsp","args":["--stdio"]}]}`)
	var captured lsp.Config
	withLSPStart(t, func(_ context.Context, cfg lsp.Config, _ *sandbox.Policy) (lspSession, error) {
		captured = cfg
		return &fakeLSPSession{tools: []rig.Tool{fakeLSPTool{name: "lsp_hover"}}}, nil
	})
	settings := newLSPMultiSettings(map[string]string{
		contracts.SettingLSPWorkspaceConfig: "allow",
	})
	_, loader, logger := loadToolkitFull(t, settings, true, &fakeLSPWorkspace{
		current: contracts.Workspace{ID: "w1", Root: root}, has: true,
	})
	defer func() { _ = loader.Close() }()
	if captured.Command != "ws-lsp" || len(captured.Args) != 1 || captured.Args[0] != "--stdio" {
		t.Fatalf("started config = %+v, want the workspace server", captured)
	}
	if !logger.contains("workspace") {
		t.Fatalf("activation log does not mention the workspace source: %v", logger.lines)
	}
}

func TestToolkitLSPWorkspaceSettingsOverrideSameName(t *testing.T) {
	root := writeWorkspaceLSPConfig(t,
		`[{"name":"GO","command":"workspace-gopls"}]`)
	var captured lsp.Config
	withLSPStart(t, func(_ context.Context, cfg lsp.Config, _ *sandbox.Policy) (lspSession, error) {
		captured = cfg
		return &fakeLSPSession{tools: []rig.Tool{fakeLSPTool{name: "lsp_hover"}}}, nil
	})
	settings := newLSPMultiSettings(map[string]string{
		contracts.SettingLSPServers:         `[{"name":"go","command":"gopls"}]`,
		contracts.SettingLSPActive:          "go",
		contracts.SettingLSPWorkspaceConfig: "allow",
	})
	_, loader, logger := loadToolkitFull(t, settings, true, &fakeLSPWorkspace{
		current: contracts.Workspace{ID: "w1", Root: root}, has: true,
	})
	defer func() { _ = loader.Close() }()
	if captured.Command != "gopls" {
		t.Fatalf("settings did not win the name conflict: %+v", captured)
	}
	if !logger.contains("1 configured") {
		t.Fatalf("merged count log = %v", logger.lines)
	}
}

func TestToolkitLSPWorkspaceOnlyServerCanBeActive(t *testing.T) {
	root := writeWorkspaceLSPConfig(t,
		`{"servers":[{"name":"ws","command":"ws-lsp"}]}`)
	var captured lsp.Config
	withLSPStart(t, func(_ context.Context, cfg lsp.Config, _ *sandbox.Policy) (lspSession, error) {
		captured = cfg
		return &fakeLSPSession{}, nil
	})
	settings := newLSPMultiSettings(map[string]string{
		contracts.SettingLSPServers:         `[{"name":"go","command":"gopls"}]`,
		contracts.SettingLSPActive:          "ws",
		contracts.SettingLSPWorkspaceConfig: "allow",
	})
	_, loader, _ := loadToolkitFull(t, settings, true, &fakeLSPWorkspace{
		current: contracts.Workspace{ID: "w1", Root: root}, has: true,
	})
	defer func() { _ = loader.Close() }()
	if captured.Command != "ws-lsp" {
		t.Fatalf("workspace-only active server started %q", captured.Command)
	}
}

func TestToolkitLSPWorkspaceBadJSONKeepsSettings(t *testing.T) {
	root := writeWorkspaceLSPConfig(t, `{bad-json`)
	var captured lsp.Config
	withLSPStart(t, func(_ context.Context, cfg lsp.Config, _ *sandbox.Policy) (lspSession, error) {
		captured = cfg
		return &fakeLSPSession{}, nil
	})
	settings := newLSPMultiSettings(map[string]string{
		contracts.SettingLSPServers:         `[{"name":"go","command":"gopls"}]`,
		contracts.SettingLSPWorkspaceConfig: "allow",
	})
	_, loader, logger := loadToolkitFull(t, settings, true, &fakeLSPWorkspace{
		current: contracts.Workspace{ID: "w1", Root: root}, has: true,
	})
	defer func() { _ = loader.Close() }()
	if captured.Command != "gopls" {
		t.Fatalf("bad workspace config broke settings activation: %+v", captured)
	}
	if !logger.contains("workspace") {
		t.Fatalf("bad workspace config did not warn: %v", logger.lines)
	}
}

func TestToolkitLSPWorkspaceMissingFileAndService(t *testing.T) {
	var captured lsp.Config
	withLSPStart(t, func(_ context.Context, cfg lsp.Config, _ *sandbox.Policy) (lspSession, error) {
		captured = cfg
		return &fakeLSPSession{}, nil
	})
	settings := newLSPMultiSettings(map[string]string{
		contracts.SettingLSPServers:         `[{"name":"go","command":"gopls"}]`,
		contracts.SettingLSPWorkspaceConfig: "allow",
	})
	emptyRoot := t.TempDir() // no .zeokeel/lsp.json
	_, loader, _ := loadToolkitFull(t, settings, true, &fakeLSPWorkspace{
		current: contracts.Workspace{ID: "w1", Root: emptyRoot}, has: true,
	})
	_ = loader.Close()
	if captured.Command != "gopls" {
		t.Fatalf("missing workspace file broke activation: %+v", captured)
	}

	// No workspace service at all is equally safe.
	_, loader2, _ := loadToolkitFull(t, settings, true, nil)
	defer func() { _ = loader2.Close() }()
	if captured.Command != "gopls" {
		t.Fatalf("missing workspace service broke activation: %+v", captured)
	}
}

func TestParseLSPWorkspaceConfigFileTable(t *testing.T) {
	object, err := contracts.ParseLSPWorkspaceConfigFile([]byte(
		`{"servers":[{"name":" go ","command":" gopls ","args":[" serve ",""]}]}`))
	if err != nil || len(object) != 1 || object[0].Name != "go" || object[0].Command != "gopls" ||
		len(object[0].Args) != 1 || object[0].Args[0] != "serve" {
		t.Fatalf("object form = %+v, %v", object, err)
	}
	array, err := contracts.ParseLSPWorkspaceConfigFile([]byte(`[{"name":"rs","command":"rust-analyzer"}]`))
	if err != nil || len(array) != 1 || array[0].Name != "rs" {
		t.Fatalf("array form = %+v, %v", array, err)
	}
	if empty, err := contracts.ParseLSPWorkspaceConfigFile(nil); err != nil || len(empty) != 0 {
		t.Fatalf("empty file = %+v, %v", empty, err)
	}
	if empty, err := contracts.ParseLSPWorkspaceConfigFile([]byte(`{"servers":[]}`)); err != nil || len(empty) != 0 {
		t.Fatalf("empty servers = %+v, %v", empty, err)
	}
	for _, raw := range []string{
		`{bad-json`,
		`{"servers":[{"command":"gopls"}]}`,
		`[{"name":"a","command":"x"},{"name":"A","command":"y"}]`,
	} {
		if _, err := contracts.ParseLSPWorkspaceConfigFile([]byte(raw)); err == nil {
			t.Fatalf("workspace config %q parsed without error", raw)
		}
	}
}
