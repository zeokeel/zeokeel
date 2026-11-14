package agent

// ZK-13 regression: headless/ACP sessions have no registered workspace, so the
// spill policy must resolve the product data directory instead of resending
// oversized tool results inline forever. With neither a workspace nor a data
// directory there is no product directory to resolve; Rig then falls back to its
// own temp directory (rig/agent/spill.go), which these tests pin only as "not a
// crash / still a policy".

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSpillPolicyFallsBackToDataDir(t *testing.T) {
	svc, _, dataDir := newSpillService(t)

	policy := svc.spillPolicy(context.Background())
	if policy == nil {
		t.Fatal("spillPolicy = nil, want a policy")
	}
	wantDir := filepath.Join(dataDir, ".zeokeel", "tool-results")
	if policy.Dir != wantDir {
		t.Fatalf("spill dir = %q, want the data-dir fallback %q", policy.Dir, wantDir)
	}
	if !filepath.IsAbs(policy.Dir) {
		t.Fatalf("spill dir = %q, want an absolute path", policy.Dir)
	}
	if policy.ThresholdBytes != spillThresholdBytes || policy.MaxFiles != spillKeepFiles {
		t.Fatalf("spill policy = %+v, want %d bytes / %d files", policy, spillThresholdBytes, spillKeepFiles)
	}
}

// TestSpillDirWithoutWorkspaceOrDataDir pins the empty resolution: the product
// has no directory to offer, so the policy leaves Dir empty and Rig uses its own
// temp directory (rig/agent/spill.go) instead of the product writing inline.
func TestSpillDirWithoutWorkspaceOrDataDir(t *testing.T) {
	svc := newService(Config{}, nil, newFakeSettings(nil), &fakeLogger{}, newFakeStore(), fakeApprovals{})
	if dir := svc.spillDir(context.Background()); dir != "" {
		t.Fatalf("spillDir without workspace/data dir = %q, want empty", dir)
	}
	policy := svc.spillPolicy(context.Background())
	if policy == nil || policy.Dir != "" {
		t.Fatalf("spillPolicy = %+v, want a policy with an empty Dir", policy)
	}
}
