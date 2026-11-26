package workflow

// Durable half of the workflow backend: one document under the product data
// directory holding every definition and every run.
//
// Why one document: the definitions and the runs that reference them must not be
// able to disagree. A run whose definition was saved in a second file could
// exist for a workflow that no longer does — and the runner would have to guess
// whether that is "deleted" or "corrupt". One atomic write keeps the pair
// consistent; the document is small by construction (32 stages per workflow).

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

// StoreFileName is the document's file name inside the module data directory.
const StoreFileName = "workflows.json"

// ErrStoreCorrupt means the document could not be decoded. Open still returns a
// usable (empty) service so the caller can quarantine the file and keep serving.
var ErrStoreCorrupt = errors.New("workflow: corrupt store")

// storeVersion is bumped only for incompatible changes; every added key so far
// has been additive (runs were added after workflows and old builds ignore them).
const storeVersion = 1

type storeDocument struct {
	Version   int                     `json:"version"`
	Workflows []contracts.Workflow    `json:"workflows"`
	Runs      []contracts.WorkflowRun `json:"runs,omitempty"`
}

// Open builds a service backed by dir/workflows.json. A missing file is a fresh
// start; a corrupt one is reported together with a usable service.
func Open(dir string, board contracts.TaskBoard, dispatcher contracts.WorkflowDispatcher, cfg Config) (*Service, error) {
	service := New(board, dispatcher, cfg)
	if dir == "" {
		return service, nil
	}
	service.path = filepath.Join(dir, StoreFileName)
	raw, err := os.ReadFile(service.path)
	if errors.Is(err, fs.ErrNotExist) {
		return service, nil
	}
	if err != nil {
		return nil, fmt.Errorf("workflow: read store: %w", err)
	}
	var document storeDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return service, fmt.Errorf("%w: %v", ErrStoreCorrupt, err)
	}
	if document.Version > storeVersion {
		return service, fmt.Errorf("%w: version %d (this build knows %d)", ErrStoreCorrupt, document.Version, storeVersion)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	for _, wf := range document.Workflows {
		if wf.ID == "" || len(wf.Stages) == 0 {
			continue // unusable row: no id or no work
		}
		service.workflows[wf.ID] = wf
	}
	for _, run := range document.Runs {
		if run.ID == "" || run.WorkflowID == "" {
			continue
		}
		stored := run
		service.runs[run.ID] = &stored
	}
	return service, nil
}

// persistLocked writes the whole document atomically (temp file + rename, 0600).
// The caller holds s.mu.
func (s *Service) persistLocked() error {
	if s.path == "" {
		return nil
	}
	document := storeDocument{Version: storeVersion}
	ids := make([]string, 0, len(s.workflows))
	for id := range s.workflows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		document.Workflows = append(document.Workflows, s.workflows[id])
	}
	runIDs := make([]string, 0, len(s.runs))
	for id := range s.runs {
		runIDs = append(runIDs, id)
	}
	sort.Strings(runIDs)
	for _, id := range runIDs {
		document.Runs = append(document.Runs, *s.runs[id])
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("workflow: encode store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("workflow: create data dir: %w", err)
	}
	temp := s.path + ".tmp"
	if err := os.WriteFile(temp, encoded, 0o600); err != nil {
		return fmt.Errorf("workflow: write store: %w", err)
	}
	if err := os.Rename(temp, s.path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("workflow: replace store: %w", err)
	}
	return nil
}

// advancePlan is one stage Advance is about to dispatch. It is prepared under the
// lock and executed after it: creating a task and starting a session are both
// slow, and holding the workflow mutex across them would serialize every reader
// behind a model call.
type advancePlan struct {
	runID    string
	workflow contracts.Workflow
	stage    contracts.WorkflowStage
}

// contextOrBackground keeps helper calls safe when a caller passes a nil context.
func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
