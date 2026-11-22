// Package memorystore provides the product's shared memory v2 backend. It opens
// one SQLite store per workspace root and exposes a small view contract to the
// agent and the session Memory tab.
package memorystore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	"github.com/zeokeel/zeokeel-rig/memoryvfs"
	rigmemory "github.com/zeokeel/zeokeel-rig/tools/memory"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	ModuleID = "zeokeel/memorystore"
	Version  = "1.0.0"
)

// Config is empty; the module reads the product data directory from the kernel.
type Config struct{}

// Module provides contracts.KeyMemoryService.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		dataDir, err := ctx.Require(contracts.KeyDataDir)
		if err != nil {
			return fmt.Errorf("%s: require data dir: %w", ModuleID, err)
		}
		svc := &service{dataDir: dataDir, stores: make(map[string]*cachedStore)}
		if _, err := ctx.Provide(contracts.KeyMemoryService, contracts.MemoryService(svc)); err != nil {
			return fmt.Errorf("%s: provide memory service: %w", ModuleID, err)
		}
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(svc.close)}
		}); err != nil {
			return fmt.Errorf("%s: register cleanup: %w", ModuleID, err)
		}
		return nil
	}).AddProvides(module.Provides(contracts.KeyMemoryService)).
		AddRequires(module.Requires(contracts.KeyDataDir)).
		WithName("ZeoKeel memory v2 store")
}

type cachedStore struct {
	store memoryvfs.Store
}

type service struct {
	dataDir string
	mu      sync.Mutex
	stores  map[string]*cachedStore
}

var _ contracts.MemoryService = (*service)(nil)

const memoriesRoot = "mem://user/self/memories"

// Store returns the workspace's shared v2 store (opened on first use).
func (s *service) Store(workspaceRoot string) (memoryvfs.Store, error) {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		root = strings.TrimSpace(s.dataDir)
	}
	if root == "" {
		root = os.TempDir()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.stores[root]; ok {
		return cached.store, nil
	}
	store, err := rigmemory.NewWorkspaceStore(filepath.Join(s.dataDir, "memory"), root)
	if err != nil {
		return nil, err
	}
	s.stores[root] = &cachedStore{store: store}
	return store, nil
}

func (s *service) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cached := range s.stores {
		_ = cached.store.Close()
	}
	s.stores = make(map[string]*cachedStore)
}

// List implements contracts.MemoryService.
func (s *service) List(ctx context.Context, workspaceRoot, query string, types []string, limit int) ([]contracts.MemoryItem, error) {
	store, err := s.Store(workspaceRoot)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	query = strings.TrimSpace(query)
	var entries []memoryvfs.Entry
	if query != "" {
		result, err := store.Search(ctx, memoryvfs.Query{
			Text:  query,
			Scope: memoryvfs.URI(memoriesRoot),
			Kinds: []memoryvfs.Kind{memoryvfs.KindMemory},
			Limit: limit,
		})
		if err != nil {
			return nil, err
		}
		entries = result.Entries
	} else {
		all, err := store.Tree(ctx, memoryvfs.URI(memoriesRoot), 3)
		if err != nil {
			return nil, err
		}
		entries = all
	}
	allowed := make(map[string]bool, len(types))
	for _, typ := range types {
		if typ = strings.TrimSpace(typ); typ != "" {
			allowed[typ] = true
		}
	}
	items := make([]contracts.MemoryItem, 0, len(entries))
	for _, entry := range entries {
		if entry.Kind != memoryvfs.KindMemory {
			continue
		}
		if len(allowed) > 0 && !allowed[entry.Type] {
			continue
		}
		items = append(items, itemOf(entry))
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Pinned != items[j].Pinned {
			return items[i].Pinned
		}
		return items[i].Updated.After(items[j].Updated)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

// Get implements contracts.MemoryService.
func (s *service) Get(ctx context.Context, workspaceRoot, uri string) (contracts.MemoryDetail, error) {
	store, err := s.Store(workspaceRoot)
	if err != nil {
		return contracts.MemoryDetail{}, err
	}
	entry, err := store.Read(ctx, memoryvfs.URI(uri), memoryvfs.Level2)
	if err != nil {
		return contracts.MemoryDetail{}, err
	}
	return contracts.MemoryDetail{
		MemoryItem: itemOf(entry),
		Overview:   entry.Overview,
		Content:    entry.Content,
	}, nil
}

// Write implements contracts.MemoryService: a new draft without a URI gets a
// stable mem://user/self/memories/<type>/<slug> URI.
func (s *service) Write(ctx context.Context, workspaceRoot string, draft contracts.MemoryDraft) (contracts.MemoryItem, error) {
	store, err := s.Store(workspaceRoot)
	if err != nil {
		return contracts.MemoryItem{}, err
	}
	typ := strings.TrimSpace(draft.Type)
	if typ == "" {
		typ = memoryvfs.TypePreferences
	}
	uri := strings.TrimSpace(draft.URI)
	if uri == "" {
		slug := slugOf(draft.Title, draft.Summary)
		uri = memoriesRoot + "/" + typ + "/" + slug
	}
	entry := memoryvfs.Entry{
		URI:      memoryvfs.URI(uri),
		Kind:     memoryvfs.KindMemory,
		Type:     typ,
		Title:    strings.TrimSpace(draft.Title),
		Summary:  strings.TrimSpace(draft.Summary),
		Overview: draft.Overview,
		Content:  draft.Content,
		Pinned:   draft.Pinned,
	}
	if entry.Summary == "" {
		entry.Summary = entry.Title
	}
	written, err := store.Write(ctx, entry)
	if err != nil {
		return contracts.MemoryItem{}, err
	}
	return itemOf(written), nil
}

// Delete implements contracts.MemoryService.
func (s *service) Delete(ctx context.Context, workspaceRoot, uri string) error {
	store, err := s.Store(workspaceRoot)
	if err != nil {
		return err
	}
	return store.Delete(ctx, memoryvfs.URI(uri))
}

func itemOf(entry memoryvfs.Entry) contracts.MemoryItem {
	updated := entry.Updated
	if updated.IsZero() {
		updated = entry.Created
	}
	return contracts.MemoryItem{
		URI:     string(entry.URI),
		Type:    entry.Type,
		Title:   entry.Title,
		Summary: entry.Summary,
		Updated: updated,
		Pinned:  entry.Pinned,
		Tokens:  entry.Tokens,
	}
}

func slugOf(title, summary string) string {
	text := strings.ToLower(strings.TrimSpace(title))
	if text == "" {
		text = strings.ToLower(strings.TrimSpace(summary))
	}
	var builder strings.Builder
	lastDash := false
	for _, r := range text {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case r == ' ' || r == '-' || r == '_' || r == '.' || r == '/':
			if !lastDash && builder.Len() > 0 {
				builder.WriteByte('-')
				lastDash = true
			}
		}
		if builder.Len() >= 48 {
			break
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		slug = "memory"
	}
	sum := sha256.Sum256([]byte(title + "\x00" + summary))
	return slug + "-" + hex.EncodeToString(sum[:])[:8]
}

// now is kept for tests that need a fixed clock.
var _ = time.Now
