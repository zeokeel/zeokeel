package regression

// Adversarial audit: the provider preset chips on the model settings page. A
// preset only fills the page's drafts (the page has one save semantic: the Save
// action), so nothing is persisted before Save; the custom entry clears the URL
// draft while keeping the model; the draft's provider URL highlights exactly one
// chip; and a rejected write surfaces as an inline error without persisting the
// partial form.

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

// clickScrolled reaches and clicks one label on the settings page: the page
// body (and navigation) scroll natively and the headless tester has no wheel,
// so bring the target into the visible dialog area first.
func clickScrolled(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	revealSettings(t, tester, label)
	if err := tester.Click(label); err != nil {
		t.Fatalf("click %q: %v; texts = %v", label, err, tester.Texts())
	}
	tester.Frame()
}

// TestProviderPresetsFillDraftsAndSave covers all three named presets, the
// custom entry and the matched-provider highlight (including a trailing-slash
// URL): the chips only fill the form, Save persists it.
func TestProviderPresetsFillDraftsAndSave(t *testing.T) {
	harness, tester := openModelPage(t, nil)
	requireHasTexts(t, tester, "服务商预设", "DeepSeek", "OpenAI", "Ollama（本地）", "自定义")
	if got := countExactText(tester, "已匹配"); got != 0 {
		t.Fatalf("unconfigured page shows %d matched badges", got)
	}

	cases := []struct {
		chip    string
		baseURL string
		model   string
		want    string
	}{
		{"DeepSeek", "https://api.deepseek.com/v1", "deepseek-chat", "已填入预设：DeepSeek"},
		{"OpenAI", "https://api.openai.com/v1", "gpt-4o-mini", "已填入预设：OpenAI"},
		{"Ollama（本地）", "http://127.0.0.1:11434/v1", "qwen2.5:7b", "已填入预设：Ollama（本地）"},
	}
	for _, test := range cases {
		revealSettings(t, tester, test.chip)
		before := harness.settings.Get(contracts.SettingsChatBaseURL, "")
		if err := tester.Click(test.chip); err != nil {
			t.Fatalf("click provider %q: %v; texts = %v", test.chip, err, tester.Texts())
		}
		tester.Frame()
		requireHasText(t, tester, test.want)
		// Nothing is written before the page's Save action.
		if got := harness.settings.Get(contracts.SettingsChatBaseURL, ""); got != before {
			t.Fatalf("%s wrote the base URL before Save: %q (was %q)", test.chip, got, before)
		}
		if got := countExactText(tester, "已匹配"); got != 1 {
			t.Fatalf("%s matched badges = %d, want 1", test.chip, got)
		}
		clickScrolled(t, tester, "保存")
		if got := harness.settings.Get(contracts.SettingsChatBaseURL, ""); got != test.baseURL {
			t.Fatalf("%s baseURL = %q, want %q", test.chip, got, test.baseURL)
		}
		if got := harness.settings.Get(contracts.SettingsChatModel, ""); got != test.model {
			t.Fatalf("%s model = %q, want %q", test.chip, got, test.model)
		}
	}

	// The custom entry clears the base URL and keeps the model.
	revealSettings(t, tester, "自定义")
	if err := tester.Click("自定义"); err != nil {
		t.Fatalf("click custom: %v", err)
	}
	tester.Frame()
	requireHasText(t, tester, "已填入预设：自定义")
	clickScrolled(t, tester, "保存")
	if got := harness.settings.Get(contracts.SettingsChatBaseURL, ""); got != "" {
		t.Fatalf("custom baseURL = %q, want empty", got)
	}
	if got := harness.settings.Get(contracts.SettingsChatModel, ""); got != "qwen2.5:7b" {
		t.Fatalf("custom changed the model: %q", got)
	}
	if got := countExactText(tester, "已匹配"); got != 0 {
		t.Fatalf("custom entry still highlights %d chips", got)
	}
}

// TestProviderPresetTrailingSlashMatch keeps the stored URL matching tolerant of
// a trailing slash.
func TestProviderPresetTrailingSlashMatch(t *testing.T) {
	_, tester := openModelPage(t, map[string]string{
		contracts.SettingsChatBaseURL: "https://api.openai.com/v1/",
	})
	tester.Frame()
	if got := countExactText(tester, "已匹配"); got != 1 {
		t.Fatalf("trailing-slash URL matched %d chips, want 1", got)
	}
	if !strings.Contains(strings.Join(tester.Texts(), "\n"), "已匹配") {
		t.Fatalf("matched badge missing for a trailing-slash URL")
	}
}

// TestProviderPresetSaveFailureProbe covers a rejected Save: the inline error
// appears and the rejected key is not persisted.
func TestProviderPresetSaveFailureProbe(t *testing.T) {
	inner := newMemSettings(nil)
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
	if err := tester.Click("DeepSeek"); err != nil {
		t.Fatalf("click DeepSeek: %v", err)
	}
	tester.Frame()
	clickScrolled(t, tester, "保存")
	requireHasText(t, tester, "保存失败：")
	if got := inner.Get(contracts.SettingsChatModel, ""); got != "" {
		t.Fatalf("failed Save persisted the model: %q", got)
	}
}
