package toolkit

// Tests for the dynamic web_search registration: settings-driven endpoint and
// key, missing/invalid configuration errors and next-call reconfiguration.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// mutableSettings is a concurrency-safe settings store tests can change between
// tool calls.
type mutableSettings struct {
	mu     sync.Mutex
	values map[string]string
}

func newMutableSettings(values map[string]string) *mutableSettings {
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return &mutableSettings{values: copied}
}

func (s *mutableSettings) Get(key, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *mutableSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = value
	return nil
}

func (s *mutableSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out
}

func (s *mutableSettings) set(key, value string) {
	_ = s.Set(key, value)
}

// activateWebToolkit loads the toolkit module with the given network switch and
// an optional settings service, returning the shared registry.
func activateWebToolkit(t *testing.T, network bool, settings contracts.SettingsService, provideSettings bool) *rigtool.Registry {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	if provideSettings {
		if _, err := root.Provide(contracts.KeySettings, settings); err != nil {
			t.Fatalf("provide settings: %v", err)
		}
	}
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register toolkit: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "toolkit", Module: "zeokeel/toolkit",
		Config: map[string]any{"root": ".", "network": network},
	}})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	tools, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	return tools
}

// toolNamed returns one registered tool or fails.
func toolNamed(t *testing.T, registry *rigtool.Registry, name string) rig.Tool {
	t.Helper()
	for _, tool := range registry.Tools() {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("registry lacks %q; have %v", name, toolNames(registry))
	return nil
}

// hasTool reports whether a tool name is registered.
func hasTool(registry *rigtool.Registry, name string) bool {
	for _, tool := range registry.Tools() {
		if tool.Name() == name {
			return true
		}
	}
	return false
}

func toolNames(registry *rigtool.Registry) []string {
	specs := registry.Specs()
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return names
}

// invokeSearch runs the web_search tool with a query.
func invokeSearch(t *testing.T, tool rig.Tool, query string) rig.ToolResult {
	t.Helper()
	arguments, err := json.Marshal(map[string]any{"query": query, "limit": 3})
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}
	result, err := tool.Invoke(context.Background(), rig.ToolCall{
		ID: "call-1", Name: tool.Name(), Arguments: string(arguments),
	})
	if err != nil {
		t.Fatalf("web_search invoke: %v", err)
	}
	return result
}

// searchBackend is a generic search endpoint returning one fixed hit.
func searchBackend(t *testing.T, title, snippet string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Query string `json:"query"`
			Count int    `json:"count"`
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("search backend: decode request: %v", err)
		}
		if r.Method != http.MethodPost || payload.Query == "" || payload.Count <= 0 {
			t.Errorf("search backend: unexpected request %s %s count=%d", r.Method, payload.Query, payload.Count)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []any{map[string]any{
				"title": title, "url": "https://example.com/" + title, "snippet": snippet,
			}},
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func TestWebSearchRegisteredAndInvokable(t *testing.T) {
	server := searchBackend(t, "hit", "a snippet")
	settings := newMutableSettings(map[string]string{
		contracts.SettingWebSearchEndpoint: server.URL,
		contracts.SettingWebSearchAPIKey:   "secret",
	})
	registry := activateWebToolkit(t, true, settings, true)
	if !hasTool(registry, "web_search") {
		t.Fatalf("network toolkit lacks web_search; have %v", toolNames(registry))
	}
	if !hasTool(registry, "web_fetch") {
		t.Fatalf("network toolkit lacks web_fetch; have %v", toolNames(registry))
	}
	result := invokeSearch(t, toolNamed(t, registry, "web_search"), "go modules")
	if result.IsError {
		t.Fatalf("web_search error result = %q", result.Content)
	}
	if !strings.Contains(result.Content, "hit") || !strings.Contains(result.Content, "https://example.com/hit") ||
		!strings.Contains(result.Content, "a snippet") {
		t.Fatalf("web_search content = %q, want title/url/snippet", result.Content)
	}
}

func TestWebSearchMissingEndpointIsReadable(t *testing.T) {
	settings := newMutableSettings(nil)
	registry := activateWebToolkit(t, true, settings, true)
	tool := toolNamed(t, registry, "web_search")
	result := invokeSearch(t, tool, "anything")
	if !result.IsError {
		t.Fatalf("web_search without endpoint = %q, want an error result", result.Content)
	}
	if !strings.Contains(result.Content, contracts.SettingWebSearchEndpoint) {
		t.Fatalf("error = %q, want the missing endpoint key", result.Content)
	}
}

func TestWebSearchInvalidEndpointIsReadable(t *testing.T) {
	settings := newMutableSettings(map[string]string{
		contracts.SettingWebSearchEndpoint: "ftp://example.com/search",
	})
	registry := activateWebToolkit(t, true, settings, true)
	result := invokeSearch(t, toolNamed(t, registry, "web_search"), "anything")
	if !result.IsError {
		t.Fatalf("web_search with invalid endpoint = %q, want an error result", result.Content)
	}
	if !strings.Contains(result.Content, "http or https") {
		t.Fatalf("error = %q, want a readable scheme error", result.Content)
	}
}

func TestWebSearchEndpointChangeAppliesToNextCall(t *testing.T) {
	first := searchBackend(t, "first", "first snippet")
	second := searchBackend(t, "second", "second snippet")
	settings := newMutableSettings(map[string]string{
		contracts.SettingWebSearchEndpoint: first.URL,
	})
	registry := activateWebToolkit(t, true, settings, true)
	tool := toolNamed(t, registry, "web_search")
	if result := invokeSearch(t, tool, "q"); result.IsError || !strings.Contains(result.Content, "first") {
		t.Fatalf("first call = %+v, want the first backend", result)
	}
	settings.set(contracts.SettingWebSearchEndpoint, second.URL)
	if result := invokeSearch(t, tool, "q"); result.IsError || !strings.Contains(result.Content, "second") {
		t.Fatalf("second call = %+v, want the reconfigured backend", result)
	}
	// The API key is read at call time too.
	settings.set(contracts.SettingWebSearchAPIKey, "new-key")
	if result := invokeSearch(t, tool, "q"); result.IsError {
		t.Fatalf("call after key change = %+v", result)
	}
}

func TestWebSearchNotRegisteredWithoutSettings(t *testing.T) {
	registry := activateWebToolkit(t, true, nil, false)
	if hasTool(registry, "web_search") {
		t.Fatal("web_search registered without a settings service")
	}
	if !hasTool(registry, "web_fetch") {
		t.Fatal("web_fetch missing when network is enabled")
	}
}

func TestWebToolsNotRegisteredWithoutNetwork(t *testing.T) {
	settings := newMutableSettings(map[string]string{
		contracts.SettingWebSearchEndpoint: "https://example.com/search",
	})
	registry := activateWebToolkit(t, false, settings, true)
	for _, name := range []string{"web_search", "web_fetch"} {
		if hasTool(registry, name) {
			t.Fatalf("%s registered with network disabled", name)
		}
	}
}
