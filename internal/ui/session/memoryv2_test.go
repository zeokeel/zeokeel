package session_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeMemoryService struct {
	items  []contracts.MemoryItem
	writes []contracts.MemoryDraft
}

func (f *fakeMemoryService) List(context.Context, string, string, []string, int) ([]contracts.MemoryItem, error) {
	return f.items, nil
}

func (f *fakeMemoryService) Get(_ context.Context, _, uri string) (contracts.MemoryDetail, error) {
	for _, item := range f.items {
		if item.URI == uri {
			return contracts.MemoryDetail{MemoryItem: item, Overview: "overview of " + item.Title, Content: "content of " + item.Title}, nil
		}
	}
	return contracts.MemoryDetail{}, nil
}

func (f *fakeMemoryService) Write(_ context.Context, _ string, draft contracts.MemoryDraft) (contracts.MemoryItem, error) {
	f.writes = append(f.writes, draft)
	item := contracts.MemoryItem{URI: "mem://user/self/memories/preferences/new", Type: draft.Type, Title: draft.Title, Summary: draft.Summary}
	f.items = append(f.items, item)
	return item, nil
}

func (f *fakeMemoryService) Delete(context.Context, string, string) error { return nil }

type fakeMemoryWorkspace struct{ root string }

func (f fakeMemoryWorkspace) List(context.Context) ([]contracts.Workspace, error) { return nil, nil }
func (f fakeMemoryWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	return contracts.Workspace{ID: "ws", Root: f.root}, true, nil
}
func (f fakeMemoryWorkspace) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}
func (f fakeMemoryWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}
func (f fakeMemoryWorkspace) Delete(context.Context, string) error { return nil }
func (f fakeMemoryWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}
func (f fakeMemoryWorkspace) ReadFile(context.Context, string, int) ([]byte, error) { return nil, nil }
func (f fakeMemoryWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

func TestMemoryTabListsOpensDetailAndAdds(t *testing.T) {
	chat := newFakeChat()
	service := &fakeMemoryService{items: []contracts.MemoryItem{
		{URI: "mem://user/self/memories/preferences/alpha", Type: "preferences", Title: "Alpha 项目", Summary: "用中文回复"},
		{URI: "mem://user/self/memories/events/beta", Type: "events", Title: "Beta 事件", Summary: "修好了输入法"},
	}}
	h := newHarnessWithMemory(t, chat, service)
	provide[contracts.WorkspaceService](t, h.ctx, contracts.KeyWorkspace, fakeMemoryWorkspace{root: t.TempDir()})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "记忆")
	tester.Frame()
	waitForText(t, tester, "Alpha 项目", "Beta 事件")

	h.click(tester, "Alpha 项目")
	tester.Frame()
	waitForText(t, tester, "content of Alpha 项目", "overview of Alpha 项目")

	h.click(tester, "返回")
	tester.Frame()
	h.click(tester, "新建记忆")
	tester.Type("新的记忆")
	tester.Frame()
	h.click(tester, "保存")
	tester.Frame()
	if len(service.writes) != 1 || service.writes[0].Type != "preferences" || !strings.Contains(service.writes[0].Content, "新的记忆") {
		t.Fatalf("writes = %+v", service.writes)
	}
}

func waitForText(t *testing.T, tester *ui.Tester, wanted ...string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		tester.Frame()
		ok := true
		for _, want := range wanted {
			if !tester.HasText(want) {
				ok = false
				break
			}
		}
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("missing %v; texts = %v", wanted, tester.Texts())
		}
		time.Sleep(10 * time.Millisecond)
	}
}
