package skills

// Tests for the skills editor's UpdateSkill half.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func createTestSkill(t *testing.T, service *service, name, description, body string) {
	t.Helper()
	if _, err := service.CreateSkill(context.Background(), contracts.SkillDraft{
		Name: name, Description: description, Body: body,
	}); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
}

func TestUpdateSkillRewritesDataDirSkill(t *testing.T) {
	service, dataDir := editService(t)
	createTestSkill(t, service, "editable", "old description", "# old body")

	updated, err := service.UpdateSkill(context.Background(), "editable", contracts.SkillDraft{
		Name: "editable", Description: "new description\nwith newline", Body: "# new body\n",
	})
	if err != nil {
		t.Fatalf("UpdateSkill: %v", err)
	}
	wantPath := filepath.Join(dataDir, DataDirName, "editable", "SKILL.md")
	if updated.Name != "editable" || updated.Path != wantPath || updated.Description != "new description with newline" {
		t.Fatalf("updated = %+v", updated)
	}
	raw, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	if strings.Contains(string(raw), "old body") || strings.Contains(string(raw), "old description") {
		t.Fatalf("old content survived: %q", raw)
	}
	want := "---\nname: editable\ndescription: new description with newline\n---\n\n# new body"
	if string(raw) != want {
		t.Fatalf("SKILL.md = %q, want %q", raw, want)
	}
	info, err := os.Stat(wantPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %o, want 0644", info.Mode().Perm())
	}

	read, err := service.Read(context.Background(), "editable")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(read.Body, "# new body") || strings.Contains(read.Body, "old body") {
		t.Fatalf("Read body = %q", read.Body)
	}
	if read.Description != "new description with newline" {
		t.Fatalf("Read description = %q", read.Description)
	}
	listed, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].Description != "new description with newline" {
		t.Fatalf("List = %+v", listed)
	}
}

func TestUpdateSkillRejectsUnknownWorkspaceAndInvalidDrafts(t *testing.T) {
	dataDir := t.TempDir()
	workspaceRoot := t.TempDir()
	writeSkill(t, filepath.Join(workspaceRoot, WorkspaceDirName), "workspace-skill", "Workspace skill")
	workspace := &fakeWorkspace{}
	workspace.setRoot(workspaceRoot)
	service := newTestService(t, dataDir, Config{}, workspace)
	createTestSkill(t, service, "kept", "", "body")

	for _, test := range []struct {
		name  string
		skill string
		draft contracts.SkillDraft
	}{
		{name: "unknown", skill: "missing", draft: contracts.SkillDraft{Name: "missing", Body: "body"}},
		{name: "workspace", skill: "workspace-skill", draft: contracts.SkillDraft{Name: "workspace-skill", Body: "body"}},
		{name: "empty body", skill: "kept", draft: contracts.SkillDraft{Name: "kept", Body: "   "}},
		{name: "empty name", skill: "kept", draft: contracts.SkillDraft{Name: "  ", Body: "body"}},
		{name: "renamed", skill: "kept", draft: contracts.SkillDraft{Name: "renamed", Body: "body"}},
		{name: "traversal", skill: "kept", draft: contracts.SkillDraft{Name: "../escape", Body: "body"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.UpdateSkill(context.Background(), test.skill, test.draft); err == nil {
				t.Fatalf("UpdateSkill(%q, %+v) = nil error", test.skill, test.draft)
			}
		})
	}
	if raw, err := os.ReadFile(filepath.Join(dataDir, DataDirName, "kept", "SKILL.md")); err != nil || !strings.Contains(string(raw), "body") {
		t.Fatalf("kept skill damaged: %q, %v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, WorkspaceDirName, "workspace-skill", "SKILL.md")); err != nil {
		t.Fatalf("workspace skill removed: %v", err)
	}
}

func TestUpdateSkillConcurrent(t *testing.T) {
	service, _ := editService(t)
	createTestSkill(t, service, "race", "", "initial")
	const count = 8
	var group sync.WaitGroup
	errs := make([]error, count)
	for index := range errs {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, errs[index] = service.UpdateSkill(context.Background(), "race", contracts.SkillDraft{
				Name: "race", Body: "body-" + string(rune('a'+index)),
			})
		}(index)
	}
	group.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("concurrent UpdateSkill #%d: %v", index, err)
		}
	}
	read, err := service.Read(context.Background(), "race")
	if err != nil {
		t.Fatalf("Read after concurrent updates: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(read.Body), "body-") {
		t.Fatalf("final body = %q", read.Body)
	}
}
