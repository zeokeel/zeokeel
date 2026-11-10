package regression

// Adversarial audit: editing data-directory skills (contracts.SkillsEditor
// UpdateSkill) and the settings edit entry. Negative/boundary cases: rewrite
// replaces the old content, the name is immutable, unknown/configured/workspace
// skills are rejected, the body is required, canceled contexts are safe, and
// concurrent updates are atomic (-race).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/skills"
	settingsui "github.com/zeokeel/zeokeel/internal/ui/settings"
)

// skillTemps lists leftover atomic-write temp files below one skill directory.
func skillTemps(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	temps := make([]string, 0)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".SKILL.md.tmp-") {
			temps = append(temps, entry.Name())
		}
	}
	return temps
}

// TestSkillEditorUpdateRoundTrip covers a successful rewrite: the old body is
// gone, the new body/frontmatter is visible immediately, the name is unchanged,
// the file mode is 0644 and no temp files remain.
func TestSkillEditorUpdateRoundTrip(t *testing.T) {
	harness := startSkills(t, skills.Config{})
	ctx := context.Background()
	created, err := harness.editor.CreateSkill(ctx, contracts.SkillDraft{
		Name: "audit-update", Description: "old desc", Body: "OLD-MARK body",
	})
	if err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	updated, err := harness.editor.UpdateSkill(ctx, "audit-update", contracts.SkillDraft{
		Name: "audit-update", Description: "  new  desc  ", Body: "  NEW-BODY content  ",
	})
	if err != nil {
		t.Fatalf("UpdateSkill: %v", err)
	}
	if updated.Name != "audit-update" || updated.Description != "new desc" || updated.Body != "NEW-BODY content" {
		t.Fatalf("updated skill = %+v", updated)
	}
	read, err := harness.service.Read(ctx, "audit-update")
	if err != nil {
		t.Fatalf("Read after update: %v", err)
	}
	if strings.Contains(read.Body, "OLD-MARK") || !strings.Contains(read.Body, "NEW-BODY") {
		t.Fatalf("Read body = %q, want the rewritten content only", read.Body)
	}
	body, err := os.ReadFile(created.Path)
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	if strings.Contains(string(body), "OLD-MARK") || !strings.Contains(string(body), "description: new desc") {
		t.Fatalf("SKILL.md = %q", body)
	}
	info, err := os.Stat(created.Path)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("SKILL.md mode = %v (err %v), want 0644", info.Mode(), err)
	}
	if temps := skillTemps(t, filepath.Dir(created.Path)); len(temps) != 0 {
		t.Fatalf("leftover temp files after update: %v", temps)
	}
	if names := strings.Join(harness.skillNames(t), ","); strings.Count(names, "audit-update") != 1 {
		t.Fatalf("skill list after update = %v", harness.skillNames(t))
	}
}

// TestSkillEditorUpdateRejections covers the rejected shapes: rename attempts,
// unknown names, empty bodies, read-only configured roots, symlinks and a
// canceled context; nothing is rewritten.
func TestSkillEditorUpdateRejections(t *testing.T) {
	harness := startSkills(t, skills.Config{Dirs: []string{"extra"}})
	ctx := context.Background()
	created, err := harness.editor.CreateSkill(ctx, contracts.SkillDraft{
		Name: "audit-keep", Body: "KEEP-BODY",
	})
	if err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	// A configured (read-only) root skill.
	configuredRoot := filepath.Join(harness.h.dataDir, "extra")
	configuredPath := writeSkillFile(t, configuredRoot, "configured-skill", "CONFIGURED-BODY")

	for name, test := range map[string]struct {
		target string
		draft  contracts.SkillDraft
	}{
		"rename":          {target: "audit-keep", draft: contracts.SkillDraft{Name: "other-name", Body: "x"}},
		"unknown":         {target: "missing-skill", draft: contracts.SkillDraft{Name: "missing-skill", Body: "x"}},
		"empty body":      {target: "audit-keep", draft: contracts.SkillDraft{Name: "audit-keep", Body: "   "}},
		"configured root": {target: "configured-skill", draft: contracts.SkillDraft{Name: "configured-skill", Body: "x"}},
		"invalid name":    {target: "../escape", draft: contracts.SkillDraft{Name: "../escape", Body: "x"}},
	} {
		if _, err := harness.editor.UpdateSkill(ctx, test.target, test.draft); err == nil {
			t.Fatalf("%s: UpdateSkill succeeded, want an error", name)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := harness.editor.UpdateSkill(canceled, "audit-keep", contracts.SkillDraft{Name: "audit-keep", Body: "x"}); err == nil {
		t.Fatal("canceled-context UpdateSkill succeeded")
	}

	// A symlinked skill directory is refused.
	outside := t.TempDir()
	outsideFile := writeSkillFile(t, outside, "outside-skill", "OUTSIDE")
	link := filepath.Join(harness.dataSkillsDir(), "linked-skill")
	if err := os.MkdirAll(harness.dataSkillsDir(), 0o755); err != nil {
		t.Fatalf("mkdir skills root: %v", err)
	}
	if err := os.Symlink(filepath.Dir(outsideFile), link); err != nil {
		t.Fatalf("symlink skill: %v", err)
	}
	if _, err := harness.editor.UpdateSkill(ctx, "linked-skill", contracts.SkillDraft{Name: "linked-skill", Body: "x"}); err == nil {
		t.Fatal("UpdateSkill followed a symlinked skill directory")
	}

	// The original data-dir skill and the configured skill are untouched.
	read, err := harness.service.Read(ctx, "audit-keep")
	if err != nil || !strings.Contains(read.Body, "KEEP-BODY") {
		t.Fatalf("data-dir skill after rejections = %+v, %v", read, err)
	}
	configured, err := os.ReadFile(configuredPath)
	if err != nil || !strings.Contains(string(configured), "CONFIGURED-BODY") {
		t.Fatalf("configured skill changed: %q, %v", configured, err)
	}
	if temps := skillTemps(t, filepath.Dir(created.Path)); len(temps) != 0 {
		t.Fatalf("leftover temp files after rejections: %v", temps)
	}
}

// TestSkillEditorUpdateConcurrent covers concurrent writers and readers: updates
// stay atomic (-race), the final file is one complete body and no temp files
// remain.
func TestSkillEditorUpdateConcurrent(t *testing.T) {
	harness := startSkills(t, skills.Config{})
	ctx := context.Background()
	created, err := harness.editor.CreateSkill(ctx, contracts.SkillDraft{Name: "audit-race", Body: "BASE"})
	if err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	bodies := make([]string, 8)
	for index := range bodies {
		bodies[index] = "BODY-" + itoa(int64(index))
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(bodies)*2)
	for _, body := range bodies {
		body := body
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := harness.editor.UpdateSkill(ctx, "audit-race", contracts.SkillDraft{
				Name: "audit-race", Body: body,
			}); err != nil {
				errs <- err
			}
		}()
	}
	for index := 0; index < 4; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := harness.service.List(ctx); err != nil {
				errs <- err
			}
			if _, err := harness.service.Read(ctx, "audit-race"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent skill update/read: %v", err)
	}

	read, err := harness.service.Read(ctx, "audit-race")
	if err != nil {
		t.Fatalf("Read after concurrent updates: %v", err)
	}
	valid := false
	for _, body := range bodies {
		if read.Body == body {
			valid = true
		}
	}
	if !valid {
		t.Fatalf("final body = %q, want one complete writer body", read.Body)
	}
	fileBody, err := os.ReadFile(created.Path)
	if err != nil || !strings.Contains(string(fileBody), read.Body) {
		t.Fatalf("SKILL.md final content mismatch: %v", err)
	}
	if temps := skillTemps(t, filepath.Dir(created.Path)); len(temps) != 0 {
		t.Fatalf("leftover temp files after concurrent updates: %v", temps)
	}
}

// TestSettingsSkillsEditEntry covers the settings page edit entry: the data-dir
// skill card has 编辑, the modal is prefilled with the read-only name, saving
// reaches UpdateSkill and the new body is visible in the list.
func TestSettingsSkillsEditEntry(t *testing.T) {
	harness := startSkills(t, skills.Config{})
	ctx := context.Background()
	if _, err := harness.editor.CreateSkill(ctx, contracts.SkillDraft{
		Name: "alpha", Description: "first", Body: "old body",
	}); err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	provide(t, harness.h.ctx, contracts.KeyChat, contracts.ChatService(newFakeChat()))
	activate(t, harness.h.ctx, settingsui.Module(), settingsui.Config{})
	harness.h.ui.Navigator().ToggleSettings()
	tester := harness.h.tester(1280, 800)
	tester.Frame()
	if err := tester.Click("技能"); err != nil {
		t.Fatalf("open skills page: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if err := tester.Click("技能操作"); err != nil {
		t.Fatalf("open skill row menu: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if err := tester.Click("编辑技能"); err != nil {
		t.Fatalf("click 编辑技能: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasTexts(t, tester, "编辑技能", "alpha", "名称不可修改", "描述", "内容", "保存", "取消")

	// Replace the body text and save. The interactive target is the accessibly
	// labeled body input ("内容 输入"), not the field's visible label.
	if err := tester.Click("内容 输入"); err != nil {
		t.Fatalf("focus body: %v", err)
	}
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type("brand new body")
	tester.Frame()
	if err := tester.Click("保存"); err != nil {
		t.Fatalf("click 保存: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	requireHasText(t, tester, "已保存")
	requireNoText(t, tester, "编辑技能")
	read, err := harness.service.Read(ctx, "alpha")
	if err != nil || !strings.Contains(read.Body, "brand new body") || strings.Contains(read.Body, "old body") {
		t.Fatalf("skill after page edit = %+v, %v", read, err)
	}
}
