package feishu_test

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/modules/feishu"
)

func TestApplyCredentialFillsOnlyEmptyFields(t *testing.T) {
	cfg := feishu.Config{AppID: "explicit-app"}
	got := feishu.ApplyCredential(cfg, map[string]string{
		"appId": "stored-app", "appSecret": "stored-secret", "encryptKey": "k",
	})
	if got.AppID != "explicit-app" {
		t.Fatalf("appId = %q, want explicit to win", got.AppID)
	}
	if got.AppSecret != "stored-secret" || got.EncryptKey != "k" {
		t.Fatalf("cfg = %+v", got)
	}
}

func TestApplyCredentialIgnoresUnknownFields(t *testing.T) {
	got := feishu.ApplyCredential(feishu.Config{}, map[string]string{"nonsense": "x"})
	if got.AppSecret != "" || got.EncryptKey != "" {
		t.Fatalf("cfg = %+v, want nothing applied", got)
	}
}
