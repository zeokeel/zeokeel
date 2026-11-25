package skilldraft

// Durable half of the draft store: one JSON document under the module data
// directory. The in-memory map stays the running source of truth; Open loads the
// document and every mutation (propose, decide) rewrites it atomically (temp
// file + rename, mode 0600) so a crash never leaves a half-written file.
//
// Unlike the lease table, nothing here is authoritative for work in flight: a
// draft is a proposal. A file this build cannot read is therefore reported as a
// typed error and then ignored — the product boots with an empty store, and the
// first new draft replaces the unreadable file.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// storeVersion is bumped when the on-disk shape changes incompatibly. The
// document is one object, so a version this build does not know is reported as
// corrupt instead of being guessed at: drafts it cannot read are proposals, and
// misreading a decision would be worse than dropping it.
const storeVersion = 1

// storeName is the document's file name inside the module data directory.
const storeName = "drafts.json"

// storeDocument is the on-disk shape.
type storeDocument struct {
	Version int                        `json:"version"`
	Drafts  []contracts.TaskSkillDraft `json:"drafts"`
}

// Open builds a service whose drafts live in dir/drafts.json. An empty dir is an
// in-memory store (the same Service, without the durable half).
//
// A missing file is a fresh start. A corrupt (or too-new) file is reported as
// contracts.ErrSkillDraftStoreCorrupt together with a usable, empty store: the
// caller logs it and keeps serving, and the first successful Draft replaces the
// unreadable file.
func Open(dir string) (*Service, error) {
	service := NewService()
	if strings.TrimSpace(dir) == "" {
		return service, nil
	}
	service.path = filepath.Join(dir, storeName)

	raw, err := os.ReadFile(service.path)
	if errors.Is(err, fs.ErrNotExist) {
		return service, nil
	}
	if err != nil {
		return nil, fmt.Errorf("skilldraft: read draft store: %w", err)
	}
	var document storeDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return service, fmt.Errorf("%w: %v", contracts.ErrSkillDraftStoreCorrupt, err)
	}
	if document.Version > storeVersion {
		return service, fmt.Errorf("%w: version %d (this build knows %d)", contracts.ErrSkillDraftStoreCorrupt, document.Version, storeVersion)
	}
	// The loader is lenient row by row: a row with no id, no name or a status
	// this build does not recognise is not a draft anyone could decide on, so it
	// is dropped instead of poisoning the whole file.
	for _, draft := range document.Drafts {
		draft.ID = strings.TrimSpace(draft.ID)
		draft.Name = strings.TrimSpace(draft.Name)
		if draft.ID == "" || draft.Name == "" || !knownStatus(draft.Status) {
			continue
		}
		draft.CreatedAt = draft.CreatedAt.UTC().Truncate(time.Second)
		service.drafts[draft.ID] = draft
	}
	return service, nil
}

// knownStatus reports whether status is one this build can honour.
func knownStatus(status string) bool {
	switch status {
	case contracts.SkillDraftPending, contracts.SkillDraftAccepted, contracts.SkillDraftRejected:
		return true
	}
	return false
}

// save rewrites the whole document atomically (temp file + rename, mode 0600,
// directory 0700). Must be called with the lock held: the map is the input.
func (s *Service) save() error {
	if s.path == "" {
		return nil
	}
	document := storeDocument{Version: storeVersion}
	document.Drafts = make([]contracts.TaskSkillDraft, 0, len(s.drafts))
	for _, draft := range s.drafts {
		document.Drafts = append(document.Drafts, draft)
	}
	// A map iterates randomly, so sort before encoding: an unchanged store then
	// writes an identical file, which makes hand-inspection and diffs sane.
	sort.Slice(document.Drafts, func(i, j int) bool {
		return document.Drafts[i].ID < document.Drafts[j].ID
	})
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("skilldraft: encode draft store: %w", err)
	}
	encoded = append(encoded, '\n')

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("skilldraft: create data dir: %w", err)
	}
	temp := s.path + ".tmp"
	if err := os.WriteFile(temp, encoded, 0o600); err != nil {
		return fmt.Errorf("skilldraft: write draft store: %w", err)
	}
	if err := os.Rename(temp, s.path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("skilldraft: replace draft store: %w", err)
	}
	return nil
}
