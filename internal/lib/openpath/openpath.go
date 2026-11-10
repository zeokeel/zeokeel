// Package openpath launches one filesystem path in the platform's file manager
// or default application. UI surfaces use it for the "用系统应用打开" action on
// generated files; tests replace Open to stay headless.
package openpath

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

// Open is the replaceable launcher seam (tests inject a fake).
var Open = func(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("openpath: empty path")
	}
	command := "xdg-open"
	switch runtime.GOOS {
	case "darwin":
		command = "open"
	case "windows":
		command = "explorer"
	}
	cmd := exec.Command(command, path)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
