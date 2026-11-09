package settings

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// skillsState is the skills page's UI state: the cached List result, the lazy
// per-skill body cache and the per-skill "查看内容" fold state.
type skillsState struct {
	loaded  bool
	skills  []contracts.Skill
	listErr error
	roots   []string

	bodies  map[string]string
	bodyErr map[string]string
	open    map[string]bool

	// query filters the skill list by name, description or path (H20).
	query string

	// Editor half (skills_edit.go): the create/edit modal drafts, the success
	// badge and the delete confirmation.
	creating         bool
	draftName        string
	draftDescription string
	draftBody        string
	editing          bool
	editName         string
	editDescription  string
	editBody         string
	editStatus       string
	editErr          string
	confirmDelete    bool
	pendingDelete    string
}

func newSkillsState() *skillsState {
	return &skillsState{
		bodies:  make(map[string]string),
		bodyErr: make(map[string]string),
		open:    make(map[string]bool),
	}
}

// refresh invalidates the cached list and bodies; the next frame re-reads.
func (s *skillsState) refresh() {
	s.loaded = false
	s.skills = nil
	s.listErr = nil
	s.bodies = make(map[string]string)
	s.bodyErr = make(map[string]string)
}

// buildSkillsPage renders the skills page: header with the page actions, the
// scanned roots, one card per skill (a skill is an independent object, so cards
// are the right container here) and its native collapsible Markdown content.
func buildSkillsPage(c *ui.Context, t *design.Tokens, kctx cordis.Context, st *skillsState,
	locale contracts.LocaleService) {
	skills, hasSkills := kctx.Get(contracts.KeySkills)
	if !hasSkills || skills == nil {
		uikit.PageHeader(c, localized(locale, "技能", "Skills"), "")
		uikit.EmptyState(c, localized(locale, "技能后端未启用", "Skills backend is not enabled"))
		return
	}
	if !st.loaded {
		st.loaded = true
		st.skills, st.listErr = skills.List(context.Background())
		st.roots = skills.Roots()
	}
	editor, dataDir := skillsEditor(kctx)

	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		// The page's immediate-effect actions live at the page header's right
		// edge (uikit.PageHeaderAction); the shell's footer owns explicit saves.
		uikit.PageHeaderAction(c,
			localized(locale, "技能", "Skills"),
			localized(locale,
				"从配置目录、dataDir/skills 与工作区 /skills 中发现的 SKILL.md 技能。",
				"SKILL.md skills discovered in the configured directories, dataDir/skills and the workspace /skills."),
			func(c *ui.Context) {
				if editor != nil {
					if design.Apply(ui.PrimaryButton(c, localized(locale, "新建技能", "New skill")),
						t.PrimaryButton()).Clicked() {
						startCreateSkill(c, st)
					}
				}
				if design.Apply(ui.Button(c, localized(locale, "刷新", "Refresh")),
					t.SecondaryButton()).Tooltip(localized(locale,
					"重新扫描技能目录", "Rescan the skill directories")).Clicked() {
					st.refresh()
					c.Invalidate()
				}
			})

		buildSkillsEditorDialogs(c, t, st, editor, locale)

		if st.editStatus != "" {
			statusBadgesRow(c, t, st.editStatus, false)
		}
		if st.editErr != "" && !st.creating && !st.editing {
			// Delete failures come back from the native alert dialog, which has
			// no error slot; show them in the page's flow instead.
			uikit.ErrorLine(c, st.editErr)
		}

		if len(st.roots) > 0 {
			uikit.Section(c, localized(locale, "扫描目录", "Scanned directories"), func(*ui.Context) {
				for _, root := range st.roots {
					design.Apply(ui.Text(c, root), t.Font(design.TypeXXXS), t.MonoFamily()).
						TextColor(t.LabelTertiary).MaxLines(2)
				}
			})
		}

		if st.listErr != nil {
			uikit.ErrorLine(c, localized(locale, "读取技能失败：",
				"Failed to read skills: ")+st.listErr.Error())
			return
		}
		filtered := filterSkills(st.skills, st.query)
		uikit.Section(c, localized(locale, "技能列表", "Skills"), func(*ui.Context) {
			ui.SearchField(c, &st.query).
				Label(localized(locale, "搜索技能", "Search skills")).
				Placeholder(localized(locale, "按名称、说明或路径搜索…",
					"Search by name, description or path…")).FillWidth()
			if count := len(filtered); len(st.skills) > 0 {
				design.Apply(ui.Text(c, fmt.Sprintf(localized(locale,
					"%d / %d 个技能", "%d of %d skills"), count, len(st.skills))),
					t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
			}
			switch {
			case len(st.skills) == 0:
				uikit.EmptyStateBlock(c,
					localized(locale, "还没有发现技能", "No skills discovered yet"),
					localized(locale,
						"在工作区 /skills 或 dataDir/skills 放置 <name>/SKILL.md 后点击刷新。",
						"Place <name>/SKILL.md in the workspace /skills or dataDir/skills, then refresh."),
					nil)
				return
			case len(filtered) == 0:
				uikit.EmptyStateBlock(c,
					localized(locale, "没有匹配的技能", "No matching skills"),
					localized(locale, "换个关键词试试。", "Try a different search term."),
					nil)
				return
			}
			for _, skill := range filtered {
				buildSkillCard(c, t, skills, st, skill, editor, dataDir, st.roots, locale)
			}
		})
	})
}

// buildSkillCard is one discovered skill: name, description, path and the
// native collapsible content preview.
func buildSkillCard(c *ui.Context, t *design.Tokens, skills contracts.SkillsService, st *skillsState,
	skill contracts.Skill, editor contracts.SkillsEditor, dataDir string, roots []string,
	locale contracts.LocaleService) {
	name := firstNonEmpty(skill.Name, localized(locale, "(未命名技能)", "(Unnamed skill)"))
	key := name + "\x00" + skill.Path
	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		design.Apply(ui.Text(c, name), t.Font(design.TypeS)).
			FontWeight(600).TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
		if description := strings.TrimSpace(skill.Description); description != "" {
			design.Apply(ui.Text(c, description), t.TextMuted(design.TypeXS)).MaxLines(3)
		}
		if path := strings.TrimSpace(skill.Path); path != "" {
			// H20: the card shows a short relative path plus its source and
			// keeps the absolute path in the tooltip instead of dumping it.
			source, relative := skillDisplayPath(path, dataDir, roots)
			meta := relative
			if source != "" {
				meta = source + " · " + relative
			}
			design.Apply(ui.Text(c, meta), t.Font(design.TypeXXXS), t.MonoFamily()).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…").Tooltip(path)
		}
		if editor != nil && skillsEditActionsAllowed(skill.Path, dataDir) {
			ui.Row(c).FillWidth().Justify(ui.End).Gap(design.Space2).Children(func() {
				if design.Apply(ui.Button(c, localized(locale, "编辑", "Edit")),
					t.SecondaryButton()).Tooltip(localized(locale,
					"编辑这个技能", "Edit this skill")).Clicked() {
					body, bodyErr := skillBody(skills, st, skill)
					if bodyErr != "" {
						st.editErr = localized(locale, "读取技能内容失败：",
							"Failed to read skill content: ") + bodyErr
						body = ""
					}
					startEditSkill(c, st, skill, body)
				}
				if design.Apply(ui.Button(c, localized(locale, "删除", "Delete")),
					t.DangerButton()).Tooltip(localized(locale,
					"删除这个技能", "Delete this skill")).Clicked() {
					st.pendingDelete = skill.Name
					st.confirmDelete = true
					st.editErr = ""
					c.Invalidate()
				}
			})
		}
		open := st.open[key]
		ui.Collapsible(c, localized(locale, "查看内容", "View content"), &open, func() {
			body, bodyErr := skillBody(skills, st, skill)
			if bodyErr != "" {
				uikit.ErrorLine(c, localized(locale,
					"读取技能内容失败：", "Failed to read skill content: ")+bodyErr)
				return
			}
			if strings.TrimSpace(body) == "" {
				design.Apply(ui.Text(c, localized(locale, "技能内容为空", "Skill content is empty")),
					t.TextMuted(design.TypeXXS))
				return
			}
			ui.Scroll(c).FillWidth().MaxHeight(design.Space6 * 10).Children(func() {
				markdown.View(c, body, nil)
			})
		})
		st.open[key] = open
	})
}

// filterSkills narrows the list by the page's search query (case-insensitive
// over name, description and path).
func filterSkills(skills []contracts.Skill, query string) []contracts.Skill {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return skills
	}
	filtered := make([]contracts.Skill, 0, len(skills))
	for _, skill := range skills {
		haystack := strings.ToLower(skill.Name + "\x00" + skill.Description + "\x00" + skill.Path)
		if strings.Contains(haystack, query) {
			filtered = append(filtered, skill)
		}
	}
	return filtered
}

// skillDisplayPath classifies a skill by its scanned root and returns the
// localized source label plus a path relative to that root: dataDir skills are
// global, workspace roots are the project's. The absolute path stays available
// as the card's tooltip.
func skillDisplayPath(path, dataDir string, roots []string) (source, relative string) {
	clean := filepath.Clean(path)
	if dataDir != "" {
		globalRoot := filepath.Join(dataDir, "skills")
		if rel, ok := relativeTo(clean, globalRoot); ok {
			return "global", rel
		}
	}
	for _, root := range roots {
		if rel, ok := relativeTo(clean, root); ok {
			return "workspace", rel
		}
	}
	// Unknown root: never print the absolute path; the tooltip still has it.
	return "", filepath.Base(filepath.Dir(clean)) + "/" + filepath.Base(clean)
}

// relativeTo reports whether path sits below root and returns the slash path
// from root, without matching sibling directories by prefix.
func relativeTo(path, root string) (string, bool) {
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// skillBody returns a skill's Markdown body: the List read model first, then a
// cached lazy Read for stores that only return bodies on demand.
func skillBody(skills contracts.SkillsService, st *skillsState, skill contracts.Skill) (string, string) {
	if body := strings.TrimSpace(skill.Body); body != "" {
		return skill.Body, ""
	}
	key := skill.Name + "\x00" + skill.Path
	if body, ok := st.bodies[key]; ok {
		return body, st.bodyErr[key]
	}
	read, err := skills.Read(context.Background(), skill.Name)
	if err != nil {
		st.bodyErr[key] = err.Error()
		return "", st.bodyErr[key]
	}
	st.bodies[key] = read.Body
	return read.Body, ""
}
