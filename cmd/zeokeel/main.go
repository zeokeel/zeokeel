// Command zeokeel is ZeoKeel's product entry point.
//
// It is deliberately thin: parse nothing, own nothing, build nothing. The
// process host (internal/host) owns the command line, the window, the kernel
// assembly and the shutdown sequence; the assembly (internal/assembly) owns
// which modules exist and which product enables them.
package main

import (
	"os"

	"github.com/zeokeel/zeokeel/internal/host"
)

func main() {
	os.Exit(host.Main(os.Args[1:]))
}
