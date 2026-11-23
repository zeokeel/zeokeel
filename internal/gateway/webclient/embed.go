// Package webclient embeds the built browser client (web/dist) into the gateway
// binary, so `zeokeel --product serve` is a single self-contained artifact that
// serves the app from the same origin as its control plane.
//
// Why the bundle lives in two places: go:embed can only name paths below its own
// package directory, and the source of truth is web/ (owned by the web client
// track). scripts/build-web-client.sh copies web/dist into dist/ here, and the
// committed dist/.keep keeps the embed directive compiling when nobody has run
// the copy yet: the gateway then answers GET / with a readable "bundle not built"
// page instead of failing to build (docs/TRANSPORT.md §4.6).
package webclient

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// dist is the embedded client tree, rooted at web/dist.
var dist fs.FS

func init() {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		// Unreachable: the embed directive pins dist/, and dist/.keep is
		// committed. Keep the zero value so Available reports false instead of
		// panicking at startup.
		return
	}
	dist = sub
}

// FS returns the embedded client tree. It is nil when the bundle was never
// copied in (see Available).
func FS() fs.FS { return dist }

// Available reports whether a built client is embedded. The marker file that
// keeps the embed compiling is not a client: only a real index.html counts.
func Available() bool {
	if dist == nil {
		return false
	}
	info, err := fs.Stat(dist, "index.html")
	return err == nil && !info.IsDir()
}

// Index returns index.html.
func Index() ([]byte, error) {
	return fs.ReadFile(dist, "index.html")
}

// BundleInfo is the fingerprint scripts/build-web-client.sh writes next to the
// copied client: which build this binary actually carries.
type BundleInfo struct {
	BuiltAt string
	Source  string
	Files   int
	SHA256  string
	// Line is the one-line form for logs.
	Line string
}

// Fingerprint returns the embedded bundle's fingerprint. ok is false when no
// bundle was copied in (the binary serves the "not built" page).
func Fingerprint() (BundleInfo, bool) {
	if !Available() {
		return BundleInfo{}, false
	}
	raw, err := fs.ReadFile(dist, "BUNDLE_INFO")
	if err != nil {
		// A bundle copied by an older script has no fingerprint: report the
		// entry point's own identity rather than pretending to have one.
		return BundleInfo{Line: "web client bundle (no BUNDLE_INFO; rebuild with scripts/build-web-client.sh)"}, true
	}
	info := BundleInfo{}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found {
			continue
		}
		switch key {
		case "builtAt":
			info.BuiltAt = value
		case "source":
			info.Source = value
		case "sha256":
			info.SHA256 = value
		case "files":
			info.Files = atoiOrZero(value)
		}
	}
	info.Line = fmt.Sprintf("web client bundle sha256 %s (%d files, built %s from %s)",
		shortDigest(info.SHA256), info.Files, info.BuiltAt, info.Source)
	return info, true
}

// shortDigest is the first 12 hex characters of a digest.
func shortDigest(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	if digest == "" {
		return "unknown"
	}
	return digest
}

// atoiOrZero parses a small decimal, returning 0 for garbage.
func atoiOrZero(value string) int {
	total := 0
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0
		}
		total = total*10 + int(char-'0')
	}
	return total
}
