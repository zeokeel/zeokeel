// Package dingtalk is the DingTalk chat-bot adapter. DingTalk pushes messages to
// an HTTP callback we publish, signed with the app secret, so the adapter first
// proves a callback is genuine and fresh and only then files the text in the
// inbox; replies go back through the robot send API with a pre-issued access
// token. Everything about the wire format is pinned by adapter_test.go.
//
// Signature headers: DingTalk signs with the app secret as
//
//	stringToSign = timestamp + "\n" + secret
//	sign        = base64(HMAC-SHA256(key = secret, message = stringToSign))
//
// and delivers it in the documented HTTP-mode callback headers `timestamp` (unix
// *milliseconds*) and `sign`. The X-DingTalk-Signature / X-DingTalk-Timestamp
// spelling (unix seconds) is accepted as well because scripts/channel-verify.sh
// and the gateway passthrough test send it. The HMAC is computed over whichever
// timestamp string arrived, so the unit is never attacker-chosen — it only
// decides how the value is read as a clock reading (see callbackTime).
//
// We accept the base64 form only — that is what DingTalk's callback delivers; a
// hex signature is refused rather than silently reinterpreted (a signature we
// cannot decode is just a signature we cannot verify).
package dingtalk

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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

// DefaultBaseURL is the DingTalk OpenAPI host.
const DefaultBaseURL = "https://api.dingtalk.com"

// sendPath is the robot group-message endpoint (msgKey "sampleText").
const sendPath = "/v1.0/robot/groupMessages/send"

// Callback header names. Lookups are case-insensitive; a request may use either
// spelling (see the package comment for why both exist).
const (
	// HeaderSignature / HeaderTimestamp are the spelling our own verify script
	// and gateway tests send; the timestamp there is in unix seconds.
	HeaderSignature = "X-DingTalk-Signature"
	HeaderTimestamp = "X-DingTalk-Timestamp"
	// HeaderSignDocumented / HeaderTimestampDocumented are the platform's
	// documented HTTP-mode callback headers; the timestamp is in milliseconds.
	// https://open.dingtalk.com/document/robots/receive-message
	HeaderSignDocumented      = "sign"
	HeaderTimestampDocumented = "timestamp"
)

// msgKeyText is the robot message template for a plain-text reply.
const msgKeyText = "sampleText"

// maxTitleRunes keeps an inbox title scannable; the full text stays in Body.
const maxTitleRunes = 60

// Config is the module's typed assembly config.
type Config struct {
	// BaseURL overrides the API host (tests use a local server).
	BaseURL string `json:"baseUrl,omitempty"`
	// Token is the robot access token ("" = not connected). It is supplied by
	// configuration; the adapter performs no OAuth dance.
	Token string `json:"-"`
	// SignSecret is the app secret the callback signature is computed with. An
	// empty secret disables nothing: every callback is refused.
	SignSecret string `json:"-"`
	// RobotCode is the robot identity the send API expects.
	RobotCode string `json:"robotCode,omitempty"`
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
		c.Client = &http.Client{Timeout: 45 * time.Second}
	}
	return c
}

// callback is the chat-bot text callback body. Only the fields the inbox needs
// are decoded; everything else is data the platform owns.
type callback struct {
	MsgType        string `json:"msgtype"`
	MsgID          string `json:"msgId"`
	ConversationID string `json:"conversationId"`
	SenderStaffID  string `json:"senderStaffId"`
	SenderNick     string `json:"senderNick"`
	Text           struct {
		Content string `json:"content"`
	} `json:"text"`
}

// Adapter implements contracts.DingTalkService.
type Adapter struct {
	inbox contracts.InboxService
	cfg   Config

	mu sync.Mutex
	// lastInbound is the newest verified callback time.
	lastInbound time.Time
}

// NewAdapter wires the adapter.
func NewAdapter(inboxService contracts.InboxService, cfg Config) *Adapter {
	return &Adapter{inbox: inboxService, cfg: cfg.normalized()}
}

// Receive implements contracts.DingTalkService.
func (a *Adapter) Receive(ctx context.Context, headers map[string]string, body []byte) (contracts.InboxItem, bool, error) {
	if err := ctx.Err(); err != nil {
		return contracts.InboxItem{}, false, err
	}
	if err := a.verifySignature(headers, a.cfg.Now()); err != nil {
		return contracts.InboxItem{}, false, err
	}

	var message callback
	if err := json.Unmarshal(body, &message); err != nil {
		return contracts.InboxItem{}, false, fmt.Errorf("%w: decode body: %v", contracts.ErrDingTalkInvalid, err)
	}
	if message.MsgType != "text" {
		return contracts.InboxItem{}, false, fmt.Errorf("%w: msgtype %q", contracts.ErrDingTalkUnsupportedMessage, message.MsgType)
	}
	if message.MsgID == "" || message.ConversationID == "" || message.SenderStaffID == "" {
		return contracts.InboxItem{}, false, fmt.Errorf("%w: missing msgId/conversationId/senderStaffId", contracts.ErrDingTalkInvalid)
	}
	text := strings.TrimSpace(message.Text.Content)
	if text == "" {
		return contracts.InboxItem{}, false, fmt.Errorf("%w: empty text content", contracts.ErrDingTalkInvalid)
	}

	title := []rune(text)
	if len(title) > maxTitleRunes {
		title = title[:maxTitleRunes]
	}
	item := contracts.InboxItem{
		Source:   "dingtalk",
		Channel:  message.ConversationID,
		Sender:   message.SenderStaffID,
		DedupKey: "dingtalk:" + message.MsgID,
		Title:    strings.TrimSpace(string(title)),
		Body:     text,
	}
	stored, merged, err := a.inbox.Add(ctx, item)
	if err != nil {
		return contracts.InboxItem{}, false, err
	}

	a.mu.Lock()
	a.lastInbound = a.cfg.Now()
	a.mu.Unlock()
	return stored, merged, nil
}

// verifySignature checks the callback signature and then its freshness. The
// order matters: presence -> decode -> HMAC -> skew, so the timestamp is only
// trusted once the request is proven authentic, and a replay of an old (but
// genuine) callback is refused with ErrDingTalkStaleTimestamp.
func (a *Adapter) verifySignature(headers map[string]string, now time.Time) error {
	signatureValue := lookupHeaderAny(headers, HeaderSignDocumented, HeaderSignature)
	timestampValue := lookupHeaderAny(headers, HeaderTimestampDocumented, HeaderTimestamp)
	if signatureValue == "" || timestampValue == "" {
		return fmt.Errorf("%w: missing %s/%s (or %s/%s)",
			contracts.ErrDingTalkBadSignature,
			HeaderSignDocumented, HeaderTimestampDocumented, HeaderSignature, HeaderTimestamp)
	}
	signature, err := base64.StdEncoding.DecodeString(signatureValue)
	if err != nil || len(signature) == 0 {
		return fmt.Errorf("%w: signature is not base64", contracts.ErrDingTalkBadSignature)
	}
	at, ok := callbackTime(timestampValue)
	if !ok {
		return fmt.Errorf("%w: timestamp %q is not unix seconds or milliseconds", contracts.ErrDingTalkBadSignature, timestampValue)
	}

	secret := []byte(a.cfg.SignSecret)
	if len(secret) == 0 {
		return fmt.Errorf("%w: no sign secret configured", contracts.ErrDingTalkBadSignature)
	}
	// The platform signs the header string it sent, so the unit cannot be
	// swapped without the secret.
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestampValue + "\n" + a.cfg.SignSecret))
	if !hmac.Equal(mac.Sum(nil), signature) {
		return contracts.ErrDingTalkBadSignature
	}

	if skew := now.Sub(at); skew > contracts.DingTalkMaxSkew || skew < -contracts.DingTalkMaxSkew {
		return fmt.Errorf("%w: %s off by %s", contracts.ErrDingTalkStaleTimestamp, timestampValue, skew.Round(time.Second))
	}
	return nil
}

// callbackTime reads the callback timestamp header as a clock reading. DingTalk's
// documented header quotes unix *milliseconds* (e.g. 1708327204136) while the
// X-DingTalk-Timestamp spelling quotes seconds; the magnitude decides, because a
// seconds value can never reach 1e12 before the year 33658.
func callbackTime(value string) (time.Time, bool) {
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number <= 0 {
		return time.Time{}, false
	}
	const millisecondThreshold = 1_000_000_000_000
	if number >= millisecondThreshold {
		return time.UnixMilli(number), true
	}
	return time.Unix(number, 0), true
}

// lookupHeader finds a header case-insensitively (net/http canonicalizes on the
// client side, but a proxy or a test may hand us the raw spelling).
func lookupHeader(headers map[string]string, name string) string {
	if value, ok := headers[name]; ok {
		return strings.TrimSpace(value)
	}
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// lookupHeaderAny returns the first non-empty value among the accepted spellings
// of one header, in priority order.
func lookupHeaderAny(headers map[string]string, names ...string) string {
	for _, name := range names {
		if value := lookupHeader(headers, name); value != "" {
			return value
		}
	}
	return ""
}

// sendRequest is the robot group-message body:
//
//	{"robotCode": "...", "openConversationId": "...",
//	 "msgKey": "sampleText", "msgParam": "{\"content\":\"<text>\"}"}
//
// msgParam is a JSON *string* because the template takes parameters, not text.
type sendRequest struct {
	RobotCode          string `json:"robotCode"`
	OpenConversationID string `json:"openConversationId"`
	MsgKey             string `json:"msgKey"`
	MsgParam           string `json:"msgParam"`
}

// Send implements contracts.DingTalkService.
func (a *Adapter) Send(ctx context.Context, chatID, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(chatID) == "" {
		return fmt.Errorf("%w: missing conversation id", contracts.ErrDingTalkInvalid)
	}
	if strings.TrimSpace(text) == "" {
		return contracts.ErrDingTalkEmptyMessage
	}
	if a.cfg.Token == "" {
		return contracts.ErrDingTalkUnauthorized
	}

	param, err := json.Marshal(map[string]string{"content": text})
	if err != nil {
		return fmt.Errorf("%w: encode msgParam: %v", contracts.ErrDingTalkUnavailable, err)
	}
	encoded, err := json.Marshal(sendRequest{
		RobotCode:          a.cfg.RobotCode,
		OpenConversationID: chatID,
		MsgKey:             msgKeyText,
		MsgParam:           string(param),
	})
	if err != nil {
		return fmt.Errorf("%w: encode body: %v", contracts.ErrDingTalkUnavailable, err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(a.cfg.BaseURL, "/")+sendPath, bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("%w: %v", contracts.ErrDingTalkUnavailable, err)
	}
	request.Header.Set("Content-Type", "application/json")
	// DingTalk v1.0 OpenAPI reads the token from this header; the bearer form is
	// kept as well so a gateway that fronts the API with standard auth works.
	request.Header.Set("x-acs-dingtalk-access-token", a.cfg.Token)
	request.Header.Set("Authorization", "Bearer "+a.cfg.Token)

	response, err := a.cfg.Client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %v", contracts.ErrDingTalkUnavailable, err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		// Drain so the connection can be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return contracts.ErrDingTalkUnauthorized
	default:
		return fmt.Errorf("%w: status %d", contracts.ErrDingTalkUnavailable, response.StatusCode)
	}
}

// Status implements contracts.DingTalkService.
func (a *Adapter) Status(ctx context.Context) (contracts.DingTalkStatus, error) {
	if err := ctx.Err(); err != nil {
		return contracts.DingTalkStatus{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return contracts.DingTalkStatus{Connected: a.cfg.Token != "", LastInbound: a.lastInbound}, nil
}

var _ contracts.DingTalkService = (*Adapter)(nil)
