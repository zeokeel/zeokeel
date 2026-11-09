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
	"github.com/zeokeel/zeokeel/internal/uikit"
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
// current list is empty or failed. Every input has a visible field label and a
// description; the action bars follow the product's dialog order (cancel left,
// primary right) through uikit.DialogFooter.
func buildSkillsEditorDialogs(c *ui.Context, t *design.Tokens, st *skillsState,
	editor contracts.SkillsEditor, locale contracts.LocaleService) {
	if editor == nil {
		return
	}
	if st.creating {
		ui.Modal(c, &st.creating, func() {
			design.Apply(ui.Column(c).Width(design.DialogWidthM).Gap(design.Space3), func(element ui.Element) ui.Element {
				return element.PaddingX(design.Space5).PaddingY(design.Space2)
			}).Children(func() {
				design.Apply(ui.Text(c, localized(locale, "新建技能", "New skill")),
					t.Font(design.TypeL)).FontWeight(600).TextColor(t.LabelPrimary)
				design.Apply(ui.Text(c, localized(locale,
					"技能保存为 dataDir/skills/<名称>/SKILL.md。",
					"Skills are saved as dataDir/skills/<name>/SKILL.md.")),
					t.TextMuted(design.TypeXS)).MaxLines(2)
				// The dialog's first field takes the focus and Enter submits, as
				// in every desktop form: the audit found every settings dialog
				// needed a click on the field and then on the button.
				submit := func() { createSkillFromDraft(c, editor, st, locale) }
				ui.Field(c, localized(locale, "名称", "Name"), func() {
					ui.TextInput(c, &st.draftName).
						Label(inputLabel(locale, localized(locale, "名称", "Name"))).
						FillWidth().AutoFocus().OnTextKey(uikit.EnterTo(submit))
				}).Description(localized(locale, "技能目录名，也是技能菜单中的名称。",
					"The skill's directory and menu name."))
				ui.Field(c, localized(locale, "描述", "Description"), func() {
					ui.TextInput(c, &st.draftDescription).
						Label(inputLabel(locale, localized(locale, "描述", "Description"))).
						FillWidth().OnTextKey(uikit.EnterTo(func() {
						createSkillFromDraft(c, editor, st, locale)
					}))
				}).Description(localized(locale, "给模型看的一句话说明。",
					"One sentence the model sees in the skill menu."))
				ui.Field(c, localized(locale, "内容", "Body"), func() {
					ui.TextArea(c, &st.draftBody).
						Label(inputLabel(locale, localized(locale, "内容", "Body"))).
						FillWidth().MinHeight(design.Space6 * 5)
				}).Description(localized(locale, "SKILL.md 的 Markdown 正文。",
					"The Markdown body of SKILL.md."))
				if st.editErr != "" {
					uikit.ErrorLine(c, st.editErr)
				}
				uikit.DialogFooter(c, []uikit.DialogAction{
					{Label: localized(locale, "取消", "Cancel"), OnClick: func() {
						st.creating = false
						st.editErr = ""
						c.Invalidate()
					}},
					{Label: localized(locale, "创建", "Create"), Primary: true, OnClick: func() {
						createSkillFromDraft(c, editor, st, locale)
					}},
				})
			})
		})
	}
	if st.editing {
		ui.Modal(c, &st.editing, func() {
			design.Apply(ui.Column(c).Width(design.DialogWidthM).Gap(design.Space3), func(element ui.Element) ui.Element {
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
				ui.Field(c, localized(locale, "描述", "Description"), func() {
					ui.TextInput(c, &st.editDescription).
						Label(inputLabel(locale, localized(locale, "描述", "Description"))).
						FillWidth().AutoFocus().
						OnTextKey(uikit.EnterTo(func() {
							updateSkillFromDraft(c, editor, st, locale)
						}))
				}).Description(localized(locale, "给模型看的一句话说明。",
					"One sentence the model sees in the skill menu."))
				ui.Field(c, localized(locale, "内容", "Body"), func() {
					ui.TextArea(c, &st.editBody).
						Label(inputLabel(locale, localized(locale, "内容", "Body"))).
						FillWidth().MinHeight(design.Space6 * 5)
				}).Description(localized(locale, "SKILL.md 的 Markdown 正文。",
					"The Markdown body of SKILL.md."))
				if st.editErr != "" {
					uikit.ErrorLine(c, st.editErr)
				}
				uikit.DialogFooter(c, []uikit.DialogAction{
					{Label: localized(locale, "取消", "Cancel"), OnClick: func() {
						st.editing = false
						st.editErr = ""
						c.Invalidate()
					}},
					{Label: localized(locale, "保存", "Save"), Primary: true, OnClick: func() {
						updateSkillFromDraft(c, editor, st, locale)
					}},
				})
			})
		})
	}
	// Deleting a skill is destructive: the native alert dialog gives it the
	// same confirm/cancel order, Esc handling, focus return and default action
	// as the product's other sixteen confirmations.
	if st.confirmDelete {
		switch ui.AlertDialog(c, &st.confirmDelete,
			localized(locale, "删除技能", "Delete skill"),
			localized(locale, "确定删除技能「", "Delete skill \"")+
				st.pendingDelete+localized(locale, "」？此操作不可撤销。", "\"? This cannot be undone."),
			localized(locale, "取消", "Cancel"),
			localized(locale, "确认删除", "Confirm delete")) {
		case 1:
			deleteSkillByName(c, editor, st, locale)
		case ui.AlertDismissed:
			// Esc/backdrop cancel: drop the pending name, keep no error.
			st.editErr = ""
			st.pendingDelete = ""
			c.Invalidate()
		}
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
