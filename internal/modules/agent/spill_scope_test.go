package agent

// BE-11 (2026-11 audit): the spill directory followed the CURRENT workspace
// while the run's tool root follows the SESSION binding — a user switching
// workspaces mid-run (or an automation session bound to another project)
// spilled tool results into a directory outside the run's sandbox roots, where
// the model's read_file could not read them back (WithRoots contract). The
// spill policy now takes the run's captured session-bound root.

import (
	"context"
	"path/filepath"
	"testing"

	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestSpillPolicyUsesSessionBoundRootNotCurrent(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	workspace := &multiRootWorkspace{
		roots:     map[string]string{"w1": rootA, "w2": rootB},
		currentID: "w2", // the user is looking at another project
	}
	svc := newService(Config{}, rigtools.NewRegistry(),
		contracts.SettingsService(newFakeSettings(nil)), &fakeLogger{}, newFakeStore(), fakeApprovals{})
	svc.workspace = workspace
	id, err := svc.NewSession("bound")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetSessionWorkspace(id, "w1"); err != nil {
		t.Fatalf("SetSessionWorkspace: %v", err)
	}
	// The run captures its root once at start; the spill policy must use it.
	capturedRoot := svc.sessionWorkspaceRoot(id)
	if capturedRoot != rootA {
		t.Fatalf("session root = %q, want the bound %q", capturedRoot, rootA)
	}
	policy := svc.spillPolicy(context.Background(), capturedRoot)
	wantDir := filepath.Join(rootA, ".zeokeel", "tool-results")
	if policy.Dir != wantDir {
		t.Fatalf("BE-11: spill dir = %q, want the session-bound %q (Current is %q)", policy.Dir, wantDir, rootB)
	}
	// A mid-run workspace switch cannot move the captured spill directory.
	workspace.currentID = "w1"
	if again := svc.spillPolicy(context.Background(), capturedRoot); again.Dir != wantDir {
		t.Fatalf("spill dir moved after a workspace switch: %q", again.Dir)
	}
}

// An unbound session follows the current workspace (the existing fallback),
// resolved through the same session-root helper the run uses.
func TestSpillPolicyUnboundSessionFollowsCurrent(t *testing.T) {
	rootB := t.TempDir()
	workspace := &multiRootWorkspace{
		roots:     map[string]string{"w2": rootB},
		currentID: "w2",
	}
	svc := newService(Config{}, rigtools.NewRegistry(),
		contracts.SettingsService(newFakeSettings(nil)), &fakeLogger{}, newFakeStore(), fakeApprovals{})
	svc.workspace = workspace
	id, err := svc.NewSession("unbound")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	policy := svc.spillPolicy(context.Background(), svc.sessionWorkspaceRoot(id))
	if want := filepath.Join(rootB, ".zeokeel", "tool-results"); policy.Dir != want {
		t.Fatalf("spill dir = %q, want the current-workspace fallback %q", policy.Dir, want)
	}
}
