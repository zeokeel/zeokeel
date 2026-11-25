package gateway

// Inbound webhook endpoint. It is the one HTTP surface that does not take the
// browser token: the sender proves itself with an HMAC signature, which the
// webhook adapter verifies. Everything else stays strict — the Host must be
// ours, a browser Origin is refused outright, the body has a hard cap, and the
// adapter's typed errors map to honest status codes.

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// inboundMaxBody caps a delivery before any parsing (1 MiB is far more than a
// webhook needs and small enough to bound memory).
const inboundMaxBody = 1 << 20

// inboundSourceID extracts the source segment from /api/inbound/<source>.
func inboundSourceID(path string) string {
	rest := strings.TrimPrefix(path, "/api/inbound/")
	rest = strings.Trim(rest, "/")
	if rest == "" || strings.Contains(rest, "/") {
		return ""
	}
	return rest
}

// handleInbound serves POST /api/inbound/<source>.
func (g *Gateway) handleInbound(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Host check is kept: a DNS-rebinding page must not reach a signed endpoint
	// either. The token is not required — the signature replaces it — but a
	// browser Origin means a page is driving the request, which a webhook
	// sender never does.
	if err := g.auth.checkHost(r); err != nil {
		writeProtocolError(w, http.StatusForbidden, err)
		return
	}
	if strings.TrimSpace(r.Header.Get("Origin")) != "" {
		writeProtocolError(w, http.StatusForbidden, appError(codeBadOrigin, "bad_origin", false))
		return
	}

	sourceID := inboundSourceID(r.URL.Path)
	if sourceID == "" {
		http.NotFound(w, r)
		return
	}
	var webhook contracts.WebhookService
	if sourceID != "dingtalk" && sourceID != "feishu" {
		service, ok := g.root.Get(contracts.KeyWebhook)
		if !ok || service == nil {
			http.NotFound(w, r)
			return
		}
		webhook = service
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, inboundMaxBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	switch sourceID {
	case "dingtalk":
		g.handleDingTalkInbound(w, r, body)
		return
	case "feishu":
		g.handleFeishuInbound(w, r, body)
		return
	}

	result, err := webhook.Receive(r.Context(), sourceID, r.Header.Get(contracts.WebhookSignatureHeader), body)
	if err != nil {
		status := inboundStatus(err)
		if status == http.StatusInternalServerError {
			g.logger.Printf("gateway: inbound %q failed: %v", sourceID, err)
		}
		http.Error(w, err.Error(), status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":     result.Item.ID,
		"merged": result.Merged,
		"count":  result.Item.Count,
	})
}

// headerMap copies the request headers into the adapter's case-insensitive map.
func headerMap(r *http.Request) map[string]string {
	out := make(map[string]string, len(r.Header))
	for key, values := range r.Header {
		if len(values) > 0 {
			out[key] = values[0]
		}
	}
	return out
}

// handleDingTalkInbound serves the DingTalk chat-bot callback.
func (g *Gateway) handleDingTalkInbound(w http.ResponseWriter, r *http.Request, body []byte) {
	service, ok := g.root.Get(contracts.KeyDingTalk)
	if !ok || service == nil {
		http.NotFound(w, r)
		return
	}
	item, merged, err := service.Receive(r.Context(), headerMap(r), body)
	if err != nil {
		http.Error(w, err.Error(), inboundStatus(err))
		return
	}
	writeInboundAccepted(w, item.ID, merged, item.Count)
}

// handleFeishuInbound serves the Feishu event callback. The URL handshake must
// be answered 200 with the challenge; every other payload is a message.
func (g *Gateway) handleFeishuInbound(w http.ResponseWriter, r *http.Request, body []byte) {
	service, ok := g.root.Get(contracts.KeyFeishu)
	if !ok || service == nil {
		http.NotFound(w, r)
		return
	}
	if challenge, ok := service.VerifyURL(body); ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"challenge": challenge})
		return
	}
	item, merged, err := service.Receive(r.Context(), headerMap(r), body)
	if err != nil {
		http.Error(w, err.Error(), inboundStatus(err))
		return
	}
	if item.ID == "" {
		// A verified but irrelevant event (wrong event type, non-text message).
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeInboundAccepted(w, item.ID, merged, item.Count)
}

// writeInboundAccepted answers a delivery that reached the inbox.
func writeInboundAccepted(w http.ResponseWriter, id string, merged bool, count int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "merged": merged, "count": count})
}

// inboundStatus maps the adapter's typed errors to HTTP codes.
func inboundStatus(err error) int {
	switch {
	case errors.Is(err, contracts.ErrWebhookUnknownSource):
		return http.StatusNotFound
	case errors.Is(err, contracts.ErrWebhookBadSignature), errors.Is(err, contracts.ErrWebhookStale):
		return http.StatusUnauthorized
	case errors.Is(err, contracts.ErrWebhookBadPayload):
		return http.StatusBadRequest
	case errors.Is(err, contracts.ErrInboxRateLimited), errors.Is(err, contracts.ErrInboxFull):
		return http.StatusTooManyRequests
	case errors.Is(err, contracts.ErrDingTalkBadSignature), errors.Is(err, contracts.ErrDingTalkStaleTimestamp),
		errors.Is(err, contracts.ErrDingTalkUnauthorized),
		errors.Is(err, contracts.ErrFeishuBadSignature), errors.Is(err, contracts.ErrFeishuStale),
		errors.Is(err, contracts.ErrFeishuUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, contracts.ErrDingTalkInvalid), errors.Is(err, contracts.ErrDingTalkUnsupportedMessage),
		errors.Is(err, contracts.ErrDingTalkEmptyMessage),
		errors.Is(err, contracts.ErrFeishuBadPayload), errors.Is(err, contracts.ErrFeishuEmptyMessage):
		return http.StatusBadRequest
	case errors.Is(err, contracts.ErrDingTalkUnavailable), errors.Is(err, contracts.ErrFeishuUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// handleFeishuCard serves a card-button callback. The card carries the approval
// id, the decision and the one-time token; the adapter verifies the signature and
// extracts all three, and this handler applies them through the approval action
// service — the card never bypasses the token check.
func (g *Gateway) handleFeishuCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := g.auth.checkHost(r); err != nil {
		writeProtocolError(w, http.StatusForbidden, err)
		return
	}
	if strings.TrimSpace(r.Header.Get("Origin")) != "" {
		writeProtocolError(w, http.StatusForbidden, appError(codeBadOrigin, "bad_origin", false))
		return
	}
	feishuService, ok := g.root.Get(contracts.KeyFeishu)
	if !ok || feishuService == nil {
		http.NotFound(w, r)
		return
	}
	verifier, ok := feishuService.(contracts.FeishuRequestVerifier)
	if !ok {
		http.NotFound(w, r)
		return
	}
	extractor, ok := feishuService.(contracts.FeishuCardActionReader)
	if !ok {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, inboundMaxBody))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if err := verifier.VerifyRequest(headerMap(r), body); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	approvalID, decision, token, ok := extractor.CardActionWithToken(body)
	if !ok {
		http.Error(w, "card callback carries no approval decision", http.StatusBadRequest)
		return
	}
	actionService, ok := g.root.Get(contracts.KeyApprovalAction)
	if !ok || actionService == nil {
		http.NotFound(w, r)
		return
	}
	if err := actionService.Apply(r.Context(), approvalID, contracts.ApprovalDecision(decision), token); err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, contracts.ErrApprovalTokenInvalid), errors.Is(err, contracts.ErrApprovalTokenExpired),
			errors.Is(err, contracts.ErrApprovalTokenUsed):
			status = http.StatusForbidden
		case errors.Is(err, contracts.ErrApprovalNotPending):
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
