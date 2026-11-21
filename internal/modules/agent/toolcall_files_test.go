package agent

// The transcript bounds tool arguments (4000 runes), so a real apply_patch body
// is truncated and no longer valid JSON: the file list has to be extracted from
// the full arguments when the call is recorded.

import (
	"encoding/json"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func patchArgs(t *testing.T, patch string, dryRun bool) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"patch": patch, "dry_run": dryRun})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(encoded)
}

// TestGeneratedCallFilesSurvivesTheArgumentBound is the P1 regression: a patch
// whose JSON exceeds the transcript bound still yields its file list.
func TestGeneratedCallFilesSurvivesTheArgumentBound(t *testing.T) {
	var body strings.Builder
	body.WriteString("*** Begin Patch\n*** Update File: src/big.go\n@@\n")
	for index := 0; index < 300; index++ {
		body.WriteString("-old line\n+new line\n")
	}
	body.WriteString("*** End Patch")
	args := patchArgs(t, body.String(), false)
	if len(args) <= 4000 {
		t.Fatalf("fixture arguments = %d bytes, want beyond the transcript bound", len(args))
	}
	files := generatedCallFiles("apply_patch", args)
	if len(files) != 1 || files[0].Path != "src/big.go" {
		t.Fatalf("generated files = %+v, want src/big.go", files)
	}
}

// TestGeneratedCallFilesKinds covers add/update/delete/move and the unified
// diff form (including /dev/null and a rename).
func TestGeneratedCallFilesKinds(t *testing.T) {
	applyStyle := "*** Begin Patch\n*** Add File: docs/new.md\n+x\n*** Update File: README.md\n@@\n-a\n+b\n*** Delete File: old.md\n*** End Patch"
	files := generatedCallFiles("apply_patch", patchArgs(t, applyStyle, false))
	want := map[string]struct{ added, deleted bool }{
		"docs/new.md": {added: true},
		"README.md":   {},
		"old.md":      {deleted: true},
	}
	checkFiles(t, files, want)

	diffStyle := "diff --git a/new.txt b/new.txt\n--- /dev/null\n+++ b/new.txt\n@@\n+x\n" +
		"diff --git a/keep.txt b/keep.txt\n--- a/keep.txt\n+++ b/keep.txt\n@@\n-a\n+b\n" +
		"diff --git a/gone.txt b/gone.txt\n--- a/gone.txt\n+++ /dev/null\n@@\n-x\n" +
		"diff --git a/old/name.txt b/new/name.txt\n--- a/old/name.txt\n+++ b/new/name.txt\n@@\n-y\n+z\n"
	checkFiles(t, generatedCallFiles("apply_patch", patchArgs(t, diffStyle, false)), map[string]struct{ added, deleted bool }{
		"new.txt":      {added: true},
		"keep.txt":     {},
		"gone.txt":     {deleted: true},
		"new/name.txt": {},
		"old/name.txt": {deleted: true},
	})
}

// TestGeneratedCallFilesIgnoresContextAndDryRun pins the ways a patch must NOT
// produce a file: a real space-prefixed context line that mentions a directive
// (column 0 rule), hunk content that looks like a diff header, and a dry run.
func TestGeneratedCallFilesIgnoresContextAndDryRun(t *testing.T) {
	documented := "*** Begin Patch\n*** Update File: docs/patch-format.md\n@@\n *** Add File: ghost.md\n-- *** Delete File: ghost2.md\n*** End Patch"
	files := generatedCallFiles("apply_patch", patchArgs(t, documented, false))
	if len(files) != 1 || files[0].Path != "docs/patch-format.md" {
		t.Fatalf("files = %+v, want only docs/patch-format.md", files)
	}
	hunkContent := "diff --git a/x.txt b/x.txt\n--- a/x.txt\n+++ b/x.txt\n@@ -1,2 +1,2 @@\n-- foo\n++ bar\n"
	checkFiles(t, generatedCallFiles("apply_patch", patchArgs(t, hunkContent, false)), map[string]struct{ added, deleted bool }{
		"x.txt": {},
	})
	if files := generatedCallFiles("apply_patch", patchArgs(t, "*** Add File: x.md\n", true)); len(files) != 0 {
		t.Fatalf("dry-run patch produced %+v, want no files", files)
	}
}

// TestGeneratedCallFilesMoveMarksTheSourceDeleted pins the move semantics: the
// preceding Update path no longer exists after the rename.
func TestGeneratedCallFilesMoveMarksTheSourceDeleted(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: old/name.txt\n*** Move to: new/name.txt\n@@\n-a\n+b\n*** End Patch"
	checkFiles(t, generatedCallFiles("apply_patch", patchArgs(t, patch, false)), map[string]struct{ added, deleted bool }{
		"old/name.txt": {deleted: true},
		"new/name.txt": {},
	})
}

// TestRestoredCallsRecoverThePatchFiles pins the restart path: rig history keeps
// the full arguments, so a restored call still feeds the cards.
func TestRestoredCallsRecoverThePatchFiles(t *testing.T) {
	patch := "*** Begin Patch\n*** Add File: docs/a.md\n+x\n*** End Patch"
	restored := chatMessagesFromRig([]rig.Message{{
		Role: rig.RoleAssistant,
		ToolCalls: []rig.ToolCall{{
			ID: "c1", Name: "apply_patch", Arguments: patchArgs(t, patch, false),
		}},
	}})
	if len(restored) != 1 || len(restored[0].Tools) != 1 {
		t.Fatalf("restored messages = %+v, want one call", restored)
	}
	files := restored[0].Tools[0].Generated
	if len(files) != 1 || files[0].Path != "docs/a.md" || !files[0].Added {
		t.Fatalf("restored generated = %+v, want docs/a.md added", files)
	}
}

func checkFiles(t *testing.T, files []contracts.ToolCallFile, want map[string]struct{ added, deleted bool }) {
	t.Helper()
	if len(files) != len(want) {
		t.Fatalf("files = %+v, want %d entries", files, len(want))
	}
	for _, file := range files {
		expected, ok := want[file.Path]
		if !ok {
			t.Fatalf("unexpected file %q in %+v", file.Path, files)
		}
		if file.Added != expected.added || file.Deleted != expected.deleted {
			t.Fatalf("file %q = %+v, want added=%t deleted=%t", file.Path, file, expected.added, expected.deleted)
		}
	}
}
