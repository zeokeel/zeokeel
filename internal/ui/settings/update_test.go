package settings_test

// Tests for the settings 更新 page: the optional update backend, the async
// check with its checking state, the version/status result, the release-notes
// Markdown, the resource line, the copy-link action and the error states.

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
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
	h.assertTexts(tester, "更新", "当前版本", "dev", "更新服务未启用")
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
	waitForText(t, tester, "当前构建未配置更新源")
	h.assertTexts(tester, "更新未启用")
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

// --- download → immediate install & restart --------------------------------

// fakeUpdateInstaller is a scripted update backend with the optional download and
// install halves: Check reuses fakeUpdate's scripted result, Download and
// ScheduleInstall record their calls and PendingInstall serves a scripted plan.
type fakeUpdateInstaller struct {
	*fakeUpdate

	mu sync.Mutex

	download      contracts.UpdateDownload
	downloadErr   error
	downloadCalls int

	scheduleErr   error
	scheduled     []contracts.UpdateDownload
	scheduleBlock bool
	scheduleStart chan struct{}
	scheduleFree  chan struct{}

	plan    contracts.UpdateInstallPlan
	pending *contracts.UpdateInstallPlan
}

func newFakeUpdateInstaller(info contracts.UpdateInfo) *fakeUpdateInstaller {
	return &fakeUpdateInstaller{
		fakeUpdate:    newFakeUpdate(info),
		scheduleStart: make(chan struct{}, 4),
		scheduleFree:  make(chan struct{}, 4),
	}
}

func (f *fakeUpdateInstaller) Download(context.Context) (contracts.UpdateDownload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.downloadCalls++
	return f.download, f.downloadErr
}

func (f *fakeUpdateInstaller) ScheduleInstall(download contracts.UpdateDownload) (contracts.UpdateInstallPlan, error) {
	f.mu.Lock()
	block := f.scheduleBlock
	err := f.scheduleErr
	f.mu.Unlock()
	if block {
		select {
		case f.scheduleStart <- struct{}{}:
		default:
		}
		<-f.scheduleFree
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil {
		return contracts.UpdateInstallPlan{}, err
	}
	f.scheduled = append(f.scheduled, download)
	plan := f.plan
	if plan.Version == "" {
		plan = contracts.UpdateInstallPlan{
			Version: download.Version, Path: download.Path,
			SHA256: download.SHA256, Kind: "installer",
		}
	}
	return plan, nil
}

func (f *fakeUpdateInstaller) PendingInstall() (contracts.UpdateInstallPlan, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pending == nil {
		return contracts.UpdateInstallPlan{}, false
	}
	return *f.pending, true
}

func (f *fakeUpdateInstaller) scheduledCalls() []contracts.UpdateDownload {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.UpdateDownload(nil), f.scheduled...)
}

// installUpdateCommandID is the host command under test.
const installUpdateCommandID = "uiroot.install-update"

// registerInstallCommand registers the host's immediate-install command on the
// frame's command surface and returns its run counter.
func registerInstallCommand(t *testing.T, h *harness, run func() error) *atomic.Int32 {
	t.Helper()
	calls := &atomic.Int32{}
	unregister, err := h.uiRoot.Runtime().RegisterCommand(contracts.CommandSpec{
		ID:    installUpdateCommandID,
		Title: "安装更新并重启",
		Run: func() error {
			calls.Add(1)
			if run != nil {
				return run()
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("register %s: %v", installUpdateCommandID, err)
	}
	t.Cleanup(unregister)
	return calls
}

// updateDownloadFixture is one verified staged download.
func updateDownloadFixture() contracts.UpdateDownload {
	return contracts.UpdateDownload{
		Version: "1.2.0",
		Path:    "/tmp/zeokeel-updates/zeokeel-1.2.0-setup.exe",
		SHA256:  "abcdef0123456789",
		Size:    2 << 20,
	}
}

// currentInstallBehaviorHint is the phrase the behavior copy must contain for
// the OS running the test: the page explains only the current platform's install
// (Windows setup, macOS dmg, Linux AppImage).
func currentInstallBehaviorHint() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	default:
		return "AppImage"
	}
}

// currentInstallBehaviorHintEN is the English form of the platform hint.
func currentInstallBehaviorHintEN() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows: runs the downloaded setup"
	case "darwin":
		return "macOS: replaces the app from the dmg"
	default:
		return "Linux: installs the AppImage"
	}
}

// updateAvailableInfo is one check result with an available release.
func updateAvailableInfo() contracts.UpdateInfo {
	return contracts.UpdateInfo{
		CurrentVersion: "1.0.0",
		LatestVersion:  "1.2.0",
		Available:      true,
		URL:            "https://example.com/zeokeel-1.2.0-setup.exe",
		Size:           2 << 20,
		SHA256:         "abcdef0123456789",
	}
}

// downloadUpdate runs the check and the download so the page offers the install
// action.
func downloadUpdate(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	h.click(tester, "检查更新")
	waitForText(t, tester, "可更新")
	h.click(tester, "下载更新")
	waitForText(t, tester, "已下载")
}

// TestUpdateInstallAndRestartRunsHostCommand covers the immediate install: the
// action after a download is "安装并重启", clicking it schedules the verified
// download for the host and runs the host's install command, and the page shows
// the in-flight state instead of the misleading next-launch copy.
func TestUpdateInstallAndRestartRunsHostCommand(t *testing.T) {
	update := newFakeUpdateInstaller(updateAvailableInfo())
	update.download = updateDownloadFixture()
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Version: "1.0.0"})
	calls := registerInstallCommand(t, h, nil)
	tester := openUpdatePage(t, h)
	downloadUpdate(t, h, tester)

	h.assertTexts(tester, "安装并重启", currentInstallBehaviorHint())
	h.assertNoTexts(tester, "安装并在下次启动生效")

	h.click(tester, "安装并重启")
	waitForText(t, tester, "正在安装，应用即将退出…")

	scheduled := update.scheduledCalls()
	if len(scheduled) != 1 || scheduled[0] != updateDownloadFixture() {
		t.Fatalf("ScheduleInstall calls = %+v, want the downloaded plan", scheduled)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("install command runs = %d, want 1", got)
	}
}

// TestUpdateInstallFallsBackWithoutCommand covers headless/embedder builds: with
// no host install command the page keeps the honest schedule-on-next-launch
// behavior and never claims an immediate install.
func TestUpdateInstallFallsBackWithoutCommand(t *testing.T) {
	update := newFakeUpdateInstaller(updateAvailableInfo())
	update.download = updateDownloadFixture()
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Version: "1.0.0"})
	tester := openUpdatePage(t, h)
	downloadUpdate(t, h, tester)

	h.assertTexts(tester, "安装并在下次启动生效")
	h.assertNoTexts(tester, "安装并重启")
	h.click(tester, "安装并在下次启动生效")
	waitForText(t, tester, "已计划安装")
	h.assertTexts(tester, "下次启动应用时会自动安装。")
	h.assertNoTexts(tester, "正在安装，应用即将退出…")

	if scheduled := update.scheduledCalls(); len(scheduled) != 1 {
		t.Fatalf("ScheduleInstall calls = %d, want 1", len(scheduled))
	}
}

// TestUpdateInstallRegisteredDisabledCommandStillInstalls covers F7: the host may
// register "uiroot.install-update" with Enabled=has-pending-plan, so at lookup
// time (before the download is scheduled) it is registered but disabled. The page
// must treat presence as availability, schedule the plan, and then run the command
// (now enabled) — not fall back to the non-applying next-launch path.
func TestUpdateInstallRegisteredDisabledCommandStillInstalls(t *testing.T) {
	update := newFakeUpdateInstaller(updateAvailableInfo())
	update.download = updateDownloadFixture()
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Version: "1.0.0"})
	calls := &atomic.Int32{}
	pending := atomic.Bool{}
	unregister, err := h.uiRoot.Runtime().RegisterCommand(contracts.CommandSpec{
		ID:      installUpdateCommandID,
		Title:   "安装更新并重启",
		Enabled: func() bool { return pending.Load() },
		Run:     func() error { calls.Add(1); return nil },
	})
	if err != nil {
		t.Fatalf("register disabled install command: %v", err)
	}
	t.Cleanup(unregister)
	tester := openUpdatePage(t, h)
	downloadUpdate(t, h, tester)

	// Lookup-before-schedule: registered even though Enabled() is false, so the
	// page still offers the immediate install (and has not run anything yet).
	h.assertTexts(tester, "安装并重启")
	if got := calls.Load(); got != 0 {
		t.Fatalf("install command ran before the user clicked install: %d", got)
	}

	// Run-after-schedule: the plan now exists (the real host's Enabled hook
	// turns true), and the page runs the command.
	h.click(tester, "安装并重启")
	waitForText(t, tester, "正在安装，应用即将退出…")
	if scheduled := update.scheduledCalls(); len(scheduled) != 1 {
		t.Fatalf("ScheduleInstall calls = %d, want 1", len(scheduled))
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("install command runs after scheduling = %d, want 1", got)
	}
}

// TestUpdateInstallSchedulingBusyState covers the scheduling in-flight state: a
// slow ScheduleInstall shows the busy label/disabled action and never starts the
// host command before the plan is recorded.
func TestUpdateInstallSchedulingBusyState(t *testing.T) {
	update := newFakeUpdateInstaller(updateAvailableInfo())
	update.download = updateDownloadFixture()
	update.scheduleBlock = true
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Version: "1.0.0"})
	calls := registerInstallCommand(t, h, nil)
	tester := openUpdatePage(t, h)
	downloadUpdate(t, h, tester)

	h.click(tester, "安装并重启")
	select {
	case <-update.scheduleStart:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ScheduleInstall to start")
	}
	tester.Frame()
	h.assertTexts(tester, "安装中…")
	requireBusySpinner(t, tester, "安装中…")
	if got := calls.Load(); got != 0 {
		t.Fatalf("install command ran before the plan was scheduled: %d", got)
	}

	update.scheduleFree <- struct{}{}
	waitForText(t, tester, "正在安装，应用即将退出…")
	if got := calls.Load(); got != 1 {
		t.Fatalf("install command runs = %d, want 1", got)
	}
}

// TestUpdateInstallScheduleError covers the schedule failure path: the page
// reports the error and never runs the host install command.
func TestUpdateInstallScheduleError(t *testing.T) {
	update := newFakeUpdateInstaller(updateAvailableInfo())
	update.download = updateDownloadFixture()
	update.scheduleErr = errors.New("schedule boom")
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Version: "1.0.0"})
	calls := registerInstallCommand(t, h, nil)
	tester := openUpdatePage(t, h)
	downloadUpdate(t, h, tester)

	h.click(tester, "安装并重启")
	waitForText(t, tester, "安装计划失败：schedule boom")
	if got := calls.Load(); got != 0 {
		t.Fatalf("install command ran after a failed schedule: %d", got)
	}
}

// TestUpdateInstallCommandError covers the host command failure path: the page
// reports it instead of showing the installing state.
func TestUpdateInstallCommandError(t *testing.T) {
	update := newFakeUpdateInstaller(updateAvailableInfo())
	update.download = updateDownloadFixture()
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Version: "1.0.0"})
	calls := registerInstallCommand(t, h, func() error { return errors.New("install boom") })
	tester := openUpdatePage(t, h)
	downloadUpdate(t, h, tester)

	h.click(tester, "安装并重启")
	waitForText(t, tester, "安装失败：install boom")
	if got := calls.Load(); got != 1 {
		t.Fatalf("install command runs = %d, want 1", got)
	}
	h.assertNoTexts(tester, "正在安装，应用即将退出…")
}

// TestUpdateInstallRetryAfterCommandFailure covers F4: a failed immediate install
// leaves the scheduled plan visible with a retry button that re-runs the host
// command (without scheduling again) and clears the error.
func TestUpdateInstallRetryAfterCommandFailure(t *testing.T) {
	update := newFakeUpdateInstaller(updateAvailableInfo())
	update.download = updateDownloadFixture()
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Version: "1.0.0"})
	var runs atomic.Int32
	unregister, err := h.uiRoot.Runtime().RegisterCommand(contracts.CommandSpec{
		ID:    installUpdateCommandID,
		Title: "安装更新并重启",
		Run: func() error {
			if runs.Add(1) == 1 {
				return errors.New("install boom")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("register install command: %v", err)
	}
	t.Cleanup(unregister)
	tester := openUpdatePage(t, h)
	downloadUpdate(t, h, tester)

	h.click(tester, "安装并重启")
	waitForText(t, tester, "安装失败：install boom")
	// The main action is replaced by the scheduled plan; the plan offers retry.
	if got := exactTextCount(tester.Texts(), "安装并重启"); got != 0 {
		t.Fatalf("the main install action stayed after the plan was scheduled: %d", got)
	}
	h.assertTexts(tester, "重试安装并重启")

	h.click(tester, "重试安装并重启")
	waitForText(t, tester, "正在安装，应用即将退出…")
	if got := runs.Load(); got != 2 {
		t.Fatalf("install command runs = %d, want 2 (failed + retried)", got)
	}
	if scheduled := update.scheduledCalls(); len(scheduled) != 1 {
		t.Fatalf("retry scheduled another install: %d calls", len(scheduled))
	}
	h.assertNoTexts(tester, "安装失败：")
}

// TestUpdateRecheckRestoresMainInstallAction covers F4's second clause: a new
// check clears the failed plan state, so after checking and downloading again the
// main "install and restart" action is available rather than the page being a
// dead end.
func TestUpdateRecheckRestoresMainInstallAction(t *testing.T) {
	update := newFakeUpdateInstaller(updateAvailableInfo())
	update.download = updateDownloadFixture()
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Version: "1.0.0"})
	unregister, err := h.uiRoot.Runtime().RegisterCommand(contracts.CommandSpec{
		ID:    installUpdateCommandID,
		Title: "安装更新并重启",
		Run:   func() error { return errors.New("install boom") },
	})
	if err != nil {
		t.Fatalf("register install command: %v", err)
	}
	t.Cleanup(unregister)
	tester := openUpdatePage(t, h)
	downloadUpdate(t, h, tester)

	h.click(tester, "安装并重启")
	waitForText(t, tester, "安装失败：install boom")

	// Re-checking clears the staged/failed plan state; the download action and
	// then the main install action come back.
	h.click(tester, "检查更新")
	waitForText(t, tester, "可更新")
	h.assertNoTexts(tester, "安装失败：", "已计划安装")
	h.assertTexts(tester, "下载更新")

	h.click(tester, "下载更新")
	waitForText(t, tester, "已下载")
	h.assertTexts(tester, "安装并重启")
	if got := exactTextCount(tester.Texts(), "安装并重启"); got != 1 {
		t.Fatalf("main install actions after re-check = %d, want 1; texts = %v", got, tester.Texts())
	}
}

// TestUpdatePendingInstallOffersImmediateInstall covers a pending plan from an
// earlier run: it shows the version and an install-and-restart action that runs
// the host command, without scheduling anything again.
func TestUpdatePendingInstallOffersImmediateInstall(t *testing.T) {
	update := newFakeUpdateInstaller(contracts.UpdateInfo{CurrentVersion: "1.0.0"})
	pending := contracts.UpdateInstallPlan{
		Version: "1.2.0", Path: "/tmp/zeokeel-updates/pending.exe", Kind: "installer",
	}
	update.pending = &pending
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Version: "1.0.0"})
	calls := registerInstallCommand(t, h, nil)
	tester := openUpdatePage(t, h)

	h.assertTexts(tester, "待安装", "1.2.0", "安装并重启")
	h.click(tester, "安装并重启")
	waitForText(t, tester, "正在安装，应用即将退出…")
	if got := calls.Load(); got != 1 {
		t.Fatalf("install command runs = %d, want 1", got)
	}
	if scheduled := update.scheduledCalls(); len(scheduled) != 0 {
		t.Fatalf("the pending action scheduled another install: %+v", scheduled)
	}
}

// TestUpdatePendingInstallWithoutCommand covers the honest fallback for a
// pending plan when the host install command is absent.
func TestUpdatePendingInstallWithoutCommand(t *testing.T) {
	update := newFakeUpdateInstaller(contracts.UpdateInfo{CurrentVersion: "1.0.0"})
	pending := contracts.UpdateInstallPlan{Version: "1.2.0", Path: "/tmp/pending.exe", Kind: "dmg"}
	update.pending = &pending
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Version: "1.0.0"})
	tester := openUpdatePage(t, h)

	h.assertTexts(tester, "待安装", "1.2.0", "下次启动应用时会自动安装。")
	h.assertNoTexts(tester, "安装并重启")
}

// TestUpdatePendingInstallRetryAfterCommandFailure covers the pending-plan retry:
// a failed immediate install from a plan scheduled in an earlier run also leaves a
// retry action that re-runs the command from that plan.
func TestUpdatePendingInstallRetryAfterCommandFailure(t *testing.T) {
	update := newFakeUpdateInstaller(contracts.UpdateInfo{CurrentVersion: "1.0.0"})
	pending := contracts.UpdateInstallPlan{
		Version: "1.2.0", Path: "/tmp/zeokeel-updates/pending.exe", Kind: "installer",
	}
	update.pending = &pending
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Version: "1.0.0"})
	var runs atomic.Int32
	unregister, err := h.uiRoot.Runtime().RegisterCommand(contracts.CommandSpec{
		ID:    installUpdateCommandID,
		Title: "安装更新并重启",
		Run: func() error {
			if runs.Add(1) == 1 {
				return errors.New("pending boom")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("register install command: %v", err)
	}
	t.Cleanup(unregister)
	tester := openUpdatePage(t, h)

	h.click(tester, "安装并重启")
	waitForText(t, tester, "安装失败：pending boom")
	h.assertTexts(tester, "待安装：1.2.0", "重试安装并重启")

	h.click(tester, "重试安装并重启")
	waitForText(t, tester, "正在安装，应用即将退出…")
	if got := runs.Load(); got != 2 {
		t.Fatalf("pending install command runs = %d, want 2", got)
	}
	if scheduled := update.scheduledCalls(); len(scheduled) != 0 {
		t.Fatalf("the pending retry scheduled another install: %+v", scheduled)
	}
}

// TestUpdateInstallEnglishCopy covers the English copy of the immediate install
// and the platform behavior explanation.
func TestUpdateInstallEnglishCopy(t *testing.T) {
	update := newFakeUpdateInstaller(updateAvailableInfo())
	update.download = updateDownloadFixture()
	h := newLocaleHarnessWithUpdate(t, "en", update)
	calls := registerInstallCommand(t, h, nil)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Update")
	tester.Frame()
	h.click(tester, "Check for updates")
	waitForText(t, tester, "Update available")
	h.click(tester, "Download update")
	waitForText(t, tester, "Downloaded")
	h.assertTexts(tester, "Install and restart", currentInstallBehaviorHintEN())
	h.click(tester, "Install and restart")
	waitForText(t, tester, "Installing; the app will quit shortly…")
	if got := calls.Load(); got != 1 {
		t.Fatalf("install command runs = %d, want 1", got)
	}
}
