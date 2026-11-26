package toolkit

// Behavioral contract for the 2026-11-26 permission model on the file tools:
// reads are not fenced to the workspace, writes are.
//
// Rig fuses the two scopes in one sandbox.Policy (ResolvePath fences to Roots()
// and WritableRoots() *is* Roots() + extras), so the split is product-side: the
// read-only file tools (read_file, list_dir, glob, grep, read_image) are bound to
// newReadPolicy (roots = [workspace, filesystem root], ModeReadOnly) while every
// writer, the shell and the OS runner keep newPolicy (roots = [workspace]).
//
// These tests run the real toolkit registry, so they pin the tools the model
// actually receives — not a policy helper in isolation. See
// docs/APPROVAL-SURFACES.md §S4.3 for the documented limits of this split.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel-rig/sandbox"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"
)

// splitWorkspace returns a workspace root and an unrelated directory outside it
// holding one file the model should be able to read.
func splitWorkspace(t *testing.T) (root, outsideDir, outsideFile string) {
	t.Helper()
	root = t.TempDir()
	outsideDir = t.TempDir()
	outsideFile = filepath.Join(outsideDir, "outside.txt")
	writeFile(t, outsideFile, "outside the workspace\n")
	writeFile(t, filepath.Join(root, "inside.txt"), "inside the workspace\n")
	return root, outsideDir, outsideFile
}

// splitRegistry activates the toolkit over root with the given settings.
func splitRegistry(t *testing.T, root string) *rigtool.Registry {
	t.Helper()
	_, registry, _, _ := activateMCPToolkitWithRoot(t, map[string]any{"root": root},
		newMutableSettings(map[string]string{}))
	return registry
}

// TestReadsAreNotFencedToTheWorkspace is the read half: an absolute path outside
// the workspace is readable, listing/grep/glob over an outside directory work,
// and the inside path still works (the wide root is additive, not a replacement).
func TestReadsAreNotFencedToTheWorkspace(t *testing.T) {
	root, outsideDir, outsideFile := splitWorkspace(t)
	registry := splitRegistry(t, root)

	read := invoke(t, registry, "read_file", `{"path":"`+outsideFile+`"}`)
	if read.IsError {
		t.Fatalf("read_file outside the workspace was refused: %q", read.Content)
	}
	if want := "outside the workspace"; !containsText(read.Content, want) {
		t.Fatalf("read_file outside content = %q, want it to contain %q", read.Content, want)
	}

	listed := invoke(t, registry, "list_dir", `{"path":"`+outsideDir+`"}`)
	if listed.IsError || !containsText(listed.Content, "outside.txt") {
		t.Fatalf("list_dir outside the workspace = %q (error=%v), want outside.txt", listed.Content, listed.IsError)
	}

	grepped := invoke(t, registry, "grep", `{"pattern":"outside","path":"`+outsideDir+`"}`)
	if grepped.IsError || !containsText(grepped.Content, "outside.txt") {
		t.Fatalf("grep outside the workspace = %q (error=%v), want a hit", grepped.Content, grepped.IsError)
	}

	globbed := invoke(t, registry, "glob", `{"pattern":"*.txt","root":"`+outsideDir+`"}`)
	if globbed.IsError || !containsText(globbed.Content, "outside.txt") {
		t.Fatalf("glob outside the workspace = %q (error=%v), want a match", globbed.Content, globbed.IsError)
	}

	// Inside still works: the wide root is added, it does not replace the
	// workspace (relative paths keep resolving against it).
	inside := invoke(t, registry, "read_file", `{"path":"inside.txt"}`)
	if inside.IsError || !containsText(inside.Content, "inside the workspace") {
		t.Fatalf("read_file inside the workspace = %q (error=%v)", inside.Content, inside.IsError)
	}
}

// TestWritesStayInsideTheWorkspace is the write half: the same absolute path the
// readers accept is refused by every writer, and the file on disk is untouched.
func TestWritesStayInsideTheWorkspace(t *testing.T) {
	root, _, outsideFile := splitWorkspace(t)
	registry := splitRegistry(t, root)

	overwrite := invoke(t, registry, "write_file", `{"path":"`+outsideFile+`","content":"overwritten"}`)
	if !overwrite.IsError {
		t.Fatalf("write_file outside the workspace succeeded: %q", overwrite.Content)
	}
	edit := invoke(t, registry, "edit_file",
		`{"path":"`+outsideFile+`","old_string":"outside","new_string":"hijacked"}`)
	if !edit.IsError {
		t.Fatalf("edit_file outside the workspace succeeded: %q", edit.Content)
	}
	created := filepath.Join(filepath.Dir(outsideFile), "created.txt")
	create := invoke(t, registry, "write_file", `{"path":"`+created+`","content":"x"}`)
	if !create.IsError {
		t.Fatalf("write_file created a file outside the workspace: %q", create.Content)
	}
	data, err := os.ReadFile(outsideFile)
	if err != nil || string(data) != "outside the workspace\n" {
		t.Fatalf("the outside file changed despite the refusals: %q/%v", data, err)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("the outside file was created despite the refusal (%v)", err)
	}

	// The in-workspace write still works: the fence is a boundary, not a brake.
	inside := filepath.Join(root, "inside.txt")
	write := invoke(t, registry, "write_file", `{"path":"`+inside+`","content":"rewritten"}`)
	if write.IsError {
		t.Fatalf("write_file inside the workspace was refused: %q", write.Content)
	}
	data, err = os.ReadFile(inside)
	if err != nil || string(data) != "rewritten" {
		t.Fatalf("inside file = %q/%v, want rewritten", data, err)
	}
}

// TestReadPolicyCannotWrite pins the safety belt: even if a writer were bound to
// the read policy by mistake, that policy is read-only and refuses the write.
func TestReadPolicyCannotWrite(t *testing.T) {
	root, _, outsideFile := splitWorkspace(t)
	policy := newReadPolicy(Config{}, root, 0)
	if err := policy.CheckWritablePath(filepath.Join(root, "inside.txt")); err == nil {
		t.Fatal("the read policy allowed a write inside the workspace")
	}
	if err := policy.CheckWritablePath(outsideFile); err == nil {
		t.Fatal("the read policy allowed a write outside the workspace")
	}
}

// TestReadPolicyNeverReachesTheRunnerOrTheStatus is the hard constraint the
// split depends on (2026-11-26 audit): the read policy's Roots() contain the
// filesystem root, and rig's Policy.WritableRoots() returns Roots() + extras
// WITHOUT filtering by mode. Handing this policy to the OS runner would render a
// profile with "/" writable; handing it to sandbox_status would show the model a
// writable surface that does not exist. Both only ever receive newPolicy.
func TestReadPolicyNeverReachesTheRunnerOrTheStatus(t *testing.T) {
	root := t.TempDir()
	readPolicy := newReadPolicy(Config{}, root, 0)

	// The trap, demonstrated: the read policy's writable roots include the
	// filesystem root even though its Mode is read-only. WritableRoots() ignores
	// the mode, so "it is read-only, it cannot matter" is exactly the wrong
	// assumption.
	leaked := false
	for _, writable := range readPolicy.WritableRoots() {
		if filepath.Clean(writable.Root) == string(filepath.Separator) {
			leaked = true
			break
		}
	}
	if !leaked {
		t.Fatalf("read policy WritableRoots() = %+v, expected it to expose the filesystem root "+
			"(the reason this policy must never be given to a runner)", readPolicy.WritableRoots())
	}
	if readPolicy.Mode() != sandbox.ModeReadOnly {
		t.Fatalf("read policy mode = %q, want read-only", readPolicy.Mode())
	}

	// The build's own write policy stays workspace-scoped: no "/" among its roots
	// and no "/" among its writable roots (temp dirs are separate, by design).
	scoped := buildRootScoped(Config{}, root, 0, sandbox.ModeWorkspaceWrite, nil)
	for _, policyRoot := range scoped.policy.Roots() {
		if filepath.Clean(policyRoot) == string(filepath.Separator) {
			t.Fatalf("the build's write policy has the filesystem root among its roots: %v", scoped.policy.Roots())
		}
	}
	for _, writable := range scoped.policy.WritableRoots() {
		if filepath.Clean(writable.Root) == string(filepath.Separator) {
			t.Fatalf("the build's write policy exposes the filesystem root as writable: %+v", scoped.policy.WritableRoots())
		}
	}

	// And the readers are the only tools bound to the wide policy: the shell and
	// the status tool must see the workspace-scoped one.
	if scoped.policy == readPolicy {
		t.Fatal("the build handed the read policy to the write surface")
	}
}

// containsText reports whether content mentions want (tool results may wrap the
// file body with a header, so exact equality is not the contract here).
func containsText(content, want string) bool {
	return strings.Contains(content, want)
}
