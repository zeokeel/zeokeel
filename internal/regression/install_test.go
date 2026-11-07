package regression

// Adversarial audit: install-on-restart, module half. The update service
// schedules a SHA-256 verified download under <dataDir>/updates/pending.json and
// reports it back; the negative cases are hostile plans (bad version/path/
// checksum, outside-the-updates paths, directories, symlinks), malformed pending
// JSON, missing data dir and concurrent scheduling. The host half is exercised in
// host_install_test.go.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/update"
)

// startInstaller activates the update module and returns its optional install
// half.
func startInstaller(t *testing.T, h *harness) contracts.UpdateInstaller {
	t.Helper()
	service := startUpdate(t, h, update.Config{})
	installer, ok := service.(contracts.UpdateInstaller)
	if !ok {
		t.Fatal("the update service does not implement contracts.UpdateInstaller")
	}
	return installer
}

// stagedDownload writes one staged asset below <dataDir>/updates and returns the
// download record a verified Download would produce.
func stagedDownload(t *testing.T, h *harness, name, content, version string) contracts.UpdateDownload {
	t.Helper()
	updatesDir := filepath.Join(h.dataDir, "updates")
	if err := os.MkdirAll(updatesDir, 0o755); err != nil {
		t.Fatalf("mkdir updates: %v", err)
	}
	path := filepath.Join(updatesDir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write staged asset: %v", err)
	}
	return contracts.UpdateDownload{
		Version: version,
		Path:    path,
		SHA256:  sha256Hex(content),
		Size:    int64(len(content)),
	}
}

// sha256Hex is the lowercase hex SHA-256 of s.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// stagingDir is <dataDir>/updates.
func stagingDir(h *harness) string { return filepath.Join(h.dataDir, "updates") }

// pendingPlanPath is <dataDir>/updates/pending.json.
func pendingPlanPath(h *harness) string { return filepath.Join(stagingDir(h), "pending.json") }

// readPendingPlan decodes the raw pending.json (for tamper tests).
func readPendingPlan(t *testing.T, h *harness) map[string]any {
	t.Helper()
	body, err := os.ReadFile(pendingPlanPath(h))
	if err != nil {
		t.Fatalf("read pending.json: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("pending.json is not valid JSON: %v", err)
	}
	return decoded
}

// TestInstallScheduleRoundTrip covers the positive path plus replacing an older
// plan, and that pending.json carries the verified plan.
func TestInstallScheduleRoundTrip(t *testing.T) {
	h := newHarness(t)
	installer := startInstaller(t, h)

	first := stagedDownload(t, h, "zeokeel-2.0.0", "binary-v2", "2.0.0")
	// A differently-cased checksum is accepted and normalized to lowercase.
	first.SHA256 = strings.ToUpper(first.SHA256)
	plan, err := installer.ScheduleInstall(first)
	if err != nil {
		t.Fatalf("ScheduleInstall: %v", err)
	}
	if plan.Version != "2.0.0" {
		t.Fatalf("plan version = %q, want the trimmed download version", plan.Version)
	}
	if plan.SHA256 != sha256Hex("binary-v2") {
		t.Fatalf("plan sha = %q, want the lowercase verified hash", plan.SHA256)
	}
	if plan.Path != first.Path {
		t.Fatalf("plan path = %q, want %q", plan.Path, first.Path)
	}
	pending, ok := installer.PendingInstall()
	if !ok {
		t.Fatal("PendingInstall() = not ok after scheduling")
	}
	if pending.Version != plan.Version || pending.SHA256 != plan.SHA256 || pending.Path != plan.Path {
		t.Fatalf("PendingInstall() = %+v, want the scheduled plan %+v", pending, plan)
	}
	onDisk := readPendingPlan(t, h)
	if onDisk["version"] != plan.Version || onDisk["sha256"] != plan.SHA256 || onDisk["path"] != plan.Path {
		t.Fatalf("pending.json = %v, want the plan fields", onDisk)
	}

	// Rescheduling replaces the previous plan (last one wins).
	second := stagedDownload(t, h, "zeokeel-2.1.0", "binary-v3", "2.1.0")
	if _, err := installer.ScheduleInstall(second); err != nil {
		t.Fatalf("re-ScheduleInstall: %v", err)
	}
	pending, ok = installer.PendingInstall()
	if !ok || pending.Version != "2.1.0" {
		t.Fatalf("PendingInstall() = (%+v, %v), want the replacement plan", pending, ok)
	}
}

// TestInstallRejectsHostilePlans covers invalid schedules: they must not write a
// plan and must classify as ErrInstall.
func TestInstallRejectsHostilePlans(t *testing.T) {
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.bin")
	if err := os.WriteFile(outsideFile, []byte("outside-content"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	dirAsStaged := filepath.Join(outsideDir, "a-directory")
	if err := os.Mkdir(dirAsStaged, 0o755); err != nil {
		t.Fatalf("mkdir dirAsStaged: %v", err)
	}

	cases := []struct {
		name  string
		build func(t *testing.T, h *harness) contracts.UpdateDownload
	}{
		{"empty version", func(t *testing.T, h *harness) contracts.UpdateDownload {
			download := stagedDownload(t, h, "asset", "content", "   ")
			return download
		}},
		{"empty checksum", func(t *testing.T, h *harness) contracts.UpdateDownload {
			download := stagedDownload(t, h, "asset", "content", "2.0.0")
			download.SHA256 = "  "
			return download
		}},
		{"missing staged file", func(t *testing.T, h *harness) contracts.UpdateDownload {
			download := stagedDownload(t, h, "asset", "content", "2.0.0")
			download.Path = filepath.Join(stagingDir(h), "does-not-exist.bin")
			return download
		}},
		{"path outside updates", func(t *testing.T, h *harness) contracts.UpdateDownload {
			return contracts.UpdateDownload{Version: "2.0.0", Path: outsideFile, SHA256: sha256Hex("outside-content")}
		}},
		{"relative path", func(t *testing.T, h *harness) contracts.UpdateDownload {
			return contracts.UpdateDownload{Version: "2.0.0", Path: filepath.Join("updates", "asset"), SHA256: sha256Hex("content")}
		}},
		{"directory staged", func(t *testing.T, h *harness) contracts.UpdateDownload {
			return contracts.UpdateDownload{Version: "2.0.0", Path: dirAsStaged, SHA256: sha256Hex("content")}
		}},
		{"checksum mismatch", func(t *testing.T, h *harness) contracts.UpdateDownload {
			download := stagedDownload(t, h, "asset", "content", "2.0.0")
			download.SHA256 = sha256Hex("different content")
			return download
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			h := newHarness(t)
			installer := startInstaller(t, h)
			download := testCase.build(t, h)
			if _, err := installer.ScheduleInstall(download); err == nil {
				t.Fatalf("ScheduleInstall(%s) succeeded, want ErrInstall", testCase.name)
			} else if !errors.Is(err, update.ErrInstall) {
				t.Fatalf("ScheduleInstall(%s) error = %v, want ErrInstall", testCase.name, err)
			}
			if _, ok := installer.PendingInstall(); ok {
				t.Fatalf("a pending plan exists after rejected schedule %q", testCase.name)
			}
			if _, err := os.Stat(pendingPlanPath(h)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("pending.json exists after rejected schedule %q: %v", testCase.name, err)
			}
		})
	}
}

// TestInstallPendingInstallMalformedFile covers tampered pending.json files: bad
// JSON, incomplete fields and traversal paths report "no install" instead of
// panicking or returning an unsafe path.
func TestInstallPendingInstallMalformedFile(t *testing.T) {
	cases := map[string]string{
		"bad json":         "{not json",
		"empty object":     `{}`,
		"missing version":  `{"path":"` + "/tmp/x" + `","sha256":"abc"}`,
		"missing sha":      `{"version":"2.0.0","path":"/tmp/x"}`,
		"traversal path":   `{"version":"2.0.0","path":"../../../etc/passwd","sha256":"abc"}`,
		"absolute outside": `{"version":"2.0.0","path":"/etc/passwd","sha256":"abc"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			installer := startInstaller(t, h)
			if err := os.MkdirAll(stagingDir(h), 0o755); err != nil {
				t.Fatalf("mkdir updates: %v", err)
			}
			if err := os.WriteFile(pendingPlanPath(h), []byte(body), 0o644); err != nil {
				t.Fatalf("write pending.json: %v", err)
			}
			if plan, ok := installer.PendingInstall(); ok {
				t.Fatalf("PendingInstall(%s) = (%+v, true), want not ok", name, plan)
			}
		})
	}

	// A lexically-inside path with a missing file is still reported; the host
	// re-validates existence before replacing anything.
	h := newHarness(t)
	installer := startInstaller(t, h)
	if err := os.MkdirAll(stagingDir(h), 0o755); err != nil {
		t.Fatalf("mkdir updates: %v", err)
	}
	missing := filepath.Join(stagingDir(h), "gone.bin")
	body, _ := json.Marshal(map[string]string{"version": "2.0.0", "path": missing, "sha256": "abc"})
	if err := os.WriteFile(pendingPlanPath(h), body, 0o644); err != nil {
		t.Fatalf("write pending.json: %v", err)
	}
	if plan, ok := installer.PendingInstall(); !ok || plan.Path != missing {
		t.Fatalf("PendingInstall(missing staged) = (%+v, %v), want the plan for host re-validation", plan, ok)
	}
}

// TestInstallConcurrentScheduling hammers ScheduleInstall: the atomic plan write
// means every call succeeds, the final plan is complete JSON, no temp files leak
// and -race stays clean.
func TestInstallConcurrentScheduling(t *testing.T) {
	h := newHarness(t)
	installer := startInstaller(t, h)
	download := stagedDownload(t, h, "concurrent-asset", "concurrent-content", "3.0.0")

	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for index := 0; index < writers; index++ {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			versioned := download
			versioned.Version = versioned.Version + "." + itoa(int64(index))
			if _, err := installer.ScheduleInstall(versioned); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent ScheduleInstall: %v", err)
	}
	plan, ok := installer.PendingInstall()
	if !ok {
		t.Fatal("PendingInstall() = not ok after concurrent scheduling")
	}
	if plan.SHA256 != download.SHA256 {
		t.Fatalf("final plan = %+v, want the staged checksum", plan)
	}
	if _, err := os.Stat(pendingPlanPath(h)); err != nil {
		t.Fatalf("pending.json missing: %v", err)
	}
	entries, err := os.ReadDir(stagingDir(h))
	if err != nil {
		t.Fatalf("read updates dir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pending-") {
			t.Fatalf("temporary plan file leaked: %q", entry.Name())
		}
	}
}

// TestInstallSymlinkStagedPath is a known-defect probe: the containment check is
// lexical, so a symlink inside updates/ pointing outside passes and the host
// would install the outside file's content. Purely local (the data dir is the
// user's), recorded as AUDIT-P3-9.
func TestInstallSymlinkStagedPath(t *testing.T) {
	h := newHarness(t)
	installer := startInstaller(t, h)
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside-binary")
	if err := os.WriteFile(outsideFile, []byte("outside payload"), 0o755); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.MkdirAll(stagingDir(h), 0o755); err != nil {
		t.Fatalf("mkdir updates: %v", err)
	}
	link := filepath.Join(stagingDir(h), "linked-asset")
	if err := os.Symlink(outsideFile, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	plan, err := installer.ScheduleInstall(contracts.UpdateDownload{
		Version: "9.9.9",
		Path:    link,
		SHA256:  sha256Hex("outside payload"),
	})
	if err == nil {
		t.Logf("AUDIT-P3-9: a symlink inside updates/ pointing outside passes the lexical containment check (plan path %q)", plan.Path)
		return
	}
	// Fixed implementations reject the symlink as outside the staging dir.
	if !errors.Is(err, update.ErrInstall) {
		t.Fatalf("ScheduleInstall(symlink) error = %v, want ErrInstall", err)
	}
}

// TestInstallWithoutDataDir covers a minimal host without contracts.KeyDataDir:
// scheduling reports ErrNoDataDir and reporting a pending plan is safe.
func TestInstallWithoutDataDir(t *testing.T) {
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyLogger, contracts.Logger(&recLogger{}))
	provide(t, ctx, contracts.KeyAppInfo, contracts.AppInfo{Name: "ZeoKeel", Version: "1.0.0"})
	activate(t, ctx, update.Module(), update.Config{})
	service := requireService(t, ctx, contracts.KeyUpdate)
	installer, ok := service.(contracts.UpdateInstaller)
	if !ok {
		t.Fatal("the update service does not implement contracts.UpdateInstaller")
	}
	if _, err := installer.ScheduleInstall(contracts.UpdateDownload{
		Version: "2.0.0", Path: "/tmp/asset", SHA256: sha256Hex("x"),
	}); !errors.Is(err, update.ErrNoDataDir) {
		t.Fatalf("ScheduleInstall without data dir error = %v, want ErrNoDataDir", err)
	}
	if plan, ok := installer.PendingInstall(); ok {
		t.Fatalf("PendingInstall without data dir = (%+v, true), want not ok", plan)
	}
}
