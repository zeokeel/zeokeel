package settings_test

// Frame tests for the model dialogs' connection check: the check lives in the
// add/edit model modal, runs against the draft Base URL/API key shown there
// (the stored key stands in while its field is blank), never writes the store,
// and is a seam (ModelListGetter) so the outcome can be scripted.

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// withModelListGetter replaces the model-list request seam for one test.
func withModelListGetter(t *testing.T,
	fn func(ctx context.Context, baseURL, apiKey string) (int, []byte, error)) {
	t.Helper()
	previous := settings.ModelListGetter
	settings.ModelListGetter = fn
	t.Cleanup(func() { settings.ModelListGetter = previous })
}

// latencySuffix matches the formatted check latency that ends the success
// detail.
var latencySuffix = regexp.MustCompile(`^(?:\d+ ms|\d+\.\d s)$`)

// requireModelCheckDetail asserts that the frame shows one success detail line
// starting with prefix and ending with a formatted latency. The scripted getter
// returns in microseconds, so the exact number is not pinned here; the
// formatting itself is covered deterministically by
// model_check_internal_test.go.
func requireModelCheckDetail(t *testing.T, tester *ui.Tester, prefix string) string {
	t.Helper()
	for _, text := range tester.Texts() {
		if !strings.HasPrefix(text, prefix) {
			continue
		}
		if latencySuffix.MatchString(strings.TrimPrefix(text, prefix)) {
			return text
		}
	}
	t.Fatalf("no success detail starting %q with a latency; texts = %v", prefix, tester.Texts())
	return ""
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

// configuredModelStore stores one model and makes it the default.
func configuredModelStore(t *testing.T) *fakeSettings {
	t.Helper()
	return newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: modelCatalogRow(t,
			contracts.ModelInfo{
				ID: "deepseek-chat", Name: "DeepSeek Chat",
				BaseURL: "https://api.example.com/v1", APIKey: "sk-test",
			}),
		contracts.SettingsChatModel:   "deepseek-chat",
		contracts.SettingsChatBaseURL: "https://api.example.com/v1",
		contracts.SettingsChatAPIKey:  "sk-test",
	})
}

// --- tests ------------------------------------------------------------------

func TestModelDialogCheckSuccessUsesStoredKeyAndSavesNothing(t *testing.T) {
	var mu sync.Mutex
	var gotURL, gotKey string
	withModelListGetter(t, func(_ context.Context, baseURL, apiKey string) (int, []byte, error) {
		mu.Lock()
		gotURL, gotKey = baseURL, apiKey
		mu.Unlock()
		return 200, []byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`), nil
	})
	store := configuredModelStore(t)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	openEditModal(t, h, tester, "deepseek-chat")

	h.assertTexts(tester, "测试连接", "测试连接使用当前输入，不会保存。")
	requireCheckButton(t, tester, "测试连接", false)
	h.click(tester, "测试连接")
	waitModelCheckText(t, h, tester, "连接正常")
	// The success line carries the discovered models and the measured latency.
	requireModelCheckDetail(t, tester, "2 个模型：model-a、model-b · ")
	mu.Lock()
	defer mu.Unlock()
	if gotURL != "https://api.example.com/v1" || gotKey != "sk-test" {
		t.Fatalf("getter args = %q/%q, want the stored connection", gotURL, gotKey)
	}
	if sets := store.setKeys(); len(sets) != 0 {
		t.Fatalf("the connection check wrote the store: %v", sets)
	}
}

func TestModelDialogCheckUsesTypedDraft(t *testing.T) {
	var mu sync.Mutex
	var gotURL, gotKey string
	withModelListGetter(t, func(_ context.Context, baseURL, apiKey string) (int, []byte, error) {
		mu.Lock()
		gotURL, gotKey = baseURL, apiKey
		mu.Unlock()
		return 200, nil, nil
	})
	store := configuredModelStore(t)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openModelPage(t, h)
	openEditModal(t, h, tester, "deepseek-chat")
	clearAndType(t, h, tester, "Base URL 输入", "https://draft.example/v1")
	clearAndType(t, h, tester, "API Key 输入", "sk-draft")
	h.click(tester, "测试连接")
	waitModelCheckText(t, h, tester, "连接正常")
	mu.Lock()
	defer mu.Unlock()
	if gotURL != "https://draft.example/v1" || gotKey != "sk-draft" {
		t.Fatalf("getter args = %q/%q, want the typed draft", gotURL, gotKey)
	}
	// The check persisted nothing (the draft is still uncommitted).
	if got := store.get(contracts.SettingsChatBaseURL); got != "https://api.example.com/v1" {
		t.Fatalf("the check saved the draft: %q", got)
	}
}

func TestModelDialogCheckHTTPFailure(t *testing.T) {
	withModelListGetter(t, func(context.Context, string, string) (int, []byte, error) {
		return 500, []byte("boom"), nil
	})
	h := newHarness(t, &fakeChat{}, configuredModelStore(t), settings.Config{})
	tester := openModelPage(t, h)
	openEditModal(t, h, tester, "deepseek-chat")
	h.click(tester, "测试连接")
	waitModelCheckText(t, h, tester, "连接失败：HTTP 500")
}

func TestModelDialogCheckNetworkError(t *testing.T) {
	withModelListGetter(t, func(context.Context, string, string) (int, []byte, error) {
		return 0, nil, errors.New("dial tcp: connection refused")
	})
	h := newHarness(t, &fakeChat{}, configuredModelStore(t), settings.Config{})
	tester := openModelPage(t, h)
	openEditModal(t, h, tester, "deepseek-chat")
	h.click(tester, "测试连接")
	waitModelCheckText(t, h, tester, "连接失败：dial tcp: connection refused")
}

func TestModelDialogCheckDisabledWithoutBaseURL(t *testing.T) {
	called := atomic.Int32{}
	withModelListGetter(t, func(context.Context, string, string) (int, []byte, error) {
		called.Add(1)
		return 200, nil, nil
	})
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := openModelPage(t, h)
	openAddModal(t, h, tester)
	// A new model's endpoint is empty until the provider selector fills it.
	requireCheckButton(t, tester, "测试连接", true)
	h.assertTexts(tester, "填写 Base URL 后可测试连接。")
	h.click(tester, "测试连接")
	tester.Frame()
	if called.Load() != 0 {
		t.Fatalf("disabled check called the getter %d times", called.Load())
	}
	// Choosing a provider fills the endpoint and enables the check.
	h.click(tester, "服务商菜单")
	h.click(tester, "DeepSeek")
	tester.Frame()
	requireCheckButton(t, tester, "测试连接", false)
}

func TestModelDialogCheckIgnoresRepeatedClicks(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	withModelListGetter(t, func(context.Context, string, string) (int, []byte, error) {
		calls.Add(1)
		<-release
		return 200, []byte(`{"data":[{"id":"a"}]}`), nil
	})
	h := newHarness(t, &fakeChat{}, configuredModelStore(t), settings.Config{})
	tester := openModelPage(t, h)
	openEditModal(t, h, tester, "deepseek-chat")
	h.click(tester, "测试连接")
	tester.Frame()
	// The busy state is the official spinner, and the action is disabled.
	h.assertTexts(tester, "测试中…")
	requireBusySpinner(t, tester, "测试中…")
	h.click(tester, "测试中…")
	tester.Frame()
	if calls.Load() != 1 {
		t.Fatalf("getter calls = %d, want 1 while checking", calls.Load())
	}
	close(release)
	waitModelCheckText(t, h, tester, "连接正常")
}

func TestModelDialogCheckEnglishCopy(t *testing.T) {
	withModelListGetter(t, func(context.Context, string, string) (int, []byte, error) {
		return 200, []byte(`{"data":[{"id":"a"}]}`), nil
	})
	h := newLocaleHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Model")
	tester.Frame()
	h.click(tester, "Add model")
	tester.Frame()
	h.assertTexts(tester, "Test connection", "The connection test uses the current input and saves nothing.")
	h.click(tester, "Provider menu")
	h.click(tester, "DeepSeek")
	tester.Frame()
	h.click(tester, "Test connection")
	waitModelCheckText(t, h, tester, "Connection OK")
	requireModelCheckDetail(t, tester, "1 model: a · ")
	// The Chinese status copy never leaks into the English frame.
	if strings.Contains(strings.Join(tester.Texts(), "\n"), "连接正常") {
		t.Fatal("English frame shows the Chinese check result")
	}
}

func TestModelDialogCheckResultStaysInItsDialog(t *testing.T) {
	withModelListGetter(t, func(context.Context, string, string) (int, []byte, error) {
		return 200, nil, nil
	})
	h := newHarness(t, &fakeChat{}, configuredModelStore(t), settings.Config{})
	tester := openModelPage(t, h)

	// A completed check in the editor does not leak into the next dialog.
	openEditModal(t, h, tester, "deepseek-chat")
	h.click(tester, "测试连接")
	waitModelCheckText(t, h, tester, "连接正常")
	h.click(tester, "取消")
	tester.Frame()
	h.assertNoTexts(tester, "连接正常")

	openEditModal(t, h, tester, "deepseek-chat")
	h.assertNoTexts(tester, "连接正常")
}
