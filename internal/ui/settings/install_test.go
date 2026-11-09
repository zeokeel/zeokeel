package settings_test

// Tests for the update page's install half: the optional UpdateInstaller, its
// install-on-next-launch button (only after a verified download), the scheduling
// state, the pending-plan display on open, errors and the English copy.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// fakeInstallUpdate is a scripted update backend implementing Check, Download
// and the install half (ScheduleInstall/PendingInstall). ScheduleInstall can
// block until released, which exercises the page's installing state.
type fakeInstallUpdate struct {
	mu sync.Mutex

	info           contracts.UpdateInfo
	downloadResult contracts.UpdateDownload
	downloadErr    error
	pending        contracts.UpdateInstallPlan
	pendingOK      bool
	scheduleErr    error

	checks, downloads int
	schedules         []contracts.UpdateDownload
	blockInstall      bool
	installStarted    chan struct{}
	installRelease    chan struct{}
}

func newFakeInstallUpdate(info contracts.UpdateInfo) *fakeInstallUpdate {
	return &fakeInstallUpdate{
		info:           info,
		downloadResult: stagedDownload(),
		installStarted: make(chan struct{}, 4),
		installRelease: make(chan struct{}, 4),
	}
}

func (f *fakeInstallUpdate) Check(context.Context) (contracts.UpdateInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	return f.info, nil
}

func (f *fakeInstallUpdate) Download(context.Context) (contracts.UpdateDownload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.downloads++
	return f.downloadResult, f.downloadErr
}

func (f *fakeInstallUpdate) ScheduleInstall(download contracts.UpdateDownload) (contracts.UpdateInstallPlan, error) {
	f.mu.Lock()
	f.schedules = append(f.schedules, download)
	block := f.blockInstall
	err := f.scheduleErr
	f.mu.Unlock()
	if block {
		select {
		case f.installStarted <- struct{}{}:
		default:
		}
		<-f.installRelease
	}
	if err != nil {
		return contracts.UpdateInstallPlan{}, err
	}
	return contracts.UpdateInstallPlan{
		Version: download.Version, Path: download.Path, SHA256: download.SHA256,
	}, nil
}

func (f *fakeInstallUpdate) PendingInstall() (contracts.UpdateInstallPlan, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pending, f.pendingOK
}

func (f *fakeInstallUpdate) setPending(plan contracts.UpdateInstallPlan) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending, f.pendingOK = plan, true
}

func (f *fakeInstallUpdate) setScheduleErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scheduleErr = err
}

func (f *fakeInstallUpdate) setBlockInstall(block bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockInstall = block
}

func (f *fakeInstallUpdate) scheduleCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.schedules)
}

func (f *fakeInstallUpdate) scheduled() []contracts.UpdateDownload {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.UpdateDownload(nil), f.schedules...)
}

func waitForInstallStart(t *testing.T, update *fakeInstallUpdate) {
	t.Helper()
	select {
	case <-update.installStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ScheduleInstall to start")
	}
}

const installButtonZh = "安装并在下次启动生效"
const installButtonEn = "Install and apply on next launch"

// openUpdateReadyForInstall drives the page through check + download so the
// install action is visible.
func openUpdateReadyForInstall(t *testing.T, h *harness, tester *ui.Tester, language string) {
	t.Helper()
	tester.Frame()
	if language == "en" {
		h.click(tester, "Update")
		h.click(tester, "Check for updates")
		waitForText(t, tester, "Update available")
		h.click(tester, "Download update")
		waitForText(t, tester, "Downloaded")
		return
	}
	h.click(tester, "更新")
	h.click(tester, "检查更新")
	waitForText(t, tester, "可更新")
	h.click(tester, "下载更新")
	waitForText(t, tester, "已下载")
}

// TestUpdateInstallButtonVisibility covers the conditions: the install action
// appears only after a verified download when the service implements
// UpdateInstaller.
func TestUpdateInstallButtonVisibility(t *testing.T) {
	t.Run("installer available", func(t *testing.T) {
		update := newFakeInstallUpdate(availableDownloadInfo())
		h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
			update, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
		tester := openUpdatePage(t, h)
		openUpdateReadyForInstall(t, h, tester, "zh")
		if got := exactTextCount(tester.Texts(), installButtonZh); got != 1 {
			t.Fatalf("install buttons = %d, want 1; texts = %v", got, tester.Texts())
		}
	})

	t.Run("downloader only", func(t *testing.T) {
		update := newFakeDownloadUpdate(availableDownloadInfo())
		h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
			update, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
		tester := openUpdatePage(t, h)
		openUpdateReadyForInstall(t, h, tester, "zh")
		if got := exactTextCount(tester.Texts(), installButtonZh); got != 0 {
			t.Fatalf("install buttons without UpdateInstaller = %d, want 0; texts = %v", got, tester.Texts())
		}
	})

	t.Run("not available", func(t *testing.T) {
		info := availableDownloadInfo()
		info.Available = false
		info.LatestVersion = info.CurrentVersion
		update := newFakeInstallUpdate(info)
		h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
			update, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
		tester := openUpdatePage(t, h)
		h.click(tester, "检查更新")
		waitForText(t, tester, "已是最新")
		if got := exactTextCount(tester.Texts(), installButtonZh); got != 0 {
			t.Fatalf("install buttons when up to date = %d, want 0; texts = %v", got, tester.Texts())
		}
	})
}

func TestUpdateInstallSchedulesPlan(t *testing.T) {
	update := newFakeInstallUpdate(availableDownloadInfo())
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
	tester := openUpdatePage(t, h)
	openUpdateReadyForInstall(t, h, tester, "zh")

	h.click(tester, installButtonZh)
	waitForText(t, tester, "已计划安装")

	scheduled := update.scheduled()
	if len(scheduled) != 1 {
		t.Fatalf("ScheduleInstall calls = %d, want 1", len(scheduled))
	}
	want := stagedDownload()
	if scheduled[0].Version != want.Version || scheduled[0].Path != want.Path || scheduled[0].SHA256 != want.SHA256 {
		t.Fatalf("ScheduleInstall argument = %+v, want %+v", scheduled[0], want)
	}
	h.assertTexts(tester, "版本：1.2.0", stagedDownload().Path)
	if got := exactTextCount(tester.Texts(), installButtonZh); got != 0 {
		t.Fatalf("install button stayed after scheduling: %d", got)
	}
}

func TestUpdateInstallPendingShownOnOpen(t *testing.T) {
	update := newFakeInstallUpdate(availableDownloadInfo())
	update.setPending(contracts.UpdateInstallPlan{
		Version: "1.3.0",
		Path:    "/var/lib/zeokeel/updates/1.3.0-app.zip",
		SHA256:  "0123456789abcdef",
	})
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
	tester := openUpdatePage(t, h)

	// The pending plan is visible before any check runs.
	h.assertTexts(tester, "待安装：1.3.0")
	if got := exactTextCount(tester.Texts(), installButtonZh); got != 0 {
		t.Fatalf("install buttons with a pending plan = %d, want 0", got)
	}
}

func TestUpdateInstallError(t *testing.T) {
	update := newFakeInstallUpdate(availableDownloadInfo())
	update.setScheduleErr(errors.New("denied"))
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
	tester := openUpdatePage(t, h)
	openUpdateReadyForInstall(t, h, tester, "zh")

	h.click(tester, installButtonZh)
	waitForText(t, tester, "安装计划失败：denied")
	h.assertNoTexts(tester, "已计划安装")
}

func TestUpdateInstallDisabledWhileScheduling(t *testing.T) {
	update := newFakeInstallUpdate(availableDownloadInfo())
	update.setBlockInstall(true)
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
	tester := openUpdatePage(t, h)
	openUpdateReadyForInstall(t, h, tester, "zh")

	h.click(tester, installButtonZh)
	waitForInstallStart(t, update)
	tester.Frame()
	h.assertTexts(tester, "安装中…")
	// The busy state is the official spinner, and the action is disabled.
	requireBusySpinner(t, tester, "安装中…")
	h.click(tester, "安装中…")
	if got := update.scheduleCount(); got != 1 {
		t.Fatalf("ScheduleInstall calls while scheduling = %d, want 1", got)
	}

	update.installRelease <- struct{}{}
	waitForText(t, tester, "已计划安装")
}

func TestUpdateInstallEnglish(t *testing.T) {
	update := newFakeInstallUpdate(availableDownloadInfo())
	h := newLocaleHarnessWithUpdate(t, "en", update)
	tester := h.tester()
	openUpdateReadyForInstall(t, h, tester, "en")

	h.click(tester, installButtonEn)
	waitForText(t, tester, "Install scheduled")
	h.assertTexts(tester, "Version: 1.2.0", stagedDownload().Path)

	// A pending plan is localized too.
	pending := newFakeInstallUpdate(availableDownloadInfo())
	pending.setPending(contracts.UpdateInstallPlan{Version: "1.3.0", Path: "/var/lib/zeokeel/updates/1.3.0-app.zip"})
	pendingHarness := newLocaleHarnessWithUpdate(t, "en", pending)
	pendingTester := pendingHarness.tester()
	pendingTester.Frame()
	pendingHarness.click(pendingTester, "Update")
	pendingHarness.assertTexts(pendingTester, "Pending install: v1.3.0")
}
