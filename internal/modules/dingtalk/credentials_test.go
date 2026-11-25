package dingtalk_test

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/modules/dingtalk"
)

func TestApplyCredentialFillsOnlyEmptyFields(t *testing.T) {
	cfg := dingtalk.Config{Token: "explicit", RobotCode: ""}
	got := dingtalk.ApplyCredential(cfg, map[string]string{
		"token": "stored", "signSecret": "s3cret", "robotCode": "robot-1",
	})
	if got.Token != "explicit" {
		t.Fatalf("token = %q, want the explicit value to win", got.Token)
	}
	if got.SignSecret != "s3cret" || got.RobotCode != "robot-1" {
		t.Fatalf("cfg = %+v, want the stored fields filled", got)
	}
}

func TestApplyCredentialIgnoresUnknownFields(t *testing.T) {
	got := dingtalk.ApplyCredential(dingtalk.Config{}, map[string]string{"nonsense": "x"})
	if got.Token != "" || got.SignSecret != "" {
		t.Fatalf("cfg = %+v, want nothing applied", got)
	}
}
