//go:build windows

package winproc

import (
	"os/exec"
	"testing"
)

// TestHideSetsWindowsCreationFlags pins the exact flags the Windows GUI build
// relies on. It only runs on Windows; the cross build (GOOS=windows go vet)
// keeps it compiling on the Linux dev/CI host.
func TestHideSetsWindowsCreationFlags(t *testing.T) {
	cmd := exec.Command("cmd", "/C", "echo", "ok")
	Hide(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("Hide left SysProcAttr nil")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow not set")
	}
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_NO_WINDOW set", cmd.SysProcAttr.CreationFlags)
	}
}
