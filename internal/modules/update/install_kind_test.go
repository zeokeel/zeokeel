package update

// Tests for the pending.json install kind: derived from the staged file name,
// written by ScheduleInstall, normalized by PendingInstall and backward
// compatible with plans written before the field existed.

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// pendingFileWithKind mirrors pending.json including the kind field.
type pendingFileWithKind struct {
	Version string `json:"version"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Kind    string `json:"kind"`
}

func TestScheduleInstallWritesKind(t *testing.T) {
	cases := []struct {
		stagedName string
		wantKind   string
	}{
		{"1.2.0-zeokeel-1.2.0-linux-amd64.AppImage", "appimage"},
		{"1.2.0-zeokeel-1.2.0-linux-arm64.APPIMAGE", "appimage"},
		{"1.2.0-zeokeel-1.2.0-windows-amd64.tar.gz", "archive"},
		{"1.2.0-zeokeel-1.2.0-darwin-universal.tar.gz", "archive"},
		{"1.2.0-zeokeel-1.2.0-linux-amd64.tgz", "archive"},
		{"1.2.0-zeokeel-1.2.0-linux-amd64.zip", "archive"},
		{"1.2.0-zeokeel-1.2.0-windows-amd64.exe", "binary"},
		{"1.2.0-zeokeel", "binary"},
	}
	for _, testCase := range cases {
		t.Run(testCase.stagedName, func(t *testing.T) {
			dataDir := t.TempDir()
			staged, sha := writeStaged(t, dataDir, testCase.stagedName, "staged content")
			service := installService(t, dataDir)
			if _, err := service.ScheduleInstall(contracts.UpdateDownload{
				Version: "1.2.0", Path: staged, SHA256: sha,
			}); err != nil {
				t.Fatalf("ScheduleInstall: %v", err)
			}
			body, err := os.ReadFile(pendingPath(dataDir))
			if err != nil {
				t.Fatalf("read pending.json: %v", err)
			}
			var pending pendingFileWithKind
			if err := json.Unmarshal(body, &pending); err != nil {
				t.Fatalf("parse pending.json: %v", err)
			}
			if pending.Kind != testCase.wantKind {
				t.Fatalf("pending.json kind = %q, want %q (body %s)", pending.Kind, testCase.wantKind, body)
			}
		})
	}
}

func TestPendingInstallKindDefaults(t *testing.T) {
	cases := map[string]string{
		"":          "binary",
		"binary":    "binary",
		"appimage":  "appimage",
		"APPIMAGE":  "appimage",
		"archive":   "archive",
		"  tar":     "binary",
		"installer": "binary",
	}
	for kind, want := range cases {
		if got := pendingInstallKind(kind); got != want {
			t.Fatalf("pendingInstallKind(%q) = %q, want %q", kind, got, want)
		}
	}
}

func TestPendingInstallBackwardCompatibleWithoutKind(t *testing.T) {
	dataDir := t.TempDir()
	staged, sha := writeStaged(t, dataDir, "1.2.0-app.zip", "old plan content")
	// A plan written by an older build: no "kind" field at all.
	body, err := json.Marshal(map[string]string{
		"version": "1.2.0",
		"path":    staged,
		"sha256":  sha,
	})
	if err != nil {
		t.Fatalf("marshal old pending plan: %v", err)
	}
	if err := os.WriteFile(pendingPath(dataDir), body, 0o644); err != nil {
		t.Fatalf("write old pending plan: %v", err)
	}
	service := installService(t, dataDir)
	plan, ok := service.PendingInstall()
	if !ok {
		t.Fatal("PendingInstall rejected an old plan without kind")
	}
	if plan.Version != "1.2.0" || plan.Path != staged || plan.SHA256 != sha {
		t.Fatalf("PendingInstall = %+v, want the old plan", plan)
	}
}
