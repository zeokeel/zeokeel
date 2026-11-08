package uikit_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zeokeel/zeokeel/internal/uikit"
)

func TestSplitAttachmentPathsSeparatorsAndTrim(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "   \n\t ", nil},
		{"one per line", "/a/one.txt\n/a/two.txt\r\n/a/three.txt", []string{"/a/one.txt", "/a/two.txt", "/a/three.txt"}},
		{"ascii comma and semicolon", "/a/one.txt, /a/two.txt; /a/three.txt", []string{"/a/one.txt", "/a/two.txt", "/a/three.txt"}},
		{"full-width comma and semicolon", "/a/one.txt，/a/two.txt；/a/three.txt", []string{"/a/one.txt", "/a/two.txt", "/a/three.txt"}},
		{"quotes trimmed", `"/a/one two.txt"` + "\n" + `'/a/three.txt'`, []string{"/a/one two.txt", "/a/three.txt"}},
		{"blank pieces dropped", "/a/one.txt\n\n,,;\n/a/two.txt", []string{"/a/one.txt", "/a/two.txt"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := uikit.SplitAttachmentPaths(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("SplitAttachmentPaths(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for index := range tc.want {
				if got[index] != tc.want[index] {
					t.Fatalf("SplitAttachmentPaths(%q) = %v, want %v", tc.in, got, tc.want)
				}
			}
		})
	}
}

func TestResolveAttachmentPathsClassifiesEntries(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(existing, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write %s: %v", existing, err)
	}
	missing := filepath.Join(dir, "missing.txt")

	valid, invalid := uikit.ResolveAttachmentPaths(
		existing + ", relative/note.txt, " + missing + ", " + existing)

	if len(valid) != 1 || valid[0] != existing {
		t.Fatalf("valid = %v, want just %q (duplicates dropped)", valid, existing)
	}
	if len(invalid) != 2 {
		t.Fatalf("invalid = %+v, want two entries", invalid)
	}
	if !invalid[0].Relative || invalid[0].Path != "relative/note.txt" {
		t.Fatalf("invalid[0] = %+v, want a relative path", invalid[0])
	}
	if !invalid[1].Missing || invalid[1].Path != missing {
		t.Fatalf("invalid[1] = %+v, want a missing path", invalid[1])
	}
}
