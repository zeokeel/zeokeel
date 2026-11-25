package contracts

import (
	"context"
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Webhook ingress: the first channel adapter. It proves the request is really
// from the configured sender (signature + replay window), normalizes the body
// into an InboxItem, and hands it to the inbox. It never creates work directly —
// the Lead triages.
const (
	// WebhookSignatureHeader carries "t=<unix>,v1=<hex hmac>".
	WebhookSignatureHeader = "X-Zeokeel-Signature"
	// WebhookMaxSkew is the accepted clock skew for a signed request.
	WebhookMaxSkew = 5 * time.Minute
)

// WebhookSource is one configured inbound endpoint: an id (path segment) and
// the secret shared with the sender.
type WebhookSource struct {
	// ID is the endpoint identity ("github", "grafana", ...).
	ID string
	// Secret is the HMAC key. Never logged.
	Secret []byte
	// DefaultChannel labels the conversation when the payload does not carry one.
	DefaultChannel string
}

// WebhookResult reports what one accepted delivery did.
type WebhookResult struct {
	Item   InboxItem
	Merged bool
}

// WebhookService receives signed deliveries and files them in the inbox.
type WebhookService interface {
	// Receive verifies and stores one delivery. The body must be the exact bytes
	// that were signed.
	Receive(ctx context.Context, sourceID string, header string, body []byte) (WebhookResult, error)
}

// Webhook errors are typed so the HTTP layer can answer 401/400/429 honestly.
var (
	// ErrWebhookUnknownSource means the source id is not configured.
	ErrWebhookUnknownSource = errors.New("webhook: unknown source")
	// ErrWebhookBadSignature means the signature is missing or does not verify.
	ErrWebhookBadSignature = errors.New("webhook: bad signature")
	// ErrWebhookStale means the timestamp is outside the replay window.
	ErrWebhookStale = errors.New("webhook: stale timestamp")
	// ErrWebhookBadPayload means the JSON body is not a usable delivery.
	ErrWebhookBadPayload = errors.New("webhook: bad payload")
)

// KeyWebhook is provided by the webhook module.
var KeyWebhook = cordis.NewServiceKey[WebhookService]("zeokeel/webhook")
