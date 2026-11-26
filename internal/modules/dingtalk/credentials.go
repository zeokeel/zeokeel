package dingtalk

// Credentials from the shared store: the settings page writes one row per
// channel, and the adapter reads it at activation so a token never has to sit in
// an assembly document. An explicit configuration value still wins, because an
// operator overriding by hand is a deliberate act.
//
// The row may still carry the historical `signSecret` field: it is ignored here
// (kept in the store so old data round-trips, never read, because the adapter has
// no callback to verify any more).

import (
	"context"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// CredentialKind is the row kind this adapter reads.
const CredentialKind = "dingtalk"

// ApplyCredential fills the config from one credential row without overriding
// anything already set explicitly.
func ApplyCredential(cfg Config, fields map[string]string) Config {
	if cfg.Token == "" {
		cfg.Token = fields["token"]
	}
	if cfg.RobotCode == "" {
		cfg.RobotCode = fields["robotCode"]
	}
	return cfg
}

// loadCredential fetches the "default" row when a store is configured.
func loadCredential(cfg Config, store contracts.CredentialStore) Config {
	if store == nil {
		return cfg
	}
	credential, ok, err := store.Get(context.Background(), CredentialKind, "default")
	if err != nil || !ok {
		return cfg
	}
	return ApplyCredential(cfg, credential.Fields)
}
