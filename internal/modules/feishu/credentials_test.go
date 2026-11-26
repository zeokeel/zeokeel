package feishu_test

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/modules/feishu"
)

func TestApplyCredentialFillsOnlyEmptyFields(t *testing.T) {
	cfg := feishu.Config{AppID: "explicit-app"}
	got := feishu.ApplyCredential(cfg, map[string]string{
		"appId": "stored-app", "appSecret": "stored-secret",
	})
	if got.AppID != "explicit-app" {
		t.Fatalf("appId = %q, want explicit to win", got.AppID)
	}
	if got.AppSecret != "stored-secret" {
		t.Fatalf("cfg = %+v", got)
	}
}

func TestApplyCredentialIgnoresUnknownFields(t *testing.T) {
	got := feishu.ApplyCredential(feishu.Config{}, map[string]string{"nonsense": "x"})
	if got.AppSecret != "" {
		t.Fatalf("cfg = %+v, want nothing applied", got)
	}
}

// A row written before the demotion may still carry encryptKey. It must be
// ignored — not applied, not an error: there is no event callback left whose
// signature it could cover, and old credential rows must keep loading.
func TestApplyCredentialIgnoresTheHistoricalEncryptKey(t *testing.T) {
	got := feishu.ApplyCredential(feishu.Config{}, map[string]string{
		"appId": "stored-app", "appSecret": "stored-secret", "encryptKey": "leftover",
	})
	want := feishu.Config{AppID: "stored-app", AppSecret: "stored-secret"}
	if got != want {
		t.Fatalf("cfg = %+v, want encryptKey to leave no trace", got)
	}
}
