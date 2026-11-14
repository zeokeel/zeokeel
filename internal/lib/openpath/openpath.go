// Package openpath launches one filesystem path in the platform's file manager
// or default application. UI surfaces use it for the "用系统应用打开" action on
// generated files; tests replace Open to stay headless.
//
// The platform work belongs to the framework: mygo.Shell.OpenPath knows each
// OS's launcher (and its quirks), so this package is only the product's test
// seam around it.
package openpath

import (
	"strings"

	"github.com/egoist/mygo"
)

// Open is the replaceable launcher seam (tests inject a fake).
var Open = func(path string) error {
	return mygo.Shell.OpenPath(strings.TrimSpace(path))
}
