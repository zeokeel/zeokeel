package main

import (
	"fmt"
	"runtime"
)

// Build metadata injected by scripts/build.sh through
//
//	-ldflags "-X main.version=... -X main.commit=... -X main.buildTime=..."
//
// The defaults are what a plain `go build ./cmd/zeokeel` or `go run` reports;
// a release build always overrides all three.
var (
	version   = "0.1.0-dev"
	commit    = "unknown"
	buildTime = "unknown"
)

// versionString is the single line printed by --version and written to the
// log/crash headers. It deliberately carries the runtime facts a bug report
// needs (platform + toolchain) so field reports are actionable.
func versionString() string {
	return fmt.Sprintf("zeokeel %s (commit %s, built %s, %s/%s, %s)",
		version, commit, buildTime, runtime.GOOS, runtime.GOARCH, runtime.Version())
}

// shortVersion is the version without the platform decoration, for one-line
// status messages.
func shortVersion() string { return version }
