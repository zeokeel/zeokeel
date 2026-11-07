package toolkit

// Tests for merging user-configured MCP servers (settings mcp.servers) into the
// assembly's startup list.

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// captureLogger records toolkit warnings.
type captureLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *captureLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *captureLogger) contains(fragment string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, fragment) {
			return true
		}
	}
	return false
}

func (l *captureLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.lines)
}

func TestMergeMCPServers(t *testing.T) {
	assembly := []MCPServer{{Name: "assembly", URL: "http://127.0.0.1:1/asm"}}
	var warnings []string
	logf := func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }

	merged := mergeMCPServers(assembly, `[
		{"name":"user-one","url":"http://127.0.0.1:1/u1"},
		{"name":"user-two","url":"http://127.0.0.1:1/u2"}
	]`, logf)
	if len(merged) != 3 {
		t.Fatalf("merged = %+v, want assembly + two users", merged)
	}
	if merged[0].Name != "assembly" || merged[1].Name != "user-one" || merged[2].Name != "user-two" {
		t.Fatalf("merged order = %+v", merged)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}

	// Assembly wins a URL conflict; the user row is reported and skipped.
	warnings = nil
	merged = mergeMCPServers(assembly, `[{"name":"duplicate","url":"  http://127.0.0.1:1/asm  "}]`, logf)
	if len(merged) != 1 || merged[0].Name != "assembly" {
		t.Fatalf("duplicate merge = %+v, want the assembly entry", merged)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "duplicate") {
		t.Fatalf("duplicate warnings = %v", warnings)
	}

	// Invalid JSON and empty raw values fall back to the assembly list.
	warnings = nil
	merged = mergeMCPServers(assembly, `not-json`, logf)
	if len(merged) != 1 || len(warnings) != 1 || !strings.Contains(warnings[0], "parse") {
		t.Fatalf("bad JSON = %+v warnings=%v", merged, warnings)
	}
	if merged := mergeMCPServers(assembly, "   ", logf); len(merged) != 1 {
		t.Fatalf("blank setting merged = %+v", merged)
	}

	// Invalid rows are skipped with per-row warnings.
	warnings = nil
	merged = mergeMCPServers(assembly, `[
		{"name":"","url":"http://127.0.0.1:1/a"},
		{"name":"no-url","url":"   "},
		{"name":"bad\u0000name","url":"http://127.0.0.1:1/b"},
		{"name":"bad-url","url":"http://127.0.0.1:1/c\u0000"},
		null
	]`, logf)
	if len(merged) != 1 {
		t.Fatalf("invalid rows merged = %+v, want only assembly", merged)
	}
	if len(warnings) != 5 {
		t.Fatalf("invalid-row warnings = %v, want 5", warnings)
	}

	// Duplicate URLs inside the user list keep the first occurrence only.
	warnings = nil
	merged = mergeMCPServers(nil, `[
		{"name":"first","url":"http://127.0.0.1:1/app"},
		{"name":"second","url":"http://127.0.0.1:1/app"}
	]`, logf)
	if len(merged) != 1 || merged[0].Name != "first" {
		t.Fatalf("user dedupe = %+v", merged)
	}
	// The original assembly slice is never mutated.
	if len(assembly) != 1 || assembly[0].Name != "assembly" {
		t.Fatalf("assembly mutated: %+v", assembly)
	}
}

// activateMCPToolkit loads the toolkit with optional settings and logger and
// returns the plugin read model.
func activateMCPToolkit(t *testing.T, config map[string]any, settings contracts.SettingsService, logger contracts.Logger) (*rigtool.Registry, contracts.PluginsService) {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	if settings != nil {
		if _, err := root.Provide(contracts.KeySettings, settings); err != nil {
			t.Fatalf("provide settings: %v", err)
		}
	}
	if logger != nil {
		if _, err := root.Provide(contracts.KeyLogger, logger); err != nil {
			t.Fatalf("provide logger: %v", err)
		}
	}
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "toolkit", Module: "zeokeel/toolkit", Config: config,
	}})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	tools, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	plugins, err := root.Require(contracts.KeyPlugins)
	if err != nil {
		t.Fatalf("require plugins: %v", err)
	}
	return tools, plugins
}

// mcpURLs returns the configured URLs in plugin order.
func mcpURLs(plugins contracts.PluginsService) []string {
	out := make([]string, 0)
	for _, plugin := range plugins.MCP() {
		out = append(out, plugin.URL)
	}
	return out
}

func TestToolkitMergesSettingsMCPServers(t *testing.T) {
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[
			{"name":"user-one","url":"http://127.0.0.1:1/u1"},
			{"name":"user-two","url":"http://127.0.0.1:1/u2"}
		]`,
	})
	_, plugins := activateMCPToolkit(t, map[string]any{
		"root": ".",
		"mcpServers": []any{
			map[string]any{"name": "assembly", "url": "http://127.0.0.1:1/asm"},
		},
	}, settings, nil)
	urls := mcpURLs(plugins)
	if len(urls) != 3 {
		t.Fatalf("MCP entries = %v, want assembly + two users", urls)
	}
	for index, want := range []string{
		"http://127.0.0.1:1/asm", "http://127.0.0.1:1/u1", "http://127.0.0.1:1/u2",
	} {
		if urls[index] != want {
			t.Fatalf("MCP entry %d = %q, want %q", index, urls[index], want)
		}
	}
}

func TestToolkitSettingsMCPDuplicatesAndErrors(t *testing.T) {
	assembly := map[string]any{
		"root": ".",
		"mcpServers": []any{
			map[string]any{"name": "assembly", "url": "http://127.0.0.1:1/asm"},
		},
	}
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[
			{"name":"duplicate","url":"http://127.0.0.1:1/asm"},
			{"name":"","url":"http://127.0.0.1:1/empty-name"},
			{"name":"empty-url","url":""}
		]`,
	})
	logger := &captureLogger{}
	_, plugins := activateMCPToolkit(t, assembly, settings, logger)
	if urls := mcpURLs(plugins); len(urls) != 1 || urls[0] != "http://127.0.0.1:1/asm" {
		t.Fatalf("MCP entries = %v, want only assembly", urls)
	}
	if !logger.contains("duplicate") || !logger.contains("required") {
		t.Fatalf("warnings = %v", logger.lines)
	}
}

func TestToolkitBadMCPJSONDoesNotFailActivation(t *testing.T) {
	settings := newMutableSettings(map[string]string{contracts.SettingMCPServers: `{"not":"an array"}`})
	logger := &captureLogger{}
	_, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, logger)
	if got := len(plugins.MCP()); got != 0 {
		t.Fatalf("MCP entries = %d, want none", got)
	}
	if !logger.contains("parse") {
		t.Fatalf("warnings = %v, want a parse warning", logger.lines)
	}
}

func TestToolkitWithoutSettingsKeepsAssemblyMCP(t *testing.T) {
	_, plugins := activateMCPToolkit(t, map[string]any{
		"root": ".",
		"mcpServers": []any{
			map[string]any{"name": "assembly", "url": "http://127.0.0.1:1/asm"},
		},
	}, nil, nil)
	urls := mcpURLs(plugins)
	if len(urls) != 1 || urls[0] != "http://127.0.0.1:1/asm" {
		t.Fatalf("MCP entries = %v, want the assembly server", urls)
	}
}

// TestToolkitSettingsMCPValidJSONButNotArray covers valid JSON of the wrong
// shape, which must behave like a parse warning rather than an activation error.
func TestToolkitSettingsMCPValidJSONButNotArray(t *testing.T) {
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: mustJSON(t, map[string]any{"servers": []any{}}),
	})
	logger := &captureLogger{}
	_, plugins := activateMCPToolkit(t, map[string]any{"root": "."}, settings, logger)
	if got := len(plugins.MCP()); got != 0 {
		t.Fatalf("MCP entries = %d, want none", got)
	}
	if !logger.contains("parse") {
		t.Fatalf("warnings = %v, want a parse warning", logger.lines)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(encoded)
}

// TestMergeMCPServersURLCaseInsensitive covers AUDIT-P3-1: URLs differing only
// by case are the same endpoint for deduplication; the assembly entry still
// wins and the first user occurrence is kept.
func TestMergeMCPServersURLCaseInsensitive(t *testing.T) {
	var warnings []string
	logf := func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }

	assembly := []MCPServer{{Name: "assembly", URL: "http://127.0.0.1:1/shared"}}
	merged := mergeMCPServers(assembly, `[{"name":"cased","url":"HTTP://127.0.0.1:1/SHARED"}]`, logf)
	if len(merged) != 1 || merged[0].Name != "assembly" {
		t.Fatalf("case-different assembly conflict merged = %+v, want the assembly entry", merged)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "duplicate") {
		t.Fatalf("warnings = %v, want one duplicate warning", warnings)
	}

	warnings = nil
	merged = mergeMCPServers(nil, `[
		{"name":"first","url":"HTTP://Host.Example/A/Path"},
		{"name":"second","url":"http://host.example/a/path"}
	]`, logf)
	if len(merged) != 1 || merged[0].Name != "first" {
		t.Fatalf("case-different user dedupe merged = %+v, want the first occurrence", merged)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "duplicate") {
		t.Fatalf("warnings = %v, want one duplicate warning", warnings)
	}
}
