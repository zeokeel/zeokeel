package webhook

// Source management: the settings UI adds, rotates and removes inbound
// endpoints, and the list is persisted next to the product data with mode 0600.
// Reads never expose a secret (contracts.WebhookSourceInfo has no secret field),
// so no UI or log path can leak one.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// sourceIDPattern keeps ids usable as a URL path segment.
var sourceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// reservedSourceIDs are names a webhook source may not take because something
// else in the product already means that word:
//
//   - dingtalk / feishu / wechat / email are the channel adapters. Their inbound
//     paths are gone (docs/research/channel-capabilities.md §6), but the names
//     still name a channel everywhere else — an inbox `source`, a notify route, a
//     credential kind. Letting /api/inbound/dingtalk answer the generic webhook
//     adapter would make a configured webhook look like a rewired channel to
//     anyone reading a request log or the settings list.
//   - cron / teammate are the internal, non-human inbox sources (automation runs
//     and child-agent proposals). A webhook that files rows under those names
//     would launder machine input into a source the product treats as its own.
//
// The check lives in one place so both the settings write path
// (`webhook.source.set`) and the reload path (Open) apply it.
var reservedSourceIDs = map[string]bool{
	"dingtalk": true,
	"feishu":   true,
	"wechat":   true,
	"email":    true,
	"cron":     true,
	"teammate": true,
}

// minSecretBytes is the shortest secret we accept; anything shorter is a guess.
const minSecretBytes = 16

// sourceFile is the persisted shape (base64 keeps the JSON printable).
type sourceFile struct {
	Version int         `json:"version"`
	Sources []sourceRow `json:"sources"`
}

type sourceRow struct {
	ID      string `json:"id"`
	Secret  string `json:"secret"`
	Channel string `json:"channel,omitempty"`
}

// Open builds a service whose source list lives in dir/sources.json. A missing
// file means "no sources yet"; a corrupt one is an error (the caller can quarantine).
func Open(dir string, inboxService contracts.InboxService, cfg Config) (*Service, error) {
	service := NewService(inboxService, cfg)
	if dir == "" {
		return service, nil
	}
	service.persistPath = filepath.Join(dir, "sources.json")

	raw, err := os.ReadFile(service.persistPath)
	if errors.Is(err, fs.ErrNotExist) {
		return service, nil
	}
	if err != nil {
		return nil, fmt.Errorf("webhook: read sources: %w", err)
	}
	var document sourceFile
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("%w: %v", contracts.ErrWebhookInvalidSource, err)
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	for _, row := range document.Sources {
		secret, err := base64.StdEncoding.DecodeString(row.Secret)
		if err != nil || !validSourceID(row.ID) || len(secret) < minSecretBytes {
			continue // skip unusable rows rather than wiping the whole list
		}
		service.sources[row.ID] = contracts.WebhookSource{ID: row.ID, Secret: secret, DefaultChannel: row.Channel}
	}
	return service, nil
}

// Sources implements contracts.WebhookSourceAdmin.
func (s *Service) Sources(ctx context.Context) ([]contracts.WebhookSourceInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]contracts.WebhookSourceInfo, 0, len(s.sources))
	for _, source := range s.sources {
		out = append(out, contracts.WebhookSourceInfo{ID: source.ID, Channel: source.DefaultChannel})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// UpsertSource implements contracts.WebhookSourceAdmin.
func (s *Service) UpsertSource(ctx context.Context, source contracts.WebhookSource) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !sourceIDPattern.MatchString(source.ID) {
		return fmt.Errorf("%w: id must match %s", contracts.ErrWebhookInvalidSource, sourceIDPattern.String())
	}
	if reservedSourceIDs[source.ID] {
		return fmt.Errorf("%w: %q is a reserved name (channel adapters and internal inbox sources own it)",
			contracts.ErrWebhookInvalidSource, source.ID)
	}
	if len(source.Secret) < minSecretBytes {
		return fmt.Errorf("%w: the secret must be at least %d bytes", contracts.ErrWebhookInvalidSource, minSecretBytes)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.sources[source.ID]
	s.sources[source.ID] = source
	if err := s.persistSourcesLocked(); err != nil {
		if existed {
			s.sources[source.ID] = previous
		} else {
			delete(s.sources, source.ID)
		}
		return err
	}
	return nil
}

// RemoveSource implements contracts.WebhookSourceAdmin.
func (s *Service) RemoveSource(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, ok := s.sources[id]
	if !ok {
		return fmt.Errorf("%w: %s", contracts.ErrWebhookUnknownSource, id)
	}
	delete(s.sources, id)
	if err := s.persistSourcesLocked(); err != nil {
		s.sources[id] = previous
		return err
	}
	return nil
}

// persistSourcesLocked rewrites the source file atomically with mode 0600.
func (s *Service) persistSourcesLocked() error {
	if s.persistPath == "" {
		return nil
	}
	ids := make([]string, 0, len(s.sources))
	for id := range s.sources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	document := sourceFile{Version: 1, Sources: make([]sourceRow, 0, len(ids))}
	for _, id := range ids {
		source := s.sources[id]
		document.Sources = append(document.Sources, sourceRow{
			ID: source.ID, Channel: source.DefaultChannel,
			Secret: base64.StdEncoding.EncodeToString(source.Secret),
		})
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("webhook: encode sources: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.persistPath), 0o700); err != nil {
		return fmt.Errorf("webhook: create data dir: %w", err)
	}
	temp := s.persistPath + ".tmp"
	if err := os.WriteFile(temp, encoded, 0o600); err != nil {
		return fmt.Errorf("webhook: write sources: %w", err)
	}
	if err := os.Rename(temp, s.persistPath); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("webhook: replace sources: %w", err)
	}
	return nil
}

// validSourceID reports whether an id may address an endpoint: the URL-shape
// rule plus the reserved-name rule. A hand-edited sources.json gets the same
// answer as the API.
func validSourceID(id string) bool {
	return sourceIDPattern.MatchString(id) && !reservedSourceIDs[id]
}

var _ contracts.WebhookSourceAdmin = (*Service)(nil)
