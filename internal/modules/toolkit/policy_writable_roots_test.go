package toolkit

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel-rig/sandbox"
)

func TestNewPolicyAddsConfiguredWritableRoots(t *testing.T) {
	root := t.TempDir()
	extra := filepath.Join(t.TempDir(), "tool-state")
	if err := os.Mkdir(extra, 0o700); err != nil {
		t.Fatalf("mkdir extra: %v", err)
	}
	policy := newPolicy(Config{WritableRoots: []string{extra}}, root, time.Second, sandbox.ModeWorkspaceWrite)
	found := false
	for _, got := range policy.AdditionalWritableRoots() {
		if got == extra {
			found = true
		}
	}
	if !found {
		t.Fatalf("AdditionalWritableRoots() = %v, want %q", policy.AdditionalWritableRoots(), extra)
	}
}
