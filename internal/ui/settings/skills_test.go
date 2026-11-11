package settings_test

// Tests for the settings Skills page: the optional skills backend, the scanned
// roots, the skill cards, the lazy Markdown content preview, refresh and the
// error/empty states.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// fakeSkills is a scripted contracts.SkillsService.
type fakeSkills struct {
	mu sync.Mutex

	list    []contracts.Skill
	roots   []string
	bodies  map[string]string
	listErr error
	readErr error

	listCalls int
	readCalls []string

	warnings []string
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

func (f *fakeSkills) Warnings() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.warnings...)
}

func (f *fakeSkills) Roots() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.roots...)
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

func skillPageHarness(t *testing.T, skills contracts.SkillsService) *harness {
	t.Helper()
	h := newHarnessWithSkills(t, &fakeChat{}, newFakeSettings(nil), settings.Config{}, skills)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "技能")
	return h
}

// --- tests ------------------------------------------------------------------

func TestSkillsPageWithoutBackend(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "技能")
	h.assertTexts(tester, "技能后端未启用")
}

func TestSkillsPageRendersSkills(t *testing.T) {
	skills := newFakeSkills()
	skills.roots = []string{"/data/skills", "/work/proj/skills"}
	skills.setList(
		contracts.Skill{
			Name:        "alpha",
			Description: "Alpha 技能说明",
			Path:        "/data/skills/alpha/SKILL.md",
			Body:        "# Alpha 正文\n\n这是 alpha 的说明。",
		},
		contracts.Skill{Name: "beta", Path: "/data/skills/beta/SKILL.md"},
	)
	h := skillPageHarness(t, skills)
	tester := h.tester()

	h.assertTexts(tester,
		"技能", "从工作区技能目录（/skills、.agents/skills、.zeokeel/skills）、配置目录与 dataDir/skills 中发现的 SKILL.md 技能；工作区优先。",
		"/data/skills", "/work/proj/skills", "额外技能目录", "应用",
		"alpha", "Alpha 技能说明", "global · alpha/SKILL.md",
		"beta", "global · beta/SKILL.md", "查看内容", "刷新")
	// H20: the card never dumps the absolute path (it lives in the tooltip).
	h.assertNoTexts(tester, "/data/skills/alpha/SKILL.md", "/data/skills/beta/SKILL.md")
	h.assertNoTexts(tester, "还没有发现技能", "技能后端未启用")
}

func TestSkillsPageExpandsMarkdownBody(t *testing.T) {
	skills := newFakeSkills()
	skills.setList(contracts.Skill{
		Name: "alpha", Path: "/data/skills/alpha/SKILL.md",
		Body: "# Alpha 正文\n\n- 步骤一\n- 步骤二",
	})
	h := skillPageHarness(t, skills)
	tester := h.tester()
	h.assertTexts(tester, "查看内容")
	h.assertNoTexts(tester, "Alpha 正文")

	if err := tester.Click("查看内容"); err != nil {
		t.Fatalf("expand skill content: %v; texts = %v", err, tester.Texts())
	}
	tester.Frame()
	frame := strings.Join(tester.Texts(), "\n")
	for _, want := range []string{"Alpha 正文", "步骤一", "步骤二"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("expanded Markdown content missing %q; texts = %v", want, tester.Texts())
		}
	}

	if err := tester.Click("查看内容"); err != nil {
		t.Fatalf("collapse skill content: %v", err)
	}
	tester.Frame()
	h.assertNoTexts(tester, "Alpha 正文")
}

func TestSkillsPageLazyReadsBody(t *testing.T) {
	skills := newFakeSkills()
	skills.setList(contracts.Skill{Name: "lazy", Path: "/data/skills/lazy/SKILL.md"})
	skills.bodies["lazy"] = "# 懒加载正文\n\n只有展开时才读取。"
	h := skillPageHarness(t, skills)
	tester := h.tester()

	if got := skills.reads(); len(got) != 0 {
		t.Fatalf("Read calls before expanding = %v, want none", got)
	}
	if err := tester.Click("查看内容"); err != nil {
		t.Fatalf("expand skill: %v", err)
	}
	tester.Frame()
	frame := strings.Join(tester.Texts(), "\n")
	if !strings.Contains(frame, "懒加载正文") {
		t.Fatalf("lazy body missing after expand; texts = %v", tester.Texts())
	}
	if got := skills.reads(); len(got) != 1 || got[0] != "lazy" {
		t.Fatalf("Read calls = %v, want [lazy]", got)
	}
}

func TestSkillsPageRefreshReloads(t *testing.T) {
	skills := newFakeSkills()
	skills.setList(contracts.Skill{Name: "alpha", Path: "/a/SKILL.md", Body: "# Alpha"})
	h := skillPageHarness(t, skills)
	tester := h.tester()
	if calls := skills.listCallCount(); calls != 1 {
		t.Fatalf("List calls = %d after opening, want 1", calls)
	}
	tester.Frame()
	if calls := skills.listCallCount(); calls != 1 {
		t.Fatalf("List calls = %d after an extra frame, want the cache", calls)
	}

	skills.setList(contracts.Skill{Name: "gamma", Path: "/g/SKILL.md", Body: "# Gamma"})
	h.click(tester, "刷新")
	if calls := skills.listCallCount(); calls != 2 {
		t.Fatalf("List calls = %d after refresh, want 2", calls)
	}
	h.assertTexts(tester, "gamma", "g/SKILL.md")
	h.assertNoTexts(tester, "alpha")
}

func TestSkillsPageErrors(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		skills := newFakeSkills()
		skills.listErr = errors.New("scan failed")
		h := skillPageHarness(t, skills)
		tester := h.tester()
		h.assertTexts(tester, "读取技能失败：scan failed")
	})

	t.Run("read", func(t *testing.T) {
		skills := newFakeSkills()
		skills.setList(contracts.Skill{Name: "broken", Path: "/b/SKILL.md"})
		skills.readErr = errors.New("read failed")
		h := skillPageHarness(t, skills)
		tester := h.tester()
		if err := tester.Click("查看内容"); err != nil {
			t.Fatalf("expand skill: %v", err)
		}
		tester.Frame()
		h.assertTexts(tester, "读取技能内容失败：read failed")
	})
}

func TestSkillsPageEmptyState(t *testing.T) {
	skills := newFakeSkills()
	skills.roots = []string{"/data/skills"}
	h := skillPageHarness(t, skills)
	tester := h.tester()
	h.assertTexts(tester,
		"还没有发现技能",
		"在工作区技能目录（/skills、.agents/skills）或 dataDir/skills 放置 <name>/SKILL.md 后点击刷新。",
		"/data/skills")
}

// TestSkillsPageSearchFilters covers H20's search box: it narrows the cards by
// name, description or path and shows a no-match empty state.
func TestSkillsPageSearchFilters(t *testing.T) {
	skills := newFakeSkills()
	skills.roots = []string{"/data/skills"}
	skills.setList(
		contracts.Skill{Name: "alpha", Description: "代码审查清单", Path: "/data/skills/alpha/SKILL.md"},
		contracts.Skill{Name: "beta", Description: "发布检查", Path: "/data/skills/beta/SKILL.md"},
	)
	h := skillPageHarness(t, skills)
	tester := h.tester()

	h.focusInput(tester, "搜索技能")
	tester.Type("发布")
	tester.Frame()
	h.assertTexts(tester, "beta", "1 / 2 个技能")
	h.assertNoTexts(tester, "alpha")
	h.focusInput(tester, "搜索技能")
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(0, ui.KeyBackspace)
	tester.Type("zzz")
	tester.Frame()
	h.assertTexts(tester, "没有匹配的技能")
	h.assertNoTexts(tester, "alpha", "beta")
}

// --- S-02 / S-03 / S-05 (Wave-1 fix regressions) ----------------------------

func TestSkillsPageShowsDiscoveryWarnings(t *testing.T) {
	skills := newFakeSkills()
	skills.setList(contracts.Skill{Name: "alpha", Path: "/data/skills/alpha/SKILL.md"})
	skills.warnings = []string{`skill "alpha" from /work/proj/skills is shadowed by a higher-precedence root`}
	h := skillPageHarness(t, skills)
	tester := h.tester()
	h.assertTexts(tester, "技能发现警告：", "shadowed by a higher-precedence root")
}

func TestSkillsPageSavesExtraSkillDirectories(t *testing.T) {
	skills := newFakeSkills()
	settingsStore := newFakeSettings(nil)
	h := newHarnessWithSkills(t, &fakeChat{}, settingsStore, settings.Config{}, skills)
	tester := h.tester()
	tester.Frame()
	h.click(tester, "技能")
	if calls := skills.listCallCount(); calls != 1 {
		t.Fatalf("List calls = %d after open, want 1", calls)
	}

	h.focusInput(tester, "目录列表 输入")
	tester.Type("/home/me/skills-extra")
	tester.Frame()
	h.click(tester, "应用")

	if got := settingsStore.Get(contracts.SettingSkillsDirs, ""); got != "/home/me/skills-extra" {
		t.Fatalf("skills.dirs = %q, want the typed directory list", got)
	}
	if calls := skills.listCallCount(); calls != 2 {
		t.Fatalf("List calls = %d after applying, want a rescan (2)", calls)
	}
}

func TestSkillsPageReadsFullBodyWhenListExcerpted(t *testing.T) {
	skills := newFakeSkills()
	skills.setList(contracts.Skill{
		Name: "big", Path: "/data/skills/big/SKILL.md",
		Body: "前情提要\n[truncated: skill file is larger than the read limit]\n",
	})
	skills.bodies["big"] = "前情提要\n\n## 完整正文\n\nTAIL-MARKER"
	h := skillPageHarness(t, skills)
	tester := h.tester()
	if err := tester.Click("查看内容"); err != nil {
		t.Fatalf("expand: %v", err)
	}
	tester.Frame()
	frame := strings.Join(tester.Texts(), "\n")
	if !strings.Contains(frame, "TAIL-MARKER") {
		t.Fatalf("preview did not read the full file; texts = %v", tester.Texts())
	}
	if got := skills.reads(); len(got) != 1 || got[0] != "big" {
		t.Fatalf("Read calls = %v, want [big]", got)
	}
}
