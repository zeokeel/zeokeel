package feishu

// Credentials from the shared store: the settings page writes one row per
// channel and the adapter reads it at activation, so app secrets never live in an
// assembly document. Explicit configuration wins over the stored row.

import (
	"context"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// CredentialKind is the row kind this adapter reads.
const CredentialKind = "feishu"

// ApplyCredential fills the config from one credential row without overriding
// anything already set explicitly.
func ApplyCredential(cfg Config, fields map[string]string) Config {
	if cfg.AppID == "" {
		cfg.AppID = fields["appId"]
	}
	if cfg.AppSecret == "" {
		cfg.AppSecret = fields["appSecret"]
	}
	if cfg.EncryptKey == "" {
		cfg.EncryptKey = fields["encryptKey"]
	}
	if cfg.Token == "" {
		cfg.Token = fields["token"]
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
