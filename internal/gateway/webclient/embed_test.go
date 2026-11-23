package webclient

import (
	"io/fs"
	"strings"
	"testing"
)

// TestEmbeddedTreeAlwaysCompiles pins the contract that keeps `go build`
// working whether or not the client was copied in: the package-level FS is
// never nil, and Available() tells the truth about a real bundle.
func TestEmbeddedTreeAlwaysCompiles(t *testing.T) {
	tree := FS()
	if tree == nil {
		t.Fatal("FS() is nil: the embed directory or its .keep marker is missing")
	}
	entries, err := fs.ReadDir(tree, ".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("the embedded tree is empty")
	}
	if !Available() {
		// Acceptable: the bundle was not copied in. The gateway then answers a
		// readable "not built" page; nothing here may panic or fail the build.
		if _, err := Index(); err == nil {
			t.Fatal("Index() succeeded while Available() is false")
		}
		return
	}
	index, err := Index()
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(index)), "<!doctype html") {
		t.Fatalf("index.html does not look like a document: %q", firstBytes(index))
	}
}

// TestFingerprintReportsTheEmbeddedBuild covers the "which UI am I running?"
// log line: a real bundle carries BUNDLE_INFO, and an empty tree reports
// not-available instead of inventing a fingerprint.
func TestFingerprintReportsTheEmbeddedBuild(t *testing.T) {
	info, ok := Fingerprint()
	if !Available() {
		if ok {
			t.Fatalf("Fingerprint reported %+v with no bundle", info)
		}
		return
	}
	if !ok {
		t.Fatal("a bundle is embedded but Fingerprint reported none")
	}
	if !strings.Contains(info.Line, "web client bundle") {
		t.Fatalf("fingerprint line = %q", info.Line)
	}
	if info.SHA256 == "" || info.Files == 0 {
		t.Fatalf("fingerprint is missing its facts: %+v", info)
	}
}

// firstBytes is a short preview for failure messages.
func firstBytes(value []byte) string {
	if len(value) > 80 {
		return string(value[:80]) + "…"
	}
	return string(value)
}
