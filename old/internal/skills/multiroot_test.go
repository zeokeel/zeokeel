package skills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkillFixture creates one <dir>/<name>/SKILL.md fixture.
func writeSkillFixture(t *testing.T, dir, name, description, body string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", skillDir, err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(skillDir, SkillFile), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", skillDir, err)
	}
}

func toolList(t *testing.T, tool *Tool) string {
	t.Helper()
	out, err := tool.Invoke(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatalf("skill list: %v", err)
	}
	return out
}

func toolRead(t *testing.T, tool *Tool, name string) string {
	t.Helper()
	args, err := json.Marshal(map[string]string{"action": "read", "name": name})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := tool.Invoke(context.Background(), args)
	if err != nil {
		t.Fatalf("skill read %q: %v", name, err)
	}
	return out
}

// TestRootsForWorkspaceLayout pins FP-17's discovery order: project skills,
// project-local .zeokeel/skills, then the user-level root.
func TestRootsForWorkspaceLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	workspace := t.TempDir()
	roots := RootsFor(filepath.Join(workspace, "skills"))
	if len(roots) != 3 {
		t.Fatalf("roots = %v, want 3 (project, project-local, user)", roots)
	}
	if want := filepath.Join(workspace, "skills"); roots[0] != want {
		t.Fatalf("roots[0] = %q, want %q", roots[0], want)
	}
	if want := filepath.Join(workspace, ".zeokeel", "skills"); roots[1] != want {
		t.Fatalf("roots[1] = %q, want %q", roots[1], want)
	}
	if want := filepath.Join(home, ".zeokeel", "skills"); roots[2] != want {
		t.Fatalf("roots[2] = %q, want %q", roots[2], want)
	}

	// A custom configured root is not widened: the operator's override wins.
	custom := t.TempDir()
	if got := RootsFor(custom); len(got) != 1 || got[0] != custom {
		t.Fatalf("RootsFor(custom) = %v, want only %q", got, custom)
	}
}

// TestMultiRootMergeAndPrecedence pins the merge rule: every root contributes
// skills, and a duplicate name keeps the earliest (most specific) root.
func TestMultiRootMergeAndPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := t.TempDir()
	projectRoot := filepath.Join(workspace, "skills")
	localRoot := filepath.Join(workspace, ".zeokeel", "skills")
	userRoot := filepath.Join(home, ".zeokeel", "skills")
	writeSkillFixture(t, projectRoot, "shared", "project version", "PROJECT BODY")
	writeSkillFixture(t, projectRoot, "only-workspace", "workspace only", "WS BODY")
	writeSkillFixture(t, localRoot, "shared", "local version", "LOCAL BODY")
	writeSkillFixture(t, localRoot, "local-only", "local only", "LOCAL ONLY")
	writeSkillFixture(t, userRoot, "shared", "user version", "USER BODY")
	writeSkillFixture(t, userRoot, "user-only", "user only", "USER ONLY")

	loaded, errs := LoadRootsTolerant(RootsFor(projectRoot))
	if len(errs) != 0 {
		t.Fatalf("LoadRootsTolerant errors = %v", errs)
	}
	got := make([]string, 0, len(loaded))
	for _, skill := range loaded {
		got = append(got, skill.Name)
	}
	want := []string{"local-only", "only-workspace", "shared", "user-only"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("merged skills = %v, want %v", got, want)
	}
	// The project version shadows both the local and user copies.
	tool := NewFromSkills(loaded)
	if body := toolRead(t, tool, "shared"); !strings.Contains(body, "PROJECT BODY") {
		t.Fatalf("shadowed body = %q, want the project version", body)
	}

	// The real constructor must expose the same merged catalog.
	fresh, freshErrs := NewTolerant(projectRoot)
	if len(freshErrs) != 0 {
		t.Fatalf("NewTolerant errors = %v", freshErrs)
	}
	list := toolList(t, fresh)
	for _, name := range want {
		if !strings.Contains(list, "- "+name+":") {
			t.Fatalf("catalog missing %q:\n%s", name, list)
		}
	}
	if count := strings.Count(list, "- shared:"); count != 1 {
		t.Fatalf("catalog lists the shared skill %d times, want 1:\n%s", count, list)
	}
}

// TestSkillToolRefreshesWithoutRestart pins automatic refresh: a SKILL.md added
// or edited after tool construction is visible on the next tool call.
func TestSkillToolRefreshesWithoutRestart(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(t.TempDir(), "skills")
	writeSkillFixture(t, root, "alpha", "first", "ALPHA V1")

	tool, errs := NewTolerant(root)
	if errs != nil {
		t.Fatalf("NewTolerant errors = %v", errs)
	}
	if list := toolList(t, tool); strings.Contains(list, "beta") {
		t.Fatalf("catalog unexpectedly lists beta before it exists:\n%s", list)
	}

	writeSkillFixture(t, root, "beta", "second", "BETA BODY")
	if list := toolList(t, tool); !strings.Contains(list, "- beta: second") {
		t.Fatalf("catalog did not refresh after adding a skill:\n%s", list)
	}

	writeSkillFixture(t, root, "alpha", "first rewritten", "ALPHA V2")
	if list := toolList(t, tool); !strings.Contains(list, "- alpha: first rewritten") {
		t.Fatalf("catalog did not refresh after editing a skill:\n%s", list)
	}
	if body := toolRead(t, tool, "alpha"); !strings.Contains(body, "ALPHA V2") {
		t.Fatalf("read after edit = %q, want the new body", body)
	}

	// Manual refresh remains available and reports nothing for a clean root.
	if errs := tool.Reload(); len(errs) != 0 {
		t.Fatalf("Reload errors = %v", errs)
	}
}

// TestSkillToolMissingOptionalRootsIgnored pins that absent project-local /
// user roots are normal: the primary root alone still works and reports no
// errors.
func TestSkillToolMissingOptionalRootsIgnored(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // no ~/.zeokeel/skills
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(t.TempDir(), "skills")
	writeSkillFixture(t, root, "solo", "only skill", "SOLO")

	tool, errs := NewTolerant(root)
	if len(errs) != 0 {
		t.Fatalf("missing optional roots produced errors: %v", errs)
	}
	if list := toolList(t, tool); !strings.Contains(list, "- solo: only skill") {
		t.Fatalf("catalog = %q, want the solo skill", list)
	}
}

// TestRootsForDeduplicatesIdenticalRoots pins that a workspace literally named
// .zeokeel/skills does not load the same directory twice.
func TestRootsForDeduplicatesIdenticalRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(t.TempDir(), "skills")
	roots := RootsFor(root)
	seen := map[string]bool{}
	for _, entry := range roots {
		if seen[entry] {
			t.Fatalf("duplicate root %q in %v", entry, roots)
		}
		seen[entry] = true
	}
}
