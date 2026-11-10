package regression

// Adversarial audit: the common-provider presets now live inside the add-model
// dialog (the old "quick presets" row that only filled a page-level connection
// form is gone). Choosing a preset prefills the model's own base URL and example
// model id; every model, including its provider connection, lands in the model
// list; the first model becomes the default model and its connection is mirrored
// into the legacy chat.* rows transactionally — a rejected write rolls the
// already-written rows back instead of leaving a partial configuration
// (AUDIT-P3-15 discipline).

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	settingsui "github.com/zeokeel/zeokeel/internal/ui/settings"
)

// providerFaultSettings delegates reads but rejects writes for one key.
type providerFaultSettings struct {
	inner   contracts.SettingsService
	failKey string
	mu      sync.Mutex
}

func (s *providerFaultSettings) Get(key, fallback string) string { return s.inner.Get(key, fallback) }
func (s *providerFaultSettings) All() map[string]string          { return s.inner.All() }

func (s *providerFaultSettings) Set(key, value string) error {
	s.mu.Lock()
	fail := key == s.failKey
	s.mu.Unlock()
	if fail {
		return errors.New("write denied by the audit fault")
	}
	return s.inner.Set(key, value)
}

// startFaultModelSettings opens the model page over a fault-injecting wrapper
// whose accepted writes land in inner (also the harness's read store).
func startFaultModelSettings(t *testing.T, inner *memSettings, failKey string) (*harness, *ui.Tester) {
	t.Helper()
	fault := &providerFaultSettings{inner: inner, failKey: failKey}
	h := newHarnessCoreWith(t, t.TempDir(), true, inner, fault)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(newFakeChat()))
	activate(t, h.ctx, settingsui.Module(), settingsui.Config{})
	h.ui.Navigator().ToggleSettings()
	tester := h.tester(1280, 800)
	tester.Frame()
	if err := tester.Click("模型"); err != nil {
		t.Fatalf("open model page: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	return h, tester
}

// chooseCatalogProvider opens the add dialog's provider selector and picks one
// option by its display label.
func chooseCatalogProvider(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	revealSettings(t, tester, "服务商菜单")
	if err := tester.Click("服务商菜单"); err != nil {
		t.Fatalf("open provider selector: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if err := tester.Click(label); err != nil {
		t.Fatalf("choose provider %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

// addDialogModel stores the model shown in the add dialog.
func addDialogModel(t *testing.T, tester *ui.Tester) {
	t.Helper()
	if err := tester.Click("添加"); err != nil {
		t.Fatalf("add model: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
}

// storedModelList parses the persisted model list.
func storedModelList(t *testing.T, settings contracts.SettingsService) []contracts.ModelInfo {
	t.Helper()
	models, err := contracts.ParseModelInfoList(settings.Get(contracts.SettingModelCatalogModels, ""))
	if err != nil {
		t.Fatalf("parse stored model list: %v", err)
	}
	return models
}

// TestProviderPresetAddDialog covers the preset-prefilled add flow: the preset
// fills the model's base URL and example model id, the row lands in the model
// list and the first model is mirrored as the default.
func TestProviderPresetAddDialog(t *testing.T) {
	h, tester := startFaultModelSettings(t, newMemSettings(nil), "")
	openAddModelDialog(t, tester)

	chooseCatalogProvider(t, tester, "OpenAI")
	if got := modelInputValue(t, tester, "模型 ID 输入"); got != "gpt-4o-mini" {
		t.Fatalf("preset model ID = %q, want gpt-4o-mini", got)
	}
	if got := modelInputValue(t, tester, "Base URL 输入"); got != "https://api.openai.com/v1" {
		t.Fatalf("preset base URL = %q", got)
	}
	focusAndType(t, tester, "API Key 输入", "sk-openai")
	addDialogModel(t, tester)

	models := storedModelList(t, h.settings)
	if len(models) != 1 || models[0].ID != "gpt-4o-mini" ||
		models[0].BaseURL != "https://api.openai.com/v1" || models[0].APIKey != "sk-openai" {
		t.Fatalf("stored model list = %+v", models)
	}
	snapshot := tester.Texts()
	for _, want := range []string{"gpt-4o-mini", "OpenAI", "https://api.openai.com/v1", "密钥已设置", "默认"} {
		if !hasTextIn(snapshot, want) {
			t.Fatalf("model row does not show %q; texts = %v", want, snapshot)
		}
	}
	if got := h.settings.Get(contracts.SettingsChatModel, ""); got != "gpt-4o-mini" {
		t.Fatalf("default mirror model = %q", got)
	}
	if got := h.settings.Get(contracts.SettingsChatBaseURL, ""); got != "https://api.openai.com/v1" {
		t.Fatalf("default mirror base URL = %q", got)
	}
	if got := h.settings.Get(contracts.SettingsChatAPIKey, ""); got != "sk-openai" {
		t.Fatalf("default mirror API key = %q", got)
	}
	// The dialog closed: the add form is gone and the row is the page's.
	requireNoText(t, tester, "服务商菜单")
}

// TestProviderPresetCustomKeepsTypedEndpoint covers the custom entry: no preset
// URL is invented and the typed endpoint/key are stored on the model row.
func TestProviderPresetCustomKeepsTypedEndpoint(t *testing.T) {
	h, tester := startFaultModelSettings(t, newMemSettings(nil), "")
	openAddModelDialog(t, tester)
	focusAndType(t, tester, "模型 ID 输入", "local-llm")
	focusAndType(t, tester, "Base URL 输入", "http://127.0.0.1:8080/v1/")
	focusAndType(t, tester, "API Key 输入", "sk-local")
	addDialogModel(t, tester)

	models := storedModelList(t, h.settings)
	if len(models) != 1 || models[0].BaseURL != "http://127.0.0.1:8080/v1" {
		t.Fatalf("stored custom model = %+v", models)
	}
	if models[0].APIKey != "sk-local" {
		t.Fatalf("stored custom key = %q", models[0].APIKey)
	}
	// An unknown endpoint renders as the custom-service label, not a preset name.
	if !hasTextIn(tester.Texts(), "自定义服务") {
		t.Fatalf("custom row has no custom-service label; texts = %v", tester.Texts())
	}
}

// TestProviderPresetTrailingSlashMatch keeps the provider label tolerant of a
// stored URL with a trailing slash: the row still names its provider.
func TestProviderPresetTrailingSlashMatch(t *testing.T) {
	catalog := encodeModelFixture(t, contracts.ModelInfo{
		ID: "openai-mini", BaseURL: "https://api.openai.com/v1/", APIKey: "sk-openai",
	})
	_, tester := startFaultModelSettings(t, newMemSettings(map[string]string{
		contracts.SettingModelCatalogModels: catalog,
	}), "")
	if !hasTextIn(tester.Texts(), "OpenAI") {
		t.Fatalf("trailing-slash URL did not name its provider; texts = %v", tester.Texts())
	}
	revealSettings(t, tester, "编辑模型 openai-mini")
	if err := tester.Click("编辑模型 openai-mini"); err != nil {
		t.Fatalf("open editor: %v", err)
	}
	tester.Frame()
	// The editor's provider selector resolved the stored URL back to the preset.
	if !hasTextIn(tester.Texts(), "服务商菜单") {
		t.Fatalf("editor shows no provider field; texts = %v", tester.Texts())
	}
	if got := modelInputValue(t, tester, "Base URL 输入"); got != "https://api.openai.com/v1" {
		t.Fatalf("editor base URL = %q, want the trimmed stored value", got)
	}
}

// TestProviderPresetMirrorWriteFailureRollsBack covers the default-model mirror
// (AUDIT-P3-15): a rejected mirror write must not leave a partially written
// chat.* configuration and must not add the model to the list.
func TestProviderPresetMirrorWriteFailureRollsBack(t *testing.T) {
	t.Run("base URL rejected", func(t *testing.T) {
		inner := newMemSettings(nil)
		_, tester := startFaultModelSettings(t, inner, contracts.SettingsChatBaseURL)
		openAddModelDialog(t, tester)
		chooseCatalogProvider(t, tester, "DeepSeek")
		addDialogModel(t, tester)
		requireHasText(t, tester, "保存失败：")
		if got := inner.Get(contracts.SettingsChatModel, ""); got != "" {
			t.Fatalf("failed mirror left chat.model = %q", got)
		}
		if got := inner.Get(contracts.SettingsChatBaseURL, ""); got != "" {
			t.Fatalf("failed mirror left chat.baseURL = %q", got)
		}
		if got := inner.Get(contracts.SettingModelCatalogModels, ""); got != "" {
			t.Fatalf("failed mirror wrote the model list: %q", got)
		}
		// The dialog stays open so the user can retry.
		requireHasText(t, tester, "服务商菜单")
	})

	t.Run("model list rejected after mirror", func(t *testing.T) {
		inner := newMemSettings(nil)
		_, tester := startFaultModelSettings(t, inner, contracts.SettingModelCatalogModels)
		openAddModelDialog(t, tester)
		chooseCatalogProvider(t, tester, "Ollama（本地）")
		addDialogModel(t, tester)
		requireHasText(t, tester, "保存失败：")
		// The mirror was written first; the list failure must roll it back.
		for _, key := range []string{
			contracts.SettingsChatModel,
			contracts.SettingsChatBaseURL,
			contracts.SettingsChatAPIKey,
		} {
			if got := inner.Get(key, ""); got != "" {
				t.Fatalf("model-list failure left %s = %q", key, got)
			}
		}
		if got := inner.Get(contracts.SettingModelCatalogModels, ""); got != "" {
			t.Fatalf("model-list failure left the list: %q", got)
		}
	})
}

// hasTextIn reports whether any frame label contains want.
func hasTextIn(texts []string, want string) bool {
	for _, text := range texts {
		if strings.Contains(text, want) {
			return true
		}
	}
	return false
}
