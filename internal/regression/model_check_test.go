package regression

// Adversarial audit: the model connection check on the settings model page. The
// HTTP request goes through the exported ModelListGetter seam: success renders
// the model count, non-2xx/transport failures render readable errors, and a page
// without a configured URL disables the action (the seam is never called).

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

// pumpModelCheck renders frames until the connection-check result is visible.
func pumpModelCheck(t *testing.T, tester *ui.Tester, condition func() bool) {
	t.Helper()
	waitFor(t, "connection check result", func() bool {
		tester.Frame()
		return condition()
	})
}

// clickModelCheck clicks the check button and pumps until condition holds.
func clickModelCheck(t *testing.T, tester *ui.Tester, condition func() bool) {
	t.Helper()
	if err := tester.Click("测试连接"); err != nil {
		t.Fatalf("click 测试连接: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	pumpModelCheck(t, tester, condition)
}

// TestModelConnectionCheck covers the seam-driven check: success/count, HTTP
// failure, transport failure and the unconfigured page.
func TestModelConnectionCheck(t *testing.T) {
	t.Run("success with model count", func(t *testing.T) {
		calls := overrideModelGetter(t, func(context.Context, string, string) (int, []byte, error) {
			return 200, []byte(`{"data":[{"id":"a"},{"id":"b"},{"id":"c"}]}`), nil
		})
		_, tester := startModelSettings(t, map[string]string{
			contracts.SettingsChatBaseURL: "http://saved.test/v1",
			contracts.SettingsChatAPIKey:  "sk-test",
			contracts.SettingsChatModel:   "saved-model",
		})
		requireHasText(t, tester, "连接检查")
		requireHasText(t, tester, "http://saved.test/v1")
		clickModelCheck(t, tester, func() bool { return tester.HasText("连接正常") })
		requireHasText(t, tester, "3 个模型")
		if len(*calls) != 1 || (*calls)[0][0] != "http://saved.test/v1" || (*calls)[0][1] != "sk-test" {
			t.Fatalf("seam calls = %v, want the saved URL/key", *calls)
		}
	})

	t.Run("http failure", func(t *testing.T) {
		overrideModelGetter(t, func(context.Context, string, string) (int, []byte, error) {
			return 401, []byte(`{"error":"nope"}`), nil
		})
		_, tester := startModelSettings(t, map[string]string{
			contracts.SettingsChatBaseURL: "http://saved.test/v1",
		})
		clickModelCheck(t, tester, func() bool { return tester.HasText("连接失败：HTTP 401") })
		requireNoText(t, tester, "个模型")
	})

	t.Run("transport error", func(t *testing.T) {
		overrideModelGetter(t, func(context.Context, string, string) (int, []byte, error) {
			return 0, nil, errors.New("network down")
		})
		_, tester := startModelSettings(t, map[string]string{
			contracts.SettingsChatBaseURL: "http://saved.test/v1",
		})
		clickModelCheck(t, tester, func() bool { return tester.HasText("连接失败：network down") })
	})

	t.Run("unconfigured URL", func(t *testing.T) {
		calls := overrideModelGetter(t, func(context.Context, string, string) (int, []byte, error) {
			return 200, []byte(`{"data":[]}`), nil
		})
		_, tester := startModelSettings(t, nil)
		requireHasText(t, tester, "尚未配置服务地址")
		// The action is disabled: clicking it is a no-op and never calls the seam.
		_ = tester.Click("测试连接")
		tester.Frame()
		pumpModelCheck(t, tester, func() bool { return len(*calls) == 0 })
	})
}
