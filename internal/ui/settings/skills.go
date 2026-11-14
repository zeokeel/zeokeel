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
	"github.com/zeokeel/zeokeel/internal/icons"
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
	// list backs the skill cards' official List: rows are keyed by a skill's
	// name+path so filtering and refreshes keep row identity.
	list ui.ListState

	// User-configured extra skill directories (settings key skills.dirs) and
	// the discovery warnings of the last scan (S-02/S-03).
	extraDirs       string
	extraDirsLoaded bool
	extraNotice     string
	extraErr        bool

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
			"",
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
				// Each root names its scope and offers to open it in the file
				// manager; the absolute path stays in the row (mono, ellipsized)
				// and in its tooltip, so "two bare paths" become actionable rows
				// (B19).
				for _, root := range st.roots {
					root := root
					scopeZh, scopeEn := skillRootScope(kctx, root)
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
						design.Apply(ui.Text(c, localized(locale, scopeZh, scopeEn)),
							t.Font(design.TypeXXS)).TextColor(t.LabelTertiary).
							Width(design.Space8 * 2).Shrink(0)
						design.Apply(ui.Text(c, root), t.MonoText(design.TypeXS)).
							TextColor(t.LabelTertiary).Grow(1).Shrink(1).
							MinWidth(design.Space0).MaxLines(1).Ellipsis("…").Tooltip(root)
						openLabel := localized(locale, "打开目录", "Open directory")
						uikit.IconButton(c, uikit.IconButtonOptions{
							Key:     "skills-root/" + root,
							Label:   openLabel + ": " + root,
							Tooltip: openLabel,
							Icon:    icons.FolderOpen(),
							Size:    design.IconSizeS,
							OnClick: func() {
								if err := openPathFunc(root); err != nil {
									st.editErr = localized(locale, "打开目录失败：",
										"Failed to open the directory: ") + err.Error()
									c.Invalidate()
								}
							},
						})
					})
				}
			})
		}

		if warner, ok := skills.(contracts.SkillWarnings); ok {
			if warnings := warner.Warnings(); len(warnings) > 0 {
				uikit.ErrorLine(c, localized(locale, "技能发现警告：", "Skill discovery warnings: ")+
					strings.Join(warnings, "；"))
			}
		}
		buildSkillsExtraDirsSection(c, t, kctx, st, locale)

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
						"在工作区技能目录（/skills、.agents/skills）或 dataDir/skills 放置 <name>/SKILL.md 后点击刷新。",
						"Place <name>/SKILL.md in the workspace skill directories (/skills, .agents/skills) or dataDir/skills, then refresh."),
					nil)
				return
			case len(filtered) == 0:
				uikit.EmptyStateBlock(c,
					localized(locale, "没有匹配的技能", "No matching skills"),
					localized(locale, "换个关键词试试。", "Try a different search term."),
					nil)
				return
			}
			// The skill cards are a collection: the official List keys and names
			// the rows instead of a hand-drawn Column loop.
			st.list.Key = func(index int) any {
				if index < 0 || index >= len(filtered) {
					return nil
				}
				return filtered[index].Name + "\x00" + filtered[index].Path
			}
			st.list.Label = func(index int) string {
				if index < 0 || index >= len(filtered) {
					return ""
				}
				return firstNonEmpty(filtered[index].Name,
					localized(locale, "(未命名技能)", "(Unnamed skill)"))
			}
			ui.List(c, &st.list, len(filtered)).
				Label(localized(locale, "技能列表", "Skills")).
				Gap(design.Space0).
				Rows(func(row ui.ListRow) {
					buildSkillCard(row.Context, t, skills, st, filtered[row.Index], editor, dataDir, currentWorkspaceRoot(kctx), st.roots, locale)
				})
		})
	})
}

// buildSkillCard is one discovered skill as a flat management row: name,
// description, source path and the native collapsible content preview, with the
// edit and destructive delete behind the row's trailing ⋯ menu.
func buildSkillCard(c *ui.Context, t *design.Tokens, skills contracts.SkillsService, st *skillsState,
	skill contracts.Skill, editor contracts.SkillsEditor, dataDir, workspaceRoot string, roots []string,
	locale contracts.LocaleService) {
	name := firstNonEmpty(skill.Name, localized(locale, "(未命名技能)", "(Unnamed skill)"))
	key := name + "\x00" + skill.Path
	// A flat management row, not a card: the list's divider carries the
	// grouping (DESIGN.md §5.2 ListRow), and the row's ⋯ menu owns the edit and
	// the destructive delete so the list paints no red button per skill.
	ui.Column(c).FillWidth().Gap(design.Space2).PaddingY(design.Space3).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, name), t.Font(design.TypeS)).
				FontWeight(design.WeightSemibold).TextColor(t.LabelPrimary).
				Grow(1).Shrink(1).MinWidth(design.Space0).MaxLines(1).Ellipsis("…")
			buildSkillRowMenu(c, t, skills, st, skill, editor, dataDir, locale)
		})
		if description := strings.TrimSpace(skill.Description); description != "" {
			design.Apply(ui.Text(c, description), t.TextMuted(design.TypeXS)).MaxLines(3)
		}
		if path := strings.TrimSpace(skill.Path); path != "" {
			// H20: the row shows a short relative path plus its source and keeps
			// the absolute path in the tooltip instead of dumping it.
			source, relative := skillDisplayPath(path, dataDir, workspaceRoot, roots)
			meta := relative
			if source != "" {
				meta = source + " · " + relative
			}
			design.Apply(ui.Text(c, meta), t.Font(design.TypeXXS), t.MonoFamily()).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…").Tooltip(path)
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
	ui.Divider(c)
}

// buildSkillRowMenu is one skill row's trailing ⋯ menu: Edit (when the editor
// allows it) and the destructive Delete as the danger item.
func buildSkillRowMenu(c *ui.Context, t *design.Tokens, skills contracts.SkillsService, st *skillsState,
	skill contracts.Skill, editor contracts.SkillsEditor, dataDir string, locale contracts.LocaleService) {
	editable := editor != nil && skillsEditActionsAllowed(skill.Path, dataDir)
	if !editable {
		return
	}
	label := localized(locale, "技能操作", "Skill actions")
	menu := uikit.IconButton(c, uikit.IconButtonOptions{
		Key:     "skill-actions/" + skill.Name + "\x00" + skill.Path,
		Label:   label,
		Tooltip: label,
		Icon:    icons.More(),
		Size:    design.IconSizeS,
	})
	menu.PopoverMenuButton(func(m *ui.Menu) {
		if m.Item(localized(locale, "编辑技能", "Edit skill")).Chosen() {
			body, bodyErr := skillBody(skills, st, skill)
			if bodyErr != "" {
				st.editErr = localized(locale, "读取技能内容失败：",
					"Failed to read skill content: ") + bodyErr
				body = ""
			}
			startEditSkill(c, st, skill, body)
		}
		remove := m.Item(localized(locale, "删除技能", "Delete skill"))
		remove.Danger()
		if remove.Chosen() {
			st.pendingDelete = skill.Name
			st.confirmDelete = true
			st.editErr = ""
			c.Invalidate()
		}
	})
}

// skillRootScope classifies one scanned skill directory as 全局 (configured and
// data-directory roots) or 工作区 (the current workspace's /skills), from the
// in-memory workspace registry.
func skillRootScope(kctx cordis.Context, root string) (zh, en string) {
	clean := filepath.Clean(strings.TrimSpace(root))
	if clean == "." || clean == "" {
		return "全局", "global"
	}
	if workspaceRoot := currentWorkspaceRoot(kctx); workspaceRoot != "" {
		if clean == workspaceRoot || strings.HasPrefix(clean, workspaceRoot+string(filepath.Separator)) {
			return "工作区", "workspace"
		}
	}
	return "全局", "global"
}

// currentWorkspaceRoot returns the open workspace root ("" when none).
func currentWorkspaceRoot(kctx cordis.Context) string {
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

// buildSkillsExtraDirsSection renders the user-configurable extra skill
// directories (S-03): the value is stored in skills.dirs and a refresh rescans
// immediately.
func buildSkillsExtraDirsSection(c *ui.Context, t *design.Tokens, kctx cordis.Context,
	st *skillsState, locale contracts.LocaleService) {
	settingsStore, ok := kctx.Get(contracts.KeySettings)
	if !ok || settingsStore == nil {
		return
	}
	if !st.extraDirsLoaded {
		st.extraDirsLoaded = true
		st.extraDirs = settingsStore.Get(contracts.SettingSkillsDirs, "")
	}
	uikit.Section(c, localized(locale, "额外技能目录", "Extra skill directories"), func(*ui.Context) {
		ui.Field(c, localized(locale, "目录列表", "Directory list"), func() {
			ui.TextInput(c, &st.extraDirs).
				Label(inputLabel(locale, localized(locale, "目录列表", "Directory list"))).
				Placeholder(localized(locale, "例如 ~/.zeokeel/skills", "e.g. ~/.zeokeel/skills")).
				FillWidth()
		}).Description(localized(locale,
			"用路径分隔符（Windows 为 ;，其它为 :）或换行分隔；相对路径按数据目录解析。留空表示不额外扫描。",
			"Separate entries with the OS path separator (; on Windows, : elsewhere) or newlines; relative paths resolve against the data directory. Empty means no extra directories."))
		uikit.DialogFooter(c, []uikit.DialogAction{{
			Label: localized(locale, "应用", "Apply"),
			// Not primary: the page's one primary is "新建技能".
			OnClick: func() {
				value := strings.TrimSpace(st.extraDirs)
				if err := settingsStore.Set(contracts.SettingSkillsDirs, value); err != nil {
					st.extraNotice = localized(locale, "保存失败：", "Save failed: ") + err.Error()
					st.extraErr = true
				} else {
					st.extraNotice = localized(locale, "已保存", "Saved")
					st.extraErr = false
					st.refresh()
				}
				c.Invalidate()
			},
		}})
		statusBadgesRow(c, t, st.extraNotice, st.extraErr)
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
func skillDisplayPath(path, dataDir, workspaceRoot string, roots []string) (source, relative string) {
	clean := filepath.Clean(path)
	if dataDir != "" {
		globalRoot := filepath.Join(dataDir, "skills")
		if rel, ok := relativeTo(clean, globalRoot); ok {
			return "global", rel
		}
	}
	if workspaceRoot != "" {
		if rel, ok := relativeTo(clean, workspaceRoot); ok {
			return "workspace", rel
		}
	}
	for _, root := range roots {
		if rel, ok := relativeTo(clean, root); ok {
			return "global", rel
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
	// List returns a bounded excerpt (S-05); read the full file for the preview
	// when the excerpt was truncated.
	if body := strings.TrimSpace(skill.Body); body != "" && !strings.Contains(body, "skill file is larger than the read limit") {
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
