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
// Adding the tool bindings (M5) did NOT bump it: the `tools` key is additive and
// optional, so a v1 file loads with no bindings and an older build reading a
// file that carries bindings simply ignores the key — whereas a bump would make
// that older build (module.go) quarantine the whole document, losing live
// claims for data it never needed.
const leaseStoreVersion = 1

// leaseStoreName is the document's file name inside the module data directory.
const leaseStoreName = "leases.json"

// leaseStoreDocument is the on-disk shape.
type leaseStoreDocument struct {
	Version int                   `json:"version"`
	Leases  []contracts.TaskLease `json:"leases"`
	// Tools is the per-task tool binding (M5), same document so one atomic
	// write covers both halves and there is only one file to quarantine.
	Tools []contracts.TaskTools `json:"tools,omitempty"`
	// Approvals is the per-task pre-start approval policy (G2), again additive:
	// no version bump, so a file written by a build that knows the gate still
	// loads in a build that does not (which simply ignores the key).
	Approvals []contracts.TaskApproval `json:"approvals,omitempty"`
	// StartRejections is the last refused start per task (G2): the approver and
	// the note, so a task that reappears in the pool carries its explanation.
	StartRejections []contracts.TaskStartRejection `json:"startRejections,omitempty"`
	// Budgets is the per-task cost ceiling (G11), additive like tools and
	// approvals: no version bump, so a build that knows the stop loads a file
	// that carries budgets and an older build ignores the key. The meter must
	// survive a restart — a task that already spent its budget may not start the
	// next process with a full one.
	Budgets []contracts.TaskBudget `json:"budgets,omitempty"`
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
	// Tool bindings are restored as written. The loader is lenient on purpose:
	// a hand-edited name that no longer matches the alphabet is still what the
	// operator bound, and the binding is policy data, not an authorization
	// decision made at load time. Rows with no task id or no names carry no
	// policy at all and are dropped.
	for _, binding := range document.Tools {
		if binding.TaskID == "" || len(binding.Tools) == 0 {
			continue
		}
		names := make([]string, 0, len(binding.Tools))
		for _, name := range binding.Tools {
			if name != "" {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			continue
		}
		stored := binding
		stored.Tools = names
		service.tools[binding.TaskID] = &stored
	}
	// Approval policies (G2). Unlike the tool binding this loader is NOT
	// lenient about an unknown value: an approval gate this build cannot
	// interpret is read back as before_start (fail closed). Silently treating
	// it as "none" would open a gate an operator explicitly closed.
	for _, approval := range document.Approvals {
		if approval.TaskID == "" {
			continue
		}
		policy, ok := approvalPolicy(approval.Policy)
		if !ok {
			policy = contracts.TaskApprovalBeforeStart
		}
		stored := approval
		stored.Policy = policy
		service.approvals[approval.TaskID] = &stored
	}
	// Budgets (G11). The row is restored as written except for two derivations:
	// a negative limit/usage is clamped to 0 (the write path never produces one,
	// and a hand-edited value must not invent a stop out of nowhere), and
	// Exhausted is recomputed with the clock of *this* process, because a minutes
	// budget may well have run out while the product was down.
	for _, budget := range document.Budgets {
		if budget.TaskID == "" {
			continue
		}
		stored := budget
		if stored.MaxTokens < 0 {
			stored.MaxTokens = 0
		}
		if stored.MaxMinutes < 0 {
			stored.MaxMinutes = 0
		}
		if stored.UsedTokens < 0 {
			stored.UsedTokens = 0
		}
		stored.Exhausted = stored.Exhausted || budgetExhausted(stored, now)
		service.budgets[budget.TaskID] = &stored
	}
	// Start refusals are history, not authority: they are restored as written
	// and never gate a claim on their own (the policy does that).
	for _, refusal := range document.StartRejections {
		if refusal.TaskID == "" || refusal.Note == "" {
			continue
		}
		stored := refusal
		service.startRejections[refusal.TaskID] = &stored
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
	toolIDs := make([]string, 0, len(s.tools))
	for id := range s.tools {
		toolIDs = append(toolIDs, id)
	}
	sort.Strings(toolIDs)
	document.Tools = make([]contracts.TaskTools, 0, len(toolIDs))
	for _, id := range toolIDs {
		document.Tools = append(document.Tools, *s.tools[id])
	}
	approvalIDs := make([]string, 0, len(s.approvals))
	for id := range s.approvals {
		approvalIDs = append(approvalIDs, id)
	}
	sort.Strings(approvalIDs)
	for _, id := range approvalIDs {
		document.Approvals = append(document.Approvals, *s.approvals[id])
	}
	budgetIDs := make([]string, 0, len(s.budgets))
	for id := range s.budgets {
		budgetIDs = append(budgetIDs, id)
	}
	sort.Strings(budgetIDs)
	for _, id := range budgetIDs {
		document.Budgets = append(document.Budgets, *s.budgets[id])
	}
	refusalIDs := make([]string, 0, len(s.startRejections))
	for id := range s.startRejections {
		refusalIDs = append(refusalIDs, id)
	}
	sort.Strings(refusalIDs)
	for _, id := range refusalIDs {
		document.StartRejections = append(document.StartRejections, *s.startRejections[id])
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
