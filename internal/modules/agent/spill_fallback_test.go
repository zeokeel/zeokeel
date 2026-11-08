package agent

// ZK-13 regression: headless/ACP sessions have no registered workspace, so the
// spill fallback must use the product data directory instead of resending
// oversized tool results inline forever.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rigtools "github.com/zeokeel/zeokeel-rig/tools"
)

func TestSpillDirFallsBackToDataDir(t *testing.T) {
	dataDir := t.TempDir()
	svc := newService(Config{}, rigtools.NewRegistry(), newFakeSettings(nil),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	svc.dataDir = dataDir

	dir := svc.spillDir(context.Background())
	if dir == "" {
		t.Fatal("spillDir = \"\", want the data-dir fallback")
	}
	if !strings.HasPrefix(dir, dataDir+string(filepath.Separator)) {
		t.Fatalf("spillDir = %q, want it under the data dir %q", dir, dataDir)
	}

	inner := &recordingHistory{}
	history := newSpillHistory(inner, svc.spillDir, &fakeLogger{})
	full := strings.Repeat("result", spillThresholdBytes)
	if err := history.Append(context.Background(), "sess-1", toolMessage(full)); err != nil {
		t.Fatalf("Append: %v", err)
	}
	persisted := inner.persisted()[0].Text()
	if !strings.Contains(persisted, spillDetectMarker) {
		t.Fatalf("persisted text lacks the spill marker: %q", persisted[:min(len(persisted), 120)])
	}
	start := strings.Index(persisted, spillMarkerPrefix)
	end := strings.Index(persisted, spillMarkerSuffix)
	if start < 0 || end < start {
		t.Fatalf("persisted text has no spill path: %q", persisted[:min(len(persisted), 200)])
	}
	path := persisted[start+len(spillMarkerPrefix) : end]
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("spill file %q: %v", path, err)
	}
}
