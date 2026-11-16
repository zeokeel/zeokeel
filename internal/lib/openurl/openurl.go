// Package openurl opens one URL in the platform's default browser. UI surfaces
// use it for OAuth authorization; tests replace Open to stay headless.
//
// The platform work belongs to the framework: mygo.Shell.OpenExternal knows
// each OS's launcher, and it dispatches to the main thread internally, so this
// wrapper can be called from a background authorization goroutine. The package
// keeps the same replaceable-seam shape as internal/lib/openpath.
package openurl

import (
	"strings"

	"github.com/egoist/mygo"
)

// defaultOpenExternal is the platform browser launcher; openExternal is the
// injectable step the seam unit tests replace to observe the constructed
// command headlessly (AR-21). Production always uses the framework call.
var defaultOpenExternal = func(raw string) error { return mygo.Shell.OpenExternal(raw) }
var openExternal = defaultOpenExternal

// Open is the replaceable browser launcher seam (tests inject a fake).
var Open = func(raw string) error {
	return openExternal(strings.TrimSpace(raw))
}
