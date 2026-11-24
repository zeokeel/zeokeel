package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The session Memory tab fetched /api/sessions/{id}/memory, which the gateway never
// served (the client's own file said so: "surfaces that the mock gateway serves and
// the real gateway does not"). The memory store is workspace-scoped and this build
// has one fixed workspace, so the tab shows that workspace's memory as a whole.

type fakeMemory struct {
	items []contracts.MemoryItem
	got   struct {
		root  string
		query string
		limit int
	}
	err error
}

func (f *fakeMemory) List(_ context.Context, root, query string, _ []string, limit int) ([]contracts.MemoryItem, error) {
	f.got.root, f.got.query, f.got.limit = root, query, limit
	return f.items, f.err
}

func (f *fakeMemory) Get(context.Context, string, string) (contracts.MemoryDetail, error) {
	return contracts.MemoryDetail{}, nil
}

func (f *fakeMemory) Write(context.Context, string, contracts.MemoryDraft) (contracts.MemoryItem, error) {
	return contracts.MemoryItem{}, nil
}

func (f *fakeMemory) Delete(context.Context, string, string) error { return nil }

func newMemoryGateway(t *testing.T, memory *fakeMemory, root string) *runningGateway {
	t.Helper()
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	testRoot := testRoot(t, chat)
	if root != "" {
		workspace := &diskWorkspace{root: root}
		if _, err := testRoot.Provide(contracts.KeyWorkspace, contracts.WorkspaceService(workspace)); err != nil {
			t.Fatalf("provide workspace: %v", err)
		}
	}
	if memory != nil {
		if _, err := testRoot.Provide(contracts.KeyMemoryService, contracts.MemoryService(memory)); err != nil {
			t.Fatalf("provide memory: %v", err)
		}
	}
	gateway, err := New(testRoot, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(testRoot); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	return serveGateway(t, gateway)
}

func TestSessionMemoryListsTheWorkspaceMemory(t *testing.T) {
	updated := time.Date(2026, 11, 24, 9, 30, 0, 0, time.UTC)
	memory := &fakeMemory{items: []contracts.MemoryItem{
		{URI: "mem://user/self/memories/pages/project-codename", Type: "user", Title: "项目代号 ORCA-7", Summary: "后端 Rust", Updated: updated},
		{URI: "mem://user/self/memories/pages/style", Type: "user", Title: "偏好简洁回答", Updated: updated},
	}}
	gw := newMemoryGateway(t, memory, t.TempDir())
	defer gw.stop(t)

	status, body := filesGet(t, gw, "/api/sessions/s1/memory", true)
	if status != http.StatusOK {
		t.Fatalf("GET memory = %d %q", status, body)
	}
	var payload memoryListDTO
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode: %v (%q)", err, body)
	}
	if len(payload.Entries) != 2 {
		t.Fatalf("entries = %+v", payload.Entries)
	}
	first := payload.Entries[0]
	if first.ID == "" || first.Title != "项目代号 ORCA-7" || first.Summary != "后端 Rust" {
		t.Fatalf("entry = %+v", first)
	}
	if first.UpdatedAt != "2026-11-24T09:30:00Z" {
		t.Fatalf("updatedAt = %q", first.UpdatedAt)
	}
	// The store is asked for the open workspace, unfiltered: the tab is the whole
	// memory, not one session's slice.
	if memory.got.root == "" || memory.got.query != "" || memory.got.limit != readModelMemoryLimit {
		t.Fatalf("store query = %+v", memory.got)
	}
}

func TestSessionMemoryWithoutServicesIsEmpty(t *testing.T) {
	gw := newMemoryGateway(t, nil, "")
	defer gw.stop(t)
	status, body := filesGet(t, gw, "/api/sessions/s1/memory", true)
	if status != http.StatusOK {
		t.Fatalf("GET = %d %q", status, body)
	}
	var payload memoryListDTO
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode: %v (%q)", err, body)
	}
	if payload.Entries == nil {
		t.Fatalf("empty answer must carry an array, not null: %q", body)
	}
}

func TestSessionMemoryNeedsTheToken(t *testing.T) {
	gw := newMemoryGateway(t, &fakeMemory{}, t.TempDir())
	defer gw.stop(t)
	if status, _ := filesGet(t, gw, "/api/sessions/s1/memory", false); status != http.StatusUnauthorized {
		t.Fatalf("without a token = %d, want 401", status)
	}
}

// An unknown per-session read model stays a 404 instead of silently answering with
// something else.
func TestUnknownSessionReadModelIs404(t *testing.T) {
	gw := newMemoryGateway(t, &fakeMemory{}, t.TempDir())
	defer gw.stop(t)
	if status, _ := filesGet(t, gw, "/api/sessions/s1/nope", true); status != http.StatusNotFound {
		t.Fatalf("unknown path = %d, want 404", status)
	}
}
