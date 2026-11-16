package plugins

// The Capabilities page's Skills tab: the discovered SKILL.md instruction packs
// as a card grid, with the collection row (count, rescan, create), the search
// that appears once the collection outgrows one sight of cards, the
// scan/extra-directory disclosure and the cards' preview/edit/delete actions
// (the dialogs live in skills_edit.go).
//
// A skill is an independent object, so cards are the right container; the
// collection is the official ui.GridView (virtualized, keyed and named for
// assistive technology). The card grid measures are the package-level
// cardGrid* constants shared with the connectors tab (view.go).
//
// All state lives on skillsState and only changes from build-pass callbacks
// that ask for a redraw; no element or part is stored across frames.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/lib/openpath"
	"github.com/zeokeel/zeokeel/internal/lib/textutil"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// skillsSearchThreshold is the collection size above which the tab shows the
// search field: the search is a utility for a long inventory, not chrome the
// short list carries for nothing.
const skillsSearchThreshold = 6

// skillReadLimitMarker is the marker the List read model puts in a skill body
// it truncated; the preview reads the full file when it sees it.
const skillReadLimitMarker = "skill file is larger than the read limit"

// skillsState is the Skills tab's UI state (only touched on the UI thread).
type skillsState struct {
	// Cached List/Roots result. loaded is set on the first frame of a visit and
	// cleared by refresh() so the next frame rescans.
	loaded  bool
	skills  []contracts.Skill
	listErr error
	roots   []string

	// query filters the cards by name, description or path (H20).
	query string

	// grid backs the cards' official GridView: Key/Label are set per frame from
	// the filtered collection, so identity follows a skill across searches.
	grid ui.GridState

	// Disabled skills (skills.disabled) are hidden from the agent-facing skill
	// tool but remain listed here so they can be re-enabled.
	disabled       map[string]bool
	disabledNames  []string
	disabledLoaded bool

	// lastScan drives the periodic external-change refresh while the tab is
	// open (the filesystem has no watcher dependency in this build).
	lastScan time.Time

	// Lazy per-skill body cache: List returns a bounded excerpt (S-05), so a
	// preview/edit reads the full file through Read once and reuses it.
	bodies  map[string]string
	bodyErr map[string]string

	// The open content-preview modal, identified by the skill's stable
	// name+path key so a refresh cannot show a stale body.
	previewOpen bool
	previewName string
	previewPath string

	// Scan/extra-directory disclosure (dirsOpen) and the user-configured list
	// stored in skills.dirs (loaded once from the settings store).
	dirsOpen        bool
	extraDirs       string
	extraDirsLoaded bool
	extraNotice     string
	extraErr        bool

	// Editor half (skills_edit.go): the create/edit modal drafts, the success
	// line and the delete confirmation.
	creating         bool
	draftName        string
	draftDescription string
	draftBody        string
	editing          bool
	editName         string
	editDescription  string
	editBody         string
	editErr          string
	editStatus       string
	bodyPreviewOpen  bool
	confirmDelete    bool
	pendingDelete    string
}

// newSkillsState returns an empty Skills tab state.
func newSkillsState() *skillsState {
	return &skillsState{
		bodies:  make(map[string]string),
		bodyErr: make(map[string]string),
	}
}

// refresh invalidates the cached List result and the lazy bodies; the next
// frame rescans and re-reads.
func (p *skillsState) refresh() {
	p.loaded = false
	p.skills = nil
	p.listErr = nil
	p.bodies = make(map[string]string)
	p.bodyErr = make(map[string]string)
}

// load caches the first List/Roots read of a visit (a frame never rescans
// unasked).
func (p *skillsState) load(skills contracts.SkillsService) {
	if p.loaded {
		return
	}
	p.loaded = true
	p.skills, p.listErr = skills.List(context.Background())
	p.roots = skills.Roots()
	p.lastScan = time.Now()
}

// skillAutoRefreshInterval bounds how often the open tab rescans for external
// SKILL.md edits; it is a variable so tests can shorten it.
var skillAutoRefreshInterval = 5 * time.Second

// autoRefreshDue reports whether the tab should rescan for external changes.
func (p *skillsState) autoRefreshDue(now time.Time) bool {
	return !p.lastScan.IsZero() && now.Sub(p.lastScan) >= skillAutoRefreshInterval
}

// loadDisabledSkills reads skills.disabled once per visit; toggles update the
// in-memory set immediately.
func (p *skillsState) loadDisabledSkills(settings contracts.SettingsService) {
	if p.disabledLoaded || settings == nil {
		return
	}
	p.disabledLoaded = true
	p.disabledNames = nil
	p.disabled = make(map[string]bool)
	names, err := contracts.ParseStringListJSON(settings.Get(contracts.SettingSkillsDisabled, ""))
	if err != nil {
		return
	}
	for _, name := range names {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			p.disabled[strings.ToLower(trimmed)] = true
			p.disabledNames = append(p.disabledNames, trimmed)
		}
	}
}

// skillDisabled reports whether name is disabled for the agent-facing tool.
func (p *skillsState) skillDisabled(name string) bool {
	return p.disabled[strings.ToLower(strings.TrimSpace(name))]
}

// skillKey is a skill's stable identity across frames and refreshes.
func skillKey(skill contracts.Skill) string {
	return skill.Name + "\x00" + skill.Path
}

// skillEditor returns the optional editor half and the product data directory
// (the latter also decides which skills the card actions may touch).
func skillEditor(kctx cordis.Context) (contracts.SkillsEditor, string) {
	editor, _ := kctx.Get(contracts.KeySkillsEditor)
	dataDir, _ := kctx.Get(contracts.KeyDataDir)
	return editor, strings.TrimSpace(dataDir)
}

// buildSkillsTab renders the Skills tab's page body for one frame: the modals
// (so create/edit/preview work in every list state), the collection row, the
// search, the card grid or empty/error states, and the directory disclosure.
func buildSkillsTab(c *ui.Context, t *design.Tokens, st *state) {
	p := st.skills
	skills, hasSkills := st.ctx.Get(contracts.KeySkills)
	editor, dataDir := skillEditor(st.ctx)
	buildSkillsDialogs(c, t, p, st, skills, editor)
	if !hasSkills || skills == nil {
		uikit.EmptyState(c, st.tr("技能后端未启用", "Skills backend is not enabled"))
		return
	}
	if settingsStore, hasSettings := st.ctx.Get(contracts.KeySettings); hasSettings && settingsStore != nil {
		p.loadDisabledSkills(settingsStore)
	}
	p.load(skills)
	if p.autoRefreshDue(time.Now()) {
		p.refresh()
		p.load(skills)
	}

	workspaceRoot := skillWorkspaceRoot(st.ctx)
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		// A plain-language intro teaches what a skill is (US-08): the tab otherwise
		// opens straight into the developer-facing SKILL.md mechanics.
		design.Apply(ui.Text(c, st.tr(
			"技能＝给智能体的任务说明书：一个 Markdown 指令包，告诉它如何完成某类任务。",
			"Skills are task manuals for the agent: Markdown instruction packs that teach it how to do a kind of task.")),
			t.TextMuted(design.TypeXXS)).MaxLines(2)
		buildSkillsStatus(c, t, p, st)
		buildSkillsCollectionRow(c, t, p, st, editor)
		if p.listErr != nil {
			// The collection row stays available so the error is recoverable:
			// the refresh action rescans.
			uikit.ErrorLine(c, st.tr("读取技能失败：", "Failed to read skills: ")+p.listErr.Error())
			return
		}
		if warner, ok := skills.(contracts.SkillWarnings); ok {
			if warnings := warner.Warnings(); len(warnings) > 0 {
				uikit.ErrorLine(c, st.tr("技能发现警告：", "Skill discovery warnings: ")+
					strings.Join(warnings, "；"))
			}
		}
		if len(p.skills) > 0 && !skillsHaveEditable(p.skills, editor, dataDir) {
			// The management entry exists on every card now; explain why most
			// of them only offer read-only actions instead of leaving the user
			// guessing why Edit/Delete are missing.
			design.Apply(ui.Text(c, st.tr(
				"工作区/自定义目录中的技能只读；用卡片 ⋯ 打开文件或所在目录，或在上方操作行新建一个可编辑的技能。",
				"Skills from the workspace or custom directories are read-only; use a card's ⋯ to open the file or its folder, or create an editable skill from the row above."),
			), t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).MaxLines(2)
		}
		if len(p.skills) > skillsSearchThreshold {
			label := st.tr("搜索技能", "Search skills")
			ui.SearchField(c, &p.query).Label(label).
				Placeholder(st.tr("按名称、说明或路径搜索…", "Search by name, description or path…")).
				FillWidth()
		}
		filtered := skillFilter(p.skills, p.query)
		switch {
		case len(p.skills) == 0:
			uikit.EmptyStateBlock(c,
				st.tr("还没有发现技能", "No skills discovered yet"),
				st.tr("在技能目录放置 <name>/SKILL.md 后点击刷新。",
					"Place <name>/SKILL.md in a skill directory, then refresh."),
				nil)
			return
		case len(filtered) == 0:
			uikit.EmptyStateBlock(c,
				st.tr("没有匹配的技能", "No matching skills"),
				st.tr("换个关键词试试。", "Try a different search term."), nil)
			return
		}
		p.grid.Key = func(index int) any {
			if index < 0 || index >= len(filtered) {
				return nil
			}
			return skillKey(filtered[index])
		}
		p.grid.Label = func(index int) string {
			if index < 0 || index >= len(filtered) {
				return ""
			}
			return textutil.FirstNonEmptyTrimmed(filtered[index].Name, st.tr("(未命名技能)", "(Unnamed skill)"))
		}
		ui.GridView(c, &p.grid, len(filtered), cardGridMinWidth, cardGridHeight, func(index int) {
			buildSkillCard(c, t, p, st, skills, filtered[index], editor, dataDir, workspaceRoot)
		}).Label(st.tr("技能列表", "Skills")).
			MaxHeight(cardGridViewportThreeRows)
	})
	buildSkillsDirectories(c, t, p, st)
}

// buildSkillsStatus renders the last mutation's success line and the delete
// failure that the (error-slot-less) confirmation could not show itself.
func buildSkillsStatus(c *ui.Context, t *design.Tokens, p *skillsState, st *state) {
	if p.editStatus != "" {
		design.Apply(ui.Text(c, p.editStatus), t.Font(design.TypeXXS)).TextColor(t.SuccessText)
	}
	if p.editErr != "" && !p.creating && !p.editing {
		uikit.ErrorLine(c, p.editErr)
	}
}

// buildSkillsCollectionRow is the tab's action row: the inventory count on the
// left, the rescan and create actions on the right. It is not a page header (the
// page-level tabs already name the destination) and it is not a card.
func buildSkillsCollectionRow(c *ui.Context, t *design.Tokens, p *skillsState, st *state,
	editor contracts.SkillsEditor) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		if count := len(p.skills); count > 0 {
			design.Apply(ui.Text(c, st.tr(fmt.Sprintf("%d 个技能", count), fmt.Sprintf("%d skills", count))),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).Grow(1)
		} else {
			ui.Spacer(c).Grow(1)
		}
		refreshLabel := st.tr("刷新技能", "Refresh skills")
		uikit.IconButton(c, uikit.IconButtonOptions{
			Key: "skills.refresh", Label: refreshLabel, Tooltip: refreshLabel,
			Icon: icons.Reload(), Size: design.IconSizeS,
			OnClick: func() {
				p.refresh()
				c.Invalidate()
			},
		})
		if editor != nil {
			// Secondary, never primary: the page's single primary belongs to a
			// dialog's submit action, not to a section utility (AP-20).
			if design.Apply(ui.Button(c, st.tr("新建技能", "New skill")), t.SecondaryButton()).Clicked() {
				startCreateSkill(c, p)
			}
		}
	})
}

// skillsHaveEditable reports whether at least one skill can be edited/deleted
// from this page (a directory-form SKILL.md below the data directory).
func skillsHaveEditable(skills []contracts.Skill, editor contracts.SkillsEditor, dataDir string) bool {
	if editor == nil {
		return false
	}
	for _, skill := range skills {
		if skillsEditActionsAllowed(skill.Path, dataDir) {
			return true
		}
	}
	return false
}

// buildSkillCard is one skill as a card: sparkles + name + trailing actions,
// the two-line description, then the source badge, the relative path and the
// content-preview button. The card is one independent object, so the panel
// surface is used; the absolute path stays in the path tooltip.
func buildSkillCard(c *ui.Context, t *design.Tokens, p *skillsState, st *state,
	skills contracts.SkillsService, skill contracts.Skill, editor contracts.SkillsEditor,
	dataDir, workspaceRoot string) {
	key := skillKey(skill)
	name := textutil.FirstNonEmptyTrimmed(skill.Name, st.tr("(未命名技能)", "(Unnamed skill)"))
	source, relative := skillDisplayPath(skill.Path, dataDir, workspaceRoot, p.roots)
	design.Apply(ui.Column(c.Key("skills.card/"+key)), t.Panel()).
		FillWidth().FillHeight().Padding(design.Space3).Gap(design.Space2).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			// One accent per card at most: the glyph is quiet, the name carries
			// the hierarchy.
			ui.Icon(c, icons.Sparkles()).Size(design.IconSizeS, design.IconSizeS).
				TextColor(t.LabelSecondary)
			design.Apply(ui.Text(c, name), t.Font(design.TypeS)).
				FontWeight(design.WeightSemibold).TextColor(t.LabelPrimary).
				Grow(1).Shrink(1).MinWidth(design.Space0).MaxLines(1).Ellipsis("…")
			buildSkillCardMenu(c, t, p, st, skills, skill, editor, dataDir)
		})
		description := strings.TrimSpace(skill.Description)
		if description == "" {
			design.Apply(ui.Text(c, st.tr("无描述", "No description")), t.Font(design.TypeXS)).
				TextColor(t.LabelTertiary).MaxLines(2).Ellipsis("…")
		} else {
			design.Apply(ui.Text(c, description), t.Font(design.TypeXS)).
				TextColor(t.LabelSecondary).MaxLines(2).Ellipsis("…")
		}
		ui.Spacer(c).Grow(1)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			sourceBadge := design.Apply(ui.Badge(c, skillSourceLabel(st, source)), t.Badge())
			if source == "workspace" {
				sourceBadge.Tooltip(st.tr("仓库内容，模型按不可信数据处理；只读",
					"Repository content treated as untrusted data; read-only"))
			} else {
				sourceBadge.Tooltip(st.tr("全局技能目录；只有 dataDir 下的目录形态技能可编辑",
					"Global skill directory; only directory-form skills below dataDir are editable"))
			}
			if path := strings.TrimSpace(skill.Path); path != "" {
				design.Apply(ui.Text(c, relative), t.MonoText(design.TypeXXS)).
					TextColor(t.LabelTertiary).Grow(1).Shrink(1).MinWidth(design.Space0).
					MaxLines(1).Ellipsis("…").Tooltip(path)
			} else {
				ui.Spacer(c).Grow(1)
			}
			if p.skillDisabled(skill.Name) {
				design.Apply(ui.Badge(c, st.tr("已禁用", "Disabled")), t.Badge()).
					Tooltip(st.tr("已从模型可用的技能中排除；不会注入会话",
						"Excluded from model-facing skills; not injected into sessions"))
			}
			previewLabel := st.tr("查看内容", "View content")
			uikit.IconButton(c, uikit.IconButtonOptions{
				Key: "skills.view/" + key, Label: previewLabel, Tooltip: previewLabel,
				Icon: icons.File(), Size: design.IconSizeS,
				OnClick: func() {
					p.previewOpen = true
					p.previewName = skill.Name
					p.previewPath = skill.Path
					c.Invalidate()
				},
			})
		})
	})
}

// skillSourceLabel localizes a skill's source scope: the current workspace's
// roots are the project's, every other scanned root (configured, data dir) is
// global.
func skillSourceLabel(st *state, source string) string {
	if source == "workspace" {
		return st.tr("工作区", "Workspace")
	}
	return st.tr("全局", "Global")
}

// buildSkillsDirectories renders the scan/extra-directory disclosure: each
// scanned root names its scope, shows its path and opens in the file manager;
// the extra list is persisted in skills.dirs and applied with a rescan.
func buildSkillsDirectories(c *ui.Context, t *design.Tokens, p *skillsState, st *state) {
	settingsStore, hasSettings := st.ctx.Get(contracts.KeySettings)
	if len(p.roots) == 0 && (!hasSettings || settingsStore == nil) {
		return
	}
	ui.Collapsible(c, st.tr("技能目录与额外目录", "Skill directories"), &p.dirsOpen, func() {
		for _, root := range p.roots {
			root := root
			scopeZh, scopeEn := skillRootScope(st.ctx, root)
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
				PaddingEnd(design.Space4).Children(func() {
				design.Apply(ui.Text(c, st.tr(scopeZh, scopeEn)), t.Font(design.TypeXXS)).
					TextColor(t.LabelTertiary).Width(design.Space8 * 2).Shrink(0)
				design.Apply(ui.Text(c, root), t.MonoText(design.TypeXS)).
					TextColor(t.LabelTertiary).Grow(1).Shrink(1).MinWidth(design.Space0).
					MaxLines(1).Ellipsis("…").Tooltip(root)
				openLabel := st.tr("打开目录", "Open directory")
				uikit.IconButton(c, uikit.IconButtonOptions{
					Key: "skills.root/" + root, Label: openLabel, Tooltip: openLabel,
					Icon: icons.FolderOpen(), Size: design.IconSizeS,
					OnClick: func() {
						if err := openpath.Open(root); err != nil {
							p.editErr = st.tr("打开目录失败：", "Failed to open the directory: ") + err.Error()
							c.Invalidate()
						}
					},
				})
			})
		}
		if hasSettings && settingsStore != nil {
			buildSkillsExtraDirs(c, t, p, st, settingsStore)
		}
	})
}

// buildSkillsExtraDirs renders the user-configurable extra skill directory list
// (S-03): the value is stored in skills.dirs and an apply rescans immediately.
func buildSkillsExtraDirs(c *ui.Context, t *design.Tokens, p *skillsState, st *state,
	settingsStore contracts.SettingsService) {
	if !p.extraDirsLoaded {
		p.extraDirsLoaded = true
		p.extraDirs = settingsStore.Get(contracts.SettingSkillsDirs, "")
	}
	label := st.tr("目录列表", "Directory list")
	ui.Field(c, label, func() {
		ui.TextInput(c, &p.extraDirs).
			Label(skillInputLabel(st, label)).
			Placeholder(st.tr("例如 ~/.zeokeel/skills", "e.g. ~/.zeokeel/skills")).
			FillWidth()
	}).Description(st.tr(
		"用路径分隔符（Windows 为 ;，其它为 :）或换行分隔；相对路径按数据目录解析。留空表示不额外扫描。",
		"Separate entries with the OS path separator (; on Windows, : elsewhere) or newlines; "+
			"relative paths resolve against the data directory. Empty means no extra directories."))
	// Secondary: applying the directory list is a section utility, not the
	// page's primary action.
	if design.Apply(ui.Button(c, st.tr("应用", "Apply")), t.SecondaryButton()).Clicked() {
		value := strings.TrimSpace(p.extraDirs)
		if err := settingsStore.Set(contracts.SettingSkillsDirs, value); err != nil {
			p.extraNotice = st.tr("保存失败：", "Save failed: ") + err.Error()
			p.extraErr = true
		} else {
			p.extraNotice = st.tr("已保存", "Saved")
			p.extraErr = false
			p.refresh()
		}
		c.Invalidate()
	}
	if p.extraNotice != "" {
		if p.extraErr {
			uikit.ErrorLine(c, p.extraNotice)
		} else {
			design.Apply(ui.Text(c, p.extraNotice), t.Font(design.TypeXXS)).TextColor(t.SuccessText)
		}
	}
}

// skillFilter narrows the collection by the search query (case-insensitive over
// name, description and path). An empty query keeps every skill.
func skillFilter(skills []contracts.Skill, query string) []contracts.Skill {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return skills
	}
	filtered := make([]contracts.Skill, 0, len(skills))
	for _, skill := range skills {
		haystack := strings.ToLower(skill.Name + "\x00" + skill.Description + "\x00" + skill.Path)
		if strings.Contains(haystack, needle) {
			filtered = append(filtered, skill)
		}
	}
	return filtered
}

// skillDisplayPath classifies a skill by its scanned root and returns the source
// scope plus a path relative to that root: data-dir skills are global, workspace
// roots are the project's. The absolute path stays available as the tooltip.
func skillDisplayPath(path, dataDir, workspaceRoot string, roots []string) (source, relative string) {
	clean := filepath.Clean(strings.TrimSpace(path))
	if strings.TrimSpace(path) == "" {
		return "", ""
	}
	if dataDir != "" {
		if rel, ok := skillPathWithin(clean, filepath.Join(dataDir, "skills")); ok {
			return "global", rel
		}
	}
	if workspaceRoot != "" {
		if rel, ok := skillPathWithin(clean, workspaceRoot); ok {
			return "workspace", rel
		}
	}
	for _, root := range roots {
		if rel, ok := skillPathWithin(clean, root); ok {
			return "global", rel
		}
	}
	// Unknown root: never print the absolute path; the tooltip still has it.
	return "", filepath.Base(filepath.Dir(clean)) + "/" + filepath.Base(clean)
}

// skillPathWithin reports whether path sits below root and returns the slash
// path from root, without matching sibling directories by prefix.
func skillPathWithin(path, root string) (string, bool) {
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// skillRootScope classifies one scanned directory as 工作区 (below the open
// workspace root) or 全局 (configured and data-directory roots).
func skillRootScope(kctx cordis.Context, root string) (zh, en string) {
	clean := filepath.Clean(strings.TrimSpace(root))
	if clean == "." || clean == "" {
		return "全局", "Global"
	}
	if workspaceRoot := skillWorkspaceRoot(kctx); workspaceRoot != "" {
		if clean == workspaceRoot || strings.HasPrefix(clean, workspaceRoot+string(filepath.Separator)) {
			return "工作区", "Workspace"
		}
	}
	return "全局", "Global"
}

// skillWorkspaceRoot returns the open workspace root ("" when none).
func skillWorkspaceRoot(kctx cordis.Context) string {
	workspaceService, ok := kctx.Get(contracts.KeyWorkspace)
	if !ok || workspaceService == nil {
		return ""
	}
	current, open, err := workspaceService.Current(context.Background())
	if err != nil || !open {
		return ""
	}
	root := strings.TrimSpace(current.Root)
	if root == "" {
		return ""
	}
	return filepath.Clean(root)
}

// skillBody returns a skill's Markdown body: the List read model first, then a
// cached lazy Read for stores that only return bodies on demand. The excerpt
// marker tells a truncated List body apart from a short one.
func skillBody(skills contracts.SkillsService, p *skillsState, skill contracts.Skill) (string, string) {
	body := strings.TrimSpace(skill.Body)
	if body != "" && !strings.Contains(body, skillReadLimitMarker) {
		return skill.Body, ""
	}
	key := skillKey(skill)
	if cached, ok := p.bodies[key]; ok {
		return cached, p.bodyErr[key]
	}
	read, err := skills.Read(context.Background(), skill.Name)
	if err != nil {
		p.bodyErr[key] = err.Error()
		return "", p.bodyErr[key]
	}
	p.bodies[key] = read.Body
	return read.Body, ""
}

// skillPreviewSkill resolves the skill the content modal is showing (the skill
// may vanish after a refresh, which closes the modal).
func (p *skillsState) skillPreviewSkill() (contracts.Skill, bool) {
	for _, skill := range p.skills {
		if skill.Name == p.previewName && skill.Path == p.previewPath {
			return skill, true
		}
	}
	return contracts.Skill{}, false
}

// buildSkillPreviewDialog renders the "查看内容" modal: the shared dialog header
// (name + source/relative path), the scrollable Markdown body (read lazily) and
// a single close action as the dialog's default.
func buildSkillPreviewDialog(c *ui.Context, t *design.Tokens, p *skillsState, st *state,
	skills contracts.SkillsService) {
	if !p.previewOpen {
		return
	}
	skill, ok := p.skillPreviewSkill()
	if !ok {
		p.previewOpen = false
		return
	}
	name := textutil.FirstNonEmptyTrimmed(skill.Name, st.tr("(未命名技能)", "(Unnamed skill)"))
	_, dataDir := skillEditor(st.ctx)
	source, relative := skillDisplayPath(skill.Path, dataDir, skillWorkspaceRoot(st.ctx), p.roots)
	subtitle := relative
	if source != "" && relative != "" {
		subtitle = skillSourceLabel(st, source) + " · " + relative
	}
	ui.Modal(c, &p.previewOpen, func() {
		design.Apply(ui.Column(c.Key("skills.preview")), t.Panel()).
			Width(design.DialogWidthL).Padding(design.Space4).Gap(design.Space3).Children(func() {
			uikit.DialogHeader(c, uikit.DialogHeaderOptions{
				Key: "skills.preview", Title: name, Subtitle: subtitle,
				CloseLabel: st.tr("关闭", "Close"),
				OnClose: func() {
					p.previewOpen = false
					c.Invalidate()
				},
			})
			ui.Scroll(c).FillWidth().MaxHeight(design.DialogBodyMax).Children(func() {
				body, bodyErr := skillBody(skills, p, skill)
				switch {
				case bodyErr != "":
					uikit.ErrorLine(c, st.tr("读取技能内容失败：", "Failed to read skill content: ")+bodyErr)
				case strings.TrimSpace(body) == "":
					design.Apply(ui.Text(c, st.tr("技能内容为空", "Skill content is empty")),
						t.TextMuted(design.TypeXS))
				default:
					markdown.View(c, body, nil)
				}
			})
			uikit.DialogFooter(c, []uikit.DialogAction{{
				Label: st.tr("关闭", "Close"), Default: true,
				OnClick: func() {
					p.previewOpen = false
					c.Invalidate()
				},
			}})
		})
	})
}
