package settings_test

// TDD tests (RED first) for the multi-server LSP settings page: list, active
// server selection, add/remove, legacy single-server compatibility and i18n.

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// storedLSPServers parses the stored lsp.servers row.
func storedLSPServers(t *testing.T, store *fakeSettings) []contracts.LSPServerConfig {
	t.Helper()
	raw := store.get(contracts.SettingLSPServers)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	servers, err := contracts.ParseLSPServerConfigList(raw)
	if err != nil {
		t.Fatalf("stored lsp.servers %q is invalid: %v", raw, err)
	}
	return servers
}

func lspPage(t *testing.T, store *fakeSettings) (*harness, *ui.Tester) {
	t.Helper()
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "LSP")
	return h, tester
}

// --- tests ------------------------------------------------------------------

func TestLSPPageEmptyState(t *testing.T) {
	store := newFakeSettings(nil)
	h, tester := lspPage(t, store)
	h.assertTexts(tester, "LSP", "还没有配置语言服务器", "新增服务器",
		"名称 输入", "命令 输入", "添加", "重启生效")
	if raw := store.get(contracts.SettingLSPServers); raw != "" {
		t.Fatalf("empty page wrote lsp.servers: %q", raw)
	}
}

func TestLSPPageListsServersAndActiveBadge(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingLSPServers: `[{"name":"go","command":"gopls","args":["serve"]},{"name":"rs","command":"rust-analyzer"}]`,
		contracts.SettingLSPActive:  "rs",
	})
	h, tester := lspPage(t, store)
	h.assertTexts(tester, "go", "gopls", "rs", "rust-analyzer", "活动中", "设为活动", "移除")
	if got := strings.Count(strings.Join(tester.Texts(), "\n"), "活动中"); got != 1 {
		t.Fatalf("active badges = %d, want 1; texts = %v", got, tester.Texts())
	}
}

func TestLSPPageAddServer(t *testing.T) {
	store := newFakeSettings(nil)
	h, tester := lspPage(t, store)
	h.assertTexts(tester, "还没有配置语言服务器")

	clearAndType(t, h, tester, "名称 输入", "go")
	clearAndType(t, h, tester, "命令 输入", "gopls")
	clearAndType(t, h, tester, "参数 输入", "serve")
	clearAndType(t, h, tester, "工作目录 输入", "/tmp/project")
	clearAndType(t, h, tester, "语言 ID 输入", "go")
	h.click(tester, "添加")

	servers := storedLSPServers(t, store)
	if len(servers) != 1 || servers[0].Name != "go" || servers[0].Command != "gopls" ||
		len(servers[0].Args) != 1 || servers[0].Args[0] != "serve" ||
		servers[0].Dir != "/tmp/project" || servers[0].LanguageID != "go" {
		t.Fatalf("stored servers = %+v", servers)
	}
	if got := store.get(contracts.SettingLSPServer); got != "" {
		t.Fatalf("adding a server did not clear the legacy row: %q", got)
	}
	h.assertTexts(tester, "go", "gopls", "活动中", "已添加（重启后生效）")
}

func TestLSPPageRemoveServerClearsLegacy(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingLSPServers: `[{"name":"go","command":"gopls"},{"name":"rs","command":"rust-analyzer"}]`,
		contracts.SettingLSPActive:  "go",
		contracts.SettingLSPServer:  `{"command":"legacy"}`,
	})
	h, tester := lspPage(t, store)
	h.click(tester, "移除") // opens the confirmation for the first server (go)
	h.click(tester, "移除服务器")

	servers := storedLSPServers(t, store)
	if len(servers) != 1 || servers[0].Name != "rs" {
		t.Fatalf("servers after remove = %+v", servers)
	}
	if got := store.get(contracts.SettingLSPServer); got != "" {
		t.Fatalf("remove did not clear the legacy row: %q", got)
	}
	h.assertTexts(tester, "rs", "已移除（重启后生效）")
	h.assertNoTexts(tester, "gopls")
}

func TestLSPPageSetActiveServer(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingLSPServers: `[{"name":"go","command":"gopls"},{"name":"rs","command":"rust-analyzer"}]`,
		contracts.SettingLSPActive:  "go",
	})
	h, tester := lspPage(t, store)
	h.click(tester, "设为活动")
	if got := store.get(contracts.SettingLSPActive); got != "rs" {
		t.Fatalf("lsp.active = %q, want rs", got)
	}
	h.assertTexts(tester, "已设为活动（重启后生效）")
	if got := strings.Count(strings.Join(tester.Texts(), "\n"), "活动中"); got != 1 {
		t.Fatalf("active badges = %d, want 1", got)
	}
}

func TestLSPPageLegacySingleServerCompatibility(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingLSPServer: `{"command":"gopls","args":["serve"]}`,
	})
	h, tester := lspPage(t, store)
	h.assertTexts(tester, "default", "gopls", "活动中")

	// The first write to the multi-server list migrates the legacy row and
	// clears it (single source of truth); there is no separate save action.
	clearAndType(t, h, tester, "名称 输入", "go")
	clearAndType(t, h, tester, "命令 输入", "gopls")
	h.click(tester, "添加")
	servers := storedLSPServers(t, store)
	if len(servers) != 2 || servers[1].Name != "go" || servers[1].Command != "gopls" {
		t.Fatalf("migrated servers = %+v", servers)
	}
	if got := store.get(contracts.SettingLSPServer); got != "" {
		t.Fatalf("write kept the legacy row: %q", got)
	}
	h.assertTexts(tester, "已添加（重启后生效）")
}

func TestLSPPageBadServersJSON(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingLSPServers: `{bad-json`,
	})
	h, tester := lspPage(t, store)
	h.assertTexts(tester, "配置无法解析", "名称 输入")
}

func TestLSPPageValidation(t *testing.T) {
	store := newFakeSettings(nil)
	h, tester := lspPage(t, store)
	h.click(tester, "添加")
	h.assertTexts(tester, "名称和命令不能为空")
	if servers := storedLSPServers(t, store); len(servers) != 0 {
		t.Fatalf("empty form wrote servers: %+v", servers)
	}

	clearAndType(t, h, tester, "名称 输入", "go")
	clearAndType(t, h, tester, "命令 输入", "gopls")
	h.click(tester, "添加")
	clearAndType(t, h, tester, "名称 输入", "GO")
	clearAndType(t, h, tester, "命令 输入", "gopls2")
	h.click(tester, "添加")
	h.assertTexts(tester, "该名称已存在")
}

func TestLSPPageEnglishCopy(t *testing.T) {
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "LSP")
	h.assertTexts(tester, "LSP", "No language servers configured", "Add server",
		"Name input", "Command input", "Add", "Restart to apply")

	clearAndType(t, h, tester, "Name input", "go")
	clearAndType(t, h, tester, "Command input", "gopls")
	h.click(tester, "Add")
	servers := storedLSPServers(t, store)
	if len(servers) != 1 || servers[0].Name != "go" {
		t.Fatalf("English add stored %+v", servers)
	}
	h.assertTexts(tester, "Added (applies after restart)", "Active")
}

// --- workspace LSP config opt-in --------------------------------------------

func TestLSPWorkspaceConfigToggleDefaultOff(t *testing.T) {
	store := newFakeSettings(nil)
	h, tester := lspPage(t, store)
	h.assertTexts(tester, "工作区配置", "读取工作区 LSP 配置（.zeokeel/lsp.json）",
		"默认关闭", "风险")
	if raw := store.get(contracts.SettingLSPWorkspaceConfig); raw != "" {
		t.Fatalf("defaults wrote lsp.workspace_config: %q", raw)
	}
}

func TestLSPWorkspaceConfigToggleOnOff(t *testing.T) {
	store := newFakeSettings(nil)
	h, tester := lspPage(t, store)
	h.click(tester, "读取工作区 LSP 配置（.zeokeel/lsp.json）")
	if raw := store.get(contracts.SettingLSPWorkspaceConfig); raw != "allow" {
		t.Fatalf("enabling wrote %q, want allow", raw)
	}
	h.click(tester, "读取工作区 LSP 配置（.zeokeel/lsp.json）")
	if raw := store.get(contracts.SettingLSPWorkspaceConfig); raw != "" {
		t.Fatalf("disabling wrote %q, want empty", raw)
	}
}

func TestLSPWorkspaceConfigBadValueShowsOff(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingLSPWorkspaceConfig: "yes",
	})
	h, tester := lspPage(t, store)
	// The malformed value displays as off, so one click enables the opt-in.
	h.click(tester, "读取工作区 LSP 配置（.zeokeel/lsp.json）")
	if raw := store.get(contracts.SettingLSPWorkspaceConfig); raw != "allow" {
		t.Fatalf("toggle from a malformed value wrote %q, want allow", raw)
	}
}

func TestLSPWorkspaceConfigEnglishCopy(t *testing.T) {
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "LSP")
	label := "Read workspace LSP config (.zeokeel/lsp.json)"
	h.assertTexts(tester, "Workspace configuration", label, "off by default", "Risk")
	h.click(tester, label)
	if raw := store.get(contracts.SettingLSPWorkspaceConfig); raw != "allow" {
		t.Fatalf("English toggle wrote %q, want allow", raw)
	}
}
