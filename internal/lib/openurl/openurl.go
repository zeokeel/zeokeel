// Package openurl opens one URL in the platform's default handler. UI surfaces
// use it for OAuth authorization; tests replace Open to stay headless.
//
// The launcher is the platform's own command (open / xdg-open / rundll32) rather
// than a UI framework call: the product has no native window any more, and an
// authorization flow that needed one would not work in the browser client either.
// The command runs detached from this process's lifetime, so a caller may fire it
// from a background goroutine.
package openurl

import (
	"fmt"
	"os/exec"
	goruntime "runtime"
	"strings"
)

// launcherArgs returns the command that opens raw on this platform.
func launcherArgs(goos, raw string) (string, []string, error) {
	switch goos {
	case "darwin":
		return "open", []string{raw}, nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", raw}, nil
	case "linux", "freebsd", "openbsd", "netbsd":
		return "xdg-open", []string{raw}, nil
	default:
		return "", nil, fmt.Errorf("openurl: no launcher for %s", goos)
	}
}

// runCommand is the execution seam (unit tests replace it).
var runCommand = func(name string, args ...string) error {
	return exec.Command(name, args...).Start()
}

// defaultOpenExternal is the platform browser launcher; openExternal is the
// injectable step the seam unit tests replace to observe the constructed
// command headlessly (AR-21).
var defaultOpenExternal = func(raw string) error {
	name, args, err := launcherArgs(goruntime.GOOS, raw)
	if err != nil {
		return err
	}
	return runCommand(name, args...)
}
var openExternal = defaultOpenExternal

// Open is the replaceable browser launcher seam (tests inject a fake).
var Open = func(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("openurl: empty url")
	}
	return openExternal(trimmed)
}
