package settings

// Internal frame test for the Search page's degraded state: the page is built
// without a settings store and must render an explicit empty state instead of
// failing. This cannot go through the module (it requires KeySettings), so the
// builder is rendered directly from a small test screen.

import (
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

func TestSearchPageWithoutSettingsStore(t *testing.T) {
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	bareCtx := cordis.Root()
	t.Cleanup(func() { _ = bareCtx.DisposeAll() })

	// A screen renders the page builder with a kernel context that has no
	// settings store; the host runtime supplies the theme/locale.
	state := newSearchState()
	if _, err := uiRoot.Runtime().RegisterScreen(contracts.ScreenSpec{
		ID:    "test.search",
		Title: "search",
		Build: func(c *ui.Context) {
			buildSearchPage(c, bareCtx, state, uiRoot.Locale())
		},
	}); err != nil {
		t.Fatalf("register test screen: %v", err)
	}
	tester := ui.NewTester(uiRoot.View, 1200, 800)
	tester.Frame()

	if !tester.HasText("设置存储未启用") {
		t.Fatalf("frame does not show the degraded empty state; texts = %v", tester.Texts())
	}
	if !tester.HasText("没有可用的设置存储") {
		t.Fatalf("frame does not show the degraded explanation; texts = %v", tester.Texts())
	}

	uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	if !tester.HasText("Settings store not enabled") {
		t.Fatalf("frame does not show the English degraded empty state; texts = %v", tester.Texts())
	}
}
