package settings_test

// Tests for the settings 更新 page: the optional update backend, the async
// check with its checking state, the version/status result, the release-notes
// Markdown, the resource line, the copy-link action and the error states.

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// fakeUpdate is a scripted contracts.UpdateService. Check can block until the
// release channel closes, which exercises the page's checking state.
type fakeUpdate struct {
	mu sync.Mutex

	info contracts.UpdateInfo
	err  error

	calls   int
	block   bool
	started chan struct{}
	release chan struct{}
}

func newFakeUpdate(info contracts.UpdateInfo) *fakeUpdate {
	return &fakeUpdate{
		info:    info,
		started: make(chan struct{}, 4),
		release: make(chan struct{}, 4),
	}
}

func (f *fakeUpdate) Check(ctx context.Context) (contracts.UpdateInfo, error) {
	f.mu.Lock()
	f.calls++
	block := f.block
	info, err := f.info, f.err
	f.mu.Unlock()
	if block {
		select {
		case f.started <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return contracts.UpdateInfo{}, ctx.Err()
		case <-f.release:
		}
	}
	return info, err
}

func (f *fakeUpdate) setInfo(info contracts.UpdateInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.info = info
}

func (f *fakeUpdate) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeUpdate) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// waitForText frames the tester until want shows up (the check result arrives
// through UIRuntime.Post, which a frame drains).
func waitForText(t *testing.T, tester interface {
	Frame()
	HasText(string) bool
	Texts() []string
}, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		tester.Frame()
		if tester.HasText(want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q; texts = %v", want, tester.Texts())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitForCall(t *testing.T, update *fakeUpdate) {
	t.Helper()
	select {
	case <-update.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the update check to start")
	}
}

func openUpdatePage(t *testing.T, h *harness) *ui.Tester {
	t.Helper()
	tester := h.tester()
	tester.Frame()
	h.click(tester, "更新")
	return tester
}

// exactTextCount counts exactly equal text nodes (Tester.HasText is substring).
func exactTextCount(texts []string, want string) int {
	count := 0
	for _, text := range texts {
		if text == want {
			count++
		}
	}
	return count
}

// --- tests ------------------------------------------------------------------

func TestUpdatePageWithoutBackend(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "更新")
	h.assertTexts(tester, "更新", "检查更新并查看最新版本", "当前版本", "dev", "更新服务未启用")
	// The description mentions 检查更新; the action button itself must be absent.
	if got := exactTextCount(tester.Texts(), "检查更新"); got != 0 {
		t.Fatalf("检查更新 button labels = %d, want 0; texts = %v", got, tester.Texts())
	}
}

func TestUpdateCheckShowsNewVersion(t *testing.T) {
	update := newFakeUpdate(contracts.UpdateInfo{
		CurrentVersion: "1.0.0",
		LatestVersion:  "1.2.0",
		Available:      true,
		Notes:          "# 更新内容\n\n- 修复登录问题\n- 提升启动速度",
		URL:            "https://example.com/zeokeel-1.2.0.zip",
		Size:           2 << 20,
		SHA256:         "abcdef0123456789",
	})
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
	tester := openUpdatePage(t, h)
	h.assertTexts(tester, "当前版本", "1.0.0", "检查更新")

	h.click(tester, "检查更新")
	waitForText(t, tester, "1.2.0")
	h.assertTexts(tester, "最新版本", "可更新", "更新说明", "更新内容",
		"修复登录问题", "提升启动速度", "复制下载链接",
		"资源："+runtime.GOOS+"/"+runtime.GOARCH, "2.0 MB", "SHA256 abcdef01")

	if err := tester.Click("复制下载链接"); err != nil {
		t.Fatalf("click copy link: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := tester.Clipboard(); got != "https://example.com/zeokeel-1.2.0.zip" {
		t.Fatalf("clipboard = %q, want the download URL", got)
	}
	h.assertTexts(tester, "已复制")
	if calls := update.callCount(); calls != 1 {
		t.Fatalf("Check calls = %d, want 1", calls)
	}
}

func TestUpdateCheckAlreadyLatest(t *testing.T) {
	update := newFakeUpdate(contracts.UpdateInfo{
		CurrentVersion: "1.0.0",
		LatestVersion:  "1.0.0",
		Available:      false,
		URL:            "",
	})
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Version: "1.0.0"})
	tester := openUpdatePage(t, h)
	h.click(tester, "检查更新")
	waitForText(t, tester, "已是最新")

	h.assertNoTexts(tester, "可更新")
	h.assertTexts(tester, "没有匹配当前平台的下载资源")
	// The copy button exists but is disabled with no URL: it writes nothing.
	if err := tester.Click("复制下载链接"); err != nil {
		t.Fatalf("click disabled copy: %v", err)
	}
	tester.Frame()
	if got := tester.Clipboard(); got != "" {
		t.Fatalf("clipboard = %q, want empty without a URL", got)
	}
}

func TestUpdateCheckError(t *testing.T) {
	update := newFakeUpdate(contracts.UpdateInfo{})
	update.setErr(errors.New("update source not configured"))
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Version: "1.0.0"})
	tester := openUpdatePage(t, h)
	h.click(tester, "检查更新")
	waitForText(t, tester, "检查更新失败：update source not configured")
	h.assertNoTexts(tester, "已是最新", "可更新")
}

// TestUpdateFeedUnconfiguredIsExplainedNotPromised pins E-03: a build whose
// update backend reports no release feed must say so in product terms, must not
// echo the backend's internal config key, and must not offer a check action or
// promise a download/install it cannot perform.
func TestUpdateFeedUnconfiguredIsExplainedNotPromised(t *testing.T) {
	update := newFakeUpdate(contracts.UpdateInfo{})
	update.setErr(errors.New("更新源未配置（update.feedURL）"))
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Version: "1.0.0"})
	tester := openUpdatePage(t, h)
	h.assertNoTexts(tester, "下载安装包", "下次启动安装")
	h.click(tester, "检查更新")
	waitForText(t, tester, "当前构建未启用更新")
	h.assertTexts(tester, "更新未启用", "官方发布渠道")
	h.assertNoTexts(tester, "update.feedURL", "检查更新失败",
		"下载更新", "下载安装包", "安装并在下次启动生效", "下次启动安装")
	// The action disappears once the build is known to be unconfigured.
	if got := exactTextCount(tester.Texts(), "检查更新"); got != 0 {
		t.Fatalf("检查更新 labels = %d, want 0; texts = %v", got, tester.Texts())
	}
}

func TestUpdateCheckingStateDisablesButton(t *testing.T) {
	update := newFakeUpdate(contracts.UpdateInfo{
		CurrentVersion: "1.0.0", LatestVersion: "1.1.0", Available: true,
		URL: "https://example.com/app.zip",
	})
	update.block = true
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Version: "1.0.0"})
	tester := openUpdatePage(t, h)

	h.click(tester, "检查更新")
	waitForCall(t, update)
	tester.Frame()
	h.assertTexts(tester, "检查中…")
	// The busy state is the official spinner, and the action is disabled.
	requireBusySpinner(t, tester, "检查中…")

	// The button is disabled while the check is in flight: clicking it again
	// starts no second check.
	if err := tester.Click("检查中…"); err != nil {
		t.Fatalf("click checking button: %v", err)
	}
	tester.Frame()
	if calls := update.callCount(); calls != 1 {
		t.Fatalf("Check calls while checking = %d, want 1", calls)
	}

	update.release <- struct{}{}
	waitForText(t, tester, "1.1.0")
	h.assertTexts(tester, "检查更新", "可更新")
}

func TestUpdateRepeatCheckUpdatesState(t *testing.T) {
	update := newFakeUpdate(contracts.UpdateInfo{
		CurrentVersion: "1.0.0", LatestVersion: "1.1.0", Available: true,
		URL: "https://example.com/v1.1.zip",
	})
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Version: "1.0.0"})
	tester := openUpdatePage(t, h)
	h.click(tester, "检查更新")
	waitForText(t, tester, "1.1.0")

	update.setInfo(contracts.UpdateInfo{
		CurrentVersion: "1.0.0", LatestVersion: "1.3.0", Available: true,
		URL: "https://example.com/v1.3.zip",
	})
	h.click(tester, "检查更新")
	waitForText(t, tester, "1.3.0")
	if calls := update.callCount(); calls != 2 {
		t.Fatalf("Check calls = %d, want 2", calls)
	}
}

func TestUpdateCurrentVersionFallsBackToResult(t *testing.T) {
	update := newFakeUpdate(contracts.UpdateInfo{
		CurrentVersion: "2.0.0", LatestVersion: "2.1.0", Available: true,
	})
	// No KeyAppInfo: the current version comes from the check result (and
	// "dev" before the first check).
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{})
	tester := openUpdatePage(t, h)
	h.assertTexts(tester, "dev")
	h.click(tester, "检查更新")
	waitForText(t, tester, "2.1.0")
	h.assertTexts(tester, "2.0.0")
}
