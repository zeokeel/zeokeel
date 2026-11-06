package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/session"
)

// writeSkill writes one SKILL.md under root/<name>/SKILL.md.
func writeSkill(t *testing.T, root, name, description string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// useTemporaryHome points os.UserHomeDir (HOME/USERPROFILE) at a temp dir so
// the user-level skill root is deterministic and does not read the real one.
func useTemporaryHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// TestSkillsPromptMergesMultiRoots is the FP-17 app wiring regression: the
// prompt catalog must include the project, project-local (.zeokeel) and user
// skill roots, exactly like the registered skill tool.
func TestSkillsPromptMergesMultiRoots(t *testing.T) {
	home := useTemporaryHome(t)
	st, dir := newToolStore(t)
	writeSkill(t, filepath.Join(dir, "skills"), "project-skill", "Project skill.")
	writeSkill(t, filepath.Join(dir, ".zeokeel", "skills"), "local-skill", "Project-local skill.")
	writeSkill(t, filepath.Join(home, ".zeokeel", "skills"), "user-skill", "User skill.")

	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatalf("register tools: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	names := toolNameSet(reg)
	if !names["skill"] {
		t.Fatal("skill tool not registered for the default multi-root layout")
	}
	out := composeInstruction(st, dir, reg, filepath.Join(dir, "skills"), session.PermissionWorkspace, "")
	for _, want := range []string{"project-skill", "local-skill", "user-skill"} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt catalog missing %q from a merged skill root:\n%s", want, out)
		}
	}
}

// TestSkillsToolRegistersFromUserRootOnly covers a workspace without its own
// skills/ directory: the user-level root alone must keep the skill tool and the
// prompt catalog available.
func TestSkillsToolRegistersFromUserRootOnly(t *testing.T) {
	home := useTemporaryHome(t)
	st, dir := newToolStore(t) // workspace root with no skills/ directory
	writeSkill(t, filepath.Join(home, ".zeokeel", "skills"), "shared-skill", "Shared user skill.")

	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string { return d })
	if err != nil {
		t.Fatalf("register tools: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	if !toolNameSet(reg)["skill"] {
		t.Fatal("skill tool missing when only the user-level root has skills")
	}
	out := composeInstruction(st, dir, reg, filepath.Join(dir, "skills"), session.PermissionWorkspace, "")
	if !strings.Contains(out, "shared-skill") {
		t.Errorf("prompt catalog missing the user-level skill:\n%s", out)
	}
}

// TestSkillsCustomRootStaysSingleRoot pins the operator override: a custom
// skills.root is used exactly as configured (no implicit widening).
func TestSkillsCustomRootStaysSingleRoot(t *testing.T) {
	home := useTemporaryHome(t)
	st, dir := newToolStore(t)
	custom := filepath.Join(t.TempDir(), "custom-skills")
	writeSkill(t, custom, "custom-skill", "Custom root skill.")
	writeSkill(t, filepath.Join(home, ".zeokeel", "skills"), "user-skill", "User skill.")
	if err := st.SetSetting("skills.root", custom); err != nil {
		t.Fatal(err)
	}
	reg, closeFn, err := registerAllTools(st, dir, func(k, e, d string) string {
		if k == "skills.root" {
			return custom
		}
		return d
	})
	if err != nil {
		t.Fatalf("register tools: %v", err)
	}
	if closeFn != nil {
		t.Cleanup(func() { _ = closeFn() })
	}
	out := composeInstruction(st, dir, reg, custom, session.PermissionWorkspace, "")
	if !strings.Contains(out, "custom-skill") {
		t.Errorf("prompt catalog missing the custom-root skill:\n%s", out)
	}
	if strings.Contains(out, "user-skill") {
		t.Error("custom skills.root was widened to the user-level root")
	}
}
