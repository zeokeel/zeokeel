// Package webhook is the first channel adapter: an inbound, signed HTTP delivery
// normalized into an inbox item. It proves the sender (HMAC + replay window),
// derives a stable dedupe key (so a platform retry does not become two pieces of
// work), and hands the event to the inbox. It never creates tasks.
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Config is the module's typed assembly config.
type Config struct {
	// Now overrides the clock (tests); nil means time.Now.
	Now func() time.Time `json:"-"`
	// Sources are the configured endpoints. Secret bytes are never serialized.
	Sources []contracts.WebhookSource `json:"-"`
	// MaxSkew overrides contracts.WebhookMaxSkew (0 = default).
	MaxSkew time.Duration `json:"maxSkew"`
}

func (c Config) normalized() Config {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.MaxSkew <= 0 {
		c.MaxSkew = contracts.WebhookMaxSkew
	}
	return c
}

// Service implements contracts.WebhookService and
// contracts.WebhookSourceAdmin.
type Service struct {
	inbox contracts.InboxService
	cfg   Config

	// mu guards sources: the settings UI mutates them while deliveries read
	// them. A delivery only needs the map, so a short lock is enough.
	mu      sync.RWMutex
	sources map[string]contracts.WebhookSource
	// persistPath is the durable source list ("" = memory only).
	persistPath string
}

// NewService wires the adapter.
func NewService(inboxService contracts.InboxService, cfg Config) *Service {
	cfg = cfg.normalized()
	sources := make(map[string]contracts.WebhookSource, len(cfg.Sources))
	for _, source := range cfg.Sources {
		if source.ID != "" && len(source.Secret) > 0 {
			sources[source.ID] = source
		}
	}
	return &Service{inbox: inboxService, cfg: cfg, sources: sources}
}

// delivery is the normalized body a sender posts. Only title is required.
type delivery struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Ref      string `json:"ref"`
	Channel  string `json:"channel"`
	Sender   string `json:"sender"`
	DedupKey string `json:"dedupKey"`
	Priority int    `json:"priority"`
}

// Receive implements contracts.WebhookService.
func (s *Service) Receive(ctx context.Context, sourceID, header string, body []byte) (contracts.WebhookResult, error) {
	if err := ctx.Err(); err != nil {
		return contracts.WebhookResult{}, err
	}
	s.mu.RLock()
	source, ok := s.sources[sourceID]
	s.mu.RUnlock()
	if !ok {
		return contracts.WebhookResult{}, contracts.ErrWebhookUnknownSource
	}
	timestamp, signature, err := parseSignatureHeader(header)
	if err != nil {
		return contracts.WebhookResult{}, err
	}
	if skew := s.cfg.Now().Sub(timestamp); skew > s.cfg.MaxSkew || skew < -s.cfg.MaxSkew {
		return contracts.WebhookResult{}, contracts.ErrWebhookStale
	}
	if !verify(source.Secret, timestamp, body, signature) {
		return contracts.WebhookResult{}, contracts.ErrWebhookBadSignature
	}

	var payload delivery
	if err := json.Unmarshal(body, &payload); err != nil {
		return contracts.WebhookResult{}, fmt.Errorf("%w: %v", contracts.ErrWebhookBadPayload, err)
	}
	payload.Title = strings.TrimSpace(payload.Title)
	if payload.Title == "" {
		return contracts.WebhookResult{}, fmt.Errorf("%w: title is required", contracts.ErrWebhookBadPayload)
	}

	channel := payload.Channel
	if channel == "" {
		channel = source.DefaultChannel
	}
	dedupeKey := payload.DedupKey
	if dedupeKey == "" {
		// Stable default: the same body delivered twice is the same event. The
		// timestamp is deliberately excluded so a retry with a fresh timestamp
		// still merges.
		sum := sha256.Sum256(body)
		dedupeKey = hex.EncodeToString(sum[:16])
	}

	item, merged, err := s.inbox.Add(ctx, contracts.InboxItem{
		Source: "webhook", Channel: channel, Sender: payload.Sender,
		DedupKey: dedupeKey, Priority: payload.Priority,
		Title: payload.Title, Body: payload.Body, Ref: payload.Ref,
	})
	if err != nil {
		return contracts.WebhookResult{}, err
	}
	return contracts.WebhookResult{Item: item, Merged: merged}, nil
}

// parseSignatureHeader accepts "t=1700000000,v1=<hex>" in any field order.
func parseSignatureHeader(header string) (time.Time, []byte, error) {
	if strings.TrimSpace(header) == "" {
		return time.Time{}, nil, contracts.ErrWebhookBadSignature
	}
	var (
		timestamp int64 = -1
		signature []byte
	)
	for _, part := range strings.Split(header, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "t":
			parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil {
				return time.Time{}, nil, contracts.ErrWebhookBadSignature
			}
			timestamp = parsed
		case "v1":
			decoded, err := hex.DecodeString(strings.TrimSpace(value))
			if err != nil || len(decoded) == 0 {
				return time.Time{}, nil, contracts.ErrWebhookBadSignature
			}
			signature = decoded
		}
	}
	if timestamp < 0 || len(signature) == 0 {
		return time.Time{}, nil, contracts.ErrWebhookBadSignature
	}
	return time.Unix(timestamp, 0), signature, nil
}

// verify recomputes HMAC-SHA256 over "<timestamp>.<body>".
func verify(secret []byte, timestamp time.Time, body, signature []byte) bool {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(strconv.FormatInt(timestamp.Unix(), 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), signature)
}

var _ contracts.WebhookService = (*Service)(nil)

// Sign is the sender-side helper the docs and the acceptance script use (it is
// also what the tests sign with).
func Sign(secret []byte, timestamp time.Time, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(strconv.FormatInt(timestamp.Unix(), 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return "t=" + strconv.FormatInt(timestamp.Unix(), 10) + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// ensure errors stay comparable through wrapping.
var _ = errors.Is
