package app

import (
	"strings"

	"github.com/zeokeel/zeokeel/internal/shell"
)

// Build metadata of the running binary (About and Diagnostics). cmd/zeokeel
// injects them through -ldflags into main-package variables and calls
// app.SetBuildInfo before app.Run; a plain `go run`, a test or a build without
// ldflags keeps these defaults. They are plain variables (not consts) because
// SetBuildInfo fills them at startup.
var (
	BuildVersion = "dev"
	BuildCommit  = "unknown"
	BuildTime    = "unknown"
)

// SetBuildInfo records the build metadata main resolved from its ldflags.
// Empty values keep the current value, so a partial injection cannot erase a
// version main did provide. app.Run forwards the result to internal/shell,
// where the About and Diagnostics pages read it.
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

// buildInfoString is the one-line version string app-level diagnostics use.
func buildInfoString() string {
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
	return version + " (" + strings.Join(parts, ", ") + ")"
}

// publishBuildInfo forwards the app-level build metadata to the shell, which
// renders it in About/Diagnostics. Run calls it once before the window exists,
// so the first frame already shows the real version.
func publishBuildInfo() {
	shell.SetBuildInfo(BuildVersion, BuildCommit, BuildTime)
}
