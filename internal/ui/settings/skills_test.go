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
		"技能", "从配置目录、dataDir/skills 与工作区 /skills 中发现的 SKILL.md 技能。",
		"/data/skills", "/work/proj/skills",
		"alpha", "Alpha 技能说明", "/data/skills/alpha/SKILL.md",
		"beta", "/data/skills/beta/SKILL.md", "查看内容", "刷新")
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
	h.assertTexts(tester, "gamma", "/g/SKILL.md")
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
		"在工作区 /skills 或 dataDir/skills 放置 <name>/SKILL.md 后点击刷新。",
		"/data/skills")
}
