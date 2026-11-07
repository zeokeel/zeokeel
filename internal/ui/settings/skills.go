package settings

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
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

// buildSkillsPage renders the skills page: heading, description, scanned roots,
// one panel card per skill and its native collapsible Markdown content.
func buildSkillsPage(c *ui.Context, t *design.Tokens, kctx cordis.Context, st *skillsState) {
	skills, hasSkills := kctx.Get(contracts.KeySkills)
	if !hasSkills || skills == nil {
		buildSkillsEmpty(c, t, "技能后端未启用")
		return
	}
	if !st.loaded {
		st.loaded = true
		st.skills, st.listErr = skills.List(context.Background())
		st.roots = skills.Roots()
	}

	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		// Heading row with the native refresh action.
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, SkillsPageTitle), t.Font(design.TypeL)).
				FontWeight(600).TextColor(t.LabelPrimary)
			ui.Spacer(c).Grow(1)
			refresh := ui.Button(c, "刷新").Tooltip("重新扫描技能目录")
			if refresh.Clicked() {
				st.refresh()
				c.Invalidate()
			}
		})
		design.Apply(ui.Text(c,
			"从配置目录、dataDir/skills 与工作区 /skills 中发现的 SKILL.md 技能。"),
			t.TextMuted(design.TypeXS))

		if len(st.roots) > 0 {
			ui.Column(c).FillWidth().Gap(2).Children(func() {
				design.Apply(ui.Text(c, "扫描目录"), t.Font(design.TypeXXS)).
					TextColor(t.LabelTertiary)
				for _, root := range st.roots {
					design.Apply(ui.Text(c, root), t.MonoFamily()).FontSize(11).
						TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
				}
			})
		}

		if st.listErr != nil {
			buildSkillsErrorLine(c, t, "读取技能失败："+st.listErr.Error())
			return
		}
		if len(st.skills) == 0 {
			buildSkillsEmpty(c, t, "还没有发现技能")
			design.Apply(ui.Text(c,
				"在工作区 /skills 或 dataDir/skills 放置 <name>/SKILL.md 后点击刷新。"),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
			return
		}
		for _, skill := range st.skills {
			buildSkillCard(c, t, skills, st, skill)
		}
	})
}

// buildSkillCard is one discovered skill: name, description, path and the
// native collapsible content preview.
func buildSkillCard(c *ui.Context, t *design.Tokens, skills contracts.SkillsService, st *skillsState, skill contracts.Skill) {
	name := firstNonEmpty(skill.Name, "(未命名技能)")
	key := name + "\x00" + skill.Path
	card := design.Apply(ui.Column(c), t.Panel()).Padding(design.Space3).Gap(design.Space2)
	card.Children(func() {
		design.Apply(ui.Text(c, name), t.Font(design.TypeS)).
			FontWeight(600).TextColor(t.LabelPrimary).MaxLines(1).Ellipsis("…")
		if description := strings.TrimSpace(skill.Description); description != "" {
			design.Apply(ui.Text(c, description), t.TextMuted(design.TypeXS)).MaxLines(3)
		}
		if path := strings.TrimSpace(skill.Path); path != "" {
			design.Apply(ui.Text(c, path), t.MonoFamily()).FontSize(11).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
		}
		open := st.open[key]
		ui.Collapsible(c, "查看内容", &open, func() {
			body, bodyErr := skillBody(skills, st, skill)
			if bodyErr != "" {
				buildSkillsErrorLine(c, t, "读取技能内容失败："+bodyErr)
				return
			}
			if strings.TrimSpace(body) == "" {
				design.Apply(ui.Text(c, "技能内容为空"), t.TextMuted(design.TypeXXS))
				return
			}
			ui.Scroll(c).FillWidth().MaxHeight(design.Space6 * 10).Children(func() {
				markdown.View(c, body, nil)
			})
		})
		st.open[key] = open
	})
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

// buildSkillsEmpty is the centered empty state of the skills page.
func buildSkillsEmpty(c *ui.Context, t *design.Tokens, text string) {
	th := t.Theme()
	ui.Column(c).FillWidth().Gap(design.Space2).PaddingY(design.Space6).Children(func() {
		ui.Row(c).Size(th.Space(9), th.Space(9)).Center().
			Radius(design.RadiusMax).Background(t.BgHover).Children(func() {
			ui.Icon(c, icons.Sparkles()).Size(th.Space(4), th.Space(4)).
				TextColor(t.LabelSecondary)
		})
		design.Apply(ui.Text(c, text), t.Font(design.TypeXS)).
			FontWeight(600).TextColor(t.LabelPrimary)
	})
}

// buildSkillsErrorLine is the page's danger row.
func buildSkillsErrorLine(c *ui.Context, t *design.Tokens, text string) {
	th := t.Theme()
	ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Start).Children(func() {
		ui.Icon(c, icons.AlertTriangle()).Size(th.Space(4), th.Space(4)).
			TextColor(t.Danger)
		design.Apply(ui.Text(c, text), t.Font(design.TypeXXS)).
			TextColor(t.DangerText)
	})
}
