package update

// Tests for the install half of the update service (contracts.UpdateInstaller):
// scheduling a verified download as a pending install (pending.json), rejecting
// unsafe/invalid plans, reading the pending plan back and concurrent schedules.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// pendingFile is the on-disk schema shared with the process host.
type pendingFile struct {
	Version string `json:"version"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
}

// installService builds a service staged under dataDir.
func installService(t *testing.T, dataDir string) *service {
	t.Helper()
	service := newTestService(t, Config{}, "1.0.0")
	service.dataDir = dataDir
	return service
}

// writeStaged writes one staged asset under <dataDir>/updates and returns its
// path and SHA-256.
func writeStaged(t *testing.T, dataDir, name, content string) (string, string) {
	t.Helper()
	dir := filepath.Join(dataDir, updatesDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir updates: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write staged asset: %v", err)
	}
	sum := sha256.Sum256([]byte(content))
	return path, hex.EncodeToString(sum[:])
}

func pendingPath(dataDir string) string {
	return filepath.Join(dataDir, updatesDirName, "pending.json")
}

func TestScheduleInstallWritesPending(t *testing.T) {
	dataDir := t.TempDir()
	staged, sha := writeStaged(t, dataDir, "1.2.0-app.zip", "release content")
	service := installService(t, dataDir)

	plan, err := service.ScheduleInstall(contracts.UpdateDownload{
		Version: "1.2.0", Path: staged, SHA256: sha, Size: 15,
	})
	if err != nil {
		t.Fatalf("ScheduleInstall: %v", err)
	}
	if plan.Version != "1.2.0" || plan.Path != staged || plan.SHA256 != sha {
		t.Fatalf("plan = %+v, want the verified download", plan)
	}

	body, err := os.ReadFile(pendingPath(dataDir))
	if err != nil {
		t.Fatalf("read pending.json: %v", err)
	}
	var pending pendingFile
	if err := json.Unmarshal(body, &pending); err != nil {
		t.Fatalf("parse pending.json: %v", err)
	}
	if pending.Version != "1.2.0" || pending.Path != staged || pending.SHA256 != sha {
		t.Fatalf("pending.json = %+v, want the plan", pending)
	}
	info, err := os.Stat(pendingPath(dataDir))
	if err != nil {
		t.Fatalf("stat pending.json: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("pending.json mode = %o, want 0644", info.Mode().Perm())
	}

	pendingPlan, ok := service.PendingInstall()
	if !ok || pendingPlan != plan {
		t.Fatalf("PendingInstall = %+v/%v, want the scheduled plan", pendingPlan, ok)
	}
}

func TestScheduleInstallOverwritesPending(t *testing.T) {
	dataDir := t.TempDir()
	first, firstSHA := writeStaged(t, dataDir, "1.2.0-app.zip", "first")
	second, secondSHA := writeStaged(t, dataDir, "1.3.0-app.zip", "second")
	service := installService(t, dataDir)

	if _, err := service.ScheduleInstall(contracts.UpdateDownload{Version: "1.2.0", Path: first, SHA256: firstSHA}); err != nil {
		t.Fatalf("first ScheduleInstall: %v", err)
	}
	if _, err := service.ScheduleInstall(contracts.UpdateDownload{Version: "1.3.0", Path: second, SHA256: secondSHA}); err != nil {
		t.Fatalf("second ScheduleInstall: %v", err)
	}
	plan, ok := service.PendingInstall()
	if !ok || plan.Version != "1.3.0" || plan.Path != second {
		t.Fatalf("PendingInstall = %+v/%v, want the second plan", plan, ok)
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, updatesDirName))
	if err != nil {
		t.Fatalf("ReadDir updates: %v", err)
	}
	count := 0
	for _, entry := range entries {
		if entry.Name() == "pending.json" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("pending.json files = %d, want one", count)
	}
}

func TestScheduleInstallRejectsInvalidPlans(t *testing.T) {
	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "escape.zip")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside asset: %v", err)
	}

	tests := []struct {
		name     string
		prepare  func(t *testing.T, dataDir string) contracts.UpdateDownload
		wantData bool
	}{
		{
			name: "path outside updates",
			prepare: func(t *testing.T, dataDir string) contracts.UpdateDownload {
				return contracts.UpdateDownload{Version: "1.2.0", Path: outsidePath, SHA256: sha256Hex([]byte("outside"))}
			},
		},
		{
			name: "missing staged file",
			prepare: func(t *testing.T, dataDir string) contracts.UpdateDownload {
				dir := filepath.Join(dataDir, updatesDirName)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("mkdir updates: %v", err)
				}
				return contracts.UpdateDownload{Version: "1.2.0", Path: filepath.Join(dir, "missing.zip"), SHA256: sha256Hex([]byte("x"))}
			},
		},
		{
			name: "sha mismatch",
			prepare: func(t *testing.T, dataDir string) contracts.UpdateDownload {
				staged, _ := writeStaged(t, dataDir, "1.2.0-app.zip", "real")
				return contracts.UpdateDownload{Version: "1.2.0", Path: staged, SHA256: sha256Hex([]byte("different"))}
			},
		},
		{
			name: "empty version",
			prepare: func(t *testing.T, dataDir string) contracts.UpdateDownload {
				staged, sha := writeStaged(t, dataDir, "1.2.0-app.zip", "real")
				return contracts.UpdateDownload{Version: "   ", Path: staged, SHA256: sha}
			},
		},
		{
			name: "empty sha",
			prepare: func(t *testing.T, dataDir string) contracts.UpdateDownload {
				staged, _ := writeStaged(t, dataDir, "1.2.0-app.zip", "real")
				return contracts.UpdateDownload{Version: "1.2.0", Path: staged}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataDir := t.TempDir()
			service := installService(t, dataDir)
			if _, err := service.ScheduleInstall(test.prepare(t, dataDir)); !errors.Is(err, ErrInstall) {
				t.Fatalf("ScheduleInstall = %v, want ErrInstall", err)
			}
			if _, ok := service.PendingInstall(); ok {
				t.Fatal("a rejected plan was written as pending")
			}
		})
	}
}

func TestScheduleInstallWithoutDataDir(t *testing.T) {
	service := newTestService(t, Config{}, "1.0.0")
	_, err := service.ScheduleInstall(contracts.UpdateDownload{Version: "1.2.0", Path: "/x", SHA256: "abc"})
	if !errors.Is(err, ErrNoDataDir) {
		t.Fatalf("ScheduleInstall without dataDir = %v, want ErrNoDataDir", err)
	}
}

func TestPendingInstallMissingAndBadJSON(t *testing.T) {
	dataDir := t.TempDir()
	service := installService(t, dataDir)
	if plan, ok := service.PendingInstall(); ok || plan != (contracts.UpdateInstallPlan{}) {
		t.Fatalf("PendingInstall(no file) = %+v/%v, want false", plan, ok)
	}

	if err := os.MkdirAll(filepath.Join(dataDir, updatesDirName), 0o755); err != nil {
		t.Fatalf("mkdir updates: %v", err)
	}
	if err := os.WriteFile(pendingPath(dataDir), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write bad pending: %v", err)
	}
	if plan, ok := service.PendingInstall(); ok {
		t.Fatalf("PendingInstall(bad JSON) = %+v, want false", plan)
	}
}

func TestPendingInstallRejectsUnsafePath(t *testing.T) {
	dataDir := t.TempDir()
	service := installService(t, dataDir)
	updates := filepath.Join(dataDir, updatesDirName)
	if err := os.MkdirAll(updates, 0o755); err != nil {
		t.Fatalf("mkdir updates: %v", err)
	}
	body, err := json.Marshal(pendingFile{Version: "1.2.0", Path: filepath.Join(dataDir, "outside.zip"), SHA256: "abc"})
	if err != nil {
		t.Fatalf("marshal pending: %v", err)
	}
	if err := os.WriteFile(pendingPath(dataDir), body, 0o644); err != nil {
		t.Fatalf("write pending: %v", err)
	}
	if plan, ok := service.PendingInstall(); ok {
		t.Fatalf("PendingInstall(unsafe path) = %+v, want false", plan)
	}
}

func TestScheduleInstallConcurrent(t *testing.T) {
	dataDir := t.TempDir()
	staged, sha := writeStaged(t, dataDir, "1.2.0-app.zip", "concurrent")
	service := installService(t, dataDir)
	download := contracts.UpdateDownload{Version: "1.2.0", Path: staged, SHA256: sha}

	var group sync.WaitGroup
	errs := make([]error, 8)
	for index := range errs {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, errs[index] = service.ScheduleInstall(download)
		}(index)
	}
	group.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("concurrent ScheduleInstall #%d: %v", index, err)
		}
	}
	plan, ok := service.PendingInstall()
	if !ok || plan.Version != "1.2.0" || plan.Path != staged || plan.SHA256 != sha {
		t.Fatalf("PendingInstall after concurrent schedules = %+v/%v", plan, ok)
	}
}
