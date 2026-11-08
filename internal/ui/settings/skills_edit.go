package settings

// The skills page's editor half: a native modal to create a SKILL.md skill and
// a confirm modal to delete data-dir skills, both available only when the
// optional contracts.SkillsEditor service is provided. The settings page keeps
// the data directory read-only check on its side (only skills below dataDir get
// a delete action); the backend enforces the same rule again.

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// skillsEditor looks up the optional editor half and the product data directory
// (the latter decides which skill cards may show a delete action).
func skillsEditor(kctx cordis.Context) (contracts.SkillsEditor, string) {
	editor, ok := kctx.Get(contracts.KeySkillsEditor)
	if !ok || editor == nil {
		return nil, ""
	}
	dataDir, _ := kctx.Get(contracts.KeyDataDir)
	return editor, strings.TrimSpace(dataDir)
}

// skillsEditActionsAllowed reports whether path is below dataDir: only skills
// owned by the product data directory are deletable from this page.
func skillsEditActionsAllowed(path, dataDir string) bool {
	path = strings.TrimSpace(path)
	dataDir = strings.TrimSpace(dataDir)
	if path == "" || dataDir == "" {
		return false
	}
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(absDataDir, absPath)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

// startCreateSkill opens the create modal with a clean draft.
func startCreateSkill(c *ui.Context, st *skillsState) {
	st.creating = true
	st.editErr = ""
	st.draftName = ""
	st.draftDescription = ""
	st.draftBody = ""
	c.Invalidate()
}

// startEditSkill opens the edit modal prefilled with one existing data-dir
// skill. The name is the directory name and is intentionally read-only.
func startEditSkill(c *ui.Context, st *skillsState, skill contracts.Skill, body string) {
	st.editing = true
	st.editName = skill.Name
	st.editDescription = skill.Description
	st.editBody = body
	st.editErr = ""
	c.Invalidate()
}

// buildSkillsEditorDialogs renders the optional create/edit/delete modals. It is
// called before the list early-returns so a create modal also works when the
// current list is empty or failed.
func buildSkillsEditorDialogs(c *ui.Context, t *design.Tokens, st *skillsState,
	editor contracts.SkillsEditor, locale contracts.LocaleService) {
	if editor == nil {
		return
	}
	if st.creating {
		ui.Modal(c, &st.creating, func() {
			design.Apply(ui.Column(c).Width(560).Gap(design.Space3), func(element ui.Element) ui.Element {
				return element.PaddingX(design.Space5).PaddingY(design.Space2)
			}).Children(func() {
				design.Apply(ui.Text(c, localized(locale, "新建技能", "New skill")),
					t.Font(design.TypeL)).FontWeight(600).TextColor(t.LabelPrimary)
				design.Apply(ui.Text(c, localized(locale,
					"技能保存为 dataDir/skills/<名称>/SKILL.md。",
					"Skills are saved as dataDir/skills/<name>/SKILL.md.")),
					t.TextMuted(design.TypeXS)).MaxLines(2)
				ui.TextInput(c, &st.draftName).Label(localized(locale, "名称", "Name")).FillWidth()
				ui.TextInput(c, &st.draftDescription).Label(localized(locale, "描述", "Description")).FillWidth()
				ui.TextArea(c, &st.draftBody).Label(localized(locale, "内容", "Body")).
					FillWidth().MinHeight(design.Space6 * 5)
				if st.editErr != "" {
					buildSkillsErrorLine(c, t, st.editErr)
				}
				ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space2).Children(func() {
					create := design.Apply(ui.PrimaryButton(c, localized(locale, "创建", "Create")),
						t.PrimaryButton())
					if create.Clicked() {
						createSkillFromDraft(c, editor, st, locale)
					}
					if ui.Button(c, localized(locale, "取消", "Cancel")).Clicked() {
						st.creating = false
						st.editErr = ""
						c.Invalidate()
					}
				})
			})
		})
	}
	if st.editing {
		ui.Modal(c, &st.editing, func() {
			design.Apply(ui.Column(c).Width(560).Gap(design.Space3), func(element ui.Element) ui.Element {
				return element.PaddingX(design.Space5).PaddingY(design.Space2)
			}).Children(func() {
				design.Apply(ui.Text(c, localized(locale, "编辑技能", "Edit skill")),
					t.Font(design.TypeL)).FontWeight(600).TextColor(t.LabelPrimary)
				design.Apply(ui.Text(c, localized(locale, "名称：", "Name: ")+st.editName),
					t.Font(design.TypeXS)).FontWeight(600).TextColor(t.LabelPrimary)
				design.Apply(ui.Text(c, localized(locale,
					"名称不可修改；保存会重写 dataDir/skills/<名称>/SKILL.md。",
					"Name cannot be changed; saving rewrites dataDir/skills/<name>/SKILL.md.")),
					t.TextMuted(design.TypeXXS)).MaxLines(2)
				ui.TextInput(c, &st.editDescription).Label(localized(locale, "描述", "Description")).FillWidth()
				ui.TextArea(c, &st.editBody).Label(localized(locale, "内容", "Body")).
					FillWidth().MinHeight(design.Space6 * 5)
				if st.editErr != "" {
					buildSkillsErrorLine(c, t, st.editErr)
				}
				ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space2).Children(func() {
					save := design.Apply(ui.PrimaryButton(c, localized(locale, "保存", "Save")),
						t.PrimaryButton())
					if save.Clicked() {
						updateSkillFromDraft(c, editor, st, locale)
					}
					if ui.Button(c, localized(locale, "取消", "Cancel")).Clicked() {
						st.editing = false
						st.editErr = ""
						c.Invalidate()
					}
				})
			})
		})
	}
	if st.confirmDelete {
		ui.Modal(c, &st.confirmDelete, func() {
			design.Apply(ui.Column(c).Width(480).Gap(design.Space3), func(element ui.Element) ui.Element {
				return element.PaddingX(design.Space5).PaddingY(design.Space2)
			}).Children(func() {
				design.Apply(ui.Text(c, localized(locale, "删除技能", "Delete skill")),
					t.Font(design.TypeL)).FontWeight(600).TextColor(t.LabelPrimary)
				design.Apply(ui.Text(c, localized(locale, "确定删除技能「", "Delete skill \"")+
					st.pendingDelete+localized(locale, "」？此操作不可撤销。", "\"? This cannot be undone.")),
					t.TextMuted(design.TypeXS)).MaxLines(3)
				if st.editErr != "" {
					buildSkillsErrorLine(c, t, st.editErr)
				}
				ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space2).Children(func() {
					if ui.Button(c, localized(locale, "取消", "Cancel")).Clicked() {
						st.confirmDelete = false
						st.editErr = ""
						st.pendingDelete = ""
						c.Invalidate()
					}
					confirm := design.Apply(ui.PrimaryButton(c, localized(locale, "确认删除", "Confirm delete")),
						t.PrimaryButton())
					if confirm.Clicked() {
						deleteSkillByName(c, editor, st, locale)
					}
				})
			})
		})
	}
}

// createSkillFromDraft validates the modal draft, calls the editor and on
// success closes the modal, shows the success badge and reloads the list.
func createSkillFromDraft(c *ui.Context, editor contracts.SkillsEditor, st *skillsState,
	locale contracts.LocaleService) {
	name := strings.TrimSpace(st.draftName)
	body := strings.TrimSpace(st.draftBody)
	if name == "" || body == "" {
		st.editErr = localized(locale, "名称和内容不能为空", "Name and body are required")
		c.Invalidate()
		return
	}
	if _, err := editor.CreateSkill(context.Background(), contracts.SkillDraft{
		Name:        name,
		Description: strings.TrimSpace(st.draftDescription),
		Body:        body,
	}); err != nil {
		st.editErr = localized(locale, "创建技能失败：", "Create skill failed: ") + err.Error()
		c.Invalidate()
		return
	}
	st.creating = false
	st.editErr = ""
	st.draftName = ""
	st.draftDescription = ""
	st.draftBody = ""
	st.editStatus = localized(locale, "已创建技能", "Skill created")
	st.refresh()
	c.Invalidate()
}

// updateSkillFromDraft saves the edit modal through UpdateSkill: success closes
// the modal, shows the saved badge and reloads the list; failures keep it open
// with the danger row.
func updateSkillFromDraft(c *ui.Context, editor contracts.SkillsEditor, st *skillsState,
	locale contracts.LocaleService) {
	body := strings.TrimSpace(st.editBody)
	if body == "" {
		st.editErr = localized(locale, "内容不能为空", "Body is required")
		c.Invalidate()
		return
	}
	if _, err := editor.UpdateSkill(context.Background(), st.editName, contracts.SkillDraft{
		Name:        st.editName,
		Description: strings.TrimSpace(st.editDescription),
		Body:        body,
	}); err != nil {
		st.editErr = localized(locale, "编辑技能失败：", "Edit skill failed: ") + err.Error()
		c.Invalidate()
		return
	}
	st.editing = false
	st.editErr = ""
	st.editName = ""
	st.editDescription = ""
	st.editBody = ""
	st.editStatus = localized(locale, "已保存", "Saved")
	st.refresh()
	c.Invalidate()
}

// deleteSkillByName deletes the pending skill, closes the confirm modal on
// success and reloads the list; errors keep the modal open with the danger row.
func deleteSkillByName(c *ui.Context, editor contracts.SkillsEditor, st *skillsState,
	locale contracts.LocaleService) {
	if err := editor.DeleteSkill(context.Background(), st.pendingDelete); err != nil {
		st.editErr = localized(locale, "删除技能失败：", "Delete skill failed: ") + err.Error()
		c.Invalidate()
		return
	}
	st.confirmDelete = false
	st.pendingDelete = ""
	st.editErr = ""
	st.editStatus = localized(locale, "已删除技能", "Skill deleted")
	st.refresh()
	c.Invalidate()
}
