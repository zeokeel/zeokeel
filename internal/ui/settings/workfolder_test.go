package settings_test

// Frame tests for the single "Work folder / 工作文件夹" row of the Behavior
// page: it shows the current root, its 更改… / Change… action runs the native
// directory picker and makes the chosen folder current, and a cancelled picker
// changes nothing. The product has no multi-workspace list, so the row is the
// only folder control here.

import (
	"context"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// workFolderFake is the workspace registry seam: Create registers the root and
// makes it current (the backend's contract), Current reads it back.
type workFolderFake struct {
	current *contracts.Workspace
	created []string
}

func (f *workFolderFake) List(context.Context) ([]contracts.Workspace, error) {
	if f.current == nil {
		return nil, nil
	}
	return []contracts.Workspace{*f.current}, nil
}

func (f *workFolderFake) Current(context.Context) (contracts.Workspace, bool, error) {
	if f.current == nil {
		return contracts.Workspace{}, false, nil
	}
	return *f.current, true, nil
}

func (f *workFolderFake) Create(_ context.Context, _ string, root string) (contracts.Workspace, error) {
	created := contracts.Workspace{ID: "ws-work", Name: "work", Root: root}
	f.current = &created
	f.created = append(f.created, root)
	return created, nil
}

func (f *workFolderFake) Open(_ context.Context, id string) (contracts.Workspace, error) {
	if f.current == nil || f.current.ID != id {
		return contracts.Workspace{}, context.Canceled
	}
	return *f.current, nil
}

func (f *workFolderFake) Delete(context.Context, string) error { return nil }

func (f *workFolderFake) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (f *workFolderFake) ReadFile(context.Context, string, int) ([]byte, error) { return nil, nil }

func (f *workFolderFake) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

// newWorkFolderHarness is newHarness with the workspace backend provided before
// the settings module activates, so the Behavior page renders its row.
func newWorkFolderHarness(t *testing.T, workspace contracts.WorkspaceService, language string) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel", Language: language})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })

	store := newFakeSettings(nil)
	chat := &fakeChat{}
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide(t, ctx, contracts.KeySettings, contracts.SettingsService(store))
	provide(t, ctx, contracts.KeyChat, contracts.ChatService(chat))
	provide(t, ctx, contracts.KeyWorkspace, workspace)

	h := &harness{t: t, ctx: ctx, uiRoot: uiRoot, settings: store, chat: chat}
	h.fiber = activate(t, ctx, settings.Module(), settings.Config{})
	uiRoot.Navigator().ToggleSettings()
	return h
}

// TestBehaviorWorkFolderShowsCurrentRootAndChanges covers the daily path: the
// row names the open folder, 更改… runs the picker, and the chosen folder is
// registered, becomes current and is announced in the page status.
func TestBehaviorWorkFolderShowsCurrentRootAndChanges(t *testing.T) {
	workspace := &workFolderFake{current: &contracts.Workspace{ID: "ws-old", Name: "old", Root: "/work/old"}}
	h := newWorkFolderHarness(t, workspace, "zh")

	previous := settings.WorkFolderDirectoryPicker
	t.Cleanup(func() { settings.WorkFolderDirectoryPicker = previous })
	chosen := "/work/new"
	settings.WorkFolderDirectoryPicker = func(string) (string, error) { return chosen, nil }

	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为与限额")
	h.assertTexts(tester, "工作文件夹", "/work/old", "更改…")

	h.click(tester, "更改…")
	h.assertTexts(tester, "工作文件夹已更改", chosen)
	if len(workspace.created) != 1 || workspace.created[0] != chosen {
		t.Fatalf("created = %v, want [%q]", workspace.created, chosen)
	}
	if workspace.current == nil || workspace.current.Root != chosen {
		t.Fatalf("current = %+v, want the chosen folder", workspace.current)
	}
}

// TestBehaviorWorkFolderCancelledPickerChangesNothing covers the cancelled
// native dialog: no registration, no status line, the shown root unchanged.
func TestBehaviorWorkFolderCancelledPickerChangesNothing(t *testing.T) {
	workspace := &workFolderFake{current: &contracts.Workspace{ID: "ws-old", Name: "old", Root: "/work/old"}}
	h := newWorkFolderHarness(t, workspace, "zh")

	previous := settings.WorkFolderDirectoryPicker
	t.Cleanup(func() { settings.WorkFolderDirectoryPicker = previous })
	settings.WorkFolderDirectoryPicker = func(string) (string, error) { return "", nil }

	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为与限额")
	h.click(tester, "更改…")
	h.assertTexts(tester, "/work/old")
	h.assertNoTexts(tester, "工作文件夹已更改")
	if len(workspace.created) != 0 {
		t.Fatalf("created = %v, want none after a cancelled picker", workspace.created)
	}
}

// TestBehaviorWorkFolderEnglish covers the English copy of the same row.
func TestBehaviorWorkFolderEnglish(t *testing.T) {
	workspace := &workFolderFake{current: &contracts.Workspace{ID: "ws-old", Name: "old", Root: "/work/old"}}
	h := newWorkFolderHarness(t, workspace, "en")

	previous := settings.WorkFolderDirectoryPicker
	t.Cleanup(func() { settings.WorkFolderDirectoryPicker = previous })
	settings.WorkFolderDirectoryPicker = func(string) (string, error) { return "", nil }

	tester := h.tester()
	tester.Frame()
	h.click(tester, "Behavior & limits")
	h.assertTexts(tester, "Work folder", "/work/old", "Change…")
}
