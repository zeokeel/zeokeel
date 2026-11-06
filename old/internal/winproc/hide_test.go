package winproc

import (
	"os/exec"
	"testing"
)

// TestHideIsSafeOnEveryPlatform pins the no-op contract that lets every
// call site call Hide unconditionally: a nil command is ignored and a plain
// command remains usable. The Windows flag values are covered by the
// windows-only test and by the cross build.
func TestHideIsSafeOnEveryPlatform(t *testing.T) {
	Hide(nil)
	cmd := exec.Command("echo", "ok")
	Hide(cmd)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("command after Hide failed: %v", err)
	}
	if string(out) != "ok\n" {
		t.Fatalf("command output = %q, want ok", out)
	}
}
