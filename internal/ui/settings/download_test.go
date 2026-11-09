package settings_test

// Tests for the update page's download half: the optional UpdateDownloader,
// the download button conditions, the async download with its downloading
// state, the staged-file result panel, errors and the English copy.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// fakeDownloadUpdate is a scripted update backend that also implements
// contracts.UpdateDownloader. Download can block until released, which
// exercises the page's downloading state.
type fakeDownloadUpdate struct {
	mu sync.Mutex

	info   contracts.UpdateInfo
	result contracts.UpdateDownload
	err    error

	checks          int
	downloads       int
	block           bool
	downloadStarted chan struct{}
	downloadRelease chan struct{}
}

func newFakeDownloadUpdate(info contracts.UpdateInfo) *fakeDownloadUpdate {
	return &fakeDownloadUpdate{
		info:            info,
		downloadStarted: make(chan struct{}, 4),
		downloadRelease: make(chan struct{}, 4),
	}
}

func (f *fakeDownloadUpdate) Check(context.Context) (contracts.UpdateInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks++
	return f.info, nil
}

func (f *fakeDownloadUpdate) Download(ctx context.Context) (contracts.UpdateDownload, error) {
	f.mu.Lock()
	f.downloads++
	block := f.block
	result, err := f.result, f.err
	f.mu.Unlock()
	if block {
		select {
		case f.downloadStarted <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return contracts.UpdateDownload{}, ctx.Err()
		case <-f.downloadRelease:
		}
	}
	return result, err
}

func (f *fakeDownloadUpdate) setResult(result contracts.UpdateDownload) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.result = result
}

func (f *fakeDownloadUpdate) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeDownloadUpdate) downloadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.downloads
}

func (f *fakeDownloadUpdate) checkCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checks
}

func waitForDownloadStart(t *testing.T, update *fakeDownloadUpdate) {
	t.Helper()
	select {
	case <-update.downloadStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the update download to start")
	}
}

// availableDownloadInfo is the update result the download tests start from.
func availableDownloadInfo() contracts.UpdateInfo {
	return contracts.UpdateInfo{
		CurrentVersion: "1.0.0", LatestVersion: "1.2.0", Available: true,
		URL: "https://example.com/zeokeel-1.2.0.zip", Size: 2 << 20,
		SHA256: "abcdef0123456789",
	}
}

// stagedDownload is the verified result the page must render.
func stagedDownload() contracts.UpdateDownload {
	return contracts.UpdateDownload{
		Version: "1.2.0",
		Path:    "/var/lib/zeokeel/updates/1.2.0-zeokeel-1.2.0.zip",
		SHA256:  "abcdef0123456789",
		Size:    2 << 20,
	}
}

// TestUpdateDownloadButtonVisibility covers the button conditions: it appears
// only when an update is available AND the service implements UpdateDownloader.
func TestUpdateDownloadButtonVisibility(t *testing.T) {
	t.Run("available with downloader", func(t *testing.T) {
		update := newFakeDownloadUpdate(availableDownloadInfo())
		h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
			update, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
		tester := openUpdatePage(t, h)
		h.click(tester, "检查更新")
		waitForText(t, tester, "可更新")
		if got := exactTextCount(tester.Texts(), "下载更新"); got != 1 {
			t.Fatalf("下载更新 buttons = %d, want 1; texts = %v", got, tester.Texts())
		}
	})

	t.Run("available without downloader", func(t *testing.T) {
		update := newFakeUpdate(availableDownloadInfo())
		h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
			update, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
		tester := openUpdatePage(t, h)
		h.click(tester, "检查更新")
		waitForText(t, tester, "可更新")
		if got := exactTextCount(tester.Texts(), "下载更新"); got != 0 {
			t.Fatalf("下载更新 buttons = %d, want 0 without a downloader; texts = %v", got, tester.Texts())
		}
	})

	t.Run("up to date with downloader", func(t *testing.T) {
		info := availableDownloadInfo()
		info.Available = false
		info.LatestVersion = "1.0.0"
		update := newFakeDownloadUpdate(info)
		h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
			update, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
		tester := openUpdatePage(t, h)
		h.click(tester, "检查更新")
		waitForText(t, tester, "已是最新")
		if got := exactTextCount(tester.Texts(), "下载更新"); got != 0 {
			t.Fatalf("下载更新 buttons = %d, want 0 when up to date; texts = %v", got, tester.Texts())
		}
	})
}

// TestUpdateDownloadSuccess covers the result panel of a verified download.
func TestUpdateDownloadSuccess(t *testing.T) {
	update := newFakeDownloadUpdate(availableDownloadInfo())
	update.setResult(stagedDownload())
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
	tester := openUpdatePage(t, h)
	h.click(tester, "检查更新")
	waitForText(t, tester, "可更新")
	h.click(tester, "下载更新")
	waitForText(t, tester, "已下载")

	h.assertTexts(tester, "已下载", "版本：1.2.0", "大小：2.0 MB", "SHA256 abcdef01",
		"暂存路径", "1.2.0-zeokeel-1.2.0.zip", "下载完成，可安排在下次启动安装")
	if got := update.downloadCount(); got != 1 {
		t.Fatalf("Download calls = %d, want 1", got)
	}
	// The download action is replaced by the result panel.
	if got := exactTextCount(tester.Texts(), "下载更新"); got != 0 {
		t.Fatalf("下载更新 buttons = %d after success, want 0; texts = %v", got, tester.Texts())
	}
}

// TestUpdateDownloadError covers a failed download: the page shows the danger
// row and no success panel.
func TestUpdateDownloadError(t *testing.T) {
	update := newFakeDownloadUpdate(availableDownloadInfo())
	update.setErr(errors.New("disk full"))
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
	tester := openUpdatePage(t, h)
	h.click(tester, "检查更新")
	waitForText(t, tester, "可更新")
	h.click(tester, "下载更新")
	waitForText(t, tester, "下载更新失败：disk full")
	h.assertNoTexts(tester, "已下载", "版本：1.2.0")
}

// TestUpdateDownloadingState covers the in-flight state: the button reads
// 下载中…, stays disabled, and the double click starts no second download.
func TestUpdateDownloadingState(t *testing.T) {
	update := newFakeDownloadUpdate(availableDownloadInfo())
	update.setResult(stagedDownload())
	update.block = true
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
	tester := openUpdatePage(t, h)
	h.click(tester, "检查更新")
	waitForText(t, tester, "可更新")

	h.click(tester, "下载更新")
	waitForDownloadStart(t, update)
	tester.Frame()
	h.assertTexts(tester, "下载中…")
	if err := tester.Click("下载中…"); err != nil {
		t.Fatalf("click downloading button: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := update.downloadCount(); got != 1 {
		t.Fatalf("Download calls while downloading = %d, want 1", got)
	}

	update.downloadRelease <- struct{}{}
	waitForText(t, tester, "已下载")
	if got := update.downloadCount(); got != 1 {
		t.Fatalf("Download calls = %d, want 1", got)
	}
}

// TestUpdateRepeatCheckClearsDownload covers a new check clearing the previous
// staged result (it may belong to another version).
func TestUpdateRepeatCheckClearsDownload(t *testing.T) {
	update := newFakeDownloadUpdate(availableDownloadInfo())
	update.setResult(stagedDownload())
	h := newHarnessWithUpdate(t, &fakeChat{}, newFakeSettings(nil), settings.Config{},
		update, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
	tester := openUpdatePage(t, h)
	h.click(tester, "检查更新")
	waitForText(t, tester, "可更新")
	h.click(tester, "下载更新")
	waitForText(t, tester, "已下载")

	h.click(tester, "检查更新")
	waitForCheckCount(t, update, 2)
	tester.Frame()
	h.assertNoTexts(tester, "已下载", "暂存路径")
}

// TestUpdateDownloadEnglishCopy covers the English labels of the download
// action and result panel.
func TestUpdateDownloadEnglishCopy(t *testing.T) {
	update := newFakeDownloadUpdate(availableDownloadInfo())
	update.setResult(stagedDownload())
	h := newLocaleHarnessWithUpdate(t, "en", update)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Update")
	h.click(tester, "Check for updates")
	waitForText(t, tester, "Update available")
	h.assertTexts(tester, "Download update")
	h.click(tester, "Download update")
	waitForText(t, tester, "Downloaded")
	h.assertTexts(tester, "Version: 1.2.0", "Size: 2.0 MB", "SHA256 abcdef01", "Staged file",
		"1.2.0-zeokeel-1.2.0.zip", "Download complete; schedule it for the next launch")
}

// waitForCheckCount waits for the asynchronous update check to land, so a slow
// CI machine cannot observe the click before the backend counted it.
func waitForCheckCount(t *testing.T, update *fakeDownloadUpdate, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if update.checkCount() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Check calls = %d, want %d", update.checkCount(), want)
}
