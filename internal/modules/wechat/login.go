package wechat

// WeChat QR login: how a human actually gets the bot token that PollOnce, Send
// and Status all depend on.
//
// The flow is two calls, because the settings page has to render the code and
// then watch it:
//
//	StartLogin  → POST /ilink/bot/getloginqrcode  {qrcode, qrcode_url, expires_in}
//	LoginState  → POST /ilink/bot/getloginstate   {status, bot_token}
//
// LoginState maps the platform's loose status vocabulary onto the typed states
// and, once confirmed, runs the token through the existing SetToken seam so a
// QR login persists exactly like an explicitly configured token does. A login
// that cannot be persisted is *not* reported as confirmed: it would vanish on
// the next restart.
//
// The adapter also remembers every QR id it issued together with the deadline it
// promised the UI, so a confirmation that arrives after that deadline (the poll
// took long, or the user scanned a screenshot of an old code) is refused rather
// than silently logging in.
//
// Wire format: checked against the public iLink descriptions only. The happy
// path is covered by a fake iLink server; a real-device pass is still required,
// and the exact `status` strings (wait/scanned/…) are the part most likely to
// differ in the field.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// iLink login endpoints.
const (
	loginQRCodePath = "/ilink/bot/getloginqrcode"
	loginStatePath  = "/ilink/bot/getloginstate"
)

// loginDefaultTTL is the local deadline used when the platform does not send
// expires_in. The platform still enforces the real one; this only keeps the UI
// from polling a code forever.
const loginDefaultTTL = 5 * time.Minute

// maxLoginBody caps a login response body (they are tiny; a hostile or broken
// proxy must not be able to stream forever).
const maxLoginBody = 1 << 20

// StartLogin implements contracts.WeChatLoginService.
func (a *Adapter) StartLogin(ctx context.Context) (contracts.WeChatLogin, error) {
	if err := ctx.Err(); err != nil {
		return contracts.WeChatLogin{}, err
	}
	raw, err := a.loginPost(ctx, loginQRCodePath, map[string]any{})
	if err != nil {
		return contracts.WeChatLogin{}, err
	}

	// Some deployments wrap the payload in {"data": {...}}; both shapes are
	// decoded, and the flat fields win when both are present.
	var envelope struct {
		loginQRCodePayload
		Data loginQRCodePayload `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return contracts.WeChatLogin{}, fmt.Errorf("%w: decode qr code: %v", contracts.ErrWeChatLoginUnavailable, err)
	}
	payload := envelope.loginQRCodePayload
	payload.fillFrom(envelope.Data)
	if payload.QRCode == "" || payload.QRCodeURL == "" {
		// A code we cannot display or poll is useless: never hand it to the UI.
		return contracts.WeChatLogin{}, fmt.Errorf("%w: incomplete qr code (qrcode=%q, qrcode_url=%q)",
			contracts.ErrWeChatLoginUnavailable, payload.QRCode, payload.QRCodeURL)
	}

	ttl := loginDefaultTTL
	if payload.ExpiresIn > 0 {
		ttl = time.Duration(payload.ExpiresIn) * time.Second
	}
	login := contracts.WeChatLogin{
		QRCodeURL: payload.QRCodeURL,
		QRCodeID:  payload.QRCode,
		ExpiresAt: a.cfg.Now().Add(ttl),
	}
	a.rememberLogin(login.QRCodeID, login.ExpiresAt)
	return login, nil
}

// LoginState implements contracts.WeChatLoginService.
//
// Expiry is reported both as a state and as an error wrapping
// contracts.ErrWeChatLoginExpired: a polling loop that only checks err stops,
// and a caller that reads the state can still tell "expired" from "broken".
func (a *Adapter) LoginState(ctx context.Context, qrID string) (contracts.WeChatLoginState, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	qrID = strings.TrimSpace(qrID)
	expiresAt, known := a.loginDeadline(qrID)
	switch {
	case qrID == "" || !known:
		// Not ours: no platform call, no state to report. This also covers an id
		// left over from a previous process (the map does not survive a restart).
		return contracts.WeChatLoginExpired, "", fmt.Errorf("%w: %q was not issued by this adapter",
			contracts.ErrWeChatLoginExpired, qrID)
	case a.cfg.Now().After(expiresAt):
		a.forgetLogin(qrID)
		return contracts.WeChatLoginExpired, "", fmt.Errorf("%w: %q timed out at %s",
			contracts.ErrWeChatLoginExpired, qrID, expiresAt.UTC().Format(time.RFC3339))
	}

	raw, err := a.loginPost(ctx, loginStatePath, map[string]any{"qrcode": qrID})
	if err != nil {
		return "", "", err
	}
	var envelope struct {
		loginStatePayload
		Data loginStatePayload `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", "", fmt.Errorf("%w: decode login state: %v", contracts.ErrWeChatLoginUnavailable, err)
	}
	payload := envelope.loginStatePayload
	payload.fillFrom(envelope.Data)

	switch payload.Status {
	case "wait", "pending":
		return contracts.WeChatLoginPending, "", nil
	case "scanned", "waiting":
		return contracts.WeChatLoginWaiting, "", nil
	case "confirmed", "ok":
		// The request took time. A code that expired while this answer was in
		// flight must not become a live login — that is what the stored deadline
		// is for.
		if a.cfg.Now().After(expiresAt) {
			a.forgetLogin(qrID)
			return contracts.WeChatLoginExpired, "", fmt.Errorf("%w: %q timed out while confirming",
				contracts.ErrWeChatLoginExpired, qrID)
		}
		if payload.BotToken == "" {
			// A confirmed login without a token is a broken answer, not a state.
			return contracts.WeChatLoginConfirmed, "", fmt.Errorf("%w: confirmed without a bot token",
				contracts.ErrWeChatLoginUnavailable)
		}
		if err := a.SetToken(payload.BotToken); err != nil {
			// The token did not stick, so the login did not happen. Reporting
			// confirmed here would show a connected UI that dies on restart.
			return contracts.WeChatLoginConfirmed, "", err
		}
		a.forgetLogin(qrID) // one-shot: a confirmed id cannot be replayed
		return contracts.WeChatLoginConfirmed, payload.BotToken, nil
	case "expired":
		a.forgetLogin(qrID)
		return contracts.WeChatLoginExpired, "", fmt.Errorf("%w: %q expired at the platform",
			contracts.ErrWeChatLoginExpired, qrID)
	case "":
		// No status at all is not a state we can act on; treat it as dead rather
		// than keeping the UI polling a code that will never confirm. The id is
		// kept, so a transiently bad answer can still be re-polled.
		return contracts.WeChatLoginExpired, "", fmt.Errorf("%w: %q: the platform sent no status",
			contracts.ErrWeChatLoginExpired, qrID)
	default:
		// An unknown *non-empty* status means we do not understand the answer;
		// calling it expired would end a login that may still be in progress.
		return "", "", fmt.Errorf("%w: unexpected login status %q",
			contracts.ErrWeChatLoginUnavailable, payload.Status)
	}
}

// loginQRCodePayload is one getloginqrcode answer.
type loginQRCodePayload struct {
	QRCode    string `json:"qrcode"`
	QRCodeURL string `json:"qrcode_url"`
	ExpiresIn int    `json:"expires_in"`
}

// fillFrom copies any field this payload is missing from a fallback (the
// {"data": {...}} envelope), so either shape works and a mixed answer still
// yields a usable code.
func (p *loginQRCodePayload) fillFrom(fallback loginQRCodePayload) {
	p.QRCode = strings.TrimSpace(p.QRCode)
	p.QRCodeURL = strings.TrimSpace(p.QRCodeURL)
	if p.QRCode == "" {
		p.QRCode = strings.TrimSpace(fallback.QRCode)
	}
	if p.QRCodeURL == "" {
		p.QRCodeURL = strings.TrimSpace(fallback.QRCodeURL)
	}
	if p.ExpiresIn <= 0 {
		p.ExpiresIn = fallback.ExpiresIn
	}
}

// loginStatePayload is one getloginstate answer. Status stays a raw string: the
// platform vocabulary is mapped in LoginState, and unknown values must not crash.
type loginStatePayload struct {
	Status   string `json:"status"`
	BotToken string `json:"bot_token"`
}

func (p *loginStatePayload) fillFrom(fallback loginStatePayload) {
	p.Status = strings.ToLower(strings.TrimSpace(p.Status))
	p.BotToken = strings.TrimSpace(p.BotToken)
	if p.Status == "" {
		p.Status = strings.ToLower(strings.TrimSpace(fallback.Status))
	}
	if p.BotToken == "" {
		p.BotToken = strings.TrimSpace(fallback.BotToken)
	}
}

// loginPost posts one login request and returns the raw body. Login runs before
// a token exists, so no Authorization header is sent; failures are typed like
// the rest of the adapter (401/403 = unauthorized, everything else = login
// unavailable).
func (a *Adapter) loginPost(ctx context.Context, path string, payload any) ([]byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: encode: %v", contracts.ErrWeChatLoginUnavailable, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(a.cfg.BaseURL, "/")+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", contracts.ErrWeChatLoginUnavailable, err)
	}
	request.Header.Set("Content-Type", "application/json")
	setUINHeader(request)

	response, err := a.cfg.Client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", contracts.ErrWeChatLoginUnavailable, err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, contracts.ErrWeChatUnauthorized
	default:
		return nil, fmt.Errorf("%w: status %d", contracts.ErrWeChatLoginUnavailable, response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxLoginBody))
	if err != nil {
		return nil, fmt.Errorf("%w: read: %v", contracts.ErrWeChatLoginUnavailable, err)
	}
	return raw, nil
}

// rememberLogin records an issued QR id and its deadline. It also drops dead
// entries, so a long-lived process that starts many logins cannot grow the map
// without bound.
func (a *Adapter) rememberLogin(qrID string, expiresAt time.Time) {
	now := a.cfg.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.logins == nil {
		a.logins = make(map[string]time.Time)
	}
	for id, deadline := range a.logins {
		if !now.Before(deadline) {
			delete(a.logins, id)
		}
	}
	a.logins[qrID] = expiresAt
}

// loginDeadline returns the deadline of a QR id this adapter issued.
func (a *Adapter) loginDeadline(qrID string) (time.Time, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	expiresAt, ok := a.logins[qrID]
	return expiresAt, ok
}

// forgetLogin drops a dead or already-used QR id.
func (a *Adapter) forgetLogin(qrID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.logins, qrID)
}

var _ contracts.WeChatLoginService = (*Adapter)(nil)
