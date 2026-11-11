// Command zeokeel is ZeoKeel's product entry point.
//
// It is deliberately thin: parse nothing, own nothing, build nothing. The
// process host (internal/host) owns the command line, the window, the kernel
// assembly and the shutdown sequence; the assembly (internal/assembly) owns
// which modules exist and which product enables them.
package main

import (
	"os"

	"github.com/zeokeel/zeokeel-rig/sandbox/osrunner"

	"github.com/zeokeel/zeokeel/internal/host"
)

func main() {
	// The OS sandbox runner re-executes this binary as a hidden helper before
	// any flag parsing or window creation (Linux Landlock / Windows ACL).
	if osrunner.MaybeRunHelper(os.Args) {
		return
	}
	os.Exit(host.Main(os.Args[1:]))
}
