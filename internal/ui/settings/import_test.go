package settings_test

// Tests for the settings 导入 page: the optional importer, the DSH directory
// seam, the read-only scan preview (counts + skip notes), the import run and
// its result, the busy states and the error paths.

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

// fakeImporter is a scripted contracts.ImporterService. Scan and Import can
// block until a release channel closes, which exercises the busy states.
type fakeImporter struct {
	mu sync.Mutex

	preview   contracts.ImportPreview
	scanErr   error
	scanDir   string
	scanCalls int

	result      contracts.ImportResult
	importErr   error
	importDir   string
	importCalls int

	scanBlock     bool
	importBlock   bool
	scanStarted   chan struct{}
	scanRelease   chan struct{}
	importStarted chan struct{}
	importRelease chan struct{}
}

func newFakeImporter() *fakeImporter {
	return &fakeImporter{
		scanStarted:   make(chan struct{}, 4),
		scanRelease:   make(chan struct{}, 4),
		importStarted: make(chan struct{}, 4),
		importRelease: make(chan struct{}, 4),
	}
}

func (f *fakeImporter) Scan(ctx context.Context, dir string) (contracts.ImportPreview, error) {
	f.mu.Lock()
	f.scanCalls++
	f.scanDir = dir
	block := f.scanBlock
	preview, err := f.preview, f.scanErr
	f.mu.Unlock()
	if block {
		select {
		case f.scanStarted <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return contracts.ImportPreview{}, ctx.Err()
		case <-f.scanRelease:
		}
	}
	return preview, err
}

func (f *fakeImporter) Import(ctx context.Context, dir string) (contracts.ImportResult, error) {
	f.mu.Lock()
	f.importCalls++
	f.importDir = dir
	block := f.importBlock
	result, err := f.result, f.importErr
	f.mu.Unlock()
	if block {
		select {
		case f.importStarted <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return contracts.ImportResult{}, ctx.Err()
		case <-f.importRelease:
		}
	}
	return result, err
}

func (f *fakeImporter) setPreview(preview contracts.ImportPreview) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.preview = preview
}

func (f *fakeImporter) setResult(result contracts.ImportResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.result = result
}

func (f *fakeImporter) setScanErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scanErr = err
}

func (f *fakeImporter) setImportErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.importErr = err
}

func (f *fakeImporter) calls() (scanCalls, importCalls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.scanCalls, f.importCalls
}

func (f *fakeImporter) scannedDir() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.scanDir
}

func (f *fakeImporter) importedDir() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.importDir
}

func waitForImporterStart(t *testing.T, started chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the importer call to start")
	}
}

func openImportPage(t *testing.T, h *harness) *ui.Tester {
	t.Helper()
	tester := h.tester()
	tester.Frame()
	h.click(tester, "导入")
	return tester
}

func setImportPicker(t *testing.T, fn func() (string, error)) {
	t.Helper()
	previous := settings.ImportDirectoryPicker
	settings.ImportDirectoryPicker = func(string) (string, error) { return fn() }
	t.Cleanup(func() { settings.ImportDirectoryPicker = previous })
}

// typeDSHDir focuses the native directory input and types dir. The field's
// accessibly-labeled input carries the "输入"/" input" suffix (inputLabel), which
// is the interactive target the strict v0.9.0 Tester.Click requires.
func typeDSHDir(t *testing.T, h *harness, tester *ui.Tester, dir string) {
	t.Helper()
	h.click(tester, "DSH 数据目录 输入")
	tester.Type(dir)
	tester.Frame()
}

// --- tests ------------------------------------------------------------------

func TestImportPageWithoutBackend(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "导入")
	h.assertTexts(tester, "导入", "从 DSH 桌面版把会话和工作区搬过来", "导入服务未启用")
}

func TestImportBrowseSeamFillsDirectory(t *testing.T) {
	setImportPicker(t, func() (string, error) { return "/tmp/dsh-data", nil })
	importer := newFakeImporter()
	h := newHarnessWithImporter(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, importer)
	tester := openImportPage(t, h)
	h.assertTexts(tester, "DSH 数据目录", "浏览…", "扫描")

	h.click(tester, "浏览…")
	h.click(tester, "扫描")
	waitForText(t, tester, "扫描结果")
	if got := importer.scannedDir(); got != "/tmp/dsh-data" {
		t.Fatalf("Scan dir = %q, want the browsed directory", got)
	}

	// A picker failure is shown in DangerText and keeps the typed directory.
	setImportPicker(t, func() (string, error) { return "", errors.New("dialog failed") })
	h.click(tester, "浏览…")
	h.assertTexts(tester, "无法打开目录选择器：dialog failed")
}

func TestImportScanShowsPreview(t *testing.T) {
	importer := newFakeImporter()
	importer.setPreview(contracts.ImportPreview{
		SourceDir:  "/tmp/dsh",
		Sessions:   2,
		Messages:   10,
		Workspaces: 1,
		Skipped:    1,
		Notes: []string{
			"skipped 3 unsupported session logs",
			"found 2 sessions",
		},
	})
	h := newHarnessWithImporter(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, importer)
	tester := openImportPage(t, h)
	typeDSHDir(t, h, tester, "/tmp/dsh")
	h.click(tester, "扫描")
	waitForText(t, tester, "扫描结果")

	h.assertTexts(tester, "扫描结果", "会话", "消息", "工作区", "跳过", "10",
		"skipped 3 unsupported session logs", "found 2 sessions", "导入数据")
	if got := importer.scannedDir(); got != "/tmp/dsh" {
		t.Fatalf("Scan dir = %q, want /tmp/dsh", got)
	}
	scans, _ := importer.calls()
	if scans != 1 {
		t.Fatalf("Scan calls = %d, want 1", scans)
	}
}

func TestImportWritesAndShowsResult(t *testing.T) {
	importer := newFakeImporter()
	importer.setPreview(contracts.ImportPreview{Sessions: 2, Messages: 10, Workspaces: 1, Skipped: 1})
	importer.setResult(contracts.ImportResult{
		ImportedSessions:   2,
		ImportedMessages:   10,
		ImportedWorkspaces: 1,
		Skipped:            1,
		Errors:             []string{"session abc: corrupt tail"},
	})
	h := newHarnessWithImporter(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, importer)
	tester := openImportPage(t, h)
	typeDSHDir(t, h, tester, "/tmp/dsh")
	h.click(tester, "扫描")
	waitForText(t, tester, "扫描结果")
	h.click(tester, "导入数据")
	waitForText(t, tester, "导入结果")

	h.assertTexts(tester, "导入会话", "导入消息", "导入工作区", "10",
		"session abc: corrupt tail")
	if got := importer.importedDir(); got != "/tmp/dsh" {
		t.Fatalf("Import dir = %q, want /tmp/dsh", got)
	}
	_, imports := importer.calls()
	if imports != 1 {
		t.Fatalf("Import calls = %d, want 1", imports)
	}
}

func TestImportErrors(t *testing.T) {
	t.Run("scan", func(t *testing.T) {
		importer := newFakeImporter()
		importer.setScanErr(errors.New("bad directory"))
		h := newHarnessWithImporter(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, importer)
		tester := openImportPage(t, h)
		typeDSHDir(t, h, tester, "/tmp/nope")
		h.click(tester, "扫描")
		waitForText(t, tester, "扫描失败：bad directory")
		if got := exactTextCount(tester.Texts(), "导入数据"); got != 0 {
			t.Fatalf("import button labels = %d after a scan error, want 0", got)
		}
	})

	t.Run("import", func(t *testing.T) {
		importer := newFakeImporter()
		importer.setPreview(contracts.ImportPreview{Sessions: 1, Messages: 2})
		importer.setImportErr(errors.New("disk full"))
		h := newHarnessWithImporter(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, importer)
		tester := openImportPage(t, h)
		typeDSHDir(t, h, tester, "/tmp/dsh")
		h.click(tester, "扫描")
		waitForText(t, tester, "扫描结果")
		h.click(tester, "导入数据")
		waitForText(t, tester, "导入失败：disk full")
	})
}

func TestImportBusyStatesDisableActions(t *testing.T) {
	importer := newFakeImporter()
	importer.scanBlock = true
	importer.setPreview(contracts.ImportPreview{Sessions: 1, Messages: 2})
	h := newHarnessWithImporter(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, importer)
	tester := openImportPage(t, h)
	typeDSHDir(t, h, tester, "/tmp/dsh")

	h.click(tester, "扫描")
	waitForImporterStart(t, importer.scanStarted)
	tester.Frame()
	h.assertTexts(tester, "扫描中…")
	// The busy state is the official spinner, and the action is disabled.
	requireBusySpinner(t, tester, "扫描中…")

	// The scan button is disabled in flight: clicking it again starts nothing.
	if err := tester.Click("扫描中…"); err != nil {
		t.Fatalf("click scanning button: %v", err)
	}
	tester.Frame()
	if scans, _ := importer.calls(); scans != 1 {
		t.Fatalf("Scan calls while scanning = %d, want 1", scans)
	}
	importer.scanRelease <- struct{}{}
	waitForText(t, tester, "扫描结果")

	// The import button is disabled in flight too.
	importer.importBlock = true
	importer.setResult(contracts.ImportResult{ImportedSessions: 1})
	h.click(tester, "导入数据")
	waitForImporterStart(t, importer.importStarted)
	tester.Frame()
	h.assertTexts(tester, "导入中…")
	// The busy state is the official spinner, and the action is disabled.
	requireBusySpinner(t, tester, "导入中…")
	if err := tester.Click("导入中…"); err != nil {
		t.Fatalf("click importing button: %v", err)
	}
	tester.Frame()
	if _, imports := importer.calls(); imports != 1 {
		t.Fatalf("Import calls while importing = %d, want 1", imports)
	}
	importer.importRelease <- struct{}{}
	waitForText(t, tester, "导入结果")
}

func TestImportRepeatUpdatesResult(t *testing.T) {
	importer := newFakeImporter()
	importer.setPreview(contracts.ImportPreview{Sessions: 2, Messages: 3})
	importer.setResult(contracts.ImportResult{ImportedSessions: 2, ImportedMessages: 3})
	h := newHarnessWithImporter(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, importer)
	tester := openImportPage(t, h)
	typeDSHDir(t, h, tester, "/tmp/dsh")
	h.click(tester, "扫描")
	waitForText(t, tester, "扫描结果")
	h.click(tester, "导入数据")
	waitForText(t, tester, "导入结果")

	importer.setResult(contracts.ImportResult{ImportedSessions: 7, ImportedMessages: 11})
	h.click(tester, "导入数据")
	waitForText(t, tester, "11")
	if _, imports := importer.calls(); imports != 2 {
		t.Fatalf("Import calls = %d, want 2", imports)
	}
	h.assertTexts(tester, "7")
}
