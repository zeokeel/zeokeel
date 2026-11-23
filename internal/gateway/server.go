package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// HTTP surface (docs/TRANSPORT.md §3.2, §4.2).
//
//	GET  /            the client bundle (the gateway serves it itself, so the
//	                  client is always same-origin and no CORS header exists)
//	POST /auth        fragment token → HttpOnly cookie
//	GET  /ws          the state plane (cookie or Bearer required)
//	POST /quit        graceful shutdown, used by the tray shell
//
// Static assets do not require the token (the first paint cannot carry one),
// but Host is still checked, which is what closes DNS rebinding.
// handler builds the HTTP surface.
//
// The routing is one explicit switch rather than a ServeMux pattern set: each
// entry states its method, and every data-bearing route calls
// authorizeRequest before it does anything else, so "which handlers check the
// token?" is answered by reading this function.
func (g *Gateway) handler() http.Handler {
	inner := g.securityHeaders(http.HandlerFunc(g.route))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.handlers.Add(1)
		defer g.handlers.Done()
		inner.ServeHTTP(w, r)
	})
}

// route dispatches one request.
func (g *Gateway) route(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/auth" && r.Method == http.MethodPost:
		g.handleAuth(w, r)
	case r.URL.Path == "/ws" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		g.handleWebSocket(w, r)
	case r.URL.Path == "/quit" && r.Method == http.MethodPost:
		g.handleQuit(w, r)
	case r.URL.Path == "/api/chat/run" && r.Method == http.MethodPost:
		g.handleChatRun(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/"):
		// Unknown API path: authenticated first (see handleUnknownAPI), so an
		// unauthenticated probe cannot map the endpoint surface.
		g.handleUnknownAPI(w, r)
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		// The client bundle. Static assets are not sensitive and the first
		// paint cannot carry a token; Host is still checked inside.
		g.handleStatic(w, r)
	default:
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// securityHeaders adds the response headers every reply carries (§3.2).
func (g *Gateway) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		// default-src 'self' as specified (§3.2). The two additions are what a
		// same-origin SPA build actually needs: antd injects its styles at
		// runtime (style-src 'unsafe-inline'), and data:/blob: images are how
		// attachments and generated previews are displayed.
		// script-src stays 'self' with no unsafe-inline/unsafe-eval: the client
		// renders model output (markdown), so script injection is the surface
		// that matters. frame-ancestors 'none' is the CSP replacement for
		// X-Frame-Options.
		header.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; "+
				"object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

// handleAuth exchanges the fragment token for the HttpOnly cookie (§3.1).
func (g *Gateway) handleAuth(w http.ResponseWriter, r *http.Request) {
	source := remoteSource(r)
	if g.auth.inCooldown(source) {
		w.Header().Set("Retry-After", "60")
		writeProtocolError(w, http.StatusTooManyRequests, appError(codeUnauthorized, "unauthorized", true))
		return
	}
	if err := g.auth.checkHost(r); err != nil {
		writeProtocolError(w, http.StatusForbidden, err)
		return
	}
	if originErr := g.auth.checkOrigin(r); originErr != nil && r.Header.Get("Origin") != "" {
		writeProtocolError(w, http.StatusForbidden, originErr)
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeProtocolError(w, http.StatusBadRequest, errInvalidRequest("body is not valid JSON"))
		return
	}
	if !g.auth.tokenMatches(strings.TrimSpace(body.Token)) {
		g.auth.recordFailure(source)
		w.Header().Set("Retry-After", "1")
		writeProtocolError(w, http.StatusUnauthorized, appError(codeUnauthorized, "unauthorized", false))
		return
	}
	g.auth.clearFailures(source)
	http.SetCookie(w, g.auth.cookie())
	w.WriteHeader(http.StatusNoContent)
}

// handleUnknownAPI answers an unknown /api path with 404 JSON, after the same
// authentication every real endpoint requires.
func (g *Gateway) handleUnknownAPI(w http.ResponseWriter, r *http.Request) {
	if err := g.authorizeRequest(r, true); err != nil {
		status := http.StatusUnauthorized
		if err.Code == codeBadOrigin || err.Data["name"] == "bad_host" {
			status = http.StatusForbidden
		}
		writeProtocolError(w, status, err)
		return
	}
	writeProtocolError(w, http.StatusNotFound, errMethodNotFound(r.URL.Path))
}

// handleQuit stops the gateway on request from the tray shell. It is
// authenticated like every other write: SameSite=Strict + token.
func (g *Gateway) handleQuit(w http.ResponseWriter, r *http.Request) {
	if err := g.authorizeRequest(r, true); err != nil {
		writeProtocolError(w, http.StatusUnauthorized, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	g.requestShutdown("quit requested")
}

// handleWebSocket upgrades the state plane connection.
func (g *Gateway) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	source := remoteSource(r)
	if g.auth.inCooldown(source) {
		w.Header().Set("Retry-After", "60")
		writeProtocolError(w, http.StatusTooManyRequests, appError(codeUnauthorized, "unauthorized", true))
		return
	}
	if err := g.authorizeRequest(r, true); err != nil {
		g.auth.recordFailure(source)
		w.Header().Set("Retry-After", "1")
		status := http.StatusUnauthorized
		if err.Code == codeBadOrigin || err.Data["name"] == "bad_host" {
			status = http.StatusForbidden
		}
		writeProtocolError(w, status, err)
		return
	}
	conn, err := wsUpgrade(w, r, MaxFrameBytes)
	if err != nil {
		http.Error(w, "websocket upgrade failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	g.auth.clearFailures(source)
	target := newClient(g, conn)
	if err := g.hub.registerClient(g.clientContext(), target); err != nil {
		_ = conn.Close()
		return
	}
	go target.writeLoop()
	target.readLoop(g.clientContext())
	target.close()
}

// authorizeRequest runs the ordered checks: Host, then Origin, then token.
func (g *Gateway) authorizeRequest(r *http.Request, requireToken bool) *protocolError {
	if err := g.auth.checkHost(r); err != nil {
		return err
	}
	if err := g.auth.checkOrigin(r); err != nil {
		return err
	}
	if requireToken {
		return g.auth.checkToken(r)
	}
	return nil
}

// writeProtocolError writes one JSON error body.
func writeProtocolError(w http.ResponseWriter, status int, err *protocolError) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    err.Code,
			"message": err.Message,
			"data":    err.Data,
		},
	})
}

// shutdownWait bounds how long shutdown waits for clients to flush.
const shutdownWait = 2 * time.Second
