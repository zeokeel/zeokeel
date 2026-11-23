package plugins_test

// Frame tests for the Capabilities page's Skills tab: the discovered SKILL.md
// packs as a card grid (official ui.GridView), the search that appears once the
// collection outgrows one sight of cards, the lazy Markdown preview modal, the
// create/edit/delete dialogs and the scan-directory disclosure. The skills and
// editor backends are optional; every test provides its fakes after activation
// because the tab looks its services up lazily per frame.
//
// Written before the implementation (TDD): every test here failed on the
// section stub and passes once skills_section.go/skills_edit.go are built.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/lib/openpath"
	"github.com/zeokeel/zeokeel/internal/ui/plugins"
)

// --- fakes ------------------------------------------------------------------

// fakeSkills is a scripted contracts.SkillsService.
type fakeSkills struct {
	mu sync.Mutex

	list    []contracts.Skill
	roots   []string
	bodies  map[string]string
	listErr error
	readErr error

	warnings []string

	listCalls int
	readCalls []string
}

func newFakeSkills() *fakeSkills {
	return &fakeSkills{bodies: make(map[string]string)}
}

func (f *fakeSkills) List(context.Context) ([]contracts.Skill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]contracts.Skill(nil), f.list...), nil
}

func (f *fakeSkills) Read(_ context.Context, name string) (contracts.Skill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readCalls = append(f.readCalls, name)
	if f.readErr != nil {
		return contracts.Skill{}, f.readErr
	}
	return contracts.Skill{Name: name, Body: f.bodies[name]}, nil
}

func (f *fakeSkills) Roots() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.roots...)
}

func (f *fakeSkills) Warnings() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.warnings...)
}

func (f *fakeSkills) setList(skills ...contracts.Skill) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.list = skills
}

func (f *fakeSkills) listCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls
}

func (f *fakeSkills) reads() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.readCalls...)
}

// fakeSkillsEditor is a scripted contracts.SkillsEditor wired to the fake list
// so a successful create/update/delete is visible on the next refresh.
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
	skill := contracts.Skill{
		Name: draft.Name, Description: draft.Description,
		Path: "/created/" + draft.Name + "/SKILL.md", Body: draft.Body,
	}
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
	skill := contracts.Skill{Name: name, Description: draft.Description, Body: draft.Body}
	if f.skills != nil {
		f.skills.mu.Lock()
		for index := range f.skills.list {
			if f.skills.list[index].Name == name {
				skill.Path = f.skills.list[index].Path
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

// fakeSkillWorkspace is the minimal contracts.WorkspaceService the skills tab
// needs: the open workspace root classifies workspace skills.
type fakeSkillWorkspace struct{ root string }

func (f fakeSkillWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	return nil, nil
}

func (f fakeSkillWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	if strings.TrimSpace(f.root) == "" {
		return contracts.Workspace{}, false, nil
	}
	return contracts.Workspace{ID: "ws", Name: "workspace", Root: f.root}, true, nil
}

func (f fakeSkillWorkspace) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}

func (f fakeSkillWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}

func (f fakeSkillWorkspace) Delete(context.Context, string) error { return nil }

func (f fakeSkillWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (f fakeSkillWorkspace) ReadFile(context.Context, string, int) ([]byte, error) {
	return nil, nil
}

func (f fakeSkillWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

// --- harness ----------------------------------------------------------------

// newSkillsHarness opens the Capabilities page with the skills backend and,
// when non-nil, the optional editor. It returns the harness, a settings store and
// the data directory the base harness provided (skills below <dataDir>/skills
// are the product's own, globally scoped skills).
func newSkillsHarness(t *testing.T, skills contracts.SkillsService,
	editor contracts.SkillsEditor) (*harness, *fakePluginSettings, string) {
	t.Helper()
	store := newFakePluginSettings(nil)
	h := newHarness(t, nil, nil, plugins.Config{})
	provide[contracts.SettingsService](t, h.ctx, contracts.KeySettings, store)
	if skills != nil {
		provide[contracts.SkillsService](t, h.ctx, contracts.KeySkills, skills)
	}
	if editor != nil {
		provide[contracts.SkillsEditor](t, h.ctx, contracts.KeySkillsEditor, editor)
	}
	dataDir, _ := h.ctx.Get(contracts.KeyDataDir)
	return h, store, dataDir
}

// withWorkspace provides the open workspace used to classify workspace skills.
func withWorkspace(t *testing.T, h *harness, root string) {
	t.Helper()
	provide[contracts.WorkspaceService](t, h.ctx, contracts.KeyWorkspace, fakeSkillWorkspace{root: root})
}

// focusSkillField clicks one input by its composite label ("名称 输入").
func focusSkillField(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	tester.Frame()
	tester.Advance(400 * time.Millisecond)
	tester.Frame()
	rect, ok := tester.Find(label)
	if !ok {
		t.Fatalf("field %q not found; texts = %v", label, tester.Texts())
	}
	tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
	tester.Frame()
}

// dataDirSkill builds one skill below <dataDir>/skills/<name>/SKILL.md.
func dataDirSkill(dataDir, name, description, body string) contracts.Skill {
	return contracts.Skill{
		Name: name, Description: description, Body: body,
		Path: filepath.Join(dataDir, "skills", name, "SKILL.md"),
	}
}

// assertGridItem checks the grid is one accessible collection named "技能列表"
// with items named after the skills.
func (h *harness) assertGridItem(tester *ui.Tester, name string) {
	h.t.Helper()
	if _, ok := tester.AccessByRole(ui.RoleList, "技能列表"); !ok {
		h.t.Fatalf("skill grid is not the named List role; texts = %v", tester.Texts())
	}
	if _, ok := tester.AccessByRole(ui.RoleListItem, name); !ok {
		h.t.Fatalf("skill grid has no item named %q; texts = %v", name, tester.Texts())
	}
}

// --- tests ------------------------------------------------------------------

// TestSkillsTabWithoutBackend covers the optional backend: without
// contracts.KeySkills the skills tab shows its empty state, and switching to the
// connectors tab replaces it (the tabs are separate parallel pages).
func TestSkillsTabWithoutBackend(t *testing.T) {
	h := newHarness(t, nil, nil, plugins.Config{})
	tester := h.tester()
	h.assertTexts(tester, "技能", "技能后端未启用")

	h.click(tester, "连接器")
	// The skills tab's body is not built at all while another parallel tab is
	// chosen; the connectors tab owns its own copy (asserted by connectors_test).
	h.assertNoTexts(tester, "技能后端未启用")

	h.click(tester, "技能")
	h.assertTexts(tester, "技能后端未启用")
}

// TestSkillsCardsRender covers the card anatomy: sparkles + name + description +
// source badge + relative path + the "查看内容" icon button, inside the official
// GridView (a named List role with item identities).
func TestSkillsCardsRender(t *testing.T) {
	skills := newFakeSkills()
	h, _, dataDir := newSkillsHarness(t, skills, nil)
	workspaceRoot := t.TempDir()
	withWorkspace(t, h, workspaceRoot)
	skills.setList(
		contracts.Skill{
			Name: "alpha", Description: "Alpha 技能说明",
			Path: filepath.Join(workspaceRoot, "skills", "alpha", "SKILL.md"),
		},
		dataDirSkill(dataDir, "beta", "Beta 说明", ""),
	)
	tester := h.tester()

	h.assertTexts(tester,
		"2 个技能",
		"alpha", "Alpha 技能说明",
		"beta", "Beta 说明",
		// The data-dir skill is global; "查看内容" is the card's preview button.
		"全局", "查看内容",
		// Relative paths only; the absolute path stays in the tooltip.
		"skills/alpha/SKILL.md", "beta/SKILL.md")
	h.assertNoTexts(tester,
		filepath.Join(workspaceRoot, "skills", "alpha", "SKILL.md"),
		filepath.Join(dataDir, "skills", "beta", "SKILL.md"))
	h.assertGridItem(tester, "alpha")
	h.assertGridItem(tester, "beta")

	// The editor half is optional: no create/Edit/Delete, but every card still
	// exposes its read-only management menu.
	h.assertTexts(tester, "技能操作：alpha", "技能操作：beta",
		"工作区/自定义目录中的技能只读；用卡片 ⋯ 打开文件或所在目录，或在上方操作行新建一个可编辑的技能。")
	h.assertNoTexts(tester, "新建技能", "编辑技能", "删除技能")
}

// TestSkillsSearchAppearsForLongLists covers H20's search: it is hidden while
// the collection fits, appears above six skills, and narrows the cards by name,
// description or path (case insensitively), with a no-match empty state.
func TestSkillsSearchAppearsForLongLists(t *testing.T) {
	short := newFakeSkills()
	h, _, dataDir := newSkillsHarness(t, short, nil)
	short.setList(
		dataDirSkill(dataDir, "alpha", "代码审查清单", ""),
		dataDirSkill(dataDir, "beta", "发布检查", ""),
	)
	tester := h.tester()
	if _, ok := tester.Find("搜索技能"); ok {
		t.Fatalf("search field shows for two skills; texts = %v", tester.Texts())
	}

	long := newFakeSkills()
	h2, _, dataDir2 := newSkillsHarness(t, long, nil)
	long.setList(
		dataDirSkill(dataDir2, "alpha", "代码审查清单", ""),
		dataDirSkill(dataDir2, "beta", "发布检查", ""),
		contracts.Skill{Name: "gamma", Description: "部署脚本", Path: filepath.Join(dataDir2, "deploy", "gamma", "SKILL.md")},
		dataDirSkill(dataDir2, "delta", "", ""),
		dataDirSkill(dataDir2, "epsilon", "", ""),
		dataDirSkill(dataDir2, "zeta", "", ""),
		dataDirSkill(dataDir2, "eta", "", ""),
	)
	tester2 := h2.tester()
	h2.assertTexts(tester2, "7 个技能")
	if _, ok := tester2.Find("搜索技能"); !ok {
		t.Fatalf("search field missing for seven skills; texts = %v", tester2.Texts())
	}

	// A description match narrows the cards.
	h2.click(tester2, "搜索技能")
	tester2.Type("发布")
	tester2.Frame()
	h2.assertTexts(tester2, "beta", "发布检查")
	h2.assertNoTexts(tester2, "alpha", "gamma")
	// A path match also narrows (deploy/gamma/SKILL.md).
	h2.click(tester2, "搜索技能")
	tester2.Key(ui.Cmd, ui.KeyA)
	tester2.Key(0, ui.KeyBackspace)
	tester2.Type("deploy")
	tester2.Frame()
	h2.assertTexts(tester2, "gamma", "部署脚本")
	h2.assertNoTexts(tester2, "alpha", "beta")
	// No match shows the empty copy.
	h2.click(tester2, "搜索技能")
	tester2.Key(ui.Cmd, ui.KeyA)
	tester2.Key(0, ui.KeyBackspace)
	tester2.Type("zzz")
	tester2.Frame()
	h2.assertTexts(tester2, "没有匹配的技能")
	h2.assertNoTexts(tester2, "alpha", "beta", "gamma")
	// Clearing restores the whole collection.
	h2.click(tester2, "搜索技能")
	tester2.Key(ui.Cmd, ui.KeyA)
	tester2.Key(0, ui.KeyBackspace)
	tester2.Frame()
	h2.assertTexts(tester2, "alpha", "beta", "gamma")
}

// TestSkillsEmptyState covers the explicit empty state and its next action:
// without an editor it offers a rescan instead of the (absent) create action.
func TestSkillsEmptyState(t *testing.T) {
	skills := newFakeSkills()
	h, _, dataDir := newSkillsHarness(t, skills, nil)
	skills.roots = []string{filepath.Join(dataDir, "skills")}
	tester := h.tester()

	h.assertTexts(tester,
		"还没有发现技能",
		"在技能目录放置 <name>/SKILL.md 后点击刷新。",
		"刷新技能")
	h.assertNoTexts(tester, "新建技能")
}

// TestSkillsPreviewModal covers the "查看内容" modal: it opens over the grid,
// renders the Markdown body and closes from its footer.
func TestSkillsPreviewModal(t *testing.T) {
	skills := newFakeSkills()
	h, _, dataDir := newSkillsHarness(t, skills, nil)
	skills.setList(dataDirSkill(dataDir, "alpha", "Alpha 技能说明",
		"# Alpha 正文\n\n- 步骤一\n- 步骤二"))
	tester := h.tester()

	h.assertNoTexts(tester, "步骤一")
	h.click(tester, "查看内容")
	h.assertTexts(tester, "Alpha 正文", "步骤一", "步骤二", "关闭")
	// The header carries the source and the relative path.
	h.assertTexts(tester, "全局 · alpha/SKILL.md")

	h.click(tester, "关闭")
	h.assertNoTexts(tester, "步骤一")
	h.assertTexts(tester, "alpha")
}

// TestSkillsPreviewLazyReadsBody covers S-05: the List read model carries only
// an excerpt, so the preview lazily reads the full file (and caches it).
func TestSkillsPreviewLazyReadsBody(t *testing.T) {
	skills := newFakeSkills()
	h, _, dataDir := newSkillsHarness(t, skills, nil)
	skills.setList(contracts.Skill{Name: "lazy", Path: filepath.Join(dataDir, "skills", "lazy", "SKILL.md")})
	skills.bodies["lazy"] = "# 懒加载正文\n\n只有展开时才读取。"
	tester := h.tester()
	if got := skills.reads(); len(got) != 0 {
		t.Fatalf("Read calls before opening the preview = %v, want none", got)
	}

	h.click(tester, "查看内容")
	h.assertTexts(tester, "懒加载正文")
	if got := skills.reads(); len(got) != 1 || got[0] != "lazy" {
		t.Fatalf("Read calls = %v, want [lazy]", got)
	}
}

// TestSkillsPreviewReadsFullBodyWhenExcerpted covers the truncated List excerpt:
// the preview detects the truncation marker and reads the full file.
func TestSkillsPreviewReadsFullBodyWhenExcerpted(t *testing.T) {
	skills := newFakeSkills()
	h, _, dataDir := newSkillsHarness(t, skills, nil)
	skills.setList(contracts.Skill{
		Name: "big", Path: filepath.Join(dataDir, "skills", "big", "SKILL.md"),
		Body: "前情提要\n[truncated: skill file is larger than the read limit]\n",
	})
	skills.bodies["big"] = "前情提要\n\n## 完整正文\n\nTAIL-MARKER"
	tester := h.tester()

	h.click(tester, "查看内容")
	frame := strings.Join(tester.Texts(), "\n")
	if !strings.Contains(frame, "TAIL-MARKER") {
		t.Fatalf("preview did not read the full file; texts = %v", tester.Texts())
	}
	if got := skills.reads(); len(got) != 1 || got[0] != "big" {
		t.Fatalf("Read calls = %v, want [big]", got)
	}
}

// TestSkillsRefreshReloads covers the collection row's refresh action: it drops
// the cached List/bodies and rescans (no extra List call on an idle frame).
func TestSkillsRefreshReloads(t *testing.T) {
	skills := newFakeSkills()
	h, _, dataDir := newSkillsHarness(t, skills, nil)
	skills.setList(dataDirSkill(dataDir, "alpha", "Alpha", "# Alpha"))
	tester := h.tester()
	if calls := skills.listCallCount(); calls != 1 {
		t.Fatalf("List calls after the first frame = %d, want 1", calls)
	}
	tester.Frame()
	if calls := skills.listCallCount(); calls != 1 {
		t.Fatalf("List calls after an idle frame = %d, want the cache", calls)
	}

	skills.setList(dataDirSkill(dataDir, "gamma", "Gamma", "# Gamma"))
	h.click(tester, "刷新技能")
	if calls := skills.listCallCount(); calls != 2 {
		t.Fatalf("List calls after refresh = %d, want 2", calls)
	}
	h.assertTexts(tester, "gamma", "Gamma")
	h.assertNoTexts(tester, "alpha")
}

// TestSkillsErrorsAndWarnings covers the error/warning rows above the cards.
func TestSkillsErrorsAndWarnings(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		skills := newFakeSkills()
		h, _, dataDir := newSkillsHarness(t, skills, nil)
		skills.roots = []string{filepath.Join(dataDir, "skills")}
		skills.listErr = errors.New("scan failed")
		tester := h.tester()
		h.assertTexts(tester, "读取技能失败：scan failed", "刷新技能")
	})
	t.Run("warnings", func(t *testing.T) {
		skills := newFakeSkills()
		h, _, dataDir := newSkillsHarness(t, skills, nil)
		skills.setList(dataDirSkill(dataDir, "alpha", "Alpha", "# Alpha"))
		skills.warnings = []string{`skill "alpha" is shadowed by a higher-precedence root`}
		tester := h.tester()
		h.assertTexts(tester, "技能发现警告：", "shadowed by a higher-precedence root")
	})
}

// TestSkillsCreateModal covers the create dialog: the first field takes the
// keyboard, empty required fields are rejected, and a valid draft is written
// through CreateSkill and reloaded into the cards.
func TestSkillsCreateModal(t *testing.T) {
	skills := newFakeSkills()
	editor := newFakeSkillsEditor(skills)
	h, _, dataDir := newSkillsHarness(t, skills, editor)
	skills.setList(dataDirSkill(dataDir, "existing", "Existing", "# Existing"))
	tester := h.tester()

	h.click(tester, "新建技能")
	h.assertTexts(tester, "名称", "描述", "内容", "创建", "取消")
	if !tester.Focused("名称 输入") {
		t.Fatalf("create dialog did not focus its first field; texts = %v", tester.Texts())
	}
	// Typing lands in the auto-focused name field (no click needed).
	tester.Type("新技能")
	tester.Frame()
	focusSkillField(t, tester, "描述 输入")
	tester.Type("技能说明")
	tester.Frame()
	focusSkillField(t, tester, "内容 输入")
	tester.Type("# 正文")
	tester.Frame()
	h.click(tester, "创建")

	creates := editor.created()
	if len(creates) != 1 {
		t.Fatalf("CreateSkill calls = %d, want 1", len(creates))
	}
	if creates[0].Name != "新技能" || creates[0].Description != "技能说明" || creates[0].Body != "# 正文" {
		t.Fatalf("CreateSkill draft = %+v", creates[0])
	}
	h.assertTexts(tester, "已创建技能", "新技能")
	h.assertNoTexts(tester, "创建技能失败：")
}

// TestSkillsCreateValidation covers the required fields: an empty body keeps
// the dialog open with a danger line and never calls the editor.
func TestSkillsCreateValidation(t *testing.T) {
	skills := newFakeSkills()
	editor := newFakeSkillsEditor(skills)
	h, _, _ := newSkillsHarness(t, skills, editor)
	tester := h.tester()

	h.click(tester, "新建技能")
	tester.Type("只有名称")
	tester.Frame()
	h.click(tester, "创建")
	if got := editor.created(); len(got) != 0 {
		t.Fatalf("CreateSkill calls with an empty body = %v, want none", got)
	}
	h.assertTexts(tester, "名称和内容不能为空", "创建", "取消")
}

// TestSkillsEditModal covers the edit dialog: the name is read-only, the fields
// are prefilled and saving writes through UpdateSkill.
func TestSkillsEditModal(t *testing.T) {
	skills := newFakeSkills()
	editor := newFakeSkillsEditor(skills)
	h, _, dataDir := newSkillsHarness(t, skills, editor)
	skills.setList(dataDirSkill(dataDir, "alpha", "old desc", "old body"))
	tester := h.tester()

	h.click(tester, "技能操作：alpha")
	h.click(tester, "编辑技能")
	h.assertTexts(tester, "编辑技能", "alpha", "名称不可修改", "描述", "内容", "保存", "取消")
	focusSkillField(t, tester, "描述 输入")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type("new desc")
	tester.Frame()
	focusSkillField(t, tester, "内容 输入")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type("# new body")
	tester.Frame()
	h.click(tester, "保存")

	updates := editor.updated()
	if len(updates) != 1 {
		t.Fatalf("UpdateSkill calls = %d, want 1", len(updates))
	}
	if updates[0].Name != "alpha" || updates[0].Description != "new desc" || updates[0].Body != "# new body" {
		t.Fatalf("UpdateSkill draft = %+v (the name must stay read-only)", updates[0])
	}
	h.assertTexts(tester, "已保存", "new desc")
}

// TestSkillsDeleteConfirmEscapeCancel covers the dangerous confirmation's safe
// default: Esc closes it and deletes nothing.
func TestSkillsDeleteConfirmEscapeCancel(t *testing.T) {
	skills := newFakeSkills()
	editor := newFakeSkillsEditor(skills)
	h, _, dataDir := newSkillsHarness(t, skills, editor)
	skills.setList(dataDirSkill(dataDir, "alpha", "Alpha", "# Alpha"))
	tester := h.tester()

	h.click(tester, "技能操作：alpha")
	h.click(tester, "删除技能")
	h.assertTexts(tester, "删除技能", "确认删除")
	if !tester.HasText("alpha") {
		t.Fatalf("delete dialog does not name the skill; texts = %v", tester.Texts())
	}

	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if got := editor.deleted(); len(got) != 0 {
		t.Fatalf("Esc on the delete dialog deleted %v", got)
	}
	h.assertNoTexts(tester, "确认删除")
	h.assertTexts(tester, "alpha")
}

// TestSkillsDeleteConfirms covers the destructive path: the explicitly chosen
// danger action runs DeleteSkill, reloads and reports the deletion.
func TestSkillsDeleteConfirms(t *testing.T) {
	skills := newFakeSkills()
	editor := newFakeSkillsEditor(skills)
	h, _, dataDir := newSkillsHarness(t, skills, editor)
	skills.setList(dataDirSkill(dataDir, "alpha", "Alpha", "# Alpha"))
	tester := h.tester()

	h.click(tester, "技能操作：alpha")
	h.click(tester, "删除技能")
	h.click(tester, "确认删除")

	if got := editor.deleted(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("DeleteSkill calls = %v, want [alpha]", got)
	}
	h.assertTexts(tester, "已删除技能")
	h.assertNoTexts(tester, "确认删除")
	h.assertNoTexts(tester, "alpha")
}

// TestSkillsEditorActionsHiddenWithoutEditor covers the optional editor: only
// the read-only skills service means no create/Edit/Delete, while the card's
// read-only management menu stays available.
func TestSkillsEditorActionsHiddenWithoutEditor(t *testing.T) {
	skills := newFakeSkills()
	h, _, dataDir := newSkillsHarness(t, skills, nil)
	skills.setList(dataDirSkill(dataDir, "alpha", "Alpha", "# Alpha"))
	tester := h.tester()

	h.assertTexts(tester, "alpha", "查看内容", "技能操作：alpha")
	h.assertNoTexts(tester, "新建技能", "编辑技能", "删除技能")
}

// TestSkillsReadOnlyCardMenuActions covers the read-only management entry on a
// workspace skill: open / reveal / copy are always available, Edit/Delete are
// not.
func TestSkillsReadOnlyCardMenuActions(t *testing.T) {
	previousOpen, previousShow := openpath.Open, openpath.ShowInFolder
	var opened, revealed []string
	openpath.Open = func(path string) error {
		opened = append(opened, path)
		return nil
	}
	openpath.ShowInFolder = func(path string) error {
		revealed = append(revealed, path)
		return nil
	}
	t.Cleanup(func() {
		openpath.Open = previousOpen
		openpath.ShowInFolder = previousShow
	})
	skills := newFakeSkills()
	h, _, _ := newSkillsHarness(t, skills, newFakeSkillsEditor(skills))
	workspaceRoot := t.TempDir()
	withWorkspace(t, h, workspaceRoot)
	path := filepath.Join(workspaceRoot, "skills", "alpha", "SKILL.md")
	skills.setList(contracts.Skill{Name: "alpha", Description: "Alpha", Path: path})
	tester := h.tester()

	h.click(tester, "技能操作：alpha")
	h.assertTexts(tester, "用默认应用打开", "在文件管理器中显示", "复制路径")
	h.assertNoTexts(tester, "编辑技能", "删除技能")
	if err := tester.Click("用默认应用打开"); err != nil {
		t.Fatalf("open action: %v", err)
	}
	tester.Frame()
	if len(opened) != 1 || opened[0] != path {
		t.Fatalf("open with default app = %v, want [%s]", opened, path)
	}

	h.click(tester, "技能操作：alpha")
	if err := tester.Click("在文件管理器中显示"); err != nil {
		t.Fatalf("reveal action: %v", err)
	}
	tester.Frame()
	if len(revealed) != 1 || revealed[0] != path {
		t.Fatalf("show in file manager = %v, want [%s]", revealed, path)
	}
}

// TestSkillsTopLevelMarkdownSkillStaysReadOnly covers the discovered top-level
// <dataDir>/skills/foo.md form: it gets the read-only menu but never the
// editor actions that would fail after the click.
func TestSkillsTopLevelMarkdownSkillStaysReadOnly(t *testing.T) {
	skills := newFakeSkills()
	editor := newFakeSkillsEditor(skills)
	h, _, dataDir := newSkillsHarness(t, skills, editor)
	skills.setList(contracts.Skill{
		Name: "top", Description: "Top-level markdown",
		Path: filepath.Join(dataDir, "skills", "top.md"),
	})
	tester := h.tester()
	h.click(tester, "技能操作：top")
	h.assertTexts(tester, "用默认应用打开", "在文件管理器中显示", "复制路径")
	h.assertNoTexts(tester, "编辑技能", "删除技能")
}

// TestSkillsEmptyStateSingleCreateAction covers the duplicated create button:
// the collection row owns "New skill", the empty state only offers refresh.
func TestSkillsEmptyStateSingleCreateAction(t *testing.T) {
	skills := newFakeSkills()
	h, _, _ := newSkillsHarness(t, skills, newFakeSkillsEditor(skills))
	tester := h.tester()
	tester.Frame()
	count := 0
	for _, text := range tester.Texts() {
		if text == "新建技能" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("New skill rendered %d times, want exactly once; texts = %v", count, tester.Texts())
	}
	h.assertTexts(tester, "刷新技能")
}

// TestSkillsReadOnlyHintOnlyWithoutEditableSkill covers the hint's condition.
func TestSkillsReadOnlyHintOnlyWithoutEditableSkill(t *testing.T) {
	hint := "工作区/自定义目录中的技能只读"
	skills := newFakeSkills()
	h, _, dataDir := newSkillsHarness(t, skills, newFakeSkillsEditor(skills))
	skills.setList(dataDirSkill(dataDir, "editable", "Editable", "# Editable"))
	tester := h.tester()
	h.assertNoTexts(tester, hint)

	// A workspace-only list has no editable skill, so the hint appears.
	h2Skills := newFakeSkills()
	h2, _, _ := newSkillsHarness(t, h2Skills, newFakeSkillsEditor(h2Skills))
	workspaceRoot := t.TempDir()
	withWorkspace(t, h2, workspaceRoot)
	h2Skills.setList(contracts.Skill{Name: "readonly", Path: filepath.Join(workspaceRoot, "skills", "readonly", "SKILL.md")})
	tester2 := h2.tester()
	h2.assertTexts(tester2, hint)
}

func TestSkillsDisableToggle(t *testing.T) {
	skills := newFakeSkills()
	editor := newFakeSkillsEditor(skills)
	h, store, _ := newSkillsHarness(t, skills, editor)
	workspaceRoot := t.TempDir()
	withWorkspace(t, h, workspaceRoot)
	skills.setList(contracts.Skill{Name: "alpha", Path: filepath.Join(workspaceRoot, "skills", "alpha", "SKILL.md")})
	tester := h.tester()

	h.click(tester, "技能操作：alpha")
	if err := tester.Click("禁用技能"); err != nil {
		t.Fatalf("disable skill: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := store.Get(contracts.SettingSkillsDisabled, ""); got != `["alpha"]` {
		t.Fatalf("%s = %q, want [\"alpha\"]", contracts.SettingSkillsDisabled, got)
	}
	h.assertTexts(tester, "已禁用")

	h.click(tester, "技能操作：alpha")
	if err := tester.Click("启用技能"); err != nil {
		t.Fatalf("enable skill: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	if got := store.Get(contracts.SettingSkillsDisabled, ""); got != "[]" {
		t.Fatalf("%s after enable = %q, want []", contracts.SettingSkillsDisabled, got)
	}
}

func TestSkillsCopyToEditable(t *testing.T) {
	skills := newFakeSkills()
	editor := newFakeSkillsEditor(skills)
	h, _, _ := newSkillsHarness(t, skills, editor)
	workspaceRoot := t.TempDir()
	withWorkspace(t, h, workspaceRoot)
	skills.setList(contracts.Skill{
		Name: "alpha", Description: "Alpha desc", Body: "# Alpha body",
		Path: filepath.Join(workspaceRoot, "skills", "alpha", "SKILL.md"),
	})
	skills.bodies["alpha"] = "# Alpha body"
	tester := h.tester()

	h.click(tester, "技能操作：alpha")
	if err := tester.Click("复制到我的技能"); err != nil {
		t.Fatalf("copy skill: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	h.assertTexts(tester, "新建技能", "名称 输入", "内容 输入")
	h.click(tester, "创建")
	created := editor.created()
	if len(created) != 1 || created[0].Name != "alpha-copy" || created[0].Body != "# Alpha body" {
		t.Fatalf("CreateSkill draft = %+v", created)
	}
}

// TestSkillsEnglishCopy covers the localized English surface.
func TestSkillsEnglishCopy(t *testing.T) {
	skills := newFakeSkills()
	editor := newFakeSkillsEditor(skills)
	h, _, dataDir := newSkillsHarness(t, skills, editor)
	skills.setList(dataDirSkill(dataDir, "alpha", "Alpha desc", "# Alpha"))
	tester := h.tester()

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Skills", "Connectors", "New skill", "View content", "Skill actions: alpha")

	h.click(tester, "New skill")
	h.assertTexts(tester, "Name", "Description", "Body", "Create", "Cancel")
	focusSkillField(t, tester, "Name input")
	tester.Type("english-skill")
	tester.Frame()
	focusSkillField(t, tester, "Body input")
	tester.Type("body")
	tester.Frame()
	h.click(tester, "Create")
	h.assertTexts(tester, "Skill created", "english-skill")

	h.click(tester, "Skill actions: alpha")
	h.click(tester, "Edit skill")
	h.assertTexts(tester, "Edit skill", "The name cannot be changed", "Save", "Cancel")
}

// TestSkillsDirectoriesDisclosure covers the scan/extra-directory disclosure:
// the scanned roots name their scope and open in the file manager, and the
// extra directory list saves skills.dirs and rescans.
func TestSkillsDirectoriesDisclosure(t *testing.T) {
	oldOpen := openpath.Open
	var opened []string
	openpath.Open = func(path string) error {
		opened = append(opened, path)
		return nil
	}
	t.Cleanup(func() { openpath.Open = oldOpen })

	skills := newFakeSkills()
	h, store, dataDir := newSkillsHarness(t, skills, nil)
	workspaceRoot := t.TempDir()
	withWorkspace(t, h, workspaceRoot)
	workspaceSkills := filepath.Join(workspaceRoot, "skills")
	globalSkills := filepath.Join(dataDir, "skills")
	skills.roots = []string{workspaceSkills, globalSkills}
	skills.setList(dataDirSkill(dataDir, "alpha", "Alpha", "# Alpha"))
	tester := h.tester()
	h.assertNoTexts(tester, workspaceSkills)

	h.click(tester, "技能目录与额外目录")
	h.assertTexts(tester, "工作区", "全局", workspaceSkills, globalSkills, "打开目录",
		"仅对当前工作区生效")
	h.click(tester, "打开目录")
	if len(opened) != 1 || opened[0] != workspaceSkills {
		t.Fatalf("openpath calls = %v, want the first scanned root", opened)
	}

	if calls := skills.listCallCount(); calls != 1 {
		t.Fatalf("List calls before applying = %d, want 1", calls)
	}
	focusSkillField(t, tester, "目录列表 输入")
	tester.Type(filepath.Join(workspaceRoot, "extra-skills"))
	tester.Frame()
	h.click(tester, "应用")
	scopedKey := contracts.SkillsDirsSettingKey("ws")
	if got := store.Get(scopedKey, ""); got != filepath.Join(workspaceRoot, "extra-skills") {
		t.Fatalf("%s = %q, want the typed workspace-scoped directory list", scopedKey, got)
	}
	if got := store.Get(contracts.SettingSkillsDirs, ""); got != "" {
		t.Fatalf("global %s = %q, want it untouched while a workspace is open", contracts.SettingSkillsDirs, got)
	}
	h.assertTexts(tester, "已保存")
	if calls := skills.listCallCount(); calls != 2 {
		t.Fatalf("List calls after applying = %d, want a rescan (2)", calls)
	}
}

// TestSkillsWorkspaceScopeNote covers the strict per-workspace scoping note: it
// appears only while a workspace is open and explains where global skills went.
func TestSkillsWorkspaceScopeNote(t *testing.T) {
	note := "已打开工作区：只显示该工作区的技能"
	skills := newFakeSkills()
	h, _, _ := newSkillsHarness(t, skills, nil)
	tester := h.tester()
	h.assertNoTexts(tester, note)

	workspaceRoot := t.TempDir()
	withWorkspace(t, h, workspaceRoot)
	tester.Frame()
	h.assertTexts(tester, note)
	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "A workspace is open: only this workspace's skills are shown")
}

// TestSkillsWorkspaceEditorSkillIsEditable covers the editor-root fix: a skill
// created under the open workspace's .zeokeel/skills is editable, unlike the
// read-only <root>/skills convention directory.
func TestSkillsWorkspaceEditorSkillIsEditable(t *testing.T) {
	skills := newFakeSkills()
	editor := newFakeSkillsEditor(skills)
	h, _, _ := newSkillsHarness(t, skills, editor)
	workspaceRoot := t.TempDir()
	withWorkspace(t, h, workspaceRoot)
	skills.setList(contracts.Skill{
		Name: "mine", Description: "Mine",
		Path: filepath.Join(workspaceRoot, ".zeokeel", "skills", "mine", "SKILL.md"),
	})
	tester := h.tester()
	h.click(tester, "技能操作：mine")
	h.assertTexts(tester, "编辑技能", "删除技能")
}
