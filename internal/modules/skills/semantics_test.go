package skills

// Wave-1 regressions: root precedence (workspace first, S-02), the rig/DSH
// default roots and user-configured dirs (S-03), bounded listing vs full load
// (S-05) and the frontmatter splitting rule shared with rig (S-06).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// skillSettings is a minimal contracts.SettingsService for the extra-dir key.
type skillSettings struct {
	values map[string]string
}

func (s *skillSettings) Get(key, fallback string) string {
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}

func (s *skillSettings) Set(key, value string) error {
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.values[key] = value
	return nil
}

func (s *skillSettings) All() map[string]string {
	copied := make(map[string]string, len(s.values))
	for key, value := range s.values {
		copied[key] = value
	}
	return copied
}

// isolateHome points HOME and ZEOKEEL_SKILLS at deterministic temporary values.
func isolateHome(t *testing.T) (home, envDir string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	envDir = t.TempDir()
	t.Setenv("ZEOKEEL_SKILLS", envDir)
	return home, envDir
}

func TestRootsPrecedenceWorkspaceFirstAndWarnings(t *testing.T) {
	home, envDir := isolateHome(t)
	dataDir := t.TempDir()
	settingsDir := t.TempDir()
	assemblyDir := t.TempDir()
	workspaceRoot := t.TempDir()
	workspace := &fakeWorkspace{}
	workspace.setRoot(workspaceRoot)

	writeSkill(t, filepath.Join(workspaceRoot, WorkspaceDirName), "shared", "Workspace version")
	writeSkill(t, filepath.Join(workspaceRoot, ".agents", "skills"), "dsh", "DSH skill")
	writeSkill(t, filepath.Join(workspaceRoot, ".zeokeel", "skills"), "rigws", "Workspace rig skill")
	writeSkill(t, filepath.Join(dataDir, DataDirName), "shared", "Data dir version")
	writeSkill(t, envDir, "envskill", "Env skill")
	writeSkill(t, filepath.Join(home, ".zeokeel", "skills"), "homeskill", "Home skill")

	service := newTestService(t, dataDir, Config{Dirs: []string{assemblyDir}}, workspace)
	service.settings = &skillSettings{values: map[string]string{contracts.SettingSkillsDirs: settingsDir}}

	roots := service.Roots()
	wantPrefix := []string{
		filepath.Join(workspaceRoot, WorkspaceDirName),
		filepath.Join(workspaceRoot, ".agents", "skills"),
		filepath.Join(workspaceRoot, ".zeokeel", "skills"),
		settingsDir,
		assemblyDir,
		filepath.Join(dataDir, DataDirName),
		envDir,
		filepath.Join(home, ".zeokeel", "skills"),
	}
	if len(roots) < len(wantPrefix) {
		t.Fatalf("Roots = %v, want at least %d roots", roots, len(wantPrefix))
	}
	for index, want := range wantPrefix {
		if roots[index] != want {
			t.Fatalf("Roots[%d] = %q, want %q (all roots: %v)", index, roots[index], want, roots)
		}
	}

	listed, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byName := make(map[string]contracts.Skill, len(listed))
	for _, skill := range listed {
		byName[skill.Name] = skill
	}
	if got := byName["shared"].Description; got != "Workspace version" {
		t.Fatalf("shared description = %q, want the workspace override", got)
	}
	for _, name := range []string{"dsh", "rigws", "envskill", "homeskill"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("root skill %q not discovered; listed = %v", name, skillNames(listed))
		}
	}
	warnings := service.Warnings()
	if len(warnings) == 0 {
		t.Fatal("shadowed skill was not reported in Warnings()")
	}
	if !strings.Contains(strings.Join(warnings, "\n"), `"shared"`) {
		t.Fatalf("shadow warning does not name the skill: %v", warnings)
	}
}

func TestListReadsOnlyTheBeginningAndReadLoadsTheFullBody(t *testing.T) {
	isolateHome(t)
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, DataDirName)
	// The marker sits beyond the list excerpt so only the full Read can see it.
	tail := "\nTAIL-MARKER-" + strings.Repeat("x", 200)
	writeFile(t, filepath.Join(root, "big", "SKILL.md"),
		"---\ndescription: Big skill\n---\n\n# Big\n\nshort body\n"+strings.Repeat("filler line\n", 1200)+tail)
	service := newTestService(t, dataDir, Config{}, nil)

	listed, err := service.List(context.Background())
	if err != nil || len(listed) != 1 {
		t.Fatalf("List = %v/%v", skillNames(listed), err)
	}
	if listed[0].Description != "Big skill" {
		t.Fatalf("description from the prefix = %q", listed[0].Description)
	}
	if len(listed[0].Body) > maxListReadBytes+len("\n[truncated: skill file is larger than the read limit]\n") {
		t.Fatalf("List body = %d bytes, want only the excerpt", len(listed[0].Body))
	}
	if strings.Contains(listed[0].Body, "TAIL-MARKER") {
		t.Fatal("List read the whole file; it must only read the beginning")
	}

	loaded, err := service.Read(context.Background(), "big")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(loaded.Body, "TAIL-MARKER") {
		t.Fatalf("Read body does not contain the tail marker (len=%d)", len(loaded.Body))
	}
}

func TestUnclosedFrontmatterIsOrdinaryTextLikeRig(t *testing.T) {
	isolateHome(t)
	dataDir := t.TempDir()
	content := "---\n# Real heading\n\nbody first\n---note stays body\nmore\n"
	writeFile(t, filepath.Join(dataDir, DataDirName, "unclosed", "SKILL.md"), content)
	service := newTestService(t, dataDir, Config{}, nil)
	listed, err := service.List(context.Background())
	if err != nil || len(listed) != 1 {
		t.Fatalf("List = %v/%v", skillNames(listed), err)
	}
	// Unclosed frontmatter is not stripped: "---note" and the leading dashes are
	// ordinary Markdown text (rig rule parity, S-06).
	if !strings.Contains(listed[0].Body, "---note stays body") || !strings.HasPrefix(strings.TrimSpace(listed[0].Body), "---") {
		t.Fatalf("unclosed frontmatter was stripped: %q", listed[0].Body)
	}
	loaded, err := service.Read(context.Background(), "unclosed")
	if err != nil || loaded.Body != content {
		t.Fatalf("Read body = %q/%v, want the untouched content", loaded.Body, err)
	}
}

func TestWarningsCapAndSetPerScan(t *testing.T) {
	isolateHome(t)
	dataDir := t.TempDir()
	writeSkill(t, filepath.Join(dataDir, DataDirName), "one", "One")
	service := newTestService(t, dataDir, Config{}, nil)
	if _, err := service.List(context.Background()); err != nil {
		t.Fatalf("List: %v", err)
	}
	if warnings := service.Warnings(); len(warnings) != 0 {
		t.Fatalf("healthy scan warnings = %v, want empty", warnings)
	}
}

func TestUserConfiguredSkillDirsComeFromSettings(t *testing.T) {
	isolateHome(t)
	dataDir := t.TempDir()
	userDir := t.TempDir()
	writeSkill(t, userDir, "userlib", "User library")
	service := newTestService(t, dataDir, Config{}, nil)
	service.settings = &skillSettings{values: map[string]string{contracts.SettingSkillsDirs: userDir}}
	listed, err := service.List(context.Background())
	if err != nil || len(listed) != 1 || listed[0].Name != "userlib" {
		t.Fatalf("List = %v/%v, want the user-configured skill", skillNames(listed), err)
	}
}

func TestReadUsesFirstRootMatch(t *testing.T) {
	isolateHome(t)
	dataDir := t.TempDir()
	workspaceRoot := t.TempDir()
	workspace := &fakeWorkspace{}
	workspace.setRoot(workspaceRoot)
	writeSkill(t, filepath.Join(workspaceRoot, WorkspaceDirName), "dup", "Workspace dup")
	writeSkill(t, filepath.Join(dataDir, DataDirName), "dup", "Data dup")
	service := newTestService(t, dataDir, Config{}, workspace)
	loaded, err := service.Read(context.Background(), "dup")
	if err != nil || loaded.Description != "Workspace dup" {
		t.Fatalf("Read(dup) = %+v/%v, want the workspace root's version", loaded, err)
	}
}

func TestMissingRootsAreIgnored(t *testing.T) {
	isolateHome(t)
	dataDir := t.TempDir()
	writeSkill(t, filepath.Join(dataDir, DataDirName), "base", "Base")
	service := newTestService(t, dataDir, Config{Dirs: []string{filepath.Join(dataDir, "missing")}}, nil)
	listed, err := service.List(context.Background())
	if err != nil || len(listed) != 1 || listed[0].Name != "base" {
		t.Fatalf("List = %v/%v, want only base", skillNames(listed), err)
	}
}

var _ = os.Getenv

func TestUpdateSkillPreservesUnknownFrontmatter(t *testing.T) {
	isolateHome(t)
	dataDir := t.TempDir()
	root := filepath.Join(dataDir, DataDirName)
	path := filepath.Join(root, "pdf", "SKILL.md")
	writeFile(t, path, "---\nname: pdf\ndescription: PDF helper\nallowed-tools: bash,read_file\nlicense: MIT\n---\n\nOld body\n")
	service := newTestService(t, dataDir, Config{}, nil)
	if _, err := service.UpdateSkill(context.Background(), "pdf", contracts.SkillDraft{
		Name: "pdf", Description: "Updated helper", Body: "New body\n",
	}); err != nil {
		t.Fatalf("UpdateSkill: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read updated SKILL.md: %v", err)
	}
	content := string(raw)
	for _, want := range []string{"allowed-tools: bash,read_file", "license: MIT", "description: Updated helper", "New body"} {
		if !strings.Contains(content, want) {
			t.Fatalf("updated SKILL.md lost %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "Old body") {
		t.Fatalf("old body survived the edit:\n%s", content)
	}
	loaded, err := service.Read(context.Background(), "pdf")
	if err != nil || loaded.Description != "Updated helper" || !strings.Contains(loaded.Body, "New body") {
		t.Fatalf("Read after edit = %+v/%v", loaded, err)
	}
}
