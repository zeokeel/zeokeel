package skills

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRootsForRelativeAnchorsWorkspace pins the P3 fix: a relative configured
// root must resolve against the workspace root, never the process working
// directory. The process cwd here is a decoy directory that must not appear in
// any returned root.
func TestRootsForRelativeAnchorsWorkspace(t *testing.T) {
	workspace := t.TempDir()
	decoy := t.TempDir()
	t.Chdir(decoy)

	roots := RootsFor("skills", workspace)
	if len(roots) == 0 {
		t.Fatal("RootsFor returned no roots")
	}
	if want := filepath.Join(workspace, "skills"); roots[0] != want {
		t.Fatalf("roots[0] = %q, want the workspace-anchored %q (cwd must not be used)", roots[0], want)
	}
	for _, root := range roots {
		if rel, err := filepath.Rel(decoy, root); err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			t.Fatalf("root %q is anchored under the process cwd %q", root, decoy)
		}
	}
	// The default "<workspace>/skills" layout still contributes the project
	// local and user roots, anchored at the workspace as well.
	if want := filepath.Join(workspace, ".zeokeel", "skills"); roots[1] != want {
		t.Fatalf("roots[1] = %q, want %q", roots[1], want)
	}
}

// TestRootsForAbsoluteRootStaysAbsolute pins that an absolute configured root is
// used verbatim, independent of the base.
func TestRootsForAbsoluteRootStaysAbsolute(t *testing.T) {
	workspace := t.TempDir()
	abs := filepath.Join(t.TempDir(), "custom-skills")
	roots := RootsFor(abs, workspace)
	if len(roots) != 1 || roots[0] != abs {
		t.Fatalf("RootsFor(absolute, workspace) = %v, want exactly %q", roots, abs)
	}
}

// TestRootsForExpandsTilde pins that ~ roots expand to the user home instead of
// becoming a literal path under cwd.
func TestRootsForExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := t.TempDir()

	roots := RootsFor(filepath.Join("~", "my-skills"), workspace)
	want := filepath.Join(home, "my-skills")
	if len(roots) == 0 || roots[0] != want {
		t.Fatalf("RootsFor(~/my-skills) = %v, want %q", roots, want)
	}
}

// TestNewTolerantInLoadsRelativeRootsFromWorkspace pins the constructor seam:
// the tool must load a relative skills root against the workspace, not cwd.
func TestNewTolerantInLoadsRelativeRootsFromWorkspace(t *testing.T) {
	workspace := t.TempDir()
	decoy := t.TempDir()
	writeSkillFixture(t, filepath.Join(workspace, "skills"), "anchored", "anchored skill", "ANCHORED BODY")
	t.Chdir(decoy)

	tool, errs := NewTolerantIn(workspace, "skills")
	if len(errs) != 0 {
		t.Fatalf("NewTolerantIn errors = %v", errs)
	}
	list := toolList(t, tool)
	if !contains(list, "- anchored: anchored skill") {
		t.Fatalf("catalog = %q, want the workspace-anchored skill", list)
	}
}

// contains is a tiny substring helper kept local to these P3 tests.
func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
