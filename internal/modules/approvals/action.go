package approvals

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// approvalSource is the narrow view of the approvals queue the action layer
// needs. Both the real service and the tests satisfy it.
type approvalSource interface {
	Pending() []contracts.ApprovalRequest
	Resolve(id string, decision contracts.ApprovalDecision) error
}

// actionConfig carries the clock and the signing secret (tests inject both).
type actionConfig struct {
	Now    func() time.Time
	Secret []byte
	TTL    time.Duration
}

// actionService is the contracts.ApprovalActionService implementation.
type actionService struct {
	mu     sync.Mutex
	source approvalSource
	cfg    actionConfig
	// used remembers tokens that already answered a request (single use).
	used map[string]bool
}

func newActionService(source approvalSource, cfg actionConfig) *actionService {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.TTL <= 0 {
		cfg.TTL = contracts.ApprovalTokenTTL
	}
	return &actionService{source: source, cfg: cfg, used: make(map[string]bool)}
}

// IssueToken implements contracts.ApprovalActionService.
func (s *actionService) IssueToken(approvalID string) (string, time.Time, error) {
	if approvalID == "" {
		return "", time.Time{}, contracts.ErrApprovalNotPending
	}
	pending := false
	for _, request := range s.source.Pending() {
		if request.ID == approvalID {
			pending = true
			break
		}
	}
	if !pending {
		return "", time.Time{}, contracts.ErrApprovalNotPending
	}

	expiry := s.cfg.Now().Add(s.cfg.TTL)
	encodedID := base64.RawURLEncoding.EncodeToString([]byte(approvalID))
	payload := encodedID + "." + strconv.FormatInt(expiry.Unix(), 10)
	token := payload + "." + s.sign(payload, approvalID)
	return token, expiry, nil
}

// Apply implements contracts.ApprovalActionService.
func (s *actionService) Apply(ctx context.Context, approvalID string, decision contracts.ApprovalDecision, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	boundID, expiry, err := s.verifyToken(token)
	if err != nil {
		return err
	}
	if boundID != approvalID {
		return contracts.ErrApprovalTokenInvalid
	}
	if !s.cfg.Now().Before(expiry) {
		return contracts.ErrApprovalTokenExpired
	}

	s.mu.Lock()
	if s.used[token] {
		s.mu.Unlock()
		return contracts.ErrApprovalTokenUsed
	}
	s.mu.Unlock()

	pending := false
	for _, request := range s.source.Pending() {
		if request.ID == approvalID {
			pending = true
			break
		}
	}
	if !pending {
		return contracts.ErrApprovalNotPending
	}
	if err := s.source.Resolve(approvalID, decision); err != nil {
		return err
	}

	s.mu.Lock()
	s.used[token] = true
	s.mu.Unlock()
	return nil
}

// ApplyReply implements contracts.ApprovalActionService. Chat replies are
// authenticated by the channel binding (only the owner's messages reach here),
// so they do not carry a token; they still go through the same Resolve.
func (s *actionService) ApplyReply(ctx context.Context, text string) (contracts.ReplyOutcome, error) {
	if err := ctx.Err(); err != nil {
		return contracts.ReplyOutcome{}, err
	}
	pending := s.source.Pending()
	if len(pending) == 0 {
		return contracts.ReplyOutcome{}, contracts.ErrApprovalNoPending
	}
	decision, ok := ParseReply(text, len(pending))
	if !ok {
		return contracts.ReplyOutcome{}, nil
	}
	index := 1
	if decision.Index > 0 {
		index = decision.Index
	}
	if index > len(pending) {
		return contracts.ReplyOutcome{}, nil
	}
	target := pending[index-1]

	verdict := contracts.ApprovalAllow
	if decision.Kind == DecisionReject {
		verdict = contracts.ApprovalDeny
	}
	if err := s.source.Resolve(target.ID, verdict); err != nil {
		return contracts.ReplyOutcome{}, err
	}
	return contracts.ReplyOutcome{Applied: true, ApprovalID: target.ID, Decision: verdict}, nil
}

// sign computes the truncated HMAC over "payload|approvalID" so a token cannot
// be moved to another request.
func (s *actionService) sign(payload, approvalID string) string {
	mac := hmac.New(sha256.New, s.cfg.Secret)
	mac.Write([]byte(payload + "|" + approvalID))
	return hex.EncodeToString(mac.Sum(nil))
}

// verifyToken parses and authenticates a token, returning the bound approval id
// and its expiry. It does not check the expiry against the clock: Apply does,
// so callers can distinguish "expired" from "forged".
func (s *actionService) verifyToken(token string) (string, time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", time.Time{}, contracts.ErrApprovalTokenInvalid
	}
	rawID, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(rawID) == 0 {
		return "", time.Time{}, contracts.ErrApprovalTokenInvalid
	}
	approvalID := string(rawID)
	unix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", time.Time{}, contracts.ErrApprovalTokenInvalid
	}
	payload := parts[0] + "." + parts[1]
	expected := s.sign(payload, approvalID)
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return "", time.Time{}, contracts.ErrApprovalTokenInvalid
	}
	return approvalID, time.Unix(unix, 0), nil
}

var _ contracts.ApprovalActionService = (*actionService)(nil)

// describePending is a small helper for error messages and the channel layer.
func describePending(requests []contracts.ApprovalRequest) string {
	parts := make([]string, 0, len(requests))
	for i, request := range requests {
		parts = append(parts, fmt.Sprintf("%d) %s", i+1, request.Summary))
	}
	return strings.Join(parts, "；")
}
