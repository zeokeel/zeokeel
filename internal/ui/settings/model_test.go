package settings_test

// TDD tests (RED first) for the model page's connection-check block.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// modelCheckHarness opens the settings model page with a scripted chat config.
func modelCheckHarness(t *testing.T, store *fakeSettings, chat *fakeChat) *harness {
	t.Helper()
	h := newHarness(t, chat, store, settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "模型")
	return h
}

// withModelListGetter replaces the model-list request seam for one test.
func withModelListGetter(t *testing.T,
	fn func(ctx context.Context, baseURL, apiKey string) (int, []byte, error)) {
	t.Helper()
	previous := settings.ModelListGetter
	settings.ModelListGetter = fn
	t.Cleanup(func() { settings.ModelListGetter = previous })
}

// waitModelCheckText frames until the text appears (the check runs async).
func waitModelCheckText(t *testing.T, h *harness, tester *ui.Tester, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		tester.Frame()
		if tester.HasText(want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("check result %q not shown; texts = %v", want, tester.Texts())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func configuredStore() *fakeSettings {
	return newFakeSettings(map[string]string{
		contracts.SettingsChatModel:   "deepseek-chat",
		contracts.SettingsChatBaseURL: "https://api.example.com/v1",
		contracts.SettingsChatAPIKey:  "sk-test",
	})
}

// --- tests ------------------------------------------------------------------

func TestModelCheckSuccess(t *testing.T) {
	var mu sync.Mutex
	var gotURL, gotKey string
	withModelListGetter(t, func(_ context.Context, baseURL, apiKey string) (int, []byte, error) {
		mu.Lock()
		gotURL, gotKey = baseURL, apiKey
		mu.Unlock()
		return 200, []byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`), nil
	})
	h := modelCheckHarness(t, configuredStore(), &fakeChat{})
	tester := h.tester()
	h.assertTexts(tester, "连接检查", "测试连接", "https://api.example.com/v1", "deepseek-chat")
	h.click(tester, "测试连接")
	waitModelCheckText(t, h, tester, "连接正常")
	h.assertTexts(tester, "2 个模型")
	mu.Lock()
	defer mu.Unlock()
	if gotURL != "https://api.example.com/v1" || gotKey != "sk-test" {
		t.Fatalf("getter args = %q/%q", gotURL, gotKey)
	}
}

func TestModelCheckHTTPFailure(t *testing.T) {
	withModelListGetter(t, func(context.Context, string, string) (int, []byte, error) {
		return 500, []byte("boom"), nil
	})
	h := modelCheckHarness(t, configuredStore(), &fakeChat{})
	tester := h.tester()
	h.click(tester, "测试连接")
	waitModelCheckText(t, h, tester, "连接失败：HTTP 500")
}

func TestModelCheckNetworkError(t *testing.T) {
	withModelListGetter(t, func(context.Context, string, string) (int, []byte, error) {
		return 0, nil, errors.New("dial tcp: connection refused")
	})
	h := modelCheckHarness(t, configuredStore(), &fakeChat{})
	tester := h.tester()
	h.click(tester, "测试连接")
	waitModelCheckText(t, h, tester, "连接失败：dial tcp: connection refused")
}

func TestModelCheckWithoutBaseURL(t *testing.T) {
	called := atomic.Int32{}
	withModelListGetter(t, func(context.Context, string, string) (int, []byte, error) {
		called.Add(1)
		return 200, nil, nil
	})
	h := modelCheckHarness(t, newFakeSettings(nil), &fakeChat{})
	tester := h.tester()
	h.assertTexts(tester, "尚未配置服务地址")
	h.click(tester, "测试连接")
	if called.Load() != 0 {
		t.Fatalf("disabled check called the getter %d times", called.Load())
	}
}

func TestModelCheckIgnoresRepeatedClicks(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	withModelListGetter(t, func(context.Context, string, string) (int, []byte, error) {
		calls.Add(1)
		<-release
		return 200, []byte(`{"data":[{"id":"a"}]}`), nil
	})
	h := modelCheckHarness(t, configuredStore(), &fakeChat{})
	tester := h.tester()
	h.click(tester, "测试连接")
	tester.Frame()
	h.assertTexts(tester, "检查中…")
	h.click(tester, "检查中…")
	tester.Frame()
	if calls.Load() != 1 {
		t.Fatalf("getter calls = %d, want 1 while checking", calls.Load())
	}
	close(release)
	waitModelCheckText(t, h, tester, "连接正常")
}

func TestModelCheckEnglishCopy(t *testing.T) {
	withModelListGetter(t, func(context.Context, string, string) (int, []byte, error) {
		return 200, []byte(`{"data":[{"id":"a"}]}`), nil
	})
	h := modelCheckHarness(t, configuredStore(), &fakeChat{})
	tester := h.tester()
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Connection check", "Test connection", "Current",
		"https://api.example.com/v1", "deepseek-chat")
	h.click(tester, "Test connection")
	waitModelCheckText(t, h, tester, "Connection OK")
	h.assertTexts(tester, "1 model")

	// The unconfigured state is translated too.
	h2 := modelCheckHarness(t, newFakeSettings(nil), &fakeChat{})
	tester2 := h2.tester()
	h2.uiRoot.Locale().SetLanguage("en")
	tester2.Frame()
	h2.assertTexts(tester2, "No service URL configured")
}

func TestModelCheckUsesChatConfigFallback(t *testing.T) {
	withModelListGetter(t, func(_ context.Context, baseURL, apiKey string) (int, []byte, error) {
		if baseURL != "https://fallback.example/v1" || apiKey != "sk-fallback" {
			return 0, nil, errors.New("unexpected fallback config: " + baseURL + "/" + apiKey)
		}
		return 200, nil, nil
	})
	chat := &fakeChat{config: contracts.ChatConfig{
		Model: "fallback-model", BaseURL: "https://fallback.example/v1", APIKey: "sk-fallback",
	}}
	h := modelCheckHarness(t, newFakeSettings(nil), chat)
	tester := h.tester()
	h.click(tester, "测试连接")
	waitModelCheckText(t, h, tester, "连接正常")
}

func TestModelCheckStatusTextsAreLocalized(t *testing.T) {
	withModelListGetter(t, func(context.Context, string, string) (int, []byte, error) {
		return 200, nil, nil
	})
	h := modelCheckHarness(t, configuredStore(), &fakeChat{})
	tester := h.tester()
	h.click(tester, "测试连接")
	waitModelCheckText(t, h, tester, "连接正常")
	if strings.Contains(strings.Join(tester.Texts(), "\n"), "Connection OK") {
		t.Fatal("Chinese frame shows the English result")
	}
}
