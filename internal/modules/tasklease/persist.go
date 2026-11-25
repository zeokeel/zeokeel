package tasklease

// Durable half of the lease layer: one JSON document under the product data
// directory. The in-memory table stays the source of truth for a running
// process; Open loads the document, and every mutation (claim, heartbeat,
// release, submit, accept, reject, expire) rewrites it atomically (temp file +
// rename, mode 0600) so a crash never leaves a half-written lease table.
//
// A lease that outlived its TTL while the process was down is worthless: nobody
// is proving it is alive. Open drops those rows and puts their board tasks back
// to pending/unassigned, without surfacing an error — a restart after downtime
// is normal, and leaving the work "in progress" under a dead owner would hide it
// forever.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ErrLeaseStoreCorrupt means the persisted lease table could not be decoded.
// Open still returns a usable service with an empty table so the caller can
// quarantine the file and keep serving; the typed error is how it learns to.
var ErrLeaseStoreCorrupt = errors.New("tasklease: corrupt lease store")

// leaseStoreVersion is bumped when the on-disk shape changes incompatibly.
const leaseStoreVersion = 1

// leaseStoreName is the document's file name inside the module data directory.
const leaseStoreName = "leases.json"

// leaseStoreDocument is the on-disk shape.
type leaseStoreDocument struct {
	Version int                   `json:"version"`
	Leases  []contracts.TaskLease `json:"leases"`
}

// Open builds a service whose live leases live in dir/leases.json. A missing
// file is a fresh start. A corrupt file is reported as ErrLeaseStoreCorrupt
// together with a service whose table is empty (the caller must not lose the
// file: it either fails the boot or moves it aside and continues).
func Open(dir string, board contracts.TaskBoard, cfg Config) (*Service, error) {
	service := NewService(board, cfg)
	if dir == "" {
		return service, nil
	}
	service.path = filepath.Join(dir, leaseStoreName)

	raw, err := os.ReadFile(service.path)
	if errors.Is(err, fs.ErrNotExist) {
		return service, nil
	}
	if err != nil {
		return nil, fmt.Errorf("tasklease: read lease store: %w", err)
	}
	var document leaseStoreDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return service, fmt.Errorf("%w: %v", ErrLeaseStoreCorrupt, err)
	}
	if document.Version > leaseStoreVersion {
		return service, fmt.Errorf("%w: version %d (this build knows %d)", ErrLeaseStoreCorrupt, document.Version, leaseStoreVersion)
	}

	now := service.cfg.Now()
	var released []contracts.TaskLease
	service.mu.Lock()
	for _, lease := range document.Leases {
		if lease.TaskID == "" || lease.Owner == "" {
			continue // hand-edited or pre-owner rows: unusable
		}
		if !now.Before(lease.Until) {
			released = append(released, lease)
			continue
		}
		stored := lease
		service.leases[lease.TaskID] = &stored
	}
	service.mu.Unlock()

	// The dead claims are dropped in memory only; the next mutation rewrites the
	// document. Releasing the board is best effort by design: a restart must not
	// fail because one task could not be listed.
	for _, lease := range released {
		service.releaseExpiredOnLoad(lease)
	}
	return service, nil
}

// releaseExpiredOnLoad hands a dead claim's task back to the board. Only a task
// still marked in_progress is touched: a task the board already completed or
// returned must not be resurrected by a stale lease row.
func (s *Service) releaseExpiredOnLoad(lease contracts.TaskLease) {
	if s.board == nil {
		return
	}
	ctx := context.Background()
	current, err := s.findTask(ctx, lease.TaskID)
	if err != nil || current.Status != contracts.TaskStatusInProgress {
		return
	}
	if _, err := s.board.SetStatus(ctx, lease.TaskID, contracts.TaskStatusPending); err != nil {
		return
	}
	if assigner, ok := s.board.(contracts.TaskAssigner); ok && current.Owner != "" {
		_, _ = assigner.Assign(ctx, lease.TaskID, "")
	}
}

// persistLocked rewrites the document atomically. Callers hold s.mu.
func (s *Service) persistLocked() error {
	if s.path == "" {
		return nil
	}
	ids := make([]string, 0, len(s.leases))
	for id := range s.leases {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic bytes make the document diffable
	document := leaseStoreDocument{Version: leaseStoreVersion, Leases: make([]contracts.TaskLease, 0, len(ids))}
	for _, id := range ids {
		document.Leases = append(document.Leases, *s.leases[id])
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("tasklease: encode lease store: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("tasklease: create data dir: %w", err)
	}
	temp := s.path + ".tmp"
	if err := os.WriteFile(temp, encoded, 0o600); err != nil {
		return fmt.Errorf("tasklease: write lease store: %w", err)
	}
	if err := os.Rename(temp, s.path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("tasklease: replace lease store: %w", err)
	}
	return nil
}
