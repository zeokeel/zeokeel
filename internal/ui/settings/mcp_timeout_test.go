package settings_test

// task-11 / MCP: the settings form carries the optional per-server timeout
// (contracts.MCPServerConfig.TimeoutMs). The field is optional, persists through
// the stored JSON, and editing a row must not silently drop it.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// TestMCPFormSavesTimeout covers the add path: a millisecond timeout enters the
// form, reaches the stored JSON and survives a re-edit.
func TestMCPFormSavesTimeout(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	typeMCPForm(t, h, tester, "slow", "https://mcp.example.com/mcp")
	clearAndType(t, h, tester, "超时 (ms) 输入", "250")
	h.click(tester, "添加")
	h.assertTexts(tester, "已保存")

	stored := store.get(contracts.SettingMCPServers)
	if !strings.Contains(stored, `"timeoutMs":250`) {
		t.Fatalf("stored MCP servers = %s, want a 250 ms timeout field", stored)
	}

	// Editing the row pre-fills the timeout and saving without touching it keeps
	// the value (the field must not be cleared by an edit round-trip).
	h.click(tester, "服务器操作")
	h.click(tester, "编辑")
	timeoutField, ok := tester.AccessByRole(ui.RoleTextField, "超时 (ms) 输入")
	if !ok || timeoutField.Value != "250" {
		t.Fatalf("edit form timeout = %q (found %v), want 250", timeoutField.Value, ok)
	}
	clearAndType(t, h, tester, "名称 输入", "slow-renamed")
	h.click(tester, "保存")
	h.assertTexts(tester, "已保存")
	stored = store.get(contracts.SettingMCPServers)
	if !strings.Contains(stored, `"name":"slow-renamed"`) || !strings.Contains(stored, `"timeoutMs":250`) {
		t.Fatalf("stored MCP servers after edit = %s, want the renamed row with the timeout kept", stored)
	}
}

// TestMCPFormRejectsInvalidTimeout covers validation: a non-numeric timeout is
// a field error and nothing is written.
func TestMCPFormRejectsInvalidTimeout(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	typeMCPForm(t, h, tester, "bad", "https://mcp.example.com/mcp")
	clearAndType(t, h, tester, "超时 (ms) 输入", "soon")
	h.click(tester, "添加")
	h.assertTexts(tester, "超时")
	if stored := store.get(contracts.SettingMCPServers); stored != "" {
		t.Fatalf("invalid timeout still saved MCP servers = %s", stored)
	}
}

// TestMCPFormBlankTimeoutKeepsLegacyShape covers the optional field: left blank
// the stored row keeps the legacy shape (no timeoutMs key, default 30s).
func TestMCPFormBlankTimeoutKeepsLegacyShape(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	typeMCPForm(t, h, tester, "plain", "https://mcp.example.com/mcp")
	h.click(tester, "添加")
	stored := store.get(contracts.SettingMCPServers)
	if strings.Contains(stored, "timeoutMs") {
		t.Fatalf("blank timeout wrote a timeout field: %s", stored)
	}
}
