// Package feishu is the Feishu (Lark) open-platform adapter. The platform
// pushes to us, so the adapter is an HTTP ingress (signature-checked event
// callback, url verification, card action callback) plus one outbound call. It
// files inbound text in the inbox and delivers plain-text replies; it never
// decides what to say and never resolves an approval itself.
//
// Signature contract (checked against the public open-platform descriptions; a
// real-app pass is still required):
//
//   - Canonical scheme: X-Lark-Request-Timestamp, X-Lark-Request-Nonce and
//     X-Lark-Signature, where the signature is
//     hex(sha256(timestamp + nonce + encryptKey + rawBody)). The digest is over
//     the *raw* request bytes — re-encoding the JSON changes it.
//   - A timestamp more than contracts.FeishuMaxClockSkew away from our clock is
//     a replay: rejected with contracts.ErrFeishuStale.
//   - Legacy scheme, accepted only for older bots that cannot send a nonce:
//     hex(sha256(timestamp + encryptKey + rawBody)) with the timestamp header
//     still present. It keeps the skew check; it is narrower (same secret, one
//     fewer input) but not weaker against forgery. A request without a
//     timestamp header is always rejected, so no scheme is replayable forever.
//
// Encrypted pushes ({"encrypt": "<base64>"}, what the platform sends once an
// Encrypt Key is configured — for events and for the handshake alike) are refused
// with contracts.ErrFeishuBadPayload: the signature over the wrapper verifies, so
// acknowledging the body would look like a successful ingest while the event was
// never read. We do not implement the decrypt path.
package feishu

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// DefaultBaseURL is the Feishu open-platform host.
const DefaultBaseURL = "https://open.feishu.cn"

// maxTitleRunes keeps an inbox title scannable; the full text stays in Body.
const maxTitleRunes = 60

// tokenRefreshMargin renews the tenant token slightly before it expires so a
// long Send never races the expiry.
const tokenRefreshMargin = 60 * time.Second

// authErrorCodes are the platform codes that mean "the credential is wrong",
// reported with an HTTP 200 body instead of a 401. Mapping them keeps the
// caller's fallback logic ("try another channel") working.
var authErrorCodes = map[int]bool{
	99991661: true, // token missing
	99991663: true, // invalid tenant access token
	99991664: true, // invalid app access token
	99991668: true, // tenant token expired / app mismatch
	10003:    true, // invalid parameter (app_id/app_secret)
	10014:    true, // app secret invalid
}

// Config is the module's typed assembly config. Credentials are deliberately
// not part of the assembly document (json:"-"): they arrive from configuration
// once the settings/credential store persists them, exactly like the WeChat
// login token.
type Config struct {
	// BaseURL overrides the open-platform host (tests use a local server).
	BaseURL string `json:"baseUrl,omitempty"`
	// AppID is the app identity (safe to keep in the assembly document).
	AppID string `json:"appId,omitempty"`
	// AppSecret mints the tenant access token ("" until configured).
	AppSecret string `json:"-"`
	// Token is a pre-issued tenant access token. When set it wins over the
	// app-credential flow (tests and pinned deployments).
	Token string `json:"-"`
	// EncryptKey is the event subscription encrypt key used by the signature.
	EncryptKey string `json:"-"`
	// Now overrides the clock (tests).
	Now func() time.Time `json:"-"`
	// Client overrides the HTTP client (tests / custom transport).
	Client *http.Client `json:"-"`
}

func (c Config) normalized() Config {
	if c.BaseURL == "" {
		c.BaseURL = DefaultBaseURL
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Client == nil {
		c.Client = &http.Client{Timeout: 20 * time.Second}
	}
	return c
}

// configured reports whether the adapter could authenticate outbound calls.
func (c Config) configured() bool {
	return c.Token != "" || (c.AppID != "" && c.AppSecret != "")
}

// Adapter implements contracts.FeishuService.
type Adapter struct {
	inbox contracts.InboxService
	cfg   Config

	mu sync.Mutex
	// lastInbound is the newest event time (the status page reads it).
	lastInbound time.Time
	// token/tokenExpiry cache the minted tenant access token.
	token       string
	tokenExpiry time.Time
}

// NewAdapter wires the adapter.
func NewAdapter(inboxService contracts.InboxService, cfg Config) *Adapter {
	return &Adapter{inbox: inboxService, cfg: cfg.normalized()}
}

// VerifyURL implements contracts.FeishuService. It is a pure parse: the caller
// must have verified the signature first (Receive does that and reports a
// challenge as "not a message"), so a forged handshake is never echoed.
func (a *Adapter) VerifyURL(body []byte) (string, bool) {
	var payload struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", false
	}
	if payload.Type != "url_verification" || payload.Challenge == "" {
		return "", false
	}
	return payload.Challenge, true
}

// Receive implements contracts.FeishuService.
func (a *Adapter) Receive(ctx context.Context, headers map[string]string, body []byte) (contracts.InboxItem, bool, error) {
	if err := ctx.Err(); err != nil {
		return contracts.InboxItem{}, false, err
	}
	if err := a.verifyRequest(headers, body); err != nil {
		return contracts.InboxItem{}, false, err
	}
	// An encrypted push verifies like any other request (the signature covers the
	// raw wrapper), so it has to be recognised explicitly: dropping it with a nil
	// error would answer the platform with 200 and lose the event.
	if isEncryptedPush(body) {
		return contracts.InboxItem{}, false, fmt.Errorf("%w: encrypted event body (no decrypt path)", contracts.ErrFeishuBadPayload)
	}
	// A handshake is answered by the HTTP layer through VerifyURL; it must not
	// become an inbox row.
	if _, challenge := a.VerifyURL(body); challenge {
		return contracts.InboxItem{}, false, nil
	}

	envelope, err := parseEvent(body)
	if err != nil {
		return contracts.InboxItem{}, false, err
	}
	if envelope.Header.EventType != contracts.FeishuEventMessageReceive {
		// Other subscribed events are acknowledged and dropped: they are not
		// work, and turning them into inbox rows would drown the Lead.
		return contracts.InboxItem{}, false, nil
	}
	message := envelope.Event.Message
	if message.MessageType != "" && message.MessageType != "text" {
		// The channel is text-only for now; an image is not an empty message.
		return contracts.InboxItem{}, false, nil
	}
	if message.MessageID == "" || message.ChatID == "" || envelope.Event.Sender.SenderID.OpenID == "" {
		return contracts.InboxItem{}, false, fmt.Errorf("%w: message needs message_id, chat_id and sender open_id", contracts.ErrFeishuBadPayload)
	}
	text, ok := textContent(message.Content)
	if !ok {
		return contracts.InboxItem{}, false, fmt.Errorf("%w: message content is not text JSON", contracts.ErrFeishuBadPayload)
	}
	if text == "" {
		return contracts.InboxItem{}, false, nil // empty text: nothing to triage
	}

	now := a.cfg.Now()
	a.mu.Lock()
	a.lastInbound = now
	a.mu.Unlock()

	item := contracts.InboxItem{
		Source:   contracts.FeishuSource,
		Channel:  message.ChatID,
		Sender:   envelope.Event.Sender.SenderID.OpenID,
		DedupKey: contracts.FeishuSource + ":" + message.MessageID,
		Title:    inboxTitle(text),
		Body:     text,
		Ref:      message.MessageID,
	}
	stored, merged, err := a.inbox.Add(ctx, item)
	if err != nil {
		return contracts.InboxItem{}, false, err
	}
	return stored, merged, nil
}

// CardAction implements contracts.FeishuService. The card's button value is the
// only source of truth; a value without an approval id and an allow/deny
// decision is a card we do not own.
func (a *Adapter) CardAction(body []byte) (string, string, bool) {
	var payload struct {
		Action *cardAction `json:"action"`
		Event  struct {
			Action *cardAction `json:"action"`
		} `json:"event"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", "", false
	}
	action := payload.Event.Action
	if action == nil {
		action = payload.Action
	}
	if action == nil || len(action.Value) == 0 {
		return "", "", false
	}
	return parseCardValue(action.Value)
}

// CardActionWithToken is CardAction plus the one-time approval token that our
// cards embed in action.value. The approval action layer needs all three to
// apply a decision without trusting the request body's identity.
func (a *Adapter) CardActionWithToken(body []byte) (approvalID, decision, token string, ok bool) {
	var payload struct {
		Action *cardAction `json:"action"`
		Event  struct {
			Action *cardAction `json:"action"`
		} `json:"event"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", "", "", false
	}
	action := payload.Event.Action
	if action == nil {
		action = payload.Action
	}
	if action == nil || len(action.Value) == 0 {
		return "", "", "", false
	}
	return parseCardValueWithToken(action.Value)
}

// parseCardValueWithToken reads approvalId/decision/token from an action.value
// that may be a nested object or a JSON string holding one.
func parseCardValueWithToken(raw json.RawMessage) (string, string, string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", "", "", false
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		raw = json.RawMessage(asString)
	}
	var value struct {
		ApprovalID      string `json:"approvalId"`
		ApprovalIDSnake string `json:"approval_id"`
		Decision        string `json:"decision"`
		Token           string `json:"token"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", "", "", false
	}
	approvalID := value.ApprovalID
	if approvalID == "" {
		approvalID = value.ApprovalIDSnake
	}
	if approvalID == "" || (value.Decision != contracts.FeishuDecisionAllow && value.Decision != contracts.FeishuDecisionDeny) || value.Token == "" {
		return "", "", "", false
	}
	return approvalID, value.Decision, value.Token, true
}

// cardAction is the button callback inside a card payload.
type cardAction struct {
	Tag   string          `json:"tag"`
	Value json.RawMessage `json:"value"`
}

// parseCardValue accepts both shapes the platform uses for action.value: a
// nested object, or a JSON string holding that object.
func parseCardValue(raw json.RawMessage) (string, string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", "", false
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		raw = json.RawMessage(asString)
	}
	var value struct {
		ApprovalID      string `json:"approvalId"`
		ApprovalIDSnake string `json:"approval_id"`
		Decision        string `json:"decision"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", "", false
	}
	approvalID := strings.TrimSpace(value.ApprovalID)
	if approvalID == "" {
		approvalID = strings.TrimSpace(value.ApprovalIDSnake)
	}
	decision := strings.ToLower(strings.TrimSpace(value.Decision))
	if approvalID == "" {
		return "", "", false
	}
	switch decision {
	case contracts.FeishuDecisionAllow, contracts.FeishuDecisionDeny:
	default:
		return "", "", false
	}
	return approvalID, decision, true
}

// Send implements contracts.FeishuService.
func (a *Adapter) Send(ctx context.Context, receiveID, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	receiveID = strings.TrimSpace(receiveID)
	if receiveID == "" {
		return fmt.Errorf("%w: empty receive id", contracts.ErrFeishuBadPayload)
	}
	if strings.TrimSpace(text) == "" {
		return contracts.ErrFeishuEmptyMessage
	}
	token, err := a.ensureToken(ctx)
	if err != nil {
		return err
	}

	// The platform wants content as a JSON *string*, not a nested object.
	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return fmt.Errorf("%w: encode content: %v", contracts.ErrFeishuUnavailable, err)
	}
	payload, err := json.Marshal(map[string]string{
		"receive_id": receiveID,
		"msg_type":   "text",
		"content":    string(content),
	})
	if err != nil {
		return fmt.Errorf("%w: encode message: %v", contracts.ErrFeishuUnavailable, err)
	}
	endpoint := strings.TrimRight(a.cfg.BaseURL, "/") + "/open-apis/im/v1/messages?receive_id_type=" + contracts.FeishuReceiveIDTypeChatID
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("%w: %v", contracts.ErrFeishuUnavailable, err)
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("Authorization", "Bearer "+token)

	response, err := a.cfg.Client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %v", contracts.ErrFeishuUnavailable, err)
	}
	defer response.Body.Close()
	return classifyAPIResponse(response, "send message")
}

// Status implements contracts.FeishuService.
func (a *Adapter) Status(ctx context.Context) (contracts.FeishuStatus, error) {
	if err := ctx.Err(); err != nil {
		return contracts.FeishuStatus{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return contracts.FeishuStatus{Connected: a.cfg.configured(), LastInbound: a.lastInbound}, nil
}

// ensureToken returns the bearer token for an outbound call: a pinned token
// wins, otherwise the app credentials mint (and cache) a tenant access token.
func (a *Adapter) ensureToken(ctx context.Context) (string, error) {
	if a.cfg.Token != "" {
		return a.cfg.Token, nil
	}
	if a.cfg.AppID == "" || a.cfg.AppSecret == "" {
		return "", contracts.ErrFeishuUnauthorized
	}

	a.mu.Lock()
	if a.token != "" && a.cfg.Now().Before(a.tokenExpiry) {
		token := a.token
		a.mu.Unlock()
		return token, nil
	}
	a.mu.Unlock()

	payload, err := json.Marshal(map[string]string{"app_id": a.cfg.AppID, "app_secret": a.cfg.AppSecret})
	if err != nil {
		return "", fmt.Errorf("%w: encode credentials: %v", contracts.ErrFeishuUnavailable, err)
	}
	endpoint := strings.TrimRight(a.cfg.BaseURL, "/") + "/open-apis/auth/v3/tenant_access_token/internal"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("%w: %v", contracts.ErrFeishuUnavailable, err)
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response, err := a.cfg.Client.Do(request)
	if err != nil {
		return "", fmt.Errorf("%w: %v", contracts.ErrFeishuUnavailable, err)
	}
	defer response.Body.Close()

	var result struct {
		Code   int    `json:"code"`
		Msg    string `json:"msg"`
		Token  string `json:"tenant_access_token"`
		Expire int64  `json:"expire"`
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return "", contracts.ErrFeishuUnauthorized
	}
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil {
		return "", fmt.Errorf("%w: read token response: %v", contracts.ErrFeishuUnavailable, readErr)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &result); err != nil {
			return "", fmt.Errorf("%w: decode token response: %v", contracts.ErrFeishuUnavailable, err)
		}
	}
	if result.Code != 0 {
		if authErrorCodes[result.Code] {
			return "", contracts.ErrFeishuUnauthorized
		}
		return "", fmt.Errorf("%w: token code %d %s", contracts.ErrFeishuUnavailable, result.Code, result.Msg)
	}
	if result.Token == "" {
		return "", fmt.Errorf("%w: empty tenant access token", contracts.ErrFeishuUnavailable)
	}

	expiry := a.cfg.Now().Add(time.Minute)
	if result.Expire > 0 {
		expiry = a.cfg.Now().Add(time.Duration(result.Expire)*time.Second - tokenRefreshMargin)
	}
	a.mu.Lock()
	a.token = result.Token
	a.tokenExpiry = expiry
	a.mu.Unlock()
	return result.Token, nil
}

// VerifyRequest lets the HTTP layer authenticate a non-message callback (a card
// button, for instance) with the same signature and replay rules as events.
// It never touches the inbox and never compares decisions.
func (a *Adapter) VerifyRequest(headers map[string]string, body []byte) error {
	return a.verifyRequest(headers, body)
}

// verifyRequest checks the signature headers and the replay window.
func (a *Adapter) verifyRequest(headers map[string]string, body []byte) error {
	normalized := make(map[string]string, len(headers))
	for name, value := range headers {
		normalized[strings.ToLower(strings.TrimSpace(name))] = strings.TrimSpace(value)
	}
	lookup := func(names ...string) string {
		for _, name := range names {
			if value := normalized[strings.ToLower(name)]; value != "" {
				return value
			}
		}
		return ""
	}

	signature := lookup("X-Lark-Signature", "Lark-Signature")
	if signature == "" {
		return fmt.Errorf("%w: X-Lark-Signature missing", contracts.ErrFeishuBadSignature)
	}
	timestamp := lookup("X-Lark-Request-Timestamp", "Lark-Request-Timestamp")
	if timestamp == "" {
		return fmt.Errorf("%w: request timestamp missing", contracts.ErrFeishuBadSignature)
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: timestamp %q is not a unix second", contracts.ErrFeishuBadSignature, timestamp)
	}
	skew := a.cfg.Now().Sub(time.Unix(seconds, 0))
	if skew > contracts.FeishuMaxClockSkew || skew < -contracts.FeishuMaxClockSkew {
		return fmt.Errorf("%w: timestamp %s off", contracts.ErrFeishuStale, skew.Round(time.Second))
	}

	nonce := lookup("X-Lark-Request-Nonce", "Lark-Request-Nonce")
	candidates := []string{signRequest(timestamp, nonce, a.cfg.EncryptKey, body)}
	if nonce != "" {
		// Legacy bots sign without the nonce; the skew check above still holds.
		candidates = append(candidates, signRequest(timestamp, "", a.cfg.EncryptKey, body))
	}
	want := strings.ToLower(signature)
	for _, candidate := range candidates {
		if subtle.ConstantTimeCompare([]byte(want), []byte(candidate)) == 1 {
			return nil
		}
	}
	return contracts.ErrFeishuBadSignature
}

// signRequest is hex(sha256(timestamp + nonce + encryptKey + rawBody)).
func signRequest(timestamp, nonce, encryptKey string, body []byte) string {
	hash := sha256.New()
	io.WriteString(hash, timestamp)
	io.WriteString(hash, nonce)
	io.WriteString(hash, encryptKey)
	hash.Write(body)
	return hex.EncodeToString(hash.Sum(nil))
}

// eventEnvelope is the subset of the event callback body we read.
type eventEnvelope struct {
	Header struct {
		EventID   string `json:"event_id"`
		EventType string `json:"event_type"`
	} `json:"header"`
	Event struct {
		Sender struct {
			SenderID struct {
				OpenID  string `json:"open_id"`
				UnionID string `json:"union_id"`
				UserID  string `json:"user_id"`
			} `json:"sender_id"`
			SenderType string `json:"sender_type"`
		} `json:"sender"`
		Message struct {
			MessageID   string `json:"message_id"`
			ChatID      string `json:"chat_id"`
			ChatType    string `json:"chat_type"`
			MessageType string `json:"message_type"`
			Content     string `json:"content"`
		} `json:"message"`
		// v1 payloads say "message" here instead of header.event_type.
		Type string `json:"type"`
	} `json:"event"`
	// v1 payloads carry type at the top level instead of header.event_type.
	Type string `json:"type"`
}

// parseEvent decodes the event callback. An older payload carries
// type=event_callback at the top and the kind in event.type; it is mapped onto
// the v2 header only when it really is a message, so a v1 callback for an
// unrelated event is still ignored instead of misparsed.
func parseEvent(body []byte) (eventEnvelope, error) {
	var envelope eventEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return eventEnvelope{}, fmt.Errorf("%w: %v", contracts.ErrFeishuBadPayload, err)
	}
	if envelope.Header.EventType == "" && envelope.Type == "event_callback" && envelope.Event.Type == "message" {
		envelope.Header.EventType = contracts.FeishuEventMessageReceive
	}
	return envelope, nil
}

// isEncryptedPush reports whether body is the platform's encrypted wrapper
// ({"encrypt": "<base64 ciphertext>"}) rather than a plaintext callback payload.
// It is the only shape we cannot read, and it also carries the handshake when an
// Encrypt Key is configured, so it is checked before anything is echoed or filed.
func isEncryptedPush(body []byte) bool {
	var probe struct {
		Encrypt json.RawMessage `json:"encrypt"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return false
	}
	if len(probe.Encrypt) == 0 || string(probe.Encrypt) == "null" {
		return false
	}
	var ciphertext string
	return json.Unmarshal(probe.Encrypt, &ciphertext) == nil && strings.TrimSpace(ciphertext) != ""
}

// textContent pulls the plain text out of message.content, which the platform
// sends as a JSON string.
func textContent(content string) (string, bool) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "", false
	}
	var decoded struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return "", false
	}
	return strings.TrimSpace(decoded.Text), true
}

// inboxTitle is the first line, capped so the list stays scannable.
func inboxTitle(text string) string {
	line := text
	if index := strings.IndexAny(line, "\r\n"); index >= 0 {
		line = line[:index]
	}
	runes := []rune(strings.TrimSpace(line))
	if len(runes) > maxTitleRunes {
		runes = runes[:maxTitleRunes]
	}
	return string(runes)
}

// classifyAPIResponse maps a platform response onto the typed errors: 401/403
// and the "wrong credential" codes are unauthorized, everything else that is
// not a 2xx-with-code-0 is unavailable.
func classifyAPIResponse(response *http.Response, action string) error {
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return contracts.ErrFeishuUnauthorized
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: %s: read response: %v", contracts.ErrFeishuUnavailable, action, err)
	}
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &result); err != nil {
			if response.StatusCode >= 200 && response.StatusCode <= 299 {
				return fmt.Errorf("%w: %s: decode response: %v", contracts.ErrFeishuUnavailable, action, err)
			}
			return fmt.Errorf("%w: %s: status %d", contracts.ErrFeishuUnavailable, action, response.StatusCode)
		}
	}
	if result.Code != 0 {
		if authErrorCodes[result.Code] {
			return contracts.ErrFeishuUnauthorized
		}
		return fmt.Errorf("%w: %s: code %d %s", contracts.ErrFeishuUnavailable, action, result.Code, result.Msg)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("%w: %s: status %d", contracts.ErrFeishuUnavailable, action, response.StatusCode)
	}
	return nil
}

var _ contracts.FeishuService = (*Adapter)(nil)
