package skills

// Tests for the skills editor half (contracts.SkillsEditor): creating a
// SKILL.md below <dataDir>/skills, rejecting unsafe/invalid drafts, deleting
// only data-dir skills and concurrent use. Written before the implementation
// (TDD).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// editService builds a service in a fresh data directory.
func editService(t *testing.T) (*service, string) {
	t.Helper()
	dataDir := t.TempDir()
	return newTestService(t, dataDir, Config{}, nil), dataDir
}

func TestCreateSkillWritesFrontmatter(t *testing.T) {
	service, dataDir := editService(t)
	created, err := service.CreateSkill(context.Background(), contracts.SkillDraft{
		Name:        "我的技能",
		Description: "用于测试的技能",
		Body:        "# 步骤\n\n1. 先做这个\n2. 再做那个",
	})
	if err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	wantPath := filepath.Join(dataDir, DataDirName, "我的技能", "SKILL.md")
	if created.Name != "我的技能" || created.Path != wantPath {
		t.Fatalf("created = %+v, want name/path for %s", created, wantPath)
	}
	body, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	want := "---\nname: 我的技能\ndescription: 用于测试的技能\n---\n\n# 步骤\n\n1. 先做这个\n2. 再做那个"
	if string(body) != want {
		t.Fatalf("SKILL.md = %q, want %q", body, want)
	}
	info, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("stat SKILL.md: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("SKILL.md mode = %o, want 0644", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(wantPath))
	if err != nil {
		t.Fatalf("stat skill dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o755 {
		t.Fatalf("skill dir mode = %o, want 0755", dirInfo.Mode().Perm())
	}

	// The new skill is discoverable through the normal List path immediately.
	listed, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "我的技能" || listed[0].Description != "用于测试的技能" {
		t.Fatalf("List = %+v, want the created skill", listed)
	}
	if strings.TrimSpace(listed[0].Body) == "" {
		t.Fatalf("listed body is empty: %+v", listed[0])
	}
}

func TestCreateSkillRejectsInvalidDrafts(t *testing.T) {
	service, _ := editService(t)
	tests := []struct {
		name  string
		draft contracts.SkillDraft
	}{
		{name: "empty name", draft: contracts.SkillDraft{Name: "  ", Body: "body"}},
		{name: "path traversal", draft: contracts.SkillDraft{Name: "../../evil", Body: "body"}},
		{name: "nested path", draft: contracts.SkillDraft{Name: "a/b", Body: "body"}},
		{name: "space", draft: contracts.SkillDraft{Name: "two words", Body: "body"}},
		{name: "too long", draft: contracts.SkillDraft{Name: strings.Repeat("x", 65), Body: "body"}},
		{name: "empty body", draft: contracts.SkillDraft{Name: "valid", Body: "   "}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.CreateSkill(context.Background(), test.draft); err == nil {
				t.Fatalf("CreateSkill(%+v) = nil error", test.draft)
			}
		})
	}
	listed, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List after rejected drafts: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("rejected drafts left skills: %+v", listed)
	}
}

func TestCreateSkillRejectsDuplicateName(t *testing.T) {
	service, _ := editService(t)
	draft := contracts.SkillDraft{Name: "dup", Body: "first"}
	if _, err := service.CreateSkill(context.Background(), draft); err != nil {
		t.Fatalf("first CreateSkill: %v", err)
	}
	duplicate := draft
	duplicate.Body = "second"
	if _, err := service.CreateSkill(context.Background(), duplicate); err == nil {
		t.Fatal("duplicate CreateSkill = nil error")
	}
}

func TestCreateSkillDescriptionOptional(t *testing.T) {
	service, dataDir := editService(t)
	if _, err := service.CreateSkill(context.Background(), contracts.SkillDraft{Name: "nodesc", Body: "body"}); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dataDir, DataDirName, "nodesc", "SKILL.md"))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	if !strings.Contains(string(body), "name: nodesc\n") || !strings.Contains(string(body), "description: ") {
		t.Fatalf("SKILL.md = %q, want empty description frontmatter", body)
	}
}

func TestDeleteSkillRemovesDataDirSkill(t *testing.T) {
	service, dataDir := editService(t)
	if _, err := service.CreateSkill(context.Background(), contracts.SkillDraft{Name: "gone", Body: "body"}); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	if err := service.DeleteSkill(context.Background(), "gone"); err != nil {
		t.Fatalf("DeleteSkill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, DataDirName, "gone")); !os.IsNotExist(err) {
		t.Fatalf("skill dir still exists after delete: %v", err)
	}
	listed, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("List after delete = %+v, want none", listed)
	}
}

func TestDeleteSkillRejectsWorkspaceSkill(t *testing.T) {
	dataDir := t.TempDir()
	workspaceRoot := t.TempDir()
	writeSkill(t, filepath.Join(workspaceRoot, WorkspaceDirName), "work", "Workspace skill")
	workspace := &fakeWorkspace{}
	workspace.setRoot(workspaceRoot)
	service := newTestService(t, dataDir, Config{}, workspace)

	err := service.DeleteSkill(context.Background(), "work")
	if err == nil {
		t.Fatal("DeleteSkill(workspace skill) = nil error, want a rejection")
	}
	if _, statErr := os.Stat(filepath.Join(workspaceRoot, WorkspaceDirName, "work", "SKILL.md")); statErr != nil {
		t.Fatalf("workspace skill was deleted: %v", statErr)
	}
}

func TestDeleteSkillRejectsUnknownAndUnsafeNames(t *testing.T) {
	service, dataDir := editService(t)
	for _, name := range []string{"", "missing", "../../escape", "a/b"} {
		if err := service.DeleteSkill(context.Background(), name); err == nil {
			t.Fatalf("DeleteSkill(%q) = nil error", name)
		}
	}
	if _, err := os.Stat(dataDir); err != nil {
		t.Fatalf("data dir was removed: %v", err)
	}
}

func TestSkillsEditorConcurrentCreates(t *testing.T) {
	service, _ := editService(t)
	const count = 8
	var group sync.WaitGroup
	errs := make([]error, count)
	for index := range errs {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, errs[index] = service.CreateSkill(context.Background(), contracts.SkillDraft{
				Name: "skill-" + string(rune('a'+index)),
				Body: "body",
			})
		}(index)
	}
	group.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("concurrent CreateSkill #%d: %v", index, err)
		}
	}
	listed, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != count {
		t.Fatalf("List after concurrent creates = %d skills, want %d", len(listed), count)
	}
}

// TestModuleProvidesSkillsEditor checks the module's descriptor: it provides
// both read and write keys for the same service.
func TestModuleProvidesSkillsEditor(t *testing.T) {
	provides := make(map[string]bool)
	for _, key := range Module().Provides {
		provides[key.Name] = true
	}
	for _, key := range []string{contracts.KeySkills.Name(), contracts.KeySkillsEditor.Name()} {
		if !provides[key] {
			t.Fatalf("module provides = %+v, missing %q", Module().Provides, key)
		}
	}
}
