// Package feishu is the Feishu (Lark) outbound adapter: one plain-text message to
// a chat, plus the account state the settings page reads. It is not an ingress —
// Feishu no longer pushes events to us (see
// docs/research/channel-capabilities.md §6), so there is no event callback, no
// url_verification handshake and no card action path here: a card button must
// never resolve an approval. The signature/decrypt code is gone with it.
package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// DefaultBaseURL is the Feishu open-platform host.
const DefaultBaseURL = "https://open.feishu.cn"

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
	// Client overrides the HTTP client (tests / custom transport).
	Client *http.Client `json:"-"`
}

func (c Config) normalized() Config {
	if c.BaseURL == "" {
		c.BaseURL = DefaultBaseURL
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
	cfg Config

	// mu guards the minted-tenant-token cache.
	mu sync.Mutex
	// token/tokenExpiry cache the minted tenant access token.
	token       string
	tokenExpiry time.Time
}

// NewAdapter wires the adapter.
func NewAdapter(cfg Config) *Adapter {
	return &Adapter{cfg: cfg.normalized()}
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
	return contracts.FeishuStatus{Connected: a.cfg.configured()}, nil
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
	if a.token != "" && time.Now().Before(a.tokenExpiry) {
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

	expiry := time.Now().Add(time.Minute)
	if result.Expire > 0 {
		expiry = time.Now().Add(time.Duration(result.Expire)*time.Second - tokenRefreshMargin)
	}
	a.mu.Lock()
	a.token = result.Token
	a.tokenExpiry = expiry
	a.mu.Unlock()
	return result.Token, nil
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
