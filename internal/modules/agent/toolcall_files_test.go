package agent

// The transcript bounds tool arguments (4000 runes), so a real apply_patch body
// is truncated and no longer valid JSON: the file list has to be extracted from
// the full arguments when the call is recorded.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

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
	hunkContent := "diff --git a/x.txt b/x.txt\nindex 111..222 100644\n--- a/x.txt\n+++ b/x.txt\n@@ -1,2 +1,2 @@\n--- foo\n+++ bar\n"
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

// TestPatchFileListParsesRealGitDiff pins the real-world input: git always puts
// metadata (index, mode, similarity, rename) between "diff --git" and "--- ".
func TestPatchFileListParsesRealGitDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	root := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "core.excludesFile="}, args...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init", "-q")
	for path, content := range map[string]string{"keep.txt": "one\n", "mod.txt": "a\n", "gone.txt": "x\n", "old/name.txt": "n\n"} {
		writeTestFile(t, root, path, content)
	}
	run("add", "-A")
	run("commit", "-qm", "init")
	writeTestFile(t, root, "mod.txt", "b\n")
	writeTestFile(t, root, "new.txt", "new\n")
	run("rm", "-q", "gone.txt")
	if err := os.MkdirAll(filepath.Join(root, "new"), 0o755); err != nil {
		t.Fatalf("mkdir new: %v", err)
	}
	run("mv", "old/name.txt", "new/name.txt")
	run("add", "-A")
	diff := run("diff", "-M", "--cached", "HEAD")

	files := generatedCallFiles("apply_patch", patchArgs(t, diff, false))
	want := map[string]struct{ added, deleted bool }{
		"keep.txt": {}, // not in the diff at all, so it must be absent below
	}
	_ = want
	seen := make(map[string]contracts.ToolCallFile, len(files))
	for _, file := range files {
		seen[file.Path] = file
	}
	for path, expect := range map[string]struct{ added, deleted bool }{
		"new.txt":      {added: true},
		"mod.txt":      {},
		"gone.txt":     {deleted: true},
		"new/name.txt": {},
		"old/name.txt": {deleted: true},
	} {
		file, ok := seen[path]
		if !ok {
			t.Fatalf("diff %q not parsed into %q (got %+v)", path, path, files)
		}
		if file.Added != expect.added || file.Deleted != expect.deleted {
			t.Fatalf("file %q = %+v, want added=%t deleted=%t", path, file, expect.added, expect.deleted)
		}
	}
	if _, ok := seen["keep.txt"]; ok {
		t.Fatalf("unchanged file appeared: %+v", files)
	}
}

// writeTestFile writes one workspace file, creating parent directories.
func writeTestFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestRestoreHydratesGeneratedFromTheEvent pins the event-only restore path: the
// durable tool_call event carries the file list even when rig history has no
// arguments for the call.
func TestRestoreHydratesGeneratedFromTheEvent(t *testing.T) {
	store := newFakeStore()
	ctx := context.Background()
	if err := store.CreateSession(ctx, "sess-gen", "generated"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	assistant := rig.NewMessage(rig.RoleAssistant, "done")
	assistant.ToolCalls = []rig.ToolCall{{ID: "c1", Name: "apply_patch"}}
	if err := store.History().Append(ctx, "sess-gen", rig.NewMessage(rig.RoleUser, "patch"), assistant); err != nil {
		t.Fatalf("Append history: %v", err)
	}
	payload := `{"id":"c1","name":"apply_patch","text_offset":0,"generated":[{"Path":"docs/a.md","Added":true},{"Path":"old.md","Deleted":true}]}`
	if err := store.AppendEvent(ctx, rig.SessionEvent{SessionID: "sess-gen", Type: "tool_call", Payload: []byte(payload)}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	svc := newPersistenceTestService(store)
	svc.restore(ctx)

	message := restoredToolMessage(t, svc, "sess-gen")
	if len(message.Tools) != 1 {
		t.Fatalf("restored tools = %+v, want one call", message.Tools)
	}
	files := message.Tools[0].Generated
	if len(files) != 2 || files[0].Path != "docs/a.md" || !files[0].Added || files[1].Path != "old.md" || !files[1].Deleted {
		t.Fatalf("restored generated = %+v, want docs/a.md added and old.md deleted", files)
	}
}

// TestRestoreRecoversLargePatchArguments pins the restore parser against the
// transcript bound: rig history keeps the full arguments.
func TestRestoreRecoversLargePatchArguments(t *testing.T) {
	var body strings.Builder
	body.WriteString("*** Begin Patch\n*** Update File: src/big.go\n@@\n")
	for index := 0; index < 300; index++ {
		body.WriteString("-old\n+new\n")
	}
	body.WriteString("*** End Patch")
	restored := chatMessagesFromRig([]rig.Message{{
		Role:      rig.RoleAssistant,
		ToolCalls: []rig.ToolCall{{ID: "c1", Name: "apply_patch", Arguments: patchArgs(t, body.String(), false)}},
	}})
	files := restored[0].Tools[0].Generated
	if len(files) != 1 || files[0].Path != "src/big.go" {
		t.Fatalf("restored generated = %+v, want src/big.go", files)
	}
}

// TestHandleEventRecordsPatchFilesFromFullArguments drives the live record path:
// the transcript keeps bounded arguments, but both the live call and the durable
// tool_call event must carry the file list parsed from the FULL patch.
func TestHandleEventRecordsPatchFilesFromFullArguments(t *testing.T) {
	store := newFakeStore()
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "test-model"})
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, store, fakeApprovals{})
	id, err := svc.NewSession("patch-record")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	svc.mu.Lock()
	state := svc.sessions[id]
	state.messages = append(state.messages, contracts.ChatMessage{
		ID: "m-a", Role: contracts.ChatRoleAssistant, Streaming: true,
	})
	state.assistant = len(state.messages) - 1
	svc.mu.Unlock()

	var body strings.Builder
	body.WriteString("*** Begin Patch\n*** Update File: src/big.go\n@@\n")
	for index := 0; index < 300; index++ {
		body.WriteString("-old line\n+new line\n")
	}
	body.WriteString("*** End Patch")
	arguments := patchArgs(t, body.String(), false)
	call := &rig.ToolCall{ID: "c1", Name: "apply_patch", Arguments: arguments}
	svc.handleEvent(context.Background(), id, "", rig.Event{Type: rig.EventToolCall, Iteration: 1, Call: call}, nil)

	svc.mu.Lock()
	tools := svc.sessions[id].messages[state.assistant].Tools
	svc.mu.Unlock()
	if len(tools) != 1 || len(tools[0].Generated) != 1 || tools[0].Generated[0].Path != "src/big.go" {
		t.Fatalf("live call = %+v, want the patch file list", tools)
	}
	if len([]rune(tools[0].Arguments)) > 4001 {
		t.Fatalf("live arguments = %d runes, want the bounded transcript", len([]rune(tools[0].Arguments)))
	}

	events, err := store.Events(context.Background(), id, 0, 0)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 1 || !strings.Contains(string(events[0].Payload), `"generated"`) ||
		!strings.Contains(string(events[0].Payload), "src/big.go") {
		t.Fatalf("durable tool_call payload = %s, want the generated file list", events[0].Payload)
	}
}
