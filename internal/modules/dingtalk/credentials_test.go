package dingtalk_test

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/modules/dingtalk"
)

func TestApplyCredentialFillsOnlyEmptyFields(t *testing.T) {
	cfg := dingtalk.Config{Token: "explicit", RobotCode: ""}
	got := dingtalk.ApplyCredential(cfg, map[string]string{
		"token": "stored", "robotCode": "robot-1",
	})
	if got.Token != "explicit" {
		t.Fatalf("token = %q, want the explicit value to win", got.Token)
	}
	if got.RobotCode != "robot-1" {
		t.Fatalf("cfg = %+v, want the stored robot code filled", got)
	}
}

func TestApplyCredentialIgnoresUnknownFields(t *testing.T) {
	got := dingtalk.ApplyCredential(dingtalk.Config{}, map[string]string{"nonsense": "x"})
	if got.Token != "" || got.RobotCode != "" {
		t.Fatalf("cfg = %+v, want nothing applied", got)
	}
}

// A row written before the demotion may still carry signSecret. It must be
// ignored — not applied, not an error: the adapter has no callback left to
// verify, and old credential rows must keep loading.
func TestApplyCredentialIgnoresTheHistoricalSignSecret(t *testing.T) {
	got := dingtalk.ApplyCredential(dingtalk.Config{}, map[string]string{
		"token": "stored", "signSecret": "leftover",
	})
	if got.Token != "stored" {
		t.Fatalf("token = %q, want the stored token", got.Token)
	}
	if got != (dingtalk.Config{Token: "stored"}) {
		t.Fatalf("cfg = %+v, want signSecret to leave no trace in the config", got)
	}
}
