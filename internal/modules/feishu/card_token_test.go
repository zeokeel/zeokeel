package feishu_test

// The card callback carries the approval identity AND the one-time token: the
// approval layer must never trust an id alone from a webhook body.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/feishu"
)

func newCardAdapter() *feishu.Adapter {
	return feishu.NewAdapter(nil, feishu.Config{})
}

func TestCardActionWithTokenExtractsAllThree(t *testing.T) {
	adapter := newCardAdapter()
	body := []byte(`{"event":{"action":{"value":{"approvalId":"req-1","decision":"allow","token":"tok-1"}}}}`)
	approvalID, decision, token, ok := adapter.CardActionWithToken(body)
	if !ok || approvalID != "req-1" || decision != contracts.FeishuDecisionAllow || token != "tok-1" {
		t.Fatalf("got (%q,%q,%q,%v)", approvalID, decision, token, ok)
	}
}

func TestCardActionWithTokenNeedsTheToken(t *testing.T) {
	adapter := newCardAdapter()
	body := []byte(`{"action":{"value":{"approvalId":"req-1","decision":"deny"}}}`)
	if _, _, _, ok := adapter.CardActionWithToken(body); ok {
		t.Fatal("a card without a token must not be usable for approval")
	}
}

func TestCardActionWithTokenAcceptsStringValue(t *testing.T) {
	adapter := newCardAdapter()
	body := []byte(`{"action":{"value":"{\"approvalId\":\"req-2\",\"decision\":\"deny\",\"token\":\"tok-2\"}"}}`)
	approvalID, decision, token, ok := adapter.CardActionWithToken(body)
	if !ok || approvalID != "req-2" || decision != "deny" || token != "tok-2" {
		t.Fatalf("got (%q,%q,%q,%v)", approvalID, decision, token, ok)
	}
}

func TestVerifyRequestRejectsUnsignedCardBodies(t *testing.T) {
	adapter := feishu.NewAdapter(nil, feishu.Config{EncryptKey: "k"})
	if err := adapter.VerifyRequest(map[string]string{}, []byte(`{}`)); err == nil {
		t.Fatal("an unsigned card callback passed verification")
	}
}
