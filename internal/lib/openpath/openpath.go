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

// defaultOpenPath is the platform launcher; openPath is the injectable step
// the seam unit tests replace to observe the constructed command headlessly
// (AR-21). Production always uses the framework call.
var defaultOpenPath = func(path string) error { return mygo.Shell.OpenPath(path) }
var openPath = defaultOpenPath

// Open is the replaceable launcher seam (tests inject a fake).
var Open = func(path string) error {
	return openPath(strings.TrimSpace(path))
}

// defaultShowInFolder / showInFolder mirror the Open pair for the reveal seam.
var defaultShowInFolder = func(path string) { mygo.Shell.ShowItemInFolder(path) }
var showInFolder = defaultShowInFolder

// ShowInFolder reveals one path in the platform's file manager (selecting the
// item when supported). It is a seam for the same reason as Open.
var ShowInFolder = func(path string) error {
	showInFolder(strings.TrimSpace(path))
	return nil
}
