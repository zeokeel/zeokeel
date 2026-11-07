package regression

// Adversarial audit: the skills editor (contracts.SkillsEditor) that landed in
// the working tree during this round. Negative/boundary cases: draft validation
// (empty/too-long/unsafe names, empty bodies, YAML-injection descriptions),
// duplicate creation, deletion boundaries (unknown, traversal, configured root,
// symlinks, non-skill directories) and canceled contexts leaving zero side
// effects. Only <dataDir>/skills is writable.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/skills"
)

// skillsHarness is the activated skills backend with both halves.
type skillsHarness struct {
	h       *harness
	service contracts.SkillsService
	editor  contracts.SkillsEditor
}

// startSkills activates the skills module with the supplied config.
func startSkills(t *testing.T, cfg skills.Config) *skillsHarness {
	t.Helper()
	h := newBareHarness(t)
	activate(t, h.ctx, skills.Module(), cfg)
	return &skillsHarness{
		h:       h,
		service: requireService(t, h.ctx, contracts.KeySkills),
		editor:  requireService(t, h.ctx, contracts.KeySkillsEditor),
	}
}

// dataSkillsDir returns <dataDir>/skills.
func (s *skillsHarness) dataSkillsDir() string {
	return filepath.Join(s.h.dataDir, skills.DataDirName)
}

// skillNames lists the skill names reported by the backend.
func (s *skillsHarness) skillNames(t *testing.T) []string {
	t.Helper()
	list, err := s.service.List(context.Background())
	if err != nil {
		t.Fatalf("List skills: %v", err)
	}
	names := make([]string, 0, len(list))
	for _, skill := range list {
		names = append(names, skill.Name)
	}
	return names
}

// writeSkillFile seeds one SKILL.md below root/<name>.
func writeSkillFile(t *testing.T, root, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, "SKILL.md")
	content := "---\nname: " + name + "\ndescription: seeded\n---\n\n" + body
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// TestSkillEditorCreateValidates covers draft validation: unsafe/empty/long
// names and empty bodies are rejected without touching the skills directory; a
// valid draft writes SKILL.md with folded frontmatter and is immediately
// readable; a duplicate name never overwrites.
func TestSkillEditorCreateValidates(t *testing.T) {
	harness := startSkills(t, skills.Config{})
	ctx := context.Background()

	for name, draft := range map[string]contracts.SkillDraft{
		"empty name":      {Name: "  ", Body: "body"},
		"empty body":      {Name: "valid-name", Body: "  \n "},
		"space in name":   {Name: "two words", Body: "body"},
		"dot in name":     {Name: "evil.name", Body: "body"},
		"slash in name":   {Name: "a/b", Body: "body"},
		"traversal name":  {Name: "../escape", Body: "body"},
		"backslash name":  {Name: `..\escape`, Body: "body"},
		"newline in name": {Name: "line\nbreak", Body: "body"},
		"too long name":   {Name: strings.Repeat("a", 65), Body: "body"},
	} {
		if _, err := harness.editor.CreateSkill(ctx, draft); err == nil {
			t.Fatalf("%s: CreateSkill succeeded, want a validation error", name)
		}
	}
	if entries, err := os.ReadDir(harness.dataSkillsDir()); err == nil && len(entries) != 0 {
		t.Fatalf("rejected drafts left %d entries in the skills directory", len(entries))
	}

	created, err := harness.editor.CreateSkill(ctx, contracts.SkillDraft{
		Name:        "audit-skill_1",
		Description: "first line\nsecond   line",
		Body:        "  # Body\ncontent  ",
	})
	if err != nil {
		t.Fatalf("CreateSkill(valid): %v", err)
	}
	if created.Name != "audit-skill_1" {
		t.Fatalf("created Name = %q", created.Name)
	}
	if created.Description != "first line second line" {
		t.Fatalf("created Description = %q, want folded (no newline)", created.Description)
	}
	if created.Body != "# Body\ncontent" {
		t.Fatalf("created Body = %q, want trimmed markdown", created.Body)
	}
	if !strings.Contains(created.Path, filepath.Join(harness.dataSkillsDir(), "audit-skill_1", "SKILL.md")) {
		t.Fatalf("created Path = %q, want below the data-dir skills root", created.Path)
	}
	body, err := os.ReadFile(created.Path)
	if err != nil {
		t.Fatalf("read created SKILL.md: %v", err)
	}
	if !strings.Contains(string(body), "name: audit-skill_1") ||
		!strings.Contains(string(body), "description: first line second line") ||
		strings.Contains(string(body), "\ninjection:") {
		t.Fatalf("SKILL.md frontmatter = %q", body)
	}
	listed := strings.Join(harness.skillNames(t), ",")
	if !strings.Contains(listed, "audit-skill_1") {
		t.Fatalf("created skill is not listed: %v", harness.skillNames(t))
	}
	read, err := harness.service.Read(ctx, "audit-skill_1")
	if err != nil || !strings.Contains(read.Body, "# Body") {
		t.Fatalf("Read(created) = %+v, %v", read, err)
	}

	// Chinese characters are accepted in slugs (the product is zh-first).
	if _, err := harness.editor.CreateSkill(ctx, contracts.SkillDraft{Name: "审计技能-1", Body: "中文正文"}); err != nil {
		t.Fatalf("CreateSkill(Chinese name): %v", err)
	}
	if names := strings.Join(harness.skillNames(t), ","); !strings.Contains(names, "审计技能-1") {
		t.Fatalf("Chinese-named skill is not listed: %v", harness.skillNames(t))
	}

	// Duplicate names are rejected and never overwritten.
	if _, err := harness.editor.CreateSkill(ctx, contracts.SkillDraft{
		Name: "audit-skill_1", Body: "replacement body",
	}); err == nil {
		t.Fatal("duplicate CreateSkill succeeded, want an error")
	}
	read, _ = harness.service.Read(ctx, "audit-skill_1")
	if !strings.Contains(read.Body, "# Body") || strings.Contains(read.Body, "replacement") {
		t.Fatalf("duplicate create overwrote the skill: %+v", read)
	}
}

// TestSkillEditorDeleteBoundaries covers deletion: data-dir skills are removed,
// unknown/traversal names are rejected, configured-root skills and non-skill
// directories stay untouched, and symlinks are refused.
func TestSkillEditorDeleteBoundaries(t *testing.T) {
	// One configured read-only root relative to the data directory.
	harness := startSkills(t, skills.Config{Dirs: []string{"extra-skills"}})
	ctx := context.Background()
	configuredRoot := filepath.Join(harness.h.dataDir, "extra-skills")
	configuredPath := writeSkillFile(t, configuredRoot, "seeded-config", "configured body")

	created, err := harness.editor.CreateSkill(ctx, contracts.SkillDraft{Name: "data-skill", Body: "data body"})
	if err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	names := strings.Join(harness.skillNames(t), ",")
	if !strings.Contains(names, "data-skill") || !strings.Contains(names, "seeded-config") {
		t.Fatalf("List skills = %v, want the data-dir and configured skills", harness.skillNames(t))
	}

	// A skill directory may carry nested files; delete removes the whole tree.
	nested := filepath.Join(filepath.Dir(created.Path), "references", "notes.md")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatalf("mkdir nested skill files: %v", err)
	}
	if err := os.WriteFile(nested, []byte("notes"), 0o644); err != nil {
		t.Fatalf("write nested skill file: %v", err)
	}
	if err := harness.editor.DeleteSkill(ctx, "data-skill"); err != nil {
		t.Fatalf("DeleteSkill(data-skill): %v", err)
	}
	if _, err := os.Stat(filepath.Dir(created.Path)); !os.IsNotExist(err) {
		t.Fatalf("deleted skill directory still exists (err = %v)", err)
	}
	if names := strings.Join(harness.skillNames(t), ","); strings.Contains(names, "data-skill") {
		t.Fatalf("deleted skill still listed: %v", harness.skillNames(t))
	}

	// Configured-root skills are read-only for the editor.
	if err := harness.editor.DeleteSkill(ctx, "seeded-config"); err == nil {
		t.Fatal("DeleteSkill removed a configured-root skill, want an error")
	}
	if _, err := os.Stat(configuredPath); err != nil {
		t.Fatalf("configured skill file was removed: %v", err)
	}

	for _, name := range []string{"", "missing-skill", "../escape", "a/b"} {
		if err := harness.editor.DeleteSkill(ctx, name); err == nil {
			t.Fatalf("DeleteSkill(%q) succeeded, want an error", name)
		}
	}

	// A directory without SKILL.md is not a skill and must survive.
	plainDir := filepath.Join(harness.dataSkillsDir(), "plain-dir")
	if err := os.MkdirAll(plainDir, 0o755); err != nil {
		t.Fatalf("mkdir plain dir: %v", err)
	}
	if err := harness.editor.DeleteSkill(ctx, "plain-dir"); err == nil {
		t.Fatal("DeleteSkill removed a directory without SKILL.md")
	}
	if _, err := os.Stat(plainDir); err != nil {
		t.Fatalf("plain directory was removed: %v", err)
	}

	// A symlinked skill below the data-dir root is refused.
	outside := t.TempDir()
	outsideSkill := writeSkillFile(t, outside, "outside-skill", "outside body")
	link := filepath.Join(harness.dataSkillsDir(), "linked-skill")
	if err := os.MkdirAll(harness.dataSkillsDir(), 0o755); err != nil {
		t.Fatalf("mkdir skills root: %v", err)
	}
	if err := os.Symlink(filepath.Dir(outsideSkill), link); err != nil {
		t.Fatalf("create symlinked skill: %v", err)
	}
	if err := harness.editor.DeleteSkill(ctx, "linked-skill"); err == nil {
		t.Fatal("DeleteSkill removed a symlinked skill, want a refusal")
	}
	if _, err := os.Stat(outsideSkill); err != nil {
		t.Fatalf("outside skill file was removed through the symlink: %v", err)
	}
}

// TestSkillEditorCanceledContextHasNoSideEffects covers context cancellation: a
// canceled context rejects both create and delete before touching the disk.
func TestSkillEditorCanceledContextHasNoSideEffects(t *testing.T) {
	harness := startSkills(t, skills.Config{})
	created, err := harness.editor.CreateSkill(context.Background(), contracts.SkillDraft{
		Name: "keep-skill", Body: "keep body",
	})
	if err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := harness.editor.CreateSkill(canceled, contracts.SkillDraft{Name: "new-skill", Body: "b"}); err == nil {
		t.Fatal("CreateSkill with a canceled context succeeded")
	}
	if _, err := os.Stat(filepath.Join(harness.dataSkillsDir(), "new-skill")); !os.IsNotExist(err) {
		t.Fatal("canceled CreateSkill created a directory")
	}
	if err := harness.editor.DeleteSkill(canceled, "keep-skill"); err == nil {
		t.Fatal("DeleteSkill with a canceled context succeeded")
	}
	if _, err := os.Stat(filepath.Dir(created.Path)); err != nil {
		t.Fatalf("canceled DeleteSkill removed the skill: %v", err)
	}
}
