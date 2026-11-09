package settings_test

// Tests for the skills page's editor half (contracts.SkillsEditor): the new
// skill modal, validation, create/delete calls, hiding both actions without the
// optional editor service and the English copy. Written before the
// implementation (TDD).

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeSkillsEditor is a scripted contracts.SkillsEditor wired to the fake
// skills list so a successful create/delete is visible on the next refresh.
type fakeSkillsEditor struct {
	mu sync.Mutex

	skills    *fakeSkills
	creates   []contracts.SkillDraft
	updates   []contracts.SkillDraft
	deletes   []string
	createErr error
	updateErr error
	deleteErr error
}

func newFakeSkillsEditor(skills *fakeSkills) *fakeSkillsEditor {
	return &fakeSkillsEditor{skills: skills}
}

func (f *fakeSkillsEditor) CreateSkill(_ context.Context, draft contracts.SkillDraft) (contracts.Skill, error) {
	f.mu.Lock()
	f.creates = append(f.creates, draft)
	err := f.createErr
	f.mu.Unlock()
	if err != nil {
		return contracts.Skill{}, err
	}
	skill := contracts.Skill{Name: draft.Name, Description: draft.Description, Path: "/created/" + draft.Name + "/SKILL.md", Body: draft.Body}
	if f.skills != nil {
		f.skills.mu.Lock()
		f.skills.list = append(f.skills.list, skill)
		f.skills.mu.Unlock()
	}
	return skill, nil
}

func (f *fakeSkillsEditor) UpdateSkill(_ context.Context, name string, draft contracts.SkillDraft) (contracts.Skill, error) {
	f.mu.Lock()
	f.updates = append(f.updates, draft)
	err := f.updateErr
	f.mu.Unlock()
	if err != nil {
		return contracts.Skill{}, err
	}
	skill := contracts.Skill{Name: name, Description: draft.Description, Path: "/updated/" + name + "/SKILL.md", Body: draft.Body}
	if f.skills != nil {
		f.skills.mu.Lock()
		for index := range f.skills.list {
			if f.skills.list[index].Name == name {
				f.skills.list[index] = skill
			}
		}
		f.skills.mu.Unlock()
	}
	return skill, nil
}

func (f *fakeSkillsEditor) DeleteSkill(_ context.Context, name string) error {
	f.mu.Lock()
	f.deletes = append(f.deletes, name)
	err := f.deleteErr
	f.mu.Unlock()
	if err != nil {
		return err
	}
	if f.skills != nil {
		f.skills.mu.Lock()
		remaining := f.skills.list[:0]
		for _, skill := range f.skills.list {
			if skill.Name != name {
				remaining = append(remaining, skill)
			}
		}
		f.skills.list = remaining
		f.skills.mu.Unlock()
	}
	return nil
}

func (f *fakeSkillsEditor) created() []contracts.SkillDraft {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.SkillDraft(nil), f.creates...)
}

func (f *fakeSkillsEditor) updated() []contracts.SkillDraft {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.SkillDraft(nil), f.updates...)
}

func (f *fakeSkillsEditor) deleted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deletes...)
}

// skillsEditorHarness opens the skills page with the optional editor and data
// directory provided after activation (the page looks both up lazily per frame).
func skillsEditorHarness(t *testing.T, skills *fakeSkills, editor contracts.SkillsEditor, dataDir string) *harness {
	t.Helper()
	h := newHarnessWithSkills(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, skills)
	if editor != nil {
		provide[contracts.SkillsEditor](t, h.ctx, contracts.KeySkillsEditor, editor)
	}
	if dataDir != "" {
		provide(t, h.ctx, contracts.KeyDataDir, dataDir)
	}
	tester := h.tester()
	tester.Frame()
	h.click(tester, "技能")
	return h
}

// newLocaleSkillsEditorHarness is the same harness with an explicit locale and
// the editor/data directory provided before activation.
func newLocaleSkillsEditorHarness(t *testing.T, language string, skills contracts.SkillsService,
	editor contracts.SkillsEditor, dataDir string) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel", Language: language})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	store := newFakeSettings(nil)
	chat := &fakeChat{}
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(t, ctx, contracts.KeySettings, contracts.SettingsService(store))
	provide(t, ctx, contracts.KeyChat, contracts.ChatService(chat))
	provide[contracts.SkillsService](t, ctx, contracts.KeySkills, skills)
	if editor != nil {
		provide[contracts.SkillsEditor](t, ctx, contracts.KeySkillsEditor, editor)
	}
	if dataDir != "" {
		provide(t, ctx, contracts.KeyDataDir, dataDir)
	}
	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot, settings: store, chat: chat}
	h.fiber = activate(t, ctx, settings.Module(), settings.Config{})
	uiRoot.Navigator().ToggleSettings()
	return h
}

func fillSkillDraft(t *testing.T, h *harness, tester *ui.Tester, name, description, body string) {
	t.Helper()
	if name != "" {
		h.focusInput(tester, "名称")
		tester.Type(name)
		tester.Frame()
	}
	if description != "" {
		h.focusInput(tester, "描述")
		tester.Type(description)
		tester.Frame()
	}
	if body != "" {
		h.focusInput(tester, "内容")
		tester.Type(body)
		tester.Frame()
	}
}

// TestSkillsEditorActionsHiddenWithoutEditor covers the optional service: with
// only the read-only SkillsService neither the create nor the delete action is
// rendered.
func TestSkillsEditorActionsHiddenWithoutEditor(t *testing.T) {
	dataDir := t.TempDir()
	skills := newFakeSkills()
	skills.setList(contracts.Skill{Name: "alpha", Path: filepath.Join(dataDir, "skills", "alpha", "SKILL.md")})
	h := skillsEditorHarness(t, skills, nil, dataDir)
	tester := h.tester()
	h.assertTexts(tester, "alpha")
	h.assertNoTexts(tester, "新建技能", "删除")
}

func TestSkillsCreateModalCallsEditor(t *testing.T) {
	dataDir := t.TempDir()
	skills := newFakeSkills()
	skills.setList(contracts.Skill{Name: "existing", Path: filepath.Join(dataDir, "skills", "existing", "SKILL.md")})
	editor := newFakeSkillsEditor(skills)
	h := skillsEditorHarness(t, skills, editor, dataDir)
	tester := h.tester()
	h.assertTexts(tester, "新建技能")

	h.click(tester, "新建技能")
	h.assertTexts(tester, "名称", "描述", "内容", "创建", "取消")
	fillSkillDraft(t, h, tester, "新技能", "技能说明", "# 正文")
	h.click(tester, "创建")

	creates := editor.created()
	if len(creates) != 1 {
		t.Fatalf("CreateSkill calls = %d, want 1", len(creates))
	}
	if creates[0].Name != "新技能" || creates[0].Description != "技能说明" || creates[0].Body != "# 正文" {
		t.Fatalf("CreateSkill draft = %+v", creates[0])
	}
	h.assertTexts(tester, "已创建技能", "新技能")
	if got := exactTextCount(tester.Texts(), "创建"); got != 0 {
		t.Fatalf("create modal still open: %d 创建 texts; texts = %v", got, tester.Texts())
	}
}

func TestSkillsCreateValidation(t *testing.T) {
	dataDir := t.TempDir()
	skills := newFakeSkills()
	editor := newFakeSkillsEditor(skills)
	h := skillsEditorHarness(t, skills, editor, dataDir)
	tester := h.tester()

	h.click(tester, "新建技能")
	fillSkillDraft(t, h, tester, "只有名称", "", "")
	h.click(tester, "创建")
	if got := editor.created(); len(got) != 0 {
		t.Fatalf("CreateSkill calls with empty body = %v, want none", got)
	}
	h.assertTexts(tester, "名称和内容不能为空")
}

func TestSkillsCreateError(t *testing.T) {
	dataDir := t.TempDir()
	skills := newFakeSkills()
	editor := newFakeSkillsEditor(skills)
	editor.createErr = errors.New("disk full")
	h := skillsEditorHarness(t, skills, editor, dataDir)
	tester := h.tester()

	h.click(tester, "新建技能")
	fillSkillDraft(t, h, tester, "会失败", "", "body")
	h.click(tester, "创建")
	h.assertTexts(tester, "创建技能失败：disk full")
	// The modal stays open so the draft can be fixed.
	h.assertTexts(tester, "名称", "内容")
}

func TestSkillsDeleteOnlyDataDirSkills(t *testing.T) {
	dataDir := t.TempDir()
	workspaceSkill := filepath.Join(t.TempDir(), "skills", "beta", "SKILL.md")
	skills := newFakeSkills()
	skills.setList(
		contracts.Skill{Name: "alpha", Path: filepath.Join(dataDir, "skills", "alpha", "SKILL.md")},
		contracts.Skill{Name: "beta", Path: workspaceSkill},
	)
	editor := newFakeSkillsEditor(skills)
	h := skillsEditorHarness(t, skills, editor, dataDir)
	tester := h.tester()

	if got := exactTextCount(tester.Texts(), "删除"); got != 1 {
		t.Fatalf("delete buttons = %d, want 1 (only the dataDir skill); texts = %v", got, tester.Texts())
	}
	h.click(tester, "删除")
	h.assertTexts(tester, "删除技能", "alpha")
	h.click(tester, "确认删除")

	if got := editor.deleted(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("DeleteSkill calls = %v, want [alpha]", got)
	}
	h.assertTexts(tester, "已删除技能", "beta")
	h.assertNoTexts(tester, "alpha")
}

func TestSkillsDeleteError(t *testing.T) {
	dataDir := t.TempDir()
	skills := newFakeSkills()
	skills.setList(contracts.Skill{Name: "alpha", Path: filepath.Join(dataDir, "skills", "alpha", "SKILL.md")})
	editor := newFakeSkillsEditor(skills)
	editor.deleteErr = errors.New("locked")
	h := skillsEditorHarness(t, skills, editor, dataDir)
	tester := h.tester()

	h.click(tester, "删除")
	h.click(tester, "确认删除")
	h.assertTexts(tester, "删除技能失败：locked")
	h.assertNoTexts(tester, "已删除技能")
}

func TestSkillsEditorEnglish(t *testing.T) {
	dataDir := t.TempDir()
	skills := newFakeSkills()
	skills.setList(contracts.Skill{Name: "alpha", Path: filepath.Join(dataDir, "skills", "alpha", "SKILL.md")})
	editor := newFakeSkillsEditor(skills)
	h := newLocaleSkillsEditorHarness(t, "en", skills, editor, dataDir)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Skills")
	h.assertTexts(tester, "New skill", "Delete")

	h.click(tester, "New skill")
	h.assertTexts(tester, "Name", "Description", "Body", "Create", "Cancel")
	h.focusInput(tester, "Name")
	tester.Type("english-skill")
	tester.Frame()
	h.focusInput(tester, "Body")
	tester.Type("body")
	tester.Frame()
	h.click(tester, "Create")
	h.assertTexts(tester, "Skill created", "english-skill")

	h.click(tester, "Delete")
	h.assertTexts(tester, "Delete skill", "Confirm delete")
	h.click(tester, "Confirm delete")
	h.assertTexts(tester, "Skill deleted")
}

func dataDirSkill(dataDir, name, description, body string) contracts.Skill {
	return contracts.Skill{
		Name: name, Description: description, Body: body,
		Path: filepath.Join(dataDir, "skills", name, "SKILL.md"),
	}
}

func TestSkillsEditModalPrefillsAndSaves(t *testing.T) {
	dataDir := t.TempDir()
	skills := newFakeSkills()
	skills.setList(dataDirSkill(dataDir, "alpha", "old desc", "# old body"))
	editor := newFakeSkillsEditor(skills)
	h := skillsEditorHarness(t, skills, editor, dataDir)
	tester := h.tester()
	h.assertTexts(tester, "编辑")

	h.click(tester, "编辑")
	h.assertTexts(tester, "编辑技能", "alpha", "名称不可修改", "描述", "内容", "保存", "取消")
	h.focusInput(tester, "描述")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type("new desc")
	tester.Frame()
	h.focusInput(tester, "内容")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type("# new body")
	tester.Frame()
	h.click(tester, "保存")

	updates := editor.updated()
	if len(updates) != 1 || updates[0].Name != "alpha" || updates[0].Description != "new desc" || updates[0].Body != "# new body" {
		t.Fatalf("UpdateSkill calls = %+v", updates)
	}
	h.assertTexts(tester, "已保存", "new desc")
	if got := exactTextCount(tester.Texts(), "编辑技能"); got != 0 {
		t.Fatalf("edit modal still open: %d", got)
	}
}

func TestSkillsEditFailureKeepsModal(t *testing.T) {
	dataDir := t.TempDir()
	skills := newFakeSkills()
	skills.setList(dataDirSkill(dataDir, "alpha", "desc", "body"))
	editor := newFakeSkillsEditor(skills)
	editor.updateErr = errors.New("read-only filesystem")
	h := skillsEditorHarness(t, skills, editor, dataDir)
	tester := h.tester()
	h.click(tester, "编辑")
	h.click(tester, "保存")
	h.assertTexts(tester, "编辑技能失败：read-only filesystem", "编辑技能", "保存")
}

func TestSkillsEditHiddenWithoutEditor(t *testing.T) {
	dataDir := t.TempDir()
	skills := newFakeSkills()
	skills.setList(dataDirSkill(dataDir, "alpha", "desc", "body"))
	h := skillsEditorHarness(t, skills, nil, dataDir)
	tester := h.tester()
	h.assertNoTexts(tester, "编辑", "删除", "新建技能")
}

func TestSkillsEditEnglish(t *testing.T) {
	dataDir := t.TempDir()
	skills := newFakeSkills()
	skills.setList(dataDirSkill(dataDir, "alpha", "desc", "body"))
	editor := newFakeSkillsEditor(skills)
	h := newLocaleSkillsEditorHarness(t, "en", skills, editor, dataDir)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Skills")
	h.assertTexts(tester, "Edit")
	h.click(tester, "Edit")
	h.assertTexts(tester, "Edit skill", "Name cannot be changed", "Save", "Cancel")
	h.click(tester, "Save")
	if got := editor.updated(); len(got) != 1 || got[0].Name != "alpha" {
		t.Fatalf("English UpdateSkill calls = %+v", got)
	}
	h.assertTexts(tester, "Saved")
}

// TestSkillsDeleteConfirmEscapeCancels pins the native alert dialog semantics of
// the skills delete confirmation: Esc dismisses it and deletes nothing.
func TestSkillsDeleteConfirmEscapeCancels(t *testing.T) {
	dataDir := t.TempDir()
	skills := newFakeSkills()
	skills.setList(contracts.Skill{Name: "alpha", Path: filepath.Join(dataDir, "skills", "alpha", "SKILL.md")})
	editor := newFakeSkillsEditor(skills)
	h := skillsEditorHarness(t, skills, editor, dataDir)
	tester := h.tester()

	h.click(tester, "删除")
	h.assertTexts(tester, "删除技能", "确认删除")
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if got := editor.deleted(); len(got) != 0 {
		t.Fatalf("Esc on the delete dialog deleted %v", got)
	}
	h.assertNoTexts(tester, "确认删除")
	h.assertTexts(tester, "alpha")
}

// TestSkillsCreateDialogAutofocusAndEnter pins E-07 for the settings dialogs:
// the first field takes the keyboard as the dialog opens, and Enter submits
// from a single-line field (the audit found every settings dialog needed two
// clicks: one for the field, one for the button).
func TestSkillsCreateDialogAutofocusAndEnter(t *testing.T) {
	dataDir := t.TempDir()
	skills := newFakeSkills()
	editor := newFakeSkillsEditor(skills)
	h := skillsEditorHarness(t, skills, editor, dataDir)
	tester := h.tester()

	h.click(tester, "新建技能")
	if !tester.Focused("名称 输入") {
		t.Fatalf("dialog opened without focusing its first field; texts = %v", tester.Texts())
	}
	// Typing lands in the auto-focused name field (no click needed).
	tester.Type("键盘技能")
	tester.Frame()
	h.focusInput(tester, "描述")
	tester.Type("键盘说明")
	h.focusInput(tester, "内容")
	tester.Type("# 键盘正文")
	// Enter from a single-line field submits the dialog.
	h.focusInput(tester, "名称")
	tester.Key(0, ui.KeyEnter)
	tester.Frame()

	creates := editor.created()
	if len(creates) != 1 || creates[0].Name != "键盘技能" {
		t.Fatalf("CreateSkill calls = %+v, want the Enter submit", creates)
	}
}
