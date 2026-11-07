package settings_test

// Tests for the settings MCP page: the stored server list, add/remove with the
// JSON write-back, validation, malformed configuration and the English copy.

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

const (
	mcpFilesJSON = `[{"name":"files","url":"http://127.0.0.1:9000/mcp"}]`
	mcpTwoJSON   = `[{"name":"files","url":"http://127.0.0.1:9000/mcp"},{"name":"web","url":"https://mcp.example.com/mcp"}]`
)

func openMCPPage(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	h.click(tester, "MCP")
}

func typeMCPForm(t *testing.T, h *harness, tester *ui.Tester, name, url string) {
	t.Helper()
	clearAndType(t, h, tester, "名称 输入", name)
	clearAndType(t, h, tester, "URL 输入", url)
}

// --- tests ------------------------------------------------------------------

func TestMCPPageEmptyState(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	h.assertTexts(tester, "MCP", "通过 Streamable HTTP 连接 MCP 服务器；修改后重启生效。",
		"MCP 服务器", "还没有配置 MCP 服务器", "添加服务器", "名称", "URL", "添加")
}

func TestMCPPageShowsAndRemovesServers(t *testing.T) {
	store := newFakeSettings(map[string]string{contracts.SettingMCPServers: mcpTwoJSON})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	h.assertTexts(tester, "files", "http://127.0.0.1:9000/mcp",
		"web", "https://mcp.example.com/mcp", "移除")

	// Removing the first row writes the remaining server back as JSON.
	h.click(tester, "移除")
	want := `[{"name":"web","url":"https://mcp.example.com/mcp"}]`
	if got := store.get(contracts.SettingMCPServers); got != want {
		t.Fatalf("stored MCP servers = %s, want %s", got, want)
	}
	h.assertTexts(tester, "已保存", "web")
	h.assertNoTexts(tester, "files")
}

func TestMCPPageAddsServersInOrder(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	typeMCPForm(t, h, tester, "files", "https://mcp.example.com/mcp")
	h.click(tester, "添加")
	h.assertTexts(tester, "已保存")
	wantOne := `[{"name":"files","url":"https://mcp.example.com/mcp"}]`
	if got := store.get(contracts.SettingMCPServers); got != wantOne {
		t.Fatalf("stored MCP servers = %s, want %s", got, wantOne)
	}

	// The form is cleared after a successful add; a second add appends.
	typeMCPForm(t, h, tester, "web", "http://127.0.0.1:9000/mcp")
	h.click(tester, "添加")
	wantTwo := `[{"name":"files","url":"https://mcp.example.com/mcp"},{"name":"web","url":"http://127.0.0.1:9000/mcp"}]`
	if got := store.get(contracts.SettingMCPServers); got != wantTwo {
		t.Fatalf("stored MCP servers = %s, want %s", got, wantTwo)
	}
}

func TestMCPPageValidationDoesNotWrite(t *testing.T) {
	cases := []struct {
		name     string
		stored   string
		draft    [2]string // name, url
		wantText string
	}{
		{name: "empty name", draft: [2]string{"", "https://mcp.example.com/mcp"}, wantText: "名称不能为空"},
		{name: "empty url", draft: [2]string{"files", ""}, wantText: "URL 不能为空"},
		{name: "invalid url", draft: [2]string{"files", "ftp://mcp.example.com"}, wantText: "URL 无效：请输入 http(s) URL"},
		{name: "duplicate url", stored: mcpFilesJSON, draft: [2]string{"other", "http://127.0.0.1:9000/mcp"}, wantText: "该 URL 已存在"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values := map[string]string{}
			if tc.stored != "" {
				values[contracts.SettingMCPServers] = tc.stored
			}
			store := newFakeSettings(values)
			h := newHarness(t, &fakeChat{}, store, settings.Config{})
			tester := h.tester()
			tester.Frame()
			openMCPPage(t, h, tester)
			before := store.get(contracts.SettingMCPServers)

			typeMCPForm(t, h, tester, tc.draft[0], tc.draft[1])
			h.click(tester, "添加")
			h.assertTexts(tester, tc.wantText)
			if got := store.get(contracts.SettingMCPServers); got != before {
				t.Fatalf("invalid input wrote the store: %q → %q", before, got)
			}
		})
	}
}

func TestMCPPageBadJSON(t *testing.T) {
	raw := `{"not":"an-array"}`
	store := newFakeSettings(map[string]string{contracts.SettingMCPServers: raw})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	h.assertTexts(tester, "配置解析失败：", "还没有配置 MCP 服务器")
	if got := store.get(contracts.SettingMCPServers); got != raw {
		t.Fatalf("malformed configuration was rewritten: %q", got)
	}
}

func TestMCPPageSaveError(t *testing.T) {
	store := newFakeSettings(nil)
	store.setErr = errors.New("disk is read-only")
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	openMCPPage(t, h, tester)

	typeMCPForm(t, h, tester, "files", "https://mcp.example.com/mcp")
	h.click(tester, "添加")
	h.assertTexts(tester, "保存失败：disk is read-only")
	h.assertNoTexts(tester, "已保存")
	if got := store.get(contracts.SettingMCPServers); got != "" {
		t.Fatalf("failed save wrote the store: %q", got)
	}
}

func TestMCPPageEnglishCopy(t *testing.T) {
	store := newFakeSettings(map[string]string{contracts.SettingMCPServers: mcpFilesJSON})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()

	h.click(tester, "MCP")
	h.assertTexts(tester,
		"Connect MCP servers over Streamable HTTP; changes apply after a restart.",
		"MCP servers", "Add server", "Name", "URL", "Add", "Remove")
	h.assertNoTexts(tester, "添加服务器", "移除")
}
