package session_test

// @-mention tests: the composer suggests files of the current workspace and
// inserting a suggestion writes "@path " into the draft.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/session"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// fakeWorkspace is a contracts.WorkspaceService with one file list. noCurrent
// models the normal "no workspace open" state; Open/Create clear it and record
// the call, so the @ menu's recovery actions are frame-testable.
type fakeWorkspace struct {
	files      []contracts.FileEntry
	search     []contracts.FileEntry
	listErr    error
	searchEr   error
	noCurrent  bool
	workspaces []contracts.Workspace
	opened     []string
	created    []string
}

func (f *fakeWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.workspaces != nil {
		return f.workspaces, nil
	}
	return []contracts.Workspace{{ID: "ws-1", Name: "mygo", Root: "/tmp/mygo"}}, nil
}
func (f *fakeWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	if f.noCurrent {
		return contracts.Workspace{}, false, nil
	}
	return contracts.Workspace{ID: "ws-1", Name: "mygo", Root: "/tmp/mygo"}, true, nil
}
func (f *fakeWorkspace) Create(_ context.Context, name, root string) (contracts.Workspace, error) {
	f.created = append(f.created, root)
	f.noCurrent = false
	return contracts.Workspace{ID: "ws-new", Name: name, Root: root}, nil
}
func (f *fakeWorkspace) Open(_ context.Context, id string) (contracts.Workspace, error) {
	f.opened = append(f.opened, id)
	f.noCurrent = false
	return contracts.Workspace{ID: id, Name: "opened", Root: "/tmp/" + id}, nil
}
func (f *fakeWorkspace) Delete(context.Context, string) error { return nil }
func (f *fakeWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return f.files, f.listErr
}
func (f *fakeWorkspace) ReadFile(context.Context, string, int) ([]byte, error) {
	return nil, nil
}
func (f *fakeWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	if f.searchEr != nil {
		return nil, f.searchEr
	}
	return f.search, nil
}

func newWorkspaceHarness(t *testing.T, chat *fakeChat, workspace contracts.WorkspaceService) *harness {
	t.Helper()
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	ctx := cordis.Root()
	t.Cleanup(func() { _ = ctx.DisposeAll() })
	provide(t, ctx, contracts.KeyUIRuntime, uiRoot.Runtime())
	provide(t, ctx, contracts.KeyNavigator, uiRoot.Navigator())
	provide(t, ctx, contracts.KeyTheme, uiRoot.Theme())
	provide(t, ctx, contracts.KeyLocale, uiRoot.Locale())
	provide(t, ctx, contracts.KeyScreenLister, uiRoot.ScreenLister())
	provide[contracts.ChatService](t, ctx, contracts.KeyChat, chat)
	if workspace != nil {
		provide(t, ctx, contracts.KeyWorkspace, workspace)
	}
	activate(t, ctx, session.Module(), session.Config{})
	return &harness{t: t, ctx: ctx, ui: uiRoot, chat: chat}
}

// focusComposer clicks into the text area above the send button.
func focusMentionComposer(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	sendRect, ok := tester.Find("发送")
	if !ok {
		t.Fatalf("send button missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(sendRect.X-120, sendRect.Y-50)
	tester.Frame()
}

func TestMentionSuggestionInsertsWorkspaceFile(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "提"}, true)
	workspace := &fakeWorkspace{
		search: []contracts.FileEntry{{Name: "README.md", Path: "README.md", Size: 112}},
	}
	h := newWorkspaceHarness(t, chat, workspace)
	tester := h.tester()

	focusMentionComposer(t, h, tester)
	tester.Type("@REA")
	tester.Frame()
	h.assertTexts(tester, "工作区文件", "README.md")

	h.click(tester, "/README.md")
	h.assertNoTexts(tester, "工作区文件")

	h.click(tester, "发送")
	tester.Frame()
	sends := chat.sends()
	if len(sends) != 1 || !strings.Contains(sends[0].prompt, "@README.md") {
		t.Fatalf("Send calls = %+v, want a prompt with @README.md", sends)
	}
}

func TestMentionMenuHiddenWithoutWorkspace(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "无工作区"}, true)
	h := newWorkspaceHarness(t, chat, nil)
	tester := h.tester()
	focusMentionComposer(t, h, tester)
	tester.Type("@")
	tester.Frame()
	h.assertNoTexts(tester, "工作区文件")
}

func TestMentionMenuShowsSearchError(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "错误"}, true)
	workspace := &fakeWorkspace{searchEr: errors.New("disk unavailable")}
	h := newWorkspaceHarness(t, chat, workspace)
	tester := h.tester()
	focusMentionComposer(t, h, tester)
	tester.Type("@x")
	tester.Frame()
	h.assertTexts(tester, "读取工作区失败")
}
