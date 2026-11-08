package plugins_test

// Tests for the MCP per-tool switches on the tool catalogue.

import (
	"errors"
	"sync"
	"testing"

	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/plugins"
)

// fakePluginSettings is the settings service the switches write through.
type fakePluginSettings struct {
	mu     sync.Mutex
	values map[string]string
	setErr error
	sets   []string
}

func newFakePluginSettings(values map[string]string) *fakePluginSettings {
	if values == nil {
		values = map[string]string{}
	}
	return &fakePluginSettings{values: values}
}

func (s *fakePluginSettings) Get(key, fallback string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *fakePluginSettings) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setErr != nil {
		return s.setErr
	}
	s.sets = append(s.sets, key)
	s.values[key] = value
	return nil
}

func (s *fakePluginSettings) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out
}

func (s *fakePluginSettings) get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[key]
}

// newMCPToolRegistry is a catalogue with two MCP tools plus one built-in.
func newMCPToolRegistry() *rigtools.Registry {
	return rigtools.NewRegistry(
		fakeTool{name: "mcp__srv__echo", desc: "Echo from MCP"},
		fakeTool{name: "mcp__srv__second", desc: "Second MCP tool"},
		fakeTool{name: "bash", desc: "Run shell commands"},
	)
}

func newHarnessWithSettings(t *testing.T, service contracts.PluginsService, toolkit *rigtools.Registry,
	cfg plugins.Config, settings contracts.SettingsService) *harness {
	t.Helper()
	h := newHarness(t, service, toolkit, cfg)
	provide(t, h.ctx, contracts.KeySettings, settings)
	return h
}

func TestMCPToolSwitchesRenderAndWrite(t *testing.T) {
	store := newFakePluginSettings(map[string]string{
		contracts.SettingMCPDisabledTools: `["mcp__srv__echo"]`,
	})
	service := &fakePlugins{mcp: []contracts.MCPPlugin{{Name: "srv", Status: "已连接", Tools: 2}}}
	h := newHarnessWithSettings(t, service, newMCPToolRegistry(), plugins.Config{}, store)
	tester := h.tester()
	h.assertTexts(tester, "mcp__srv__echo", "mcp__srv__second", "启用工具", "已禁用（重启生效）")

	// Built-in tools have no MCP switch: narrow to bash and check.
	h.click(tester, "搜索工具")
	tester.Type("bash")
	tester.Frame()
	h.assertTexts(tester, "bash")
	h.assertNoTexts(tester, "已禁用（重启生效）")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Frame()
	// Narrow to echo and flip the switch on: the disabled entry is removed.
	h.click(tester, "搜索工具")
	tester.Key(0, 0)
	tester.Type("echo")
	tester.Frame()
	h.click(tester, "启用工具 mcp__srv__echo")
	if got := store.get(contracts.SettingMCPDisabledTools); got != "[]" {
		t.Fatalf("disabled list after enable = %q, want []", got)
	}
	h.assertTexts(tester, "已保存")
	h.assertNoTexts(tester, "已禁用（重启生效）")

	// Flip it off again: the tool is added back to the setting.
	h.click(tester, "启用工具 mcp__srv__echo")
	if got := store.get(contracts.SettingMCPDisabledTools); got != `["mcp__srv__echo"]` {
		t.Fatalf("disabled list after disable = %q", got)
	}
	h.assertTexts(tester, "已禁用（重启生效）")
}

func TestMCPToolSwitchShowsFilteredEntryAndFailure(t *testing.T) {
	// The tool is already filtered out of the registry (it disappeared at
	// activation); the disabled setting still renders it so it can be re-enabled.
	store := newFakePluginSettings(map[string]string{
		contracts.SettingMCPDisabledTools: `["mcp__gone__tool"]`,
	})
	h := newHarnessWithSettings(t, nil, newMCPToolRegistry(), plugins.Config{}, store)
	tester := h.tester()
	h.assertTexts(tester, "mcp__gone__tool", "已禁用（重启生效）", "启用工具")
	h.click(tester, "搜索工具")
	tester.Type("gone")
	tester.Frame()
	h.click(tester, "启用工具 mcp__gone__tool")
	if got := store.get(contracts.SettingMCPDisabledTools); got != "[]" {
		t.Fatalf("disabled list after re-enable = %q, want []", got)
	}

	// A settings write failure shows the danger text and does not persist.
	failing := newFakePluginSettings(nil)
	failing.setErr = errPluginSettingsWrite
	h = newHarnessWithSettings(t, nil, newMCPToolRegistry(), plugins.Config{}, failing)
	tester = h.tester()
	h.assertTexts(tester, "启用工具")
	h.click(tester, "启用工具 mcp__srv__echo")
	h.assertTexts(tester, "保存失败")
	if got := failing.get(contracts.SettingMCPDisabledTools); got != "" {
		t.Fatalf("failed write persisted %q", got)
	}
}

func TestMCPToolSwitchesEnglishAndBadConfig(t *testing.T) {
	store := newFakePluginSettings(map[string]string{
		contracts.SettingMCPDisabledTools: "not-json",
	})
	h := newHarnessWithSettings(t, nil, newMCPToolRegistry(), plugins.Config{}, store)
	h.uiRoot.Locale().SetLanguage("en")
	tester := h.tester()
	h.assertTexts(tester, "Enable tool", "Invalid disabled-tools configuration")
	h.assertNoTexts(tester, "已禁用（重启生效）")
}

var errPluginSettingsWrite = errors.New("settings write failed")
