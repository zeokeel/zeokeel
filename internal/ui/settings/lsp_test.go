package settings_test

// TDD tests (RED first) for the LSP settings page (single optional language
// server; saved JSON applies after a restart).

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// storeLSPConfig parses the stored LSP row.
func storeLSPConfig(t *testing.T, store *fakeSettings) (contracts.LSPServerConfig, bool, error) {
	t.Helper()
	raw := store.get(contracts.SettingLSPServer)
	cfg, err := contracts.ParseLSPServerConfig(raw)
	return cfg, raw != "", err
}

func TestLSPPageDefaults(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "LSP")
	h.assertTexts(tester, "LSP", "命令 输入", "参数 输入", "工作目录 输入", "语言 ID 输入",
		"保存", "当前未配置语言服务器")
	if raw := store.get(contracts.SettingLSPServer); raw != "" {
		t.Fatalf("defaults wrote the LSP row: %q", raw)
	}
}

func TestLSPPageSaveWritesJSON(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "LSP")

	clearAndType(t, h, tester, "命令 输入", "gopls")
	clearAndType(t, h, tester, "参数 输入", "serve -rpc.trace")
	clearAndType(t, h, tester, "工作目录 输入", "/tmp/project")
	clearAndType(t, h, tester, "语言 ID 输入", "go")
	h.click(tester, "保存")

	cfg, present, err := storeLSPConfig(t, store)
	if err != nil || !present {
		t.Fatalf("stored LSP config = %q, err %v", store.get(contracts.SettingLSPServer), err)
	}
	if cfg.Command != "gopls" || cfg.Dir != "/tmp/project" || cfg.LanguageID != "go" ||
		len(cfg.Args) != 2 || cfg.Args[0] != "serve" || cfg.Args[1] != "-rpc.trace" {
		t.Fatalf("stored config = %+v", cfg)
	}
	h.assertTexts(tester, "已保存")
}

func TestLSPPageClearDisablesServer(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingLSPServer: `{"command":"gopls","args":["serve"]}`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "LSP")
	h.assertTexts(tester, "gopls")

	clearAndType(t, h, tester, "命令 输入", "")
	h.click(tester, "保存")
	if raw := store.get(contracts.SettingLSPServer); raw != "" {
		t.Fatalf("clearing the command wrote %q, want empty", raw)
	}
	h.assertTexts(tester, "已关闭语言服务器")
}

func TestLSPPageMalformedCurrentValue(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingLSPServer: `{not-json`,
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "LSP")
	h.assertTexts(tester, "当前配置无法解析")
}

func TestLSPPageEnglishCopy(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.click(tester, "LSP")
	h.assertTexts(tester, "LSP", "Command input", "Arguments input", "Working directory input",
		"Language ID input", "Save", "No language server configured")
}
