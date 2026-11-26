package reports

// Durable half of the report backend: one JSON document under the product data
// directory. Reports are history — a report the human has not read yet must
// survive a restart, and so must "delivered/deferred" (the sweep depends on it).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// StoreFileName is the document's file name inside the module data directory.
const StoreFileName = "reports.json"

// ErrStoreCorrupt means the document could not be decoded; Open still returns a
// usable service so the caller can quarantine the file.
var ErrStoreCorrupt = errors.New("reports: corrupt store")

const storeVersion = 1

type storeDocument struct {
	Version int                `json:"version"`
	Reports []contracts.Report `json:"reports"`
}

// Open builds a service backed by dir/reports.json.
func Open(dir string, notify Dispatcher, logger contracts.Logger, cfg Config) (*Service, error) {
	service := New(notify, logger, cfg)
	if dir == "" {
		return service, nil
	}
	service.path = filepath.Join(dir, StoreFileName)
	raw, err := os.ReadFile(service.path)
	if errors.Is(err, fs.ErrNotExist) {
		return service, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reports: read store: %w", err)
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
	for _, report := range document.Reports {
		if report.ID == "" || report.Goal == "" {
			continue
		}
		service.rows = append(service.rows, report)
	}
	// The next id continues after the highest numeric suffix, so a restart never
	// reuses an id the UI may still hold.
	for _, report := range service.rows {
		var n int
		if _, err := fmt.Sscanf(report.ID, "report-%d", &n); err == nil && n > service.next {
			service.next = n
		}
	}
	return service, nil
}

// persistLocked writes the document atomically (temp + rename, 0600).
func (s *Service) persistLocked() error {
	if s.path == "" {
		return nil
	}
	document := storeDocument{Version: storeVersion, Reports: append([]contracts.Report(nil), s.rows...)}
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("reports: encode store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("reports: create data dir: %w", err)
	}
	temp := s.path + ".tmp"
	if err := os.WriteFile(temp, encoded, 0o600); err != nil {
		return fmt.Errorf("reports: write store: %w", err)
	}
	if err := os.Rename(temp, s.path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("reports: replace store: %w", err)
	}
	return nil
}

// contextOrBackground keeps helpers safe when a caller passes a nil context.
func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
