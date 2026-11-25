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
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// DefaultBaseURL is the official iLink host.
const DefaultBaseURL = "https://ilinkai.weixin.qq.com"

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

	mu sync.Mutex
	// tokens is the latest context token per user: a reply must ride the token
	// of the message it answers.
	tokens map[string]string
	// lastInbound is the newest inbound time (the 24h window anchor).
	lastInbound time.Time
	// botID is the platform identity (changes on every QR login).
	botID string
}

// NewAdapter wires the adapter.
func NewAdapter(inboxService contracts.InboxService, cfg Config) *Adapter {
	return &Adapter{inbox: inboxService, cfg: cfg.normalized(), tokens: make(map[string]string)}
}

// PollOnce implements contracts.WeChatService.
func (a *Adapter) PollOnce(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if a.cfg.Token == "" {
		return 0, contracts.ErrWeChatUnauthorized
	}
	body, _ := json.Marshal(map[string]any{})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.cfg.BaseURL, "/")+"/ilink/bot/getupdates", bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("%w: %v", contracts.ErrWeChatUnavailable, err)
	}
	a.setHeaders(request)

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

	var envelope struct {
		Msgs []inboundMessage `json:"msgs"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope); err != nil {
		return 0, fmt.Errorf("%w: decode: %v", contracts.ErrWeChatUnavailable, err)
	}

	ingested := 0
	for _, message := range envelope.Msgs {
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
func (a *Adapter) Send(ctx context.Context, toUserID, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return contracts.ErrWeChatEmptyMessage
	}
	if a.cfg.Token == "" {
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
	a.setHeaders(request)
	response, err := a.cfg.Client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %v", contracts.ErrWeChatUnavailable, err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return contracts.ErrWeChatUnauthorized
	default:
		return fmt.Errorf("%w: status %d", contracts.ErrWeChatUnavailable, response.StatusCode)
	}
}

// Status implements contracts.WeChatService.
func (a *Adapter) Status(ctx context.Context) (contracts.WeChatStatus, error) {
	if err := ctx.Err(); err != nil {
		return contracts.WeChatStatus{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	status := contracts.WeChatStatus{
		Connected: a.cfg.Token != "", LastInbound: a.lastInbound, BotID: a.botID,
	}
	if !a.lastInbound.IsZero() {
		status.WindowClosesAt = a.lastInbound.Add(contracts.WeChatWindow)
	}
	return status, nil
}

// setHeaders copies the iLink header contract (bearer token + a per-request
// random uint32 so the platform cannot correlate requests by a fixed id).
func (a *Adapter) setHeaders(request *http.Request) {
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("AuthorizationType", "ilink_bot_token")
	request.Header.Set("Authorization", "Bearer "+a.cfg.Token)
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
