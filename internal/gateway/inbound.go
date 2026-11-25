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
	service, ok := g.root.Get(contracts.KeyWebhook)
	if !ok || service == nil {
		http.NotFound(w, r)
		return
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

	result, err := service.Receive(r.Context(), sourceID, r.Header.Get(contracts.WebhookSignatureHeader), body)
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
	default:
		return http.StatusInternalServerError
	}
}
