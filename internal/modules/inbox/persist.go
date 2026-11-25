package inbox

// Durable half of the inbox: one JSON document under the product data directory.
// The in-memory rules stay the same; Open loads the document, and every mutating
// call rewrites it atomically (temp file + rename) so a crash never leaves a
// half-written queue.
//
// The rate window is intentionally not persisted: it bounds a burst, and a
// restart is a natural end to a burst.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ErrCorruptStore means the persisted inbox could not be decoded. Callers must
// surface it (and keep the file) instead of starting empty and losing requests.
var ErrCorruptStore = errors.New("inbox: corrupt store")

// storeVersion is bumped when the on-disk shape changes incompatibly.
const storeVersion = 1

// storeDocument is the on-disk shape.
type storeDocument struct {
	Version int                   `json:"version"`
	Seq     int64                 `json:"seq"`
	Items   []contracts.InboxItem `json:"items"`
}

// Open builds a service whose state lives in dir/inbox.json. A missing file is a
// fresh start; a corrupt file is an error.
func Open(dir string, cfg Config) (*Service, error) {
	service := NewService(cfg)
	if dir == "" {
		return service, nil
	}
	service.path = filepath.Join(dir, "inbox.json")

	raw, err := os.ReadFile(service.path)
	if errors.Is(err, fs.ErrNotExist) {
		return service, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inbox: read store: %w", err)
	}
	var document storeDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptStore, err)
	}
	if document.Version > storeVersion {
		return nil, fmt.Errorf("%w: version %d (this build knows %d)", ErrCorruptStore, document.Version, storeVersion)
	}

	service.mu.Lock()
	defer service.mu.Unlock()
	service.seq = document.Seq
	for i := range document.Items {
		item := document.Items[i]
		if item.ID == "" || item.Source == "" || item.DedupKey == "" {
			continue // older or hand-edited rows: skip the unusable ones
		}
		if item.Status == "" {
			item.Status = contracts.InboxStatusNew
		}
		if item.Count <= 0 {
			item.Count = 1
		}
		service.items[item.ID] = &item
		if item.LastSeenAt.Before(item.ReceivedAt) {
			// Keep the dedupe window anchored on the latest arrival.
			item.LastSeenAt = item.ReceivedAt
		}
	}
	service.rebuildDedupeLocked()
	return service, nil
}

// rebuildDedupeLocked points each dedupe slot at the newest live item.
func (s *Service) rebuildDedupeLocked() {
	s.dedup = make(map[string]string, len(s.items))
	for id, item := range s.items {
		slot := dedupSlot(item.Source, item.DedupKey)
		existingID, ok := s.dedup[slot]
		if !ok {
			s.dedup[slot] = id
			continue
		}
		if s.items[existingID].LastSeenAt.Before(item.LastSeenAt) {
			s.dedup[slot] = id
		}
	}
}

// persistLocked rewrites the document atomically. Callers hold s.mu.
func (s *Service) persistLocked() error {
	if s.path == "" {
		return nil
	}
	ids := make([]string, 0, len(s.items))
	for id := range s.items {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	document := storeDocument{Version: storeVersion, Seq: s.seq, Items: make([]contracts.InboxItem, 0, len(ids))}
	for _, id := range ids {
		document.Items = append(document.Items, *s.items[id])
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("inbox: encode store: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("inbox: create data dir: %w", err)
	}
	temp := s.path + ".tmp"
	if err := os.WriteFile(temp, encoded, 0o600); err != nil {
		return fmt.Errorf("inbox: write store: %w", err)
	}
	if err := os.Rename(temp, s.path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("inbox: replace store: %w", err)
	}
	return nil
}
