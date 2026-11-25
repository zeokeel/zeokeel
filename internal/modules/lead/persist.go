package lead

// Durable half of the triage pass: the operator's rules live in one JSON
// document under the product data directory. Open loads the document,
// SetRules rewrites it atomically (temp file + rename, mode 0600), and every
// other pass reads the in-memory copy — a triage pass must never touch the disk
// per item.
//
// Rules are the operator's decisions, so a corrupt document is a typed error
// and NOT a silent empty set: the caller decides whether to fail the boot or
// quarantine the file. Open still returns the service with an empty rule set so
// the caller can keep serving (an empty set skips every item, which is the safe
// direction: nothing is dispatched on guesses).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ErrLeadStoreCorrupt means the persisted rule set could not be decoded.
var ErrLeadStoreCorrupt = errors.New("lead: corrupt rule store")

// ruleStoreVersion is bumped when the on-disk shape changes incompatibly.
const ruleStoreVersion = 1

// ruleStoreName is the document's file name inside the module data directory.
const ruleStoreName = "rules.json"

// ruleStoreDocument is the on-disk shape.
type ruleStoreDocument struct {
	Version int                  `json:"version"`
	Rules   []contracts.LeadRule `json:"rules"`
}

// Open builds the pass whose rules live in dir/rules.json. A missing file is a
// fresh start (no rules). A corrupt file is reported as ErrLeadStoreCorrupt
// together with a service whose rule set is empty.
func Open(dir string, inboxService contracts.InboxService, triage contracts.TriageService, cfg Config) (*Service, error) {
	service := NewService(inboxService, triage, RulePlanner{}, cfg)
	if dir == "" {
		return service, nil
	}
	service.path = filepath.Join(dir, ruleStoreName)

	raw, err := os.ReadFile(service.path)
	if errors.Is(err, fs.ErrNotExist) {
		return service, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lead: read rule store: %w", err)
	}
	var document ruleStoreDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return service, fmt.Errorf("%w: %v", ErrLeadStoreCorrupt, err)
	}
	if document.Version > ruleStoreVersion {
		return service, fmt.Errorf("%w: version %d (this build knows %d)", ErrLeadStoreCorrupt, document.Version, ruleStoreVersion)
	}

	service.mu.Lock()
	service.rules = append([]Rule(nil), document.Rules...)
	service.mu.Unlock()
	return service, nil
}

// persistLocked rewrites the document atomically. Callers hold s.mu.
func (s *Service) persistLocked() error {
	if s.path == "" {
		return nil
	}
	document := ruleStoreDocument{Version: ruleStoreVersion, Rules: append([]contracts.LeadRule(nil), s.rules...)}
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("lead: encode rule store: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("lead: create data dir: %w", err)
	}
	temp := s.path + ".tmp"
	if err := os.WriteFile(temp, encoded, 0o600); err != nil {
		return fmt.Errorf("lead: write rule store: %w", err)
	}
	if err := os.Rename(temp, s.path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("lead: replace rule store: %w", err)
	}
	return nil
}
