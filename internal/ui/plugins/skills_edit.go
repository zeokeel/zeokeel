package plugins

// The Skills tab's editor half: the create/edit modals and the destructive
// delete confirmation, available only when the optional
// contracts.SkillsEditor service is provided. Only skills below the product data
// directory get a card menu (the backend enforces the same rule again).
//
// The dialogs' first field takes the keyboard and Enter submits from a
// single-line field (E-07); every input has a visible field label plus the
// accessible "输入"/"input" suffix (skillInputLabel). The footer follows the
// product's dialog order through uikit.DialogFooter, and the delete goes
// through uikit.ConfirmDanger so the safe action is the default.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/lib/openpath"
	"github.com/zeokeel/zeokeel/internal/lib/textutil"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// skillInputLabel builds the accessible name of a dialog input: the visible
// field label plus the "输入"/"input" suffix, matching the settings dialogs'
// convention.
func skillInputLabel(st *state, label string) string {
	return label + st.tr(" 输入", " input")
}

// skillsEditActionsAllowed reports whether path is below dataDir: only skills
// owned by the product data directory are editable/deletable from this page.
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
	// Only the directory-form skill (<dataDir>/skills/<name>/SKILL.md) is
	// writable. A top-level <dataDir>/skills/foo.md is discovered by List but
	// the editor cannot rewrite it; showing edit/delete there would fail after
	// the click.
	if !strings.EqualFold(filepath.Base(filepath.Clean(absPath)), "SKILL.md") {
		return false
	}
	relative, err := filepath.Rel(absDataDir, absPath)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relative)
}

// startCreateSkill opens the create modal with a clean draft.
func startCreateSkill(c *ui.Context, p *skillsState) {
	p.creating = true
	p.editErr = ""
	p.draftName = ""
	p.draftDescription = ""
	p.draftBody = ""
	p.bodyPreviewOpen = false
	c.Invalidate()
}

// startCopySkill prefills the create modal from any discovered skill (including
// workspace/read-only ones), so a repo skill can be forked into the editable
// data directory.
func startCopySkill(c *ui.Context, p *skillsState, st *state, skills contracts.SkillsService,
	skill contracts.Skill, editor contracts.SkillsEditor) {
	if editor == nil {
		return
	}
	body, bodyErr := skillBody(skills, p, skill)
	if bodyErr != "" {
		p.editErr = st.tr("读取技能内容失败：", "Failed to read skill content: ") + bodyErr
		c.Invalidate()
		return
	}
	p.creating = true
	p.editErr = ""
	p.draftName = uniqueSkillCopyName(p.skills, skill.Name)
	p.draftDescription = skill.Description
	p.draftBody = body
	c.Invalidate()
}

// uniqueSkillCopyName picks a non-colliding "<name>-copy" draft name; the user
// can still edit it in the create dialog.
func uniqueSkillCopyName(existing []contracts.Skill, base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "skill"
	}
	taken := make(map[string]bool, len(existing))
	for _, skill := range existing {
		taken[strings.ToLower(strings.TrimSpace(skill.Name))] = true
	}
	candidate := base + "-copy"
	for index := 2; taken[strings.ToLower(candidate)]; index++ {
		candidate = fmt.Sprintf("%s-copy-%d", base, index)
	}
	return candidate
}

// setSkillDisabled adds/removes one name from skills.disabled, persists it and
// updates the in-memory set. The agent-facing skill tool reads the setting on
// every call, so no restart/reload is needed.
func (p *skillsState) setSkillDisabled(st *state, settings contracts.SettingsService,
	name string, disabled bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("skill name is required")
	}
	names := make([]string, 0, len(p.disabledNames)+1)
	for _, existing := range p.disabledNames {
		if strings.EqualFold(strings.TrimSpace(existing), name) {
			continue
		}
		names = append(names, existing)
	}
	if disabled {
		names = append(names, name)
	}
	encoded, err := contracts.EncodeStringListJSON(names)
	if err != nil {
		return err
	}
	if err := settings.Set(contracts.SettingSkillsDisabled, encoded); err != nil {
		return err
	}
	p.disabled = make(map[string]bool, len(names))
	p.disabledNames = names
	for _, entry := range names {
		p.disabled[strings.ToLower(entry)] = true
	}
	if disabled {
		p.editStatus = st.tr("已禁用技能", "Skill disabled")
	} else {
		p.editStatus = st.tr("已启用技能", "Skill enabled")
	}
	return nil
}

// startEditSkill opens the edit modal prefilled with one existing data-dir
// skill. The name is the directory name and is intentionally read-only.
func startEditSkill(c *ui.Context, p *skillsState, skill contracts.Skill, body string) {
	p.editing = true
	p.editName = skill.Name
	p.editDescription = skill.Description
	p.editBody = body
	p.editErr = ""
	p.bodyPreviewOpen = false
	c.Invalidate()
}

// buildSkillsDialogs renders the optional preview and create/edit/delete
// dialogs. It is called before the tab's early returns, so a create modal also
// works when the current list is empty or failed.
func buildSkillsDialogs(c *ui.Context, t *design.Tokens, p *skillsState, st *state,
	skills contracts.SkillsService, editor contracts.SkillsEditor) {
	if skills != nil {
		buildSkillPreviewDialog(c, t, p, st, skills)
	}
	if editor == nil {
		// The editor can be removed by a host change; never leave a stale
		// modal open against a service that is gone.
		p.creating = false
		p.editing = false
		p.confirmDelete = false
		p.pendingDelete = ""
		return
	}
	buildSkillCreateDialog(c, t, p, st, editor)
	buildSkillEditDialog(c, t, p, st, editor)
	buildSkillDeleteDialog(c, p, st, editor)
}

// buildSkillCreateDialog renders the create modal.
func buildSkillCreateDialog(c *ui.Context, t *design.Tokens, p *skillsState, st *state,
	editor contracts.SkillsEditor) {
	if !p.creating {
		return
	}
	ui.Modal(c, &p.creating, func() {
		design.Apply(ui.Column(c.Key("skills.create")), t.Panel()).
			Width(design.DialogWidthM).
			PaddingX(design.Space5).PaddingY(design.Space2).Gap(design.Space3).Children(func() {
			uikit.DialogHeader(c, uikit.DialogHeaderOptions{
				Key: "skills.create", Title: st.tr("新建技能", "New skill"),
				CloseLabel: st.tr("关闭", "Close"),
				OnClose: func() {
					p.creating = false
					p.editErr = ""
					c.Invalidate()
				},
			})
			submit := func() { createSkillFromDraft(c, editor, p, st) }
			ui.Field(c, st.tr("名称", "Name"), func() {
				ui.TextInput(c, &p.draftName).
					Label(skillInputLabel(st, st.tr("名称", "Name"))).
					FillWidth().AutoFocus().OnTextKey(uikit.EnterTo(submit))
			})
			ui.Field(c, st.tr("描述", "Description"), func() {
				ui.TextInput(c, &p.draftDescription).
					Label(skillInputLabel(st, st.tr("描述", "Description"))).
					FillWidth().OnTextKey(uikit.EnterTo(submit))
			})
			ui.Field(c, st.tr("内容", "Body"), func() {
				ui.TextArea(c, &p.draftBody).
					Label(skillInputLabel(st, st.tr("内容", "Body"))).
					FillWidth().MinHeight(design.Space6 * 5)
			})
			buildSkillBodyPreview(c, t, p, st, &p.draftBody)
			if p.editErr != "" {
				uikit.ErrorLine(c, p.editErr)
			}
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: st.tr("取消", "Cancel"), OnClick: func() {
					p.creating = false
					p.editErr = ""
					c.Invalidate()
				}},
				{Label: st.tr("创建", "Create"), Primary: true, OnClick: submit},
			})
		})
	})
}

// buildSkillEditDialog renders the edit modal: the name is read-only (it is the
// skill's directory name), the description and body are editable.
func buildSkillEditDialog(c *ui.Context, t *design.Tokens, p *skillsState, st *state,
	editor contracts.SkillsEditor) {
	if !p.editing {
		return
	}
	ui.Modal(c, &p.editing, func() {
		design.Apply(ui.Column(c.Key("skills.edit")), t.Panel()).
			Width(design.DialogWidthM).
			PaddingX(design.Space5).PaddingY(design.Space2).Gap(design.Space3).Children(func() {
			uikit.DialogHeader(c, uikit.DialogHeaderOptions{
				Key: "skills.edit", Title: st.tr("编辑技能", "Edit skill"),
				CloseLabel: st.tr("关闭", "Close"),
				OnClose: func() {
					p.editing = false
					p.editErr = ""
					c.Invalidate()
				},
			})
			design.Apply(ui.Text(c, st.tr("名称：", "Name: ")+p.editName),
				t.Font(design.TypeXS)).FontWeight(design.WeightSemibold).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, st.tr("名称不可修改。", "The name cannot be changed.")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
			submit := func() { updateSkillFromDraft(c, editor, p, st) }
			ui.Field(c, st.tr("描述", "Description"), func() {
				ui.TextInput(c, &p.editDescription).
					Label(skillInputLabel(st, st.tr("描述", "Description"))).
					FillWidth().AutoFocus().OnTextKey(uikit.EnterTo(submit))
			})
			ui.Field(c, st.tr("内容", "Body"), func() {
				ui.TextArea(c, &p.editBody).
					Label(skillInputLabel(st, st.tr("内容", "Body"))).
					FillWidth().MinHeight(design.Space6 * 5)
			})
			buildSkillBodyPreview(c, t, p, st, &p.editBody)
			if p.editErr != "" {
				uikit.ErrorLine(c, p.editErr)
			}
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: st.tr("取消", "Cancel"), OnClick: func() {
					p.editing = false
					p.editErr = ""
					c.Invalidate()
				}},
				{Label: st.tr("保存", "Save"), Primary: true, OnClick: submit},
			})
		})
	})
}

// buildSkillBodyPreview renders a collapsible Markdown preview of the current
// body draft, so authors see formatting without saving first.
func buildSkillBodyPreview(c *ui.Context, t *design.Tokens, p *skillsState, st *state, body *string) {
	ui.Collapsible(c, st.tr("Markdown 预览", "Markdown preview"), &p.bodyPreviewOpen, func() {
		text := strings.TrimSpace(*body)
		if text == "" {
			design.Apply(ui.Text(c, st.tr("内容为空，暂无可预览内容。", "The body is empty; nothing to preview.")),
				t.TextMuted(design.TypeXS))
			return
		}
		ui.Scroll(c).FillWidth().MaxHeight(design.DialogBodyMax).Children(func() {
			markdown.View(c, text, nil)
		})
	})
}

// buildSkillDeleteDialog renders the destructive confirmation. Deleting a skill
// is irreversible, so it goes through uikit.ConfirmDanger: the safe action is
// the default (Enter cancels) and the danger action is never Default.
func buildSkillDeleteDialog(c *ui.Context, p *skillsState, st *state,
	editor contracts.SkillsEditor) {
	if p.confirmDelete {
		pending := p.pendingDelete
		uikit.ConfirmDanger(c, &p.confirmDelete, uikit.ConfirmDangerOptions{
			Key:   "skills.delete",
			Title: st.tr("删除技能", "Delete skill"),
			Message: st.tr("确定删除技能「", "Delete skill \"") + pending +
				st.tr("」？此操作不可撤销。", "\"? This cannot be undone."),
			Confirm: st.tr("确认删除", "Confirm delete"),
			Cancel:  st.tr("取消", "Cancel"),
			OnConfirm: func() {
				deleteSkillByName(c, editor, p, st)
			},
		})
	}
	// The dialog closed with the delete still pending: the user cancelled (no
	// error) or the delete failed and its error is already in editErr. Drop the
	// pending name; the failure stays visible in the page flow.
	if !p.confirmDelete && p.pendingDelete != "" {
		p.pendingDelete = ""
	}
}

// buildSkillCardMenu is one skill card's trailing ⋯ menu. Every skill gets the
// read-only actions (open / reveal / copy path); only a directory-form skill
// below the product data directory additionally gets Edit and the destructive
// Delete. The label names the skill so several cards do not expose identical
// accessible buttons.
func buildSkillCardMenu(c *ui.Context, t *design.Tokens, p *skillsState, st *state,
	skills contracts.SkillsService, skill contracts.Skill, editor contracts.SkillsEditor,
	dataDir string) {
	path := strings.TrimSpace(skill.Path)
	if path == "" {
		return
	}
	name := textutil.FirstNonEmptyTrimmed(skill.Name, st.tr("(未命名技能)", "(Unnamed skill)"))
	label := st.tr("技能操作", "Skill actions") + " " + name
	menu := uikit.IconButton(c, uikit.IconButtonOptions{
		Key: "skills.menu/" + skillKey(skill), Label: label, Tooltip: st.tr("技能操作", "Skill actions"),
		Icon: icons.More(), Size: design.IconSizeS,
	})
	menu.PopoverMenuButton(func(m *ui.Menu) {
		if m.Item(st.tr("用默认应用打开", "Open with default app")).Chosen() {
			if err := openpath.Open(path); err != nil {
				p.editErr = st.tr("打开技能失败：", "Failed to open the skill: ") + err.Error()
			}
			c.Invalidate()
		}
		if m.Item(st.tr("在文件管理器中显示", "Show in file manager")).Chosen() {
			if err := openpath.ShowInFolder(path); err != nil {
				p.editErr = st.tr("在文件管理器中显示失败：", "Failed to reveal the skill: ") + err.Error()
			}
			c.Invalidate()
		}
		if m.Item(st.tr("复制路径", "Copy path")).Chosen() {
			c.WriteClipboard(path)
		}
		if editor != nil && m.Item(st.tr("复制到我的技能", "Copy to my skills")).Chosen() {
			startCopySkill(c, p, st, skills, skill, editor)
		}
		if settingsStore, ok := st.ctx.Get(contracts.KeySettings); ok && settingsStore != nil {
			toggleLabel := st.tr("禁用技能", "Disable skill")
			if p.skillDisabled(name) {
				toggleLabel = st.tr("启用技能", "Enable skill")
			}
			if m.Item(toggleLabel).Chosen() {
				if err := p.setSkillDisabled(st, settingsStore, name, !p.skillDisabled(name)); err != nil {
					p.editErr = st.tr("更新技能启停失败：", "Failed to update the skill state: ") + err.Error()
				}
				c.Invalidate()
			}
		}
		if editor == nil || !skillsEditActionsAllowed(path, dataDir) {
			return
		}
		m.Separator()
		if m.Item(st.tr("编辑技能", "Edit skill")).Chosen() {
			body, bodyErr := skillBody(skills, p, skill)
			if bodyErr != "" {
				p.editErr = st.tr("读取技能内容失败：", "Failed to read skill content: ") + bodyErr
				body = ""
			}
			startEditSkill(c, p, skill, body)
		}
		remove := m.Item(st.tr("删除技能", "Delete skill"))
		remove.Danger()
		if remove.Chosen() {
			p.pendingDelete = skill.Name
			p.confirmDelete = true
			p.editErr = ""
			c.Invalidate()
		}
	})
}

// createSkillFromDraft validates the modal draft, calls the editor and on
// success closes the modal, shows the success line and rescans.
func createSkillFromDraft(c *ui.Context, editor contracts.SkillsEditor, p *skillsState, st *state) {
	name := strings.TrimSpace(p.draftName)
	body := strings.TrimSpace(p.draftBody)
	if name == "" || body == "" {
		p.editErr = st.tr("名称和内容不能为空", "Name and body are required")
		c.Invalidate()
		return
	}
	if _, err := editor.CreateSkill(context.Background(), contracts.SkillDraft{
		Name:        name,
		Description: strings.TrimSpace(p.draftDescription),
		Body:        body,
	}); err != nil {
		p.editErr = st.tr("创建技能失败：", "Create skill failed: ") + err.Error()
		c.Invalidate()
		return
	}
	p.creating = false
	p.editErr = ""
	p.draftName = ""
	p.draftDescription = ""
	p.draftBody = ""
	p.editStatus = st.tr("已创建技能", "Skill created")
	p.refresh()
	c.Invalidate()
}

// updateSkillFromDraft saves the edit modal through UpdateSkill: success closes
// the modal, shows the saved line and rescans; failures keep it open with the
// danger row.
func updateSkillFromDraft(c *ui.Context, editor contracts.SkillsEditor, p *skillsState, st *state) {
	body := strings.TrimSpace(p.editBody)
	if body == "" {
		p.editErr = st.tr("内容不能为空", "Body is required")
		c.Invalidate()
		return
	}
	if _, err := editor.UpdateSkill(context.Background(), p.editName, contracts.SkillDraft{
		Name:        p.editName,
		Description: strings.TrimSpace(p.editDescription),
		Body:        body,
	}); err != nil {
		p.editErr = st.tr("编辑技能失败：", "Edit skill failed: ") + err.Error()
		c.Invalidate()
		return
	}
	p.editing = false
	p.editErr = ""
	p.editName = ""
	p.editDescription = ""
	p.editBody = ""
	p.editStatus = st.tr("已保存", "Saved")
	p.refresh()
	c.Invalidate()
}

// deleteSkillByName deletes the pending skill, closes the confirmation on
// success and rescans; errors surface in the page flow.
func deleteSkillByName(c *ui.Context, editor contracts.SkillsEditor, p *skillsState, st *state) {
	name := p.pendingDelete
	if err := editor.DeleteSkill(context.Background(), name); err != nil {
		p.editErr = st.tr("删除技能失败：", "Delete skill failed: ") + err.Error()
		c.Invalidate()
		return
	}
	p.confirmDelete = false
	p.pendingDelete = ""
	p.editErr = ""
	p.editStatus = st.tr("已删除技能", "Skill deleted")
	p.refresh()
	c.Invalidate()
}
