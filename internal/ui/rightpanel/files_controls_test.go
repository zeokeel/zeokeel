package rightpanel

// Unit tests for the Files pane's path/entry helpers (H21).

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestCompressPathKeepsShortPaths(t *testing.T) {
	for _, path := range []string{"/tmp/proj-alpha", "docs/api", ""} {
		if got := compressPath(path); got != path {
			t.Fatalf("compressPath(%q) = %q, want unchanged", path, got)
		}
	}
}

func TestCompressPathElidesMiddleOfLongPath(t *testing.T) {
	long := "/tmp/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/cccccccccccccccccccccccc/dddddddddd/project"
	got := compressPath(long)
	want := "/tmp/…/dddddddddd/project"
	if got != want {
		t.Fatalf("compressPath = %q, want %q", got, want)
	}
}

func TestFilterFileEntriesFoldsDotFilesAndFiltersNames(t *testing.T) {
	entries := []contracts.FileEntry{
		{Name: ".git", Path: ".git", Dir: true},
		{Name: ".env", Path: ".env"},
		{Name: "notes.txt", Path: "notes.txt", Size: 3},
		{Name: "main.go", Path: "main.go", Size: 9},
	}
	visible := filterFileEntries(entries, "", false)
	if len(visible) != 2 || visible[0].Name != "notes.txt" || visible[1].Name != "main.go" {
		t.Fatalf("default entries = %+v, want the two visible files", visible)
	}
	withHidden := filterFileEntries(entries, "", true)
	if len(withHidden) != 4 {
		t.Fatalf("show-hidden entries = %d, want 4", len(withHidden))
	}
	filtered := filterFileEntries(entries, "NOTES", true)
	if len(filtered) != 1 || filtered[0].Name != "notes.txt" {
		t.Fatalf("filtered entries = %+v, want notes.txt", filtered)
	}
}
