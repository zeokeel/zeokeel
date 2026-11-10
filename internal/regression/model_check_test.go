package regression

// Adversarial audit: the model connection check lives inside the add/edit model
// dialogs (the page-level connection form was removed with the multi-provider
// model list). The HTTP request goes through the exported ModelListGetter seam:
// success renders the discovered model count, non-2xx/transport failures render
// readable errors, the check tests the dialog drafts rather than the saved
// store, and a dialog without a base URL disables the action (the seam is never
// called).

import (
	"context"
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	settingsui "github.com/zeokeel/zeokeel/internal/ui/settings"
)

// startModelSettings opens the model page with the given settings rows.
func startModelSettings(t *testing.T, values map[string]string) (*harness, *ui.Tester) {
	t.Helper()
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyChat, contracts.ChatService(newFakeChat()))
	for key, value := range values {
		if err := h.settings.Set(key, value); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}
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

// openAddModelDialog opens the add-model modal from the model page.
func openAddModelDialog(t *testing.T, tester *ui.Tester) {
	t.Helper()
	revealSettings(t, tester, "添加模型")
	if err := tester.Click("添加模型"); err != nil {
		t.Fatalf("open add-model dialog: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasText(t, tester, "服务商菜单")
}

// focusAndType focuses one accessible control (input or select), replaces its
// text and types value. The settings surface scrolls natively, so the target is
// revealed first.
func focusAndType(t *testing.T, tester *ui.Tester, label, value string) {
	t.Helper()
	revealSettings(t, tester, label)
	rect, ok := tester.Find(label)
	if !ok {
		t.Fatalf("control labeled %q not found; texts = %v", label, tester.Texts())
	}
	if rect.W > 0 && rect.H > 0 {
		tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	} else if err := tester.Click(label); err != nil {
		t.Fatalf("focus %q: %v", label, err)
	}
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type(value)
	tester.Frame()
}

// modelInputValue reads one input's value through the accessibility tree.
func modelInputValue(t *testing.T, tester *ui.Tester, label string) string {
	t.Helper()
	info, ok := tester.Access(label)
	if !ok {
		t.Fatalf("input %q not accessible; texts = %v", label, tester.Texts())
	}
	return info.Value
}

// overrideModelGetter replaces the connection-check seam for one test and
// returns the recorded (baseURL, apiKey) calls.
func overrideModelGetter(t *testing.T, fn func(ctx context.Context, baseURL, apiKey string) (int, []byte, error)) *[][2]string {
	t.Helper()
	previous := settingsui.ModelListGetter
	calls := make([][2]string, 0, 2)
	settingsui.ModelListGetter = func(ctx context.Context, baseURL, apiKey string) (int, []byte, error) {
		calls = append(calls, [2]string{baseURL, apiKey})
		return fn(ctx, baseURL, apiKey)
	}
	t.Cleanup(func() { settingsui.ModelListGetter = previous })
	return &calls
}

// clickConnectionCheck clicks the dialog's check button and pumps until the
// condition holds (the request runs off the UI thread).
func clickConnectionCheck(t *testing.T, tester *ui.Tester, condition func() bool) {
	t.Helper()
	revealSettings(t, tester, "测试连接")
	if err := tester.Click("测试连接"); err != nil {
		t.Fatalf("click 测试连接: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	waitFor(t, "connection check result", func() bool {
		tester.Frame()
		return condition()
	})
}

// TestModelConnectionCheckDialog covers the seam-driven check inside the
// add-model dialog: success/count, HTTP failure and transport failure. The check
// uses the drafts and saves nothing.
func TestModelConnectionCheckDialog(t *testing.T) {
	t.Run("success with model count", func(t *testing.T) {
		calls := overrideModelGetter(t, func(context.Context, string, string) (int, []byte, error) {
			return 200, []byte(`{"data":[{"id":"a"},{"id":"b"},{"id":"c"}]}`), nil
		})
		harness, tester := startModelSettings(t, nil)
		openAddModelDialog(t, tester)
		focusAndType(t, tester, "模型 ID 输入", "draft-model")
		focusAndType(t, tester, "Base URL 输入", "http://draft.test/v1/")
		focusAndType(t, tester, "API Key 输入", "sk-draft")
		clickConnectionCheck(t, tester, func() bool { return tester.HasText("连接正常") })
		requireHasText(t, tester, "3 个模型")
		if len(*calls) != 1 || (*calls)[0][0] != "http://draft.test/v1" || (*calls)[0][1] != "sk-draft" {
			t.Fatalf("seam calls = %v, want the drafts (trailing slash trimmed)", *calls)
		}
		// Testing the connection never saves the model or the connection.
		if got := harness.settings.Get(contracts.SettingModelCatalogModels, ""); got != "" {
			t.Fatalf("connection check wrote the model list: %q", got)
		}
		if got := harness.settings.Get(contracts.SettingsChatBaseURL, ""); got != "" {
			t.Fatalf("connection check wrote the base URL: %q", got)
		}
	})

	t.Run("http failure", func(t *testing.T) {
		overrideModelGetter(t, func(context.Context, string, string) (int, []byte, error) {
			return 401, []byte(`{"error":"nope"}`), nil
		})
		_, tester := startModelSettings(t, nil)
		openAddModelDialog(t, tester)
		focusAndType(t, tester, "Base URL 输入", "http://draft.test/v1")
		clickConnectionCheck(t, tester, func() bool { return tester.HasText("连接失败：HTTP 401") })
		// No discovered-model detail is rendered on a failed check.
		requireNoText(t, tester, "个模型：")
	})

	t.Run("transport error", func(t *testing.T) {
		overrideModelGetter(t, func(context.Context, string, string) (int, []byte, error) {
			return 0, nil, errors.New("network down")
		})
		_, tester := startModelSettings(t, nil)
		openAddModelDialog(t, tester)
		focusAndType(t, tester, "Base URL 输入", "http://draft.test/v1")
		clickConnectionCheck(t, tester, func() bool { return tester.HasText("连接失败：network down") })
	})

	t.Run("no base URL disables the action", func(t *testing.T) {
		calls := overrideModelGetter(t, func(context.Context, string, string) (int, []byte, error) {
			return 200, []byte(`{"data":[]}`), nil
		})
		_, tester := startModelSettings(t, nil)
		openAddModelDialog(t, tester)
		requireHasText(t, tester, "填写 Base URL 后可测试连接。")
		// The action is disabled: clicking it is a no-op and never calls the seam.
		_ = tester.Click("测试连接")
		tester.Frame()
		pumpModelCheckIdle(t, tester, calls)
	})
}

// pumpModelCheckIdle renders a few frames and asserts the seam was never called.
func pumpModelCheckIdle(t *testing.T, tester *ui.Tester, calls *[][2]string) {
	t.Helper()
	for range 3 {
		tester.Frame()
	}
	if len(*calls) != 0 {
		t.Fatalf("disabled check called the seam: %v", *calls)
	}
}

// TestModelConnectionCheckEditDialog covers the editor: the check tests the
// stored row's connection (the key field starts blank, so the existing key is
// used) and never leaks the key into the frame.
func TestModelConnectionCheckEditDialog(t *testing.T) {
	calls := overrideModelGetter(t, func(context.Context, string, string) (int, []byte, error) {
		return 200, []byte(`{"data":[{"id":"deepseek-chat"}]}`), nil
	})
	catalog := encodeModelFixture(t, contracts.ModelInfo{
		ID: "deepseek-chat", BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-stored",
	})
	_, tester := startModelSettings(t, map[string]string{
		contracts.SettingModelCatalogModels: catalog,
	})
	revealSettings(t, tester, "编辑模型 deepseek-chat")
	if err := tester.Click("编辑模型 deepseek-chat"); err != nil {
		t.Fatalf("open editor: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasTexts(t, tester, "编辑模型", "已设置（留空保持不变）。")
	if got := len(tester.Texts()); got == 0 {
		t.Fatal("editor rendered no text")
	}
	// The stored key is never rendered in clear text.
	for _, text := range tester.Texts() {
		if text == "sk-stored" {
			t.Fatal("editor rendered the stored API key in clear text")
		}
	}
	clickConnectionCheck(t, tester, func() bool { return tester.HasText("连接正常") })
	if len(*calls) != 1 || (*calls)[0][0] != "https://api.deepseek.com/v1" || (*calls)[0][1] != "sk-stored" {
		t.Fatalf("seam calls = %v, want the stored URL/key", *calls)
	}
}

// encodeModelFixture encodes one model list for the settings row.
func encodeModelFixture(t *testing.T, models ...contracts.ModelInfo) string {
	t.Helper()
	encoded, err := contracts.EncodeModelInfoList(models)
	if err != nil {
		t.Fatalf("encode model list: %v", err)
	}
	return encoded
}
