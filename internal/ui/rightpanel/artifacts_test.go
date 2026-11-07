package rightpanel_test

// Tests for the Files pane's artifact history (ArtifactLister): list, expand a
// Markdown preview, collapse again, ordering and the absent/empty states.

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/rightpanel"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeArtifactService is both the presenter and the history lister the host
// provides on contracts.KeyArtifactPresenter.
type fakeArtifactService struct {
	history []contracts.Artifact
}

func (f *fakeArtifactService) SetArtifactHandler(func(contracts.Artifact)) {}

func (f *fakeArtifactService) Present(context.Context, string, string) (string, error) {
	return "artifact-test", nil
}

func (f *fakeArtifactService) Artifacts() []contracts.Artifact {
	return append([]contracts.Artifact(nil), f.history...)
}

// newArtifactsHarness builds the inspector frame with the optional artifact
// presenter/lister (nil means the Files pane has no 产物 section).
func newArtifactsHarness(t *testing.T, chat *fakeChat, cfg rightpanel.Config,
	workspace contracts.WorkspaceService, artifacts contracts.ArtifactPresenter, language string) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel", Language: language})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	if workspace != nil {
		provide[contracts.WorkspaceService](t, ctx, contracts.KeyWorkspace, workspace)
	}
	if artifacts != nil {
		provide[contracts.ArtifactPresenter](t, ctx, contracts.KeyArtifactPresenter, artifacts)
	}
	if _, err := uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "session",
		Title: "会话",
		Build: func(c *ui.Context) {
			uiRoot.Runtime().RenderSlot(c, contracts.SlotSessionPanelRight)
		},
	}); err != nil {
		t.Fatalf("register session screen: %v", err)
	}
	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot, chat: chat}
	h.fiber = activate(t, ctx, rightpanel.Module(), cfg)
	return h
}

func artifactAt(id, title, body string, at time.Time) contracts.Artifact {
	return contracts.Artifact{ID: id, Title: title, Body: body, CreatedAt: at}
}

func artifactTextIndex(texts []string, want string) int {
	for index, text := range texts {
		if text == want {
			return index
		}
	}
	return -1
}

// --- tests ------------------------------------------------------------------

func TestArtifactsSectionHiddenWithoutLister(t *testing.T) {
	h := newArtifactsHarness(t, newFakeChat(), rightpanel.Config{}, nil, nil, "zh")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "文件")
	h.assertNoTexts(tester, "产物", "还没有产物")
}

func TestArtifactsEmptyState(t *testing.T) {
	h := newArtifactsHarness(t, newFakeChat(), rightpanel.Config{}, nil, &fakeArtifactService{}, "zh")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "文件")
	h.assertTexts(tester, "产物", "还没有产物")
}

func TestArtifactsListNewestFirst(t *testing.T) {
	base := time.Unix(1700000000, 0)
	history := []contracts.Artifact{
		artifactAt("a-old", "旧工件", "旧内容", base),
		artifactAt("a-new", "新工件", "新内容", base.Add(5*time.Minute)),
	}
	h := newArtifactsHarness(t, newFakeChat(), rightpanel.Config{}, nil, &fakeArtifactService{history: history}, "zh")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "文件")

	h.assertTexts(tester, "产物", "新工件", "旧工件", "预览")
	newest := artifactTextIndex(tester.Texts(), "新工件")
	oldest := artifactTextIndex(tester.Texts(), "旧工件")
	if !(newest >= 0 && oldest >= 0 && newest < oldest) {
		t.Fatalf("artifact order = newest %d, oldest %d; want newest first", newest, oldest)
	}
}

func TestArtifactPreviewExpandsAndCollapses(t *testing.T) {
	base := time.Unix(1700000000, 0)
	history := []contracts.Artifact{
		artifactAt("a-old", "旧工件", "旧的正文", base),
		artifactAt("a-new", "新工件", "# 标题\n\n工件的正文内容", base.Add(time.Minute)),
	}
	h := newArtifactsHarness(t, newFakeChat(), rightpanel.Config{}, nil, &fakeArtifactService{history: history}, "zh")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "文件")
	h.assertNoTexts(tester, "工件的正文内容")

	h.click(tester, "预览")
	h.assertTexts(tester, "工件的正文内容", "收起")
	h.click(tester, "收起")
	h.assertNoTexts(tester, "工件的正文内容")
}

func TestArtifactsMaxTenRows(t *testing.T) {
	base := time.Unix(1700000000, 0)
	history := make([]contracts.Artifact, 0, 12)
	for index := 0; index < 12; index++ {
		history = append(history, artifactAt(
			"a-"+string(rune('a'+index)),
			"工件-"+string(rune('a'+index)),
			"正文",
			base.Add(time.Duration(index)*time.Minute),
		))
	}
	h := newArtifactsHarness(t, newFakeChat(), rightpanel.Config{}, nil, &fakeArtifactService{history: history}, "zh")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "文件")

	// Only the ten newest artifacts are listed.
	h.assertTexts(tester, "工件-l", "工件-c")
	h.assertNoTexts(tester, "工件-a", "工件-b")
}

func TestArtifactsEnglishCopy(t *testing.T) {
	base := time.Unix(1700000000, 0)
	history := []contracts.Artifact{
		artifactAt("a1", "Report", "Artifact body", base),
	}
	empty := newArtifactsHarness(t, newFakeChat(), rightpanel.Config{}, nil, &fakeArtifactService{}, "en")
	tester := empty.tester()
	tester.Frame()
	empty.click(tester, "Files")
	empty.assertTexts(tester, "Artifacts", "No artifacts yet")

	withHistory := newArtifactsHarness(t, newFakeChat(), rightpanel.Config{}, nil,
		&fakeArtifactService{history: history}, "en")
	tester2 := withHistory.tester()
	tester2.Frame()
	withHistory.click(tester2, "Files")
	withHistory.assertTexts(tester2, "Artifacts", "Report", "Preview")
	withHistory.click(tester2, "Preview")
	withHistory.assertTexts(tester2, "Artifact body", "Hide")
}
