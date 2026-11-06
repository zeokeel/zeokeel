package app

import "testing"

// TestQuitApplicationRunsSeam covers the tray's Quit path, the only native menu
// left after the window menu bar was retired (ADR 0006): quitApplication ends
// the process through the quit seam and marks the app as quitting, so the
// close-to-hide handlers stand down while the app winds down.
func TestQuitApplicationRunsSeam(t *testing.T) {
	a := &app{}
	quits := 0
	a.quitApp = func() { quits++ }
	a.quitApplication()
	if quits != 1 {
		t.Fatalf("quit seam ran %d times, want 1", quits)
	}
	if !a.quitting {
		t.Fatal("quitApplication did not mark the app as quitting")
	}
}
