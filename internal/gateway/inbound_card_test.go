package gateway

// Card buttons are an approval surface: the callback must be signature-verified,
// must carry the one-time token, and must be applied through the approval action
// service (never by calling the approvals queue directly).

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeCardFeishu struct {
	verifyErr error
	approval  string
	decision  string
	token     string
	hasAction bool
}

func (f *fakeCardFeishu) VerifyURL([]byte) (string, bool) { return "", false }
func (f *fakeCardFeishu) Receive(context.Context, map[string]string, []byte) (contracts.InboxItem, bool, error) {
	return contracts.InboxItem{}, false, nil
}
func (f *fakeCardFeishu) CardAction([]byte) (string, string, bool)   { return "", "", false }
func (f *fakeCardFeishu) Send(context.Context, string, string) error { return nil }
func (f *fakeCardFeishu) Status(context.Context) (contracts.FeishuStatus, error) {
	return contracts.FeishuStatus{}, nil
}
func (f *fakeCardFeishu) VerifyRequest(map[string]string, []byte) error { return f.verifyErr }
func (f *fakeCardFeishu) CardActionWithToken([]byte) (string, string, string, bool) {
	if !f.hasAction {
		return "", "", "", false
	}
	return f.approval, f.decision, f.token, true
}

type fakeApprovalAction struct {
	mu       sync.Mutex
	applied  []string
	tokenErr error
}

func (f *fakeApprovalAction) IssueToken(string) (string, time.Time, error) {
	return "t", time.Now(), nil
}
func (f *fakeApprovalAction) Apply(_ context.Context, approvalID string, decision contracts.ApprovalDecision, token string) error {
	if f.tokenErr != nil {
		return f.tokenErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, approvalID+"|"+string(decision)+"|"+token)
	return nil
}
func (f *fakeApprovalAction) ApplyReply(context.Context, string) (contracts.ReplyOutcome, error) {
	return contracts.ReplyOutcome{}, nil
}

func startCardGateway(t *testing.T, feishu contracts.FeishuService, action contracts.ApprovalActionService) *runningGateway {
	t.Helper()
	root := testRoot(t, newFakeChat())
	if _, err := root.Provide(contracts.KeyFeishu, feishu); err != nil {
		t.Fatalf("provide feishu: %v", err)
	}
	if _, err := root.Provide(contracts.KeyApprovalAction, action); err != nil {
		t.Fatalf("provide approval action: %v", err)
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	return serveGateway(t, gateway)
}

func TestFeishuCardAppliesTheDecision(t *testing.T) {
	feishu := &fakeCardFeishu{approval: "req-9", decision: "allow", token: "tok", hasAction: true}
	action := &fakeApprovalAction{}
	gw := startCardGateway(t, feishu, action)
	defer gw.stop(t)

	response := postInbound(t, gw.address, "/api/inbound/feishu/card", `{}`, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if len(action.applied) != 1 || action.applied[0] != "req-9|allow|tok" {
		t.Fatalf("applied = %v", action.applied)
	}
}

func TestFeishuCardRejectsABadSignatureBeforeApplying(t *testing.T) {
	feishu := &fakeCardFeishu{verifyErr: contracts.ErrFeishuBadSignature, approval: "req-9", decision: "allow", token: "tok", hasAction: true}
	action := &fakeApprovalAction{}
	gw := startCardGateway(t, feishu, action)
	defer gw.stop(t)

	if response := postInbound(t, gw.address, "/api/inbound/feishu/card", `{}`, nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.StatusCode)
	}
	if len(action.applied) != 0 {
		t.Fatal("a card with a bad signature reached the approval service")
	}
}

func TestFeishuCardWithoutDecisionIsBadRequest(t *testing.T) {
	feishu := &fakeCardFeishu{}
	action := &fakeApprovalAction{}
	gw := startCardGateway(t, feishu, action)
	defer gw.stop(t)
	if response := postInbound(t, gw.address, "/api/inbound/feishu/card", `{}`, nil); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
}

func TestFeishuCardTokenRejectionIsForbidden(t *testing.T) {
	feishu := &fakeCardFeishu{approval: "req-9", decision: "deny", token: "used", hasAction: true}
	action := &fakeApprovalAction{tokenErr: contracts.ErrApprovalTokenUsed}
	gw := startCardGateway(t, feishu, action)
	defer gw.stop(t)
	if response := postInbound(t, gw.address, "/api/inbound/feishu/card", `{}`, nil); response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.StatusCode)
	}
}

var _ = errors.Is
