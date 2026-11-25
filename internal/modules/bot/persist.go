package bot

// The durable half of the bot store: one versioned JSON document,
// <dir>/bots.json, mode 0600 inside a 0700 directory, rewritten atomically
// (temp file + rename) on every mutation so a crash cannot leave a half-written
// file that still parses.
//
// Two decisions worth stating:
//
//   - The loader is strict, not lenient. A row this build cannot fully honour
//     makes the whole document ErrBotStoreCorrupt: bots are not proposals but
//     permission-bearing entities, so silently dropping one would change who may
//     act. The caller gets an empty, usable store (built-in templates still
//     present) and the bytes stay on disk as evidence; the module quarantines
//     the file before the next write replaces it.
//   - Open never writes. Seeding the built-in templates happens in memory, so a
//     fresh product has a "lead" template to instantiate before anything is
//     saved, and a corrupt document is never overwritten just by starting up.

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

// FileName is the store document inside the store directory.
const FileName = "bots.json"

// storeVersion is the on-disk schema version. A document without it, or with any
// other value, is corrupt: guessing at a future/older layout is how a store
// silently loses grants.
const storeVersion = 1

// storeDocument is the on-disk shape. Both halves are lists (not maps) so the
// file stays inspectable by a human, and sorted so an unchanged store writes an
// identical file.
type storeDocument struct {
	Version   int                     `json:"version"`
	Templates []contracts.BotTemplate `json:"templates"`
	Instances []contracts.BotInstance `json:"instances"`
}

// Open builds a service whose records live in dir/bots.json. An empty dir is an
// in-memory store: the same Service without the durable half.
//
// A missing document is a fresh start (built-in templates only). A document that
// exists but cannot be trusted is contracts.ErrBotStoreCorrupt together with a
// usable, empty store: the product keeps running with the built-in templates, and
// the caller may quarantine the file.
func Open(dir string) (*Service, error) {
	service := NewService()
	if strings.TrimSpace(dir) == "" {
		return service, nil
	}
	service.path = filepath.Join(dir, FileName)

	raw, err := os.ReadFile(service.Path())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return service, nil
		}
		return nil, fmt.Errorf("bot: read bot store: %w", err)
	}
	var document storeDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return service, fmt.Errorf("%w: %s is not JSON: %v", contracts.ErrBotStoreCorrupt, service.Path(), err)
	}
	if document.Version != storeVersion {
		return service, fmt.Errorf("%w: %s has version %d, want %d", contracts.ErrBotStoreCorrupt, service.Path(), document.Version, storeVersion)
	}

	loadedTemplates := make(map[string]contracts.BotTemplate, len(document.Templates))
	for _, template := range document.Templates {
		normalized, err := normalizeTemplate(template)
		if err != nil {
			return service, fmt.Errorf("%w: %s: template %q: %v", contracts.ErrBotStoreCorrupt, service.Path(), template.ID, err)
		}
		if _, duplicate := loadedTemplates[normalized.ID]; duplicate {
			return service, fmt.Errorf("%w: %s: duplicate template %q", contracts.ErrBotStoreCorrupt, service.Path(), normalized.ID)
		}
		loadedTemplates[normalized.ID] = normalized
	}

	loadedInstances := make(map[string]contracts.BotInstance, len(document.Instances))
	for _, instance := range document.Instances {
		normalized := trimInstance(instance)
		// A missing memory scope has exactly one sensible reading — the agent
		// default resolve() would have applied at create time — so an older or
		// hand-written row that omits it loads instead of poisoning the file. A
		// missing status or name has no such reading and stays corrupt.
		if normalized.MemoryScope == "" {
			normalized.MemoryScope = contracts.BotMemoryScopeAgent
		}
		normalized.CreatedAt = normalized.CreatedAt.UTC().Truncate(time.Second)
		if !normalized.LastSeenAt.IsZero() {
			normalized.LastSeenAt = normalized.LastSeenAt.UTC().Truncate(time.Second)
		}
		if err := validateInstance(normalized); err != nil {
			return service, fmt.Errorf("%w: %s: bot %q: %v", contracts.ErrBotStoreCorrupt, service.Path(), instance.ID, err)
		}
		if _, exists := loadedTemplates[normalized.TemplateID]; !exists {
			// A bot whose template is gone could not be created through this API,
			// so the document was edited by hand. Refusing to guess is the only
			// safe reading: its grants came from a template we cannot see.
			return service, fmt.Errorf("%w: %s: bot %q names unknown template %q", contracts.ErrBotStoreCorrupt, service.Path(), normalized.ID, normalized.TemplateID)
		}
		if _, duplicate := loadedInstances[normalized.ID]; duplicate {
			return service, fmt.Errorf("%w: %s: duplicate bot %q", contracts.ErrBotStoreCorrupt, service.Path(), normalized.ID)
		}
		loadedInstances[normalized.ID] = normalized
	}

	// Stored templates win over built-ins (a user may retune "lead"); then any
	// built-in the document never mentioned is filled back in.
	for id, template := range loadedTemplates {
		service.templates[id] = template
	}
	service.seedLocked()
	service.instances = loadedInstances
	return service, nil
}

// Path is the document the store writes ("" for an in-memory store).
func (s *Service) Path() string { return s.path }

// saveLocked writes the whole store atomically. The caller holds the lock, so
// two writers can never interleave a rename with a stale snapshot.
func (s *Service) saveLocked() error {
	if s.path == "" {
		return nil
	}
	document := storeDocument{Version: storeVersion}
	document.Templates = make([]contracts.BotTemplate, 0, len(s.templates))
	for _, template := range s.templates {
		document.Templates = append(document.Templates, cloneTemplate(template))
	}
	sort.Slice(document.Templates, func(i, j int) bool { return document.Templates[i].ID < document.Templates[j].ID })
	document.Instances = make([]contracts.BotInstance, 0, len(s.instances))
	for _, instance := range s.instances {
		document.Instances = append(document.Instances, cloneInstance(instance))
	}
	sort.Slice(document.Instances, func(i, j int) bool { return document.Instances[i].ID < document.Instances[j].ID })

	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("bot: encode bot store: %w", err)
	}
	encoded = append(encoded, '\n')

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("bot: create data dir: %w", err)
	}
	temp, err := os.CreateTemp(dir, FileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("bot: temp file: %w", err)
	}
	tempName := temp.Name()
	// Every failure path below must remove the temp file: a stray file is both
	// clutter and a second copy of the store.
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}
	if err := temp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("bot: chmod temp: %w", err)
	}
	if _, err := temp.Write(encoded); err != nil {
		cleanup()
		return fmt.Errorf("bot: write bot store: %w", err)
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("bot: sync bot store: %w", err)
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("bot: close bot store: %w", err)
	}
	if err := os.Rename(tempName, s.path); err != nil {
		_ = os.Remove(tempName)
		return fmt.Errorf("bot: replace bot store: %w", err)
	}
	return nil
}
