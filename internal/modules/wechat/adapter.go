// Package wechat is the WeChat ClawBot (iLink) adapter. It polls the platform
// for direct-chat messages, files them in the inbox, and delivers plain-text
// replies — but only inside the platform's 24h reply window and only with the
// context token of the message being answered. Everything it knows about the
// wire format was checked against the public iLink descriptions; a real-device
// pass is still required (see docs/research/channel-capabilities.md).
package wechat

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/lib/backoff"
)

// DefaultBaseURL is the official iLink host.
const DefaultBaseURL = "https://ilinkai.weixin.qq.com"

// DefaultPollInterval is the pause between polls after a successful one.
const DefaultPollInterval = 30 * time.Second

// KeepaliveWarnAfter is how old the newest inbound message may get before
// NeedsKeepalive reports true. The reply window is contracts.WeChatWindow (24h);
// warning at 20h leaves four hours for the user to message the bot again.
const KeepaliveWarnAfter = contracts.WeChatWindow - 4*time.Hour

// maxTitleRunes keeps an inbox title scannable; the full text stays in Body.
const maxTitleRunes = 60

// Config is the module's typed assembly config.
type Config struct {
	// BaseURL overrides the iLink host (tests use a local server).
	BaseURL string `json:"baseUrl,omitempty"`
	// Token is the bot login token from the QR flow ("" = not connected).
	Token string `json:"-"`
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

// inboundMessage is one message from getupdates.
type inboundMessage struct {
	MsgID        string `json:"msg_id"`
	FromUserID   string `json:"from_user_id"`
	ToUserID     string `json:"to_user_id"`
	ContextToken string `json:"context_token"`
	ItemList     []struct {
		Type     int `json:"type"`
		TextItem struct {
			Text string `json:"text"`
		} `json:"text_item"`
	} `json:"item_list"`
}

// text returns the concatenated text items (the channel is text-only for now).
func (m inboundMessage) text() string {
	var parts []string
	for _, item := range m.ItemList {
		if item.Type == 1 && strings.TrimSpace(item.TextItem.Text) != "" {
			parts = append(parts, strings.TrimSpace(item.TextItem.Text))
		}
	}
	return strings.Join(parts, "\n")
}

// Adapter implements contracts.WeChatService.
type Adapter struct {
	inbox contracts.InboxService
	cfg   Config

	// store is the optional login-token store. nil means the token lives only in
	// memory for this process (tests, or a host without a data directory).
	store *TokenStore
	// credentials is the optional shared credential store; when set, the login
	// token may come from it (one place owns every channel secret).
	credentials contracts.CredentialStore
	// credentialID selects the row inside the store ("default" for the single
	// personal account).
	credentialID string

	mu sync.Mutex
	// tokens is the latest context token per user: a reply must ride the token
	// of the message it answers.
	tokens map[string]string
	// lastInbound is the newest inbound time (the 24h window anchor).
	lastInbound time.Time
	// botID is the platform identity (changes on every QR login).
	botID string
	// logins maps every QR id this adapter issued to its local deadline. It is a
	// validation aid for the login flow (see login.go), never a credential: a
	// confirmed state must belong to a code that had not expired yet.
	logins map[string]time.Time
}

// NewAdapter wires the adapter without token persistence.
func NewAdapter(inboxService contracts.InboxService, cfg Config) *Adapter {
	return &Adapter{
		inbox:  inboxService,
		cfg:    cfg.normalized(),
		tokens: make(map[string]string),
		logins: make(map[string]time.Time),
	}
}

// NewAdapterWithCredentials wires the adapter to the shared credential store.
// The token then resolves as: explicit cfg.Token, else the stored credential.
func NewAdapterWithCredentials(inboxService contracts.InboxService, cfg Config, credentials contracts.CredentialStore) *Adapter {
	adapter := NewAdapter(inboxService, cfg)
	adapter.credentials = credentials
	adapter.credentialID = "default"
	return adapter
}

// OpenAdapter wires the adapter with a token store and restores the persisted
// login, so a restart reports Connected without a new QR scan.
//
// A nil store is allowed (no persistence). A corrupt token.json yields an error
// wrapping ErrTokenStoreCorrupt and leaves the file untouched — the caller
// quarantines it (see Module) and then calls OpenAdapter again. An explicit
// cfg.Token wins over the stored one: assembly configuration is authoritative,
// and a stale login must not override an operator's token.
func OpenAdapter(inboxService contracts.InboxService, cfg Config, store *TokenStore) (*Adapter, error) {
	adapter := NewAdapter(inboxService, cfg)
	adapter.store = store
	if store == nil || strings.TrimSpace(adapter.cfg.Token) != "" {
		return adapter, nil
	}
	token, ok, err := store.Load()
	if err != nil {
		return nil, err
	}
	if ok {
		adapter.cfg.Token = token
	}
	return adapter, nil
}

// SetToken records the login token from the QR flow and persists it when a store
// is configured. The token is persisted *before* it is adopted, so a failed write
// cannot leave an in-memory login that silently disappears on the next restart;
// the error tells the caller the login did not stick. A new login also drops the
// previous identity's context tokens (they are scoped to the bot account and
// would be rejected by the platform).
func (a *Adapter) SetToken(token string) error {
	token = strings.TrimSpace(token)
	a.mu.Lock()
	store := a.store
	a.mu.Unlock()

	if store != nil {
		if err := store.Save(token); err != nil {
			return fmt.Errorf("wechat: persist token: %w", err)
		}
	}

	a.mu.Lock()
	credentials := a.credentials
	id := a.credentialID
	a.cfg.Token = token
	a.tokens = make(map[string]string)
	a.mu.Unlock()

	if credentials != nil {
		if token == "" {
			if err := credentials.Delete(context.Background(), "wechat", id); err != nil && !errors.Is(err, contracts.ErrCredentialNotFound) {
				return fmt.Errorf("wechat: clear credential: %w", err)
			}
		} else if err := credentials.Set(context.Background(), contracts.Credential{
			Kind: "wechat", ID: id, Fields: map[string]string{"token": token},
		}); err != nil {
			return fmt.Errorf("wechat: persist credential: %w", err)
		}
	}
	return nil
}

// ClearToken logs out: the token is dropped from memory and from the store.
func (a *Adapter) ClearToken() error {
	return a.SetToken("")
}

// token returns the current login token ("" = not connected). An explicit
// configuration token wins; otherwise the shared credential store is consulted,
// so the settings UI can install a login without restarting the process.
func (a *Adapter) token(ctx context.Context) string {
	a.mu.Lock()
	token := strings.TrimSpace(a.cfg.Token)
	credentials := a.credentials
	id := a.credentialID
	a.mu.Unlock()
	if token != "" || credentials == nil {
		return token
	}
	credential, ok, err := credentials.Get(ctx, "wechat", id)
	if err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(credential.Fields["token"])
}

// PollOnce implements contracts.WeChatService.
func (a *Adapter) PollOnce(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	token := a.token(ctx)
	if token == "" {
		return 0, contracts.ErrWeChatUnauthorized
	}
	body, _ := json.Marshal(map[string]any{})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.cfg.BaseURL, "/")+"/ilink/bot/getupdates", bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("%w: %v", contracts.ErrWeChatUnavailable, err)
	}
	a.setHeaders(request, token)

	response, err := a.cfg.Client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", contracts.ErrWeChatUnavailable, err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return 0, contracts.ErrWeChatUnauthorized
	case http.StatusOK:
	default:
		return 0, fmt.Errorf("%w: status %d", contracts.ErrWeChatUnavailable, response.StatusCode)
	}

	// Some iLink builds wrap the payload ({"data":{"msgs":[...]}}) and some report
	// a refusal as 200 with ret_code != 0 (channel-capabilities §1). Both used to
	// be a silent "no messages", which is the worst possible answer: the loop
	// resets its backoff and the queue quietly stops draining.
	var envelope struct {
		Msgs    []inboundMessage `json:"msgs"`
		RetCode int              `json:"ret_code"`
		ErrMsg  string           `json:"errmsg"`
		Data    *struct {
			Msgs    []inboundMessage `json:"msgs"`
			RetCode int              `json:"ret_code"`
			ErrMsg  string           `json:"errmsg"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err != nil {
		return 0, fmt.Errorf("%w: decode: %v", contracts.ErrWeChatUnavailable, err)
	}
	messages := envelope.Msgs
	retCode, errMsg := envelope.RetCode, envelope.ErrMsg
	if envelope.Data != nil {
		if len(messages) == 0 {
			messages = envelope.Data.Msgs
		}
		if retCode == 0 {
			retCode, errMsg = envelope.Data.RetCode, envelope.Data.ErrMsg
		}
	}
	if retCode != 0 {
		return 0, fmt.Errorf("%w: getupdates refused (ret_code %d: %s)", contracts.ErrWeChatUnavailable, retCode, errMsg)
	}

	ingested := 0
	for _, message := range messages {
		text := message.text()
		if message.FromUserID == "" || text == "" {
			continue // media-only or malformed: the channel is text-only for now
		}
		a.mu.Lock()
		a.tokens[message.FromUserID] = message.ContextToken
		a.lastInbound = a.cfg.Now()
		a.mu.Unlock()

		title := []rune(text)
		if len(title) > maxTitleRunes {
			title = title[:maxTitleRunes]
		}
		item := contracts.InboxItem{
			Source: "wechat", Channel: message.FromUserID, Sender: message.FromUserID,
			DedupKey: "wechat:" + message.MsgID,
			Title:    string(title),
			Body:     text,
		}
		if item.DedupKey == "wechat:" {
			// No platform id: fall back to a content-stable key so a retry merges.
			item.DedupKey = "wechat:" + fmt.Sprintf("%s:%d", message.FromUserID, len(text))
		}
		if _, _, err := a.inbox.Add(ctx, item); err != nil {
			return ingested, err
		}
		ingested++
	}
	return ingested, nil
}

// Send implements contracts.WeChatService.
//
// A 200 alone is not a delivery: the platform answers 200 for a message it
// refused (see docs/research/channel-capabilities.md §1 and
// testdata/sendmessage_error_retcode.json), so the body's verdict is checked
// before success is reported.
func (a *Adapter) Send(ctx context.Context, toUserID, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return contracts.ErrWeChatEmptyMessage
	}
	if a.token(ctx) == "" {
		return contracts.ErrWeChatUnauthorized
	}

	a.mu.Lock()
	token := a.tokens[toUserID]
	last := a.lastInbound
	a.mu.Unlock()

	if token == "" {
		return contracts.ErrWeChatNoContext
	}
	if !last.IsZero() && a.cfg.Now().Sub(last) > contracts.WeChatWindow {
		return contracts.ErrWeChatWindowClosed
	}

	payload := map[string]any{
		"msg": map[string]any{
			"from_user_id":  "",
			"to_user_id":    toUserID,
			"client_id":     randomClientID(),
			"message_type":  2,
			"message_state": 2,
			"context_token": token,
			"item_list": []map[string]any{
				{"type": 1, "text_item": map[string]any{"text": text}},
			},
		},
		"base_info": map[string]any{"channel_version": contracts.WeChatChannelVersion},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("%w: encode: %v", contracts.ErrWeChatUnavailable, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.cfg.BaseURL, "/")+"/ilink/bot/sendmessage", bytes.NewReader(encoded))
	if err != nil {
		return fmt.Errorf("%w: %v", contracts.ErrWeChatUnavailable, err)
	}
	a.setHeaders(request, a.token(ctx))
	response, err := a.cfg.Client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %v", contracts.ErrWeChatUnavailable, err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return contracts.ErrWeChatUnauthorized
	default:
		return fmt.Errorf("%w: status %d", contracts.ErrWeChatUnavailable, response.StatusCode)
	}
	// The status said 200; the body still has to say "did the platform take it".
	// Without this check a refused message was reported as delivered, and a
	// swallowed notification is the one failure nothing downstream can notice:
	// no retry, no fallback, and a success the caller trusts.
	if err := checkSendVerdict(response.Body); err != nil {
		return err
	}
	return nil
}

// sendVerdict is the body-level verdict of one sendmessage call. The fields are
// pointers so "the platform did not say" stays distinguishable from an explicit
// zero: {"ok": true} and {"ret_code": 0} are both acceptance, an absent field is
// silence (accepted, since the request already got a 200), while an explicit
// false / non-zero code is a refusal.
type sendVerdict struct {
	OK      *bool  `json:"ok"`
	RetCode *int   `json:"ret_code"`
	ErrMsg  string `json:"errmsg"`
}

// checkSendVerdict maps a sendmessage body onto an error. A body that cannot be
// read is a refusal too: something between us and iLink answered, and nothing in
// it claims the message was taken.
func checkSendVerdict(body io.Reader) error {
	raw, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: read sendmessage body: %v", contracts.ErrWeChatUnavailable, err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil // no body: nothing contradicts the 200
	}
	var verdict sendVerdict
	if err := json.Unmarshal(trimmed, &verdict); err != nil {
		return fmt.Errorf("%w: sendmessage answered 200 with an unreadable body: %v", contracts.ErrWeChatUnavailable, err)
	}
	reason := strings.TrimSpace(verdict.ErrMsg)
	retCode := 0
	if verdict.RetCode != nil {
		retCode = *verdict.RetCode
	}
	refused := retCode != 0 || (verdict.OK != nil && !*verdict.OK)
	if !refused {
		return nil
	}
	if reason == "" {
		reason = "the platform gave no reason"
	}
	if retCode != 0 {
		return fmt.Errorf("%w: ret_code %d: %s", contracts.ErrWeChatUnavailable, retCode, reason)
	}
	return fmt.Errorf("%w: ok false: %s", contracts.ErrWeChatUnavailable, reason)
}

// Status implements contracts.WeChatService.
func (a *Adapter) Status(ctx context.Context) (contracts.WeChatStatus, error) {
	if err := ctx.Err(); err != nil {
		return contracts.WeChatStatus{}, err
	}
	// Read the fields under the lock, then ask for the token outside it:
	// token() takes the same mutex, so holding it here would self-deadlock.
	a.mu.Lock()
	lastInbound := a.lastInbound
	botID := a.botID
	a.mu.Unlock()

	status := contracts.WeChatStatus{
		Connected: a.token(ctx) != "", LastInbound: lastInbound, BotID: botID,
	}
	if !lastInbound.IsZero() {
		status.WindowClosesAt = lastInbound.Add(contracts.WeChatWindow)
	}
	return status, nil
}

// NeedsKeepalive reports whether the 24h reply window is close enough to closing
// that the user should be nudged to message the bot again (the window cannot be
// refreshed from our side: only an inbound message restarts it). False before any
// inbound message, since there is no window to keep alive yet. The adapter never
// sends that nudge itself — the caller owns messaging.
func (a *Adapter) NeedsKeepalive(now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastInbound.IsZero() {
		return false
	}
	return now.Sub(a.lastInbound) > KeepaliveWarnAfter
}

// Run polls the platform until ctx is canceled. After a successful poll it waits
// interval; while the platform is unavailable it backs off on the shared
// backoff schedule (2s doubling, capped at 1m) so an outage does not turn into a
// request flood; a rejected login token is terminal and returned immediately.
// Cancellation returns nil — stopping on request is not a failure.
func (a *Adapter) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	attempt := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		_, err := a.PollOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		switch {
		case err == nil:
			attempt = 0
			if !backoff.Sleep(ctx, interval) {
				return nil
			}
		case errors.Is(err, contracts.ErrWeChatUnauthorized):
			return contracts.ErrWeChatUnauthorized
		default:
			// Transient: unavailable platform, inbox hiccup, decode failure. All
			// of them are worth retrying, slowly.
			attempt++
			if !backoff.Sleep(ctx, backoff.Delay(attempt)) {
				return nil
			}
		}
	}
}

// setHeaders copies the iLink header contract (bearer token + a per-request
// random uint32 so the platform cannot correlate requests by a fixed id).
func (a *Adapter) setHeaders(request *http.Request, token string) {
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("AuthorizationType", "ilink_bot_token")
	request.Header.Set("Authorization", "Bearer "+token)
	setUINHeader(request)
}

// setUINHeader adds the per-request random id iLink wants on every call,
// including the login calls that have no token yet.
func setUINHeader(request *http.Request) {
	var seed [4]byte
	_, _ = rand.Read(seed[:])
	request.Header.Set("X-WECHAT-UIN", base64.StdEncoding.EncodeToString(seed[:]))
}

// randomClientID builds the per-message id iLink expects.
func randomClientID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("openclaw-weixin-%d", time.Now().UnixNano())
	}
	return "openclaw-weixin-" + base64.RawURLEncoding.EncodeToString(raw[:])
}

var _ contracts.WeChatService = (*Adapter)(nil)

// uint32Seed is kept for the header format documentation: iLink wants a random
// uint32 encoded as base64, which is exactly the four bytes above.
var _ = binary.BigEndian
