package toolkit

// contracts.ToolPolicy is the bridge the agent gate uses to judge a shell call:
// it must answer with the policy of the workspace root the run is bound to, not
// with whichever workspace happens to be current. A stale/mismatched policy
// would make the "already confined" exemption judge the wrong fence.

import (
	"path/filepath"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestPolicySourceFollowsRunRoot builds the toolkit for workspace B and then, as
// a run bound to another workspace A would, asks for A's policy: it must be A's
// build, and the current-workspace answer must stay B's.
func TestPolicySourceFollowsRunRoot(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	ctx, _, _, _ := activateMCPToolkitWithRoot(t, map[string]any{"root": rootB}, newMutableSettings(nil))

	source, err := ctx.Require(contracts.KeyToolPolicy)
	if err != nil {
		t.Fatalf("require tool policy: %v", err)
	}

	current := source.PolicyFor("")
	if current == nil {
		t.Fatal("PolicyFor(\"\") = nil, want the current workspace policy")
	}
	assertRoots(t, "current policy", current, rootB)

	run := source.PolicyFor(rootA)
	if run == nil {
		t.Fatalf("PolicyFor(%q) = nil, want the run workspace policy", rootA)
	}
	assertRoots(t, "run policy", run, rootA)
	if run == current {
		t.Fatal("run policy is the current workspace's policy object")
	}

	// Asking for A must not move the current workspace to A.
	again := source.PolicyFor("")
	if again == nil {
		t.Fatal("PolicyFor(\"\") = nil after a run-root lookup")
	}
	assertRoots(t, "current policy after run lookup", again, rootB)
}

// assertRoots checks that policy's only root is want (cleaned comparison).
func assertRoots(t *testing.T, label string, policy interface{ Roots() []string }, want string) {
	t.Helper()
	roots := policy.Roots()
	if len(roots) != 1 {
		t.Fatalf("%s roots = %v, want exactly [%q]", label, roots, want)
	}
	if filepath.Clean(roots[0]) != filepath.Clean(want) {
		t.Fatalf("%s root = %q, want %q", label, roots[0], want)
	}
}
