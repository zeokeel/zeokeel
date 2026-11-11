package uimodtest

// TestMain lets the OS-sandbox helper re-exec this test binary: without it the
// Landlock helper spawned by the sandbox module would recursively run the whole
// test suite on hosts without bwrap (for example the GitHub CI runner).

import (
	"os"
	"testing"

	"github.com/zeokeel/zeokeel-rig/sandbox/osrunner"
)

func TestMain(m *testing.M) {
	if osrunner.MaybeRunHelper(os.Args) {
		return
	}
	os.Exit(m.Run())
}
