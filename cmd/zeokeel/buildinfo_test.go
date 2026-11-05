package main

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/app"
)

// TestInstallBuildInfo covers the cmd→app build-info wiring: the versions
// injected into the main package by -ldflags must reach internal/app (which
// forwards them to the shell's About/Diagnostics).
func TestInstallBuildInfo(t *testing.T) {
	oldVersion, oldCommit, oldBuild := version, commit, buildTime
	oldAppVersion, oldAppCommit, oldAppBuild := app.BuildVersion, app.BuildCommit, app.BuildTime
	defer func() {
		version, commit, buildTime = oldVersion, oldCommit, oldBuild
		app.BuildVersion, app.BuildCommit, app.BuildTime = oldAppVersion, oldAppCommit, oldAppBuild
	}()

	version, commit, buildTime = "1.2.3", "abc1234", "2026-11-05T12:00:00Z"
	installBuildInfo()

	if app.BuildVersion != "1.2.3" || app.BuildCommit != "abc1234" || app.BuildTime != "2026-11-05T12:00:00Z" {
		t.Fatalf("app build info = (%q, %q, %q), want the main-package values",
			app.BuildVersion, app.BuildCommit, app.BuildTime)
	}

	// Empty injected values must not erase a value main already provided.
	version, commit, buildTime = "", "", ""
	installBuildInfo()
	if app.BuildVersion != "1.2.3" || app.BuildCommit != "abc1234" {
		t.Fatalf("empty injection erased build info: (%q, %q)", app.BuildVersion, app.BuildCommit)
	}
}
