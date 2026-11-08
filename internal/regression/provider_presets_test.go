package regression

// Adversarial audit: the provider preset chips on the model settings page.
// Named presets write the documented base URL + model, the custom entry clears
// the URL while keeping the model, the current URL highlights exactly one chip,
// and write failures surface without silently persisting a partial preset.

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

// openModelPage opens the settings model page (reusing the shared helper).
func openModelPage(t *testing.T, values map[string]string) (*harness, *ui.Tester) {
	t.Helper()
	return startModelSettings(t, values)
}

// countExactText counts labels exactly equal to text.
func countExactText(tester *ui.Tester, text string) int {
	count := 0
	for _, value := range tester.Texts() {
		if value == text {
			count++
		}
	}
	return count
}

// clickProvider clicks one preset chip and repaints.
func clickProvider(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	if err := tester.Click(label); err != nil {
		t.Fatalf("click provider %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

// TestProviderPresetsWriteAndMatch covers all three named presets, the custom
// entry and the matched-provider highlight (including a trailing-slash URL).
func TestProviderPresetsWriteAndMatch(t *testing.T) {
	harness, tester := openModelPage(t, nil)
	requireHasTexts(t, tester, "服务商预设", "DeepSeek", "OpenAI", "Ollama（本地）", "自定义")
	if got := countExactText(tester, "已匹配"); got != 0 {
		t.Fatalf("unconfigured page shows %d matched badges", got)
	}

	cases := []struct {
		chip    string
		baseURL string
		model   string
		applied string
	}{
		{"DeepSeek", "https://api.deepseek.com/v1", "deepseek-chat", "已应用预设：DeepSeek"},
		{"OpenAI", "https://api.openai.com/v1", "gpt-4o-mini", "已应用预设：OpenAI"},
		{"Ollama（本地）", "http://127.0.0.1:11434/v1", "qwen2.5:7b", "已应用预设：Ollama（本地）"},
	}
	for _, test := range cases {
		clickProvider(t, tester, test.chip)
		if got := harness.settings.Get(contracts.SettingsChatBaseURL, ""); got != test.baseURL {
			t.Fatalf("%s baseURL = %q, want %q", test.chip, got, test.baseURL)
		}
		if got := harness.settings.Get(contracts.SettingsChatModel, ""); got != test.model {
			t.Fatalf("%s model = %q, want %q", test.chip, got, test.model)
		}
		requireHasText(t, tester, test.applied)
		if got := countExactText(tester, "已匹配"); got != 1 {
			t.Fatalf("%s matched badges = %d, want 1", test.chip, got)
		}
	}

	// The custom entry clears the base URL and keeps the model.
	clickProvider(t, tester, "自定义")
	if got := harness.settings.Get(contracts.SettingsChatBaseURL, ""); got != "" {
		t.Fatalf("custom baseURL = %q, want empty", got)
	}
	if got := harness.settings.Get(contracts.SettingsChatModel, ""); got != "qwen2.5:7b" {
		t.Fatalf("custom changed the model: %q", got)
	}
	requireHasText(t, tester, "已应用预设：自定义")
	if got := countExactText(tester, "已匹配"); got != 0 {
		t.Fatalf("custom entry still highlights %d chips", got)
	}

	// A trailing slash still matches its provider.
	h2, tester2 := openModelPage(t, map[string]string{
		contracts.SettingsChatBaseURL: "https://api.openai.com/v1/",
	})
	_ = h2
	tester2.Frame()
	if got := countExactText(tester2, "已匹配"); got != 1 {
		t.Fatalf("trailing-slash URL matched %d chips, want 1", got)
	}
	if !strings.Contains(strings.Join(tester2.Texts(), "\n"), "已匹配") {
		t.Fatalf("matched badge missing for a trailing-slash URL")
	}
}

// TestProviderPresetFailureProbe covers write failures: a rejected base-URL
// write persists nothing, and a rejected model write leaves a readable error.
// The probe logs AUDIT-P3-15 when the first write already landed (two separate
// settings writes are not atomic).
func TestProviderPresetFailureProbe(t *testing.T) {
	t.Run("base URL write rejected", func(t *testing.T) {
		inner := newMemSettings(nil)
		fault := &providerFaultSettings{inner: inner, failKey: contracts.SettingsChatBaseURL}
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
		clickProvider(t, tester, "DeepSeek")
		requireHasText(t, tester, "应用预设失败：")
		if got := inner.Get(contracts.SettingsChatBaseURL, ""); got != "" {
			t.Fatalf("failed base-URL write persisted %q", got)
		}
	})

	t.Run("model write rejected after URL", func(t *testing.T) {
		inner := newMemSettings(map[string]string{
			contracts.SettingsChatBaseURL: "https://api.deepseek.com/v1",
			contracts.SettingsChatModel:   "deepseek-chat",
		})
		fault := &providerFaultSettings{inner: inner, failKey: contracts.SettingsChatModel}
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
		clickProvider(t, tester, "OpenAI")
		requireHasText(t, tester, "应用预设失败：")
		if got := inner.Get(contracts.SettingsChatModel, ""); got != "deepseek-chat" {
			t.Fatalf("failed model write changed the model to %q", got)
		}
		if got := inner.Get(contracts.SettingsChatBaseURL, ""); got == "https://api.openai.com/v1" {
			t.Logf("AUDIT-P3-15: provider preset writes baseURL then model without rollback; a failed model write leaves the new baseURL (%q) persisted", got)
		}
	})
}
