package shell

import (
	"fmt"
	"strings"
)

// Build metadata of the running binary. The build injects them through
// -ldflags "-X main.version=... -X main.commit=... -X main.buildTime=...";
// cmd/zeokeel passes them to internal/app, which calls SetBuildInfo at
// startup. A binary built without ldflags (a plain `go run`, a test) keeps
// these defaults, so the About and Diagnostics pages can always print
// something instead of a blank.
var (
	BuildVersion = "dev"
	BuildCommit  = "unknown"
	BuildTime    = "unknown"
)

// SetBuildInfo records the build metadata the host read from its ldflags. It
// is called by internal/app during startup, before the first frame; empty
// values keep the current default so a partial injection cannot erase it.
func SetBuildInfo(version, commit, buildTime string) {
	if v := strings.TrimSpace(version); v != "" {
		BuildVersion = v
	}
	if c := strings.TrimSpace(commit); c != "" {
		BuildCommit = c
	}
	if b := strings.TrimSpace(buildTime); b != "" {
		BuildTime = b
	}
}

// BuildVersionString is the one-line version string the About page, the
// Diagnostics page and "Copy diagnostics" show: the version, then the commit
// and build time when the build provided them.
func BuildVersionString() string {
	version := strings.TrimSpace(BuildVersion)
	if version == "" {
		version = "dev"
	}
	parts := make([]string, 0, 2)
	if commit := strings.TrimSpace(BuildCommit); commit != "" && commit != "unknown" {
		if len(commit) > 12 {
			commit = commit[:12]
		}
		parts = append(parts, "commit "+commit)
	}
	if built := strings.TrimSpace(BuildTime); built != "" && built != "unknown" {
		parts = append(parts, "built "+built)
	}
	if len(parts) == 0 {
		return version
	}
	return fmt.Sprintf("%s (%s)", version, strings.Join(parts, ", "))
}
