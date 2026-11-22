package contracts

import (
	"context"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// MemoryItem is one v2 memory row for the session Memory tab.
type MemoryItem struct {
	URI     string
	Type    string
	Title   string
	Summary string
	Updated time.Time
	Pinned  bool
	Tokens  int
}

// MemoryDetail is one memory page with its L1 overview and L2 content.
type MemoryDetail struct {
	MemoryItem
	Overview string
	Content  string
}

// MemoryDraft is one create/edit request.
type MemoryDraft struct {
	URI      string
	Type     string
	Title    string
	Summary  string
	Overview string
	Content  string
	Pinned   bool
}

// MemoryService is the product view of the SDK v2 memory store, scoped to one
// workspace root per call.
type MemoryService interface {
	// List returns the newest memories of workspaceRoot, optionally filtered by
	// a search query and memory types.
	List(ctx context.Context, workspaceRoot, query string, types []string, limit int) ([]MemoryItem, error)
	// Get reads one memory with L1/L2.
	Get(ctx context.Context, workspaceRoot, uri string) (MemoryDetail, error)
	// Write creates or replaces one memory.
	Write(ctx context.Context, workspaceRoot string, draft MemoryDraft) (MemoryItem, error)
	// Delete removes one memory.
	Delete(ctx context.Context, workspaceRoot, uri string) error
}

// KeyMemoryService is provided by the memorystore module and consumed by the
// agent (shared v2 store) and the session Memory tab.
var KeyMemoryService = cordis.NewServiceKey[MemoryService]("zeokeel/memoryv2")
