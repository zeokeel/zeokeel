package contracts

// Credential storage for channel adapters (Feishu/WeChat/DingTalk app secrets,
// bot tokens, ...). The store is the only place plaintext channel secrets live;
// every adapter reads through it instead of carrying secrets in configuration.
//
// Secret discipline is a type-level rule here, not a convention:
//
//   - Credential carries the plaintext fields. It is handed to exactly one
//     consumer (the adapter that authenticates with it) and must never be
//     logged, formatted into an error, serialized to a client or included in a
//     snapshot/delta.
//   - CredentialInfo is the list/read model: kind and id only, no field map.
//     Because the type has no place to put a secret, a list/status surface
//     cannot leak one by accident — the compiler refuses, not a review.

import (
	"context"
	"errors"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Credential is one stored secret bundle: named fields under a (kind, id)
// identity. Kind names the adapter the bundle belongs to ("feishu", "wechat");
// id distinguishes multiple accounts of the same kind.
//
// The value is plaintext and must stay inside the process: do not log it, do
// not wrap it in errors, do not ship it to a client.
type Credential struct {
	// Kind is the adapter family (lowercase, [a-z][a-z0-9_-]{0,31}).
	Kind string
	// ID is the credential identity within the kind (same alphabet).
	ID string
	// Fields are the secret/parameter pairs the adapter reads (for example
	// app_id/app_secret). Keys are non-empty; values are bounded.
	Fields map[string]string
}

// CredentialInfo is the identity of a stored credential and deliberately
// carries no fields: the list/read path must be unable to expose a secret.
type CredentialInfo struct {
	// Kind is the adapter family the credential belongs to.
	Kind string
	// ID is the credential identity within the kind.
	ID string
}

// CredentialStore is the process-wide credential surface. Implementations
// persist credentials so adapters reconnect after a restart.
//
// Get is the only method that returns plaintext; List never does.
type CredentialStore interface {
	// Get returns one credential. found is false (with a nil error) when the
	// (kind, id) pair is not stored. The returned value is a copy: mutating it
	// does not change the store.
	Get(ctx context.Context, kind, id string) (credential Credential, found bool, err error)
	// Set stores cred, replacing any credential with the same (kind, id).
	// Invalid kinds, ids and fields are rejected with ErrCredentialInvalid.
	Set(ctx context.Context, credential Credential) error
	// Delete removes one credential. Deleting an unknown (but well-formed) pair
	// fails with ErrCredentialNotFound.
	Delete(ctx context.Context, kind, id string) error
	// List returns the identities stored under kind, sorted by id. It never
	// returns field values.
	List(ctx context.Context, kind string) ([]CredentialInfo, error)
}

// KeyCredentials is provided by the creds module (zeokeel/credentials) and
// consumed by channel adapters that authenticate with stored secrets.
var KeyCredentials = cordis.NewServiceKey[CredentialStore]("zeokeel/credentials")

// Typed credential errors. Callers match with errors.Is, so implementations
// wrap these instead of inventing message-only failures.
var (
	// ErrCredentialInvalid means the credential itself is malformed (kind/id
	// outside the allowed alphabet, empty field key, too many fields, value too
	// large). It never carries the offending value.
	ErrCredentialInvalid = errors.New("credentials: invalid credential")
	// ErrCredentialNotFound means the (kind, id) pair is well formed but not
	// stored.
	ErrCredentialNotFound = errors.New("credentials: not found")
	// ErrStoreCorrupt means the on-disk store exists but cannot be trusted
	// (not JSON, unknown version, invalid entry). A store reports it and keeps
	// serving an empty set; the corrupt file is left byte-for-byte in place so
	// the caller decides whether to quarantine it.
	ErrStoreCorrupt = errors.New("credentials: store corrupt")
)
