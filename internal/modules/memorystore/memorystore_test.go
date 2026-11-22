package memorystore

import (
	"context"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestWriteListGetDelete(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	svc := &service{dataDir: t.TempDir(), stores: make(map[string]*cachedStore)}
	defer svc.close()

	first, err := svc.Write(ctx, root, contracts.MemoryDraft{Type: "preferences", Title: "Alpha", Summary: "用中文回复", Content: "正文"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if first.URI == "" || first.Type != "preferences" || first.Title != "Alpha" {
		t.Fatalf("written = %+v", first)
	}
	if _, err := svc.Write(ctx, root, contracts.MemoryDraft{Type: "events", Title: "Beta", Summary: "事件"}); err != nil {
		t.Fatal(err)
	}
	items, err := svc.List(ctx, root, "", nil, 10)
	if err != nil || len(items) != 2 {
		t.Fatalf("List = %+v/%v", items, err)
	}
	filtered, err := svc.List(ctx, root, "", []string{"events"}, 10)
	if err != nil || len(filtered) != 1 || filtered[0].Title != "Beta" {
		t.Fatalf("filtered = %+v/%v", filtered, err)
	}
	found, err := svc.List(ctx, root, "Alpha", nil, 10)
	if err != nil || len(found) != 1 || found[0].URI != first.URI {
		t.Fatalf("search = %+v/%v", found, err)
	}
	detail, err := svc.Get(ctx, root, first.URI)
	if err != nil || detail.Content != "正文" {
		t.Fatalf("Get = %+v/%v", detail, err)
	}
	if err := svc.Delete(ctx, root, first.URI); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	remaining, _ := svc.List(ctx, root, "", nil, 10)
	if len(remaining) != 1 {
		t.Fatalf("remaining = %+v", remaining)
	}
}
