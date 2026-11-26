// Package dingtalk is the DingTalk outbound adapter: one plain-text message to a
// conversation, plus the account state the settings page reads. It is not an
// ingress — DingTalk no longer files inbound messages (see
// docs/research/channel-capabilities.md §6), so there is no callback, signature
// or inbox path here at all. Everything about the outbound wire format is pinned
// by adapter_test.go.
package dingtalk

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

// DefaultBaseURL is the DingTalk OpenAPI host.
const DefaultBaseURL = "https://api.dingtalk.com"

// sendPath is the robot group-message endpoint (msgKey "sampleText").
const sendPath = "/v1.0/robot/groupMessages/send"

// msgKeyText is the robot message template for a plain-text reply.
const msgKeyText = "sampleText"

// Config is the module's typed assembly config.
type Config struct {
	// BaseURL overrides the API host (tests use a local server).
	BaseURL string `json:"baseUrl,omitempty"`
	// Token is the robot access token ("" = not connected). It is supplied by
	// configuration; the adapter performs no OAuth dance.
	Token string `json:"-"`
	// RobotCode is the robot identity the send API expects. It is part of the
	// credential, not an optional extra: every send body carries it, so a token
	// without one is a row that cannot deliver (see Status).
	RobotCode string `json:"robotCode,omitempty"`
	// Client overrides the HTTP client (tests / custom transport).
	Client *http.Client `json:"-"`
}

func (c Config) normalized() Config {
	if c.BaseURL == "" {
		c.BaseURL = DefaultBaseURL
	}
	if c.Client == nil {
		c.Client = &http.Client{Timeout: 45 * time.Second}
	}
	return c
}

// Adapter implements contracts.DingTalkService.
type Adapter struct {
	cfg Config
}

// NewAdapter wires the adapter.
func NewAdapter(cfg Config) *Adapter {
	return &Adapter{cfg: cfg.normalized()}
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
	// The body always carries robotCode, so an empty one is a guaranteed 400:
	// refuse it here, where the message can name the field, instead of spending
	// a round trip to learn what the platform's error page would say.
	if strings.TrimSpace(a.cfg.RobotCode) == "" {
		return fmt.Errorf("%w: missing robotCode (configure the robot code alongside the token)", contracts.ErrDingTalkInvalid)
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
//
// "Connected" means "a send would be accepted by this machine", not "a token is
// stored". The send body carries the robot code as well as the token, so a
// token-only row is reported as not connected, with the missing field named: a
// status that said otherwise is how a broken DingTalk setup looked healthy on
// the settings page and every real send came back 400.
func (a *Adapter) Status(ctx context.Context) (contracts.DingTalkStatus, error) {
	if err := ctx.Err(); err != nil {
		return contracts.DingTalkStatus{}, err
	}
	missing := make([]string, 0, 2)
	if strings.TrimSpace(a.cfg.Token) == "" {
		missing = append(missing, "token")
	}
	if strings.TrimSpace(a.cfg.RobotCode) == "" {
		missing = append(missing, "robotCode")
	}
	if len(missing) == 0 {
		return contracts.DingTalkStatus{Connected: true}, nil
	}
	return contracts.DingTalkStatus{
		Connected:     false,
		Reason:        "dingtalk: missing " + strings.Join(missing, " and ") + " — a send needs both the robot access token and the robot code",
		MissingFields: missing,
	}, nil
}

var _ contracts.DingTalkService = (*Adapter)(nil)
