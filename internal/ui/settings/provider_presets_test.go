package settings_test

// TDD tests (RED first) for the provider preset chips on the model page.

import (
	"errors"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// providerHarness opens the model settings page with a scripted store.
func providerHarness(t *testing.T, store *fakeSettings) *harness {
	t.Helper()
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	return h
}

// --- tests ------------------------------------------------------------------

func TestProviderPresetsDefaultNoMatch(t *testing.T) {
	store := newFakeSettings(nil)
	h := providerHarness(t, store)
	tester := h.tester()
	h.assertTexts(tester, "服务商预设", "DeepSeek", "OpenAI", "Ollama（本地）", "自定义")
	if got := strings.Count(strings.Join(tester.Texts(), "\n"), "已匹配"); got != 0 {
		t.Fatalf("unconfigured page shows a matched-provider badge; texts = %v", tester.Texts())
	}
	if raw := store.get(contracts.SettingsChatBaseURL); raw != "" {
		t.Fatalf("presets wrote the base URL on render: %q", raw)
	}
}

func TestProviderPresetDeepSeek(t *testing.T) {
	store := newFakeSettings(nil)
	h := providerHarness(t, store)
	tester := h.tester()
	h.click(tester, "DeepSeek")
	if got := store.get(contracts.SettingsChatBaseURL); got != "https://api.deepseek.com/v1" {
		t.Fatalf("baseURL = %q", got)
	}
	if got := store.get(contracts.SettingsChatModel); got != "deepseek-chat" {
		t.Fatalf("model = %q", got)
	}
	h.assertTexts(tester, "已应用预设：DeepSeek")
}

func TestProviderPresetOllamaLocal(t *testing.T) {
	store := newFakeSettings(nil)
	h := providerHarness(t, store)
	tester := h.tester()
	h.click(tester, "Ollama（本地）")
	if got := store.get(contracts.SettingsChatBaseURL); got != "http://127.0.0.1:11434/v1" {
		t.Fatalf("Ollama baseURL = %q", got)
	}
	if got := store.get(contracts.SettingsChatModel); got != "qwen2.5:7b" {
		t.Fatalf("Ollama model = %q", got)
	}
}

func TestProviderPresetCustomClearsBaseURL(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingsChatBaseURL: "https://api.deepseek.com/v1",
		contracts.SettingsChatModel:   "deepseek-chat",
	})
	h := providerHarness(t, store)
	tester := h.tester()
	h.click(tester, "自定义")
	if got := store.get(contracts.SettingsChatBaseURL); got != "" {
		t.Fatalf("custom baseURL = %q, want empty", got)
	}
	if got := store.get(contracts.SettingsChatModel); got != "deepseek-chat" {
		t.Fatalf("custom changed the model: %q", got)
	}
	h.assertTexts(tester, "已应用预设：自定义")
}

func TestProviderPresetMatchingHighlight(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingsChatBaseURL: "https://api.openai.com/v1",
	})
	h := providerHarness(t, store)
	tester := h.tester()
	if got := strings.Count(strings.Join(tester.Texts(), "\n"), "已匹配"); got != 1 {
		t.Fatalf("matched-provider badges = %d, want 1; texts = %v", got, tester.Texts())
	}

	h.click(tester, "DeepSeek")
	if got := strings.Count(strings.Join(tester.Texts(), "\n"), "已匹配"); got != 1 {
		t.Fatalf("matched-provider badges after switching = %d, want 1", got)
	}
}

func TestProviderPresetWriteFailure(t *testing.T) {
	store := newFakeSettings(nil)
	store.setErr = errProviderStoreWrite
	h := providerHarness(t, store)
	tester := h.tester()
	h.click(tester, "DeepSeek")
	h.assertTexts(tester, "应用预设失败：")
	if raw := store.get(contracts.SettingsChatBaseURL); raw != "" {
		t.Fatalf("failed preset wrote the base URL: %q", raw)
	}
}

func TestProviderPresetsEnglishCopy(t *testing.T) {
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Model")
	h.assertTexts(tester, "Provider presets", "DeepSeek", "OpenAI", "Ollama (local)", "Custom")
	h.click(tester, "DeepSeek")
	if got := store.get(contracts.SettingsChatBaseURL); got != "https://api.deepseek.com/v1" {
		t.Fatalf("English preset baseURL = %q", got)
	}
	h.assertTexts(tester, "Applied preset: DeepSeek")
}

// errProviderStoreWrite scripts one settings write failure for the presets.
var errProviderStoreWrite = errors.New("provider store boom")
