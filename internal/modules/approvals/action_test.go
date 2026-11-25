package approvals

// TDD for the approval action layer: one semantic (answer a pending request)
// behind every entry point. The tests pin the security-relevant rules — tokens
// verify, expire and are single-use; a reply needs an explicit index when
// several requests wait.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeSource is the narrow approvals view the action layer needs.
type fakeSource struct {
	mu         sync.Mutex
	pending    []contracts.ApprovalRequest
	answers    map[string]contracts.ApprovalDecision
	resolveErr error
}

func newFakeSource(ids ...string) *fakeSource {
	f := &fakeSource{answers: map[string]contracts.ApprovalDecision{}}
	for _, id := range ids {
		f.pending = append(f.pending, contracts.ApprovalRequest{ID: id, Tool: "bash", Summary: "rm -rf " + id})
	}
	return f
}

func (f *fakeSource) Pending() []contracts.ApprovalRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.ApprovalRequest(nil), f.pending...)
}

func (f *fakeSource) Resolve(id string, decision contracts.ApprovalDecision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.resolveErr != nil {
		return f.resolveErr
	}
	for i, request := range f.pending {
		if request.ID == id {
			f.answers[id] = decision
			f.pending = append(f.pending[:i], f.pending[i+1:]...)
			return nil
		}
	}
	return contracts.ErrApprovalNotPending
}

func (f *fakeSource) answer(id string) (contracts.ApprovalDecision, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	decision, ok := f.answers[id]
	return decision, ok
}

type actionClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *actionClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *actionClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newAction(t *testing.T, source *fakeSource) (*actionService, *actionClock) {
	t.Helper()
	clock := &actionClock{t: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)}
	svc := newActionService(source, actionConfig{
		Now:    clock.now,
		Secret: []byte("test-secret"),
		TTL:    contracts.ApprovalTokenTTL,
	})
	return svc, clock
}

func TestIssueTokenShapeAndExpiry(t *testing.T) {
	svc, clock := newAction(t, newFakeSource("a1"))
	token, expires, err := svc.IssueToken("a1")
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if token == "" {
		t.Fatal("empty token")
	}
	if !expires.Equal(clock.now().Add(contracts.ApprovalTokenTTL)) {
		t.Fatalf("expiry = %v, want %v", expires, clock.now().Add(contracts.ApprovalTokenTTL))
	}
}

func TestApplyWithValidTokenResolves(t *testing.T) {
	source := newFakeSource("a1")
	svc, _ := newAction(t, source)
	token, _, err := svc.IssueToken("a1")
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if err := svc.Apply(context.Background(), "a1", contracts.ApprovalAllow, token); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if decision, ok := source.answer("a1"); !ok || decision != contracts.ApprovalAllow {
		t.Fatalf("answer = %v/%v, want allow", decision, ok)
	}
}

func TestApplyRejectsTamperedToken(t *testing.T) {
	source := newFakeSource("a1")
	svc, _ := newAction(t, source)
	token, _, _ := svc.IssueToken("a1")
	tampered := token[:len(token)-2] + "ff"
	if err := svc.Apply(context.Background(), "a1", contracts.ApprovalDeny, tampered); !errors.Is(err, contracts.ErrApprovalTokenInvalid) {
		t.Fatalf("err = %v, want ErrApprovalTokenInvalid", err)
	}
	if _, ok := source.answer("a1"); ok {
		t.Fatal("tampered token answered the request")
	}
}

func TestApplyRejectsTokenForAnotherApproval(t *testing.T) {
	source := newFakeSource("a1", "a2")
	svc, _ := newAction(t, source)
	token, _, _ := svc.IssueToken("a1")
	if err := svc.Apply(context.Background(), "a2", contracts.ApprovalAllow, token); !errors.Is(err, contracts.ErrApprovalTokenInvalid) {
		t.Fatalf("err = %v, want ErrApprovalTokenInvalid (token is bound to one approval)", err)
	}
}

func TestApplyRejectsExpiredToken(t *testing.T) {
	source := newFakeSource("a1")
	svc, clock := newAction(t, source)
	token, _, _ := svc.IssueToken("a1")
	clock.advance(contracts.ApprovalTokenTTL + time.Minute)
	if err := svc.Apply(context.Background(), "a1", contracts.ApprovalAllow, token); !errors.Is(err, contracts.ErrApprovalTokenExpired) {
		t.Fatalf("err = %v, want ErrApprovalTokenExpired", err)
	}
}

func TestApplyIsSingleUse(t *testing.T) {
	source := newFakeSource("a1")
	svc, _ := newAction(t, source)
	token, _, _ := svc.IssueToken("a1")
	if err := svc.Apply(context.Background(), "a1", contracts.ApprovalAllow, token); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if err := svc.Apply(context.Background(), "a1", contracts.ApprovalAllow, token); !errors.Is(err, contracts.ErrApprovalTokenUsed) {
		t.Fatalf("second apply err = %v, want ErrApprovalTokenUsed", err)
	}
}

func TestApplyRejectsRequestThatIsNotPending(t *testing.T) {
	source := newFakeSource("a1")
	svc, _ := newAction(t, source)
	token, _, _ := svc.IssueToken("a1")
	_ = source.Resolve("a1", contracts.ApprovalDeny) // answered elsewhere first
	if err := svc.Apply(context.Background(), "a1", contracts.ApprovalAllow, token); !errors.Is(err, contracts.ErrApprovalNotPending) {
		t.Fatalf("err = %v, want ErrApprovalNotPending", err)
	}
}

func TestApplyReplySinglePending(t *testing.T) {
	source := newFakeSource("a1")
	svc, _ := newAction(t, source)

	outcome, err := svc.ApplyReply(context.Background(), "同意")
	if err != nil {
		t.Fatalf("ApplyReply: %v", err)
	}
	if !outcome.Applied || outcome.ApprovalID != "a1" || outcome.Decision != contracts.ApprovalAllow {
		t.Fatalf("outcome = %+v, want applied allow on a1", outcome)
	}
}

func TestApplyReplyNeedsIndexWithSeveralPending(t *testing.T) {
	source := newFakeSource("a1", "a2")
	svc, _ := newAction(t, source)

	outcome, err := svc.ApplyReply(context.Background(), "同意")
	if err != nil {
		t.Fatalf("ApplyReply: %v", err)
	}
	if outcome.Applied {
		t.Fatalf("outcome = %+v, want not applied without an index", outcome)
	}
	indexed, err := svc.ApplyReply(context.Background(), "2 不同意")
	if err != nil {
		t.Fatalf("indexed ApplyReply: %v", err)
	}
	if !indexed.Applied || indexed.ApprovalID != "a2" || indexed.Decision != contracts.ApprovalDeny {
		t.Fatalf("indexed outcome = %+v, want deny on a2", indexed)
	}
}

func TestApplyReplyWithNothingPending(t *testing.T) {
	svc, _ := newAction(t, newFakeSource())
	if _, err := svc.ApplyReply(context.Background(), "同意"); !errors.Is(err, contracts.ErrApprovalNoPending) {
		t.Fatalf("err = %v, want ErrApprovalNoPending", err)
	}
}

func TestApplyReplyIgnoresUnrelatedText(t *testing.T) {
	source := newFakeSource("a1")
	svc, _ := newAction(t, source)
	outcome, err := svc.ApplyReply(context.Background(), "这个报价单改一下")
	if err != nil {
		t.Fatalf("ApplyReply: %v", err)
	}
	if outcome.Applied {
		t.Fatalf("outcome = %+v, want not applied", outcome)
	}
	if _, ok := source.answer("a1"); ok {
		t.Fatal("unrelated text answered the request")
	}
}

// TestApplyReplyRoundTripsThroughTheParser keeps the two halves honest: the
// action layer must reuse ParseReply rather than re-implementing a vocabulary.
func TestApplyReplyRoundTripsThroughTheParser(t *testing.T) {
	for _, text := range []string{"同意", "可以", "批准"} {
		source := newFakeSource("a1")
		svc, _ := newAction(t, source)
		outcome, err := svc.ApplyReply(context.Background(), text)
		if err != nil || !outcome.Applied {
			t.Fatalf("ApplyReply(%q) = (%+v, %v), want applied", text, outcome, err)
		}
	}
}

// guard: the token must not be a plain encoding of the approval id.
func TestTokenIsNotTheApprovalID(t *testing.T) {
	svc, _ := newAction(t, newFakeSource("a1"))
	token, _, _ := svc.IssueToken("a1")
	if token == "a1" || fmt.Sprintf("%x", hmacOf([]byte("test-secret"), []byte("a1"))) == token {
		t.Fatal("token looks like an unkeyed digest")
	}
	_ = sha256.Size
	_ = hex.EncodeToString
}

func hmacOf(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
