// Package browser drives the user's local Chrome/Edge over the Chrome DevTools
// Protocol (plan §9.3). It is a deliberately thin skeleton: it speaks only the
// three documented domains the product needs today (Target, Runtime, Page),
// keeps every byte of protocol knowledge in this file, and takes the wire
// channel as an injected Transport so tests never launch a browser.
//
// Two boundaries matter:
//   - URL validation happens *before* any send, so a bad URL cannot create a
//     target, follow a redirect, or reach the browser at all.
//   - The page's CDP session id travels in the context (WithSession), not in the
//     method params: params stay exactly the documented CDP params, and a
//     multiplexing transport reads the session to route the message.
package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// MaxURLBytes caps the URL length accepted for navigation (2048 bytes, the
// interoperable limit used by browsers and proxies).
const MaxURLBytes = 2048

// ErrProtocol marks a well-formed transport reply that does not carry the
// field the protocol promises. It is wrapped by ErrBrowserUnavailable so the
// caller still sees the umbrella error, but the message stays diagnosable.
var ErrProtocol = errors.New("browser: protocol error")

// Transport is the CDP wire channel. Send writes one CDP command and returns
// the command result (not the whole envelope). Implementations are expected to
// honour the lifecycle of ctx and to route per-page commands using SessionFrom.
//
// A transport bound to a single target may ignore the session; a transport that
// speaks to a whole browser must use SessionFrom(ctx) to attach the command to
// the page's session (CDP `sessionId`).
type Transport interface {
	Send(ctx context.Context, method string, params any) (json.RawMessage, error)
}

// sessionKey carries the page's session id through the context.
type sessionKey struct{}

// WithSession attaches the page session to ctx. Empty pageID returns ctx
// unchanged, so callers cannot accidentally send an unscoped page command.
func WithSession(ctx context.Context, pageID string) context.Context {
	if pageID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionKey{}, pageID)
}

// SessionFrom reports the page session attached to ctx.
func SessionFrom(ctx context.Context) (string, bool) {
	pageID, ok := ctx.Value(sessionKey{}).(string)
	return pageID, ok && pageID != ""
}

// ValidateURL rejects everything the browser cannot be asked to open. It is
// intentionally strict about the scheme (http/https only — no file:, no
// javascript:, no data:, no chrome:) because those schemes are how a page
// escapes the browser sandbox or reads the local disk.
func ValidateURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("%w: empty url", contracts.ErrBrowserBadURL)
	}
	if len(raw) > MaxURLBytes {
		return fmt.Errorf("%w: url exceeds %d bytes", contracts.ErrBrowserBadURL, MaxURLBytes)
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: url contains a control character", contracts.ErrBrowserBadURL)
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", contracts.ErrBrowserBadURL, err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("%w: scheme %q is not http/https", contracts.ErrBrowserBadURL, parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%w: url has no host", contracts.ErrBrowserBadURL)
	}
	return nil
}

// CDP is the minimal DevTools client. It owns no policy (timeouts, page
// bookkeeping, error mapping): that is the adapter's job, so the protocol
// layer stays easy to read against the CDP documentation.
type CDP struct {
	transport Transport
	now       func() time.Time
}

// NewCDP builds a protocol client over tr. A nil clock falls back to time.Now.
func NewCDP(tr Transport, now func() time.Time) *CDP {
	if now == nil {
		now = time.Now
	}
	return &CDP{transport: tr, now: now}
}

// createTargetParams is Target.createTarget's documented input. newWindow is
// pinned to false: pages open as tabs in the user's visible window, which is
// the "the user can take over at any time" property the plan requires.
type createTargetParams struct {
	URL       string `json:"url"`
	NewWindow bool   `json:"newWindow"`
}

type createTargetResult struct {
	TargetID string `json:"targetId"`
}

// Open creates a page target for rawURL. The URL is validated first; Title is
// left empty until the caller asks, because document.title only exists after
// the navigation commits.
func (c *CDP) Open(ctx context.Context, rawURL string) (contracts.PageInfo, error) {
	if err := ValidateURL(rawURL); err != nil {
		return contracts.PageInfo{}, err
	}
	raw, err := c.send(ctx, "Target.createTarget", createTargetParams{URL: rawURL, NewWindow: false})
	if err != nil {
		return contracts.PageInfo{}, err
	}
	var result createTargetResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return contracts.PageInfo{}, fmt.Errorf("%w: createTarget reply: %v", ErrProtocol, err)
	}
	if result.TargetID == "" {
		return contracts.PageInfo{}, fmt.Errorf("%w: createTarget returned no targetId", ErrProtocol)
	}
	return contracts.PageInfo{
		ID:       result.TargetID,
		URL:      rawURL,
		Title:    "",
		OpenedAt: c.now(),
	}, nil
}

// evaluateParams is Runtime.evaluate's documented input (returnByValue keeps
// the value in the reply instead of a remote object handle we would have to
// release).
type evaluateParams struct {
	Expression    string `json:"expression"`
	ReturnByValue bool   `json:"returnByValue"`
}

type evaluateResult struct {
	Result struct {
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
	} `json:"result"`
	ExceptionDetails json.RawMessage `json:"exceptionDetails"`
}

// Title evaluates document.title in the page's session.
func (c *CDP) Title(ctx context.Context, pageID string) (string, error) {
	raw, err := c.send(WithSession(ctx, pageID), "Runtime.evaluate", evaluateParams{
		Expression:    "document.title",
		ReturnByValue: true,
	})
	if err != nil {
		return "", err
	}
	var result evaluateResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("%w: evaluate reply: %v", ErrProtocol, err)
	}
	if len(result.ExceptionDetails) > 0 && string(result.ExceptionDetails) != "null" {
		return "", fmt.Errorf("%w: document.title threw", ErrProtocol)
	}
	if len(result.Result.Value) == 0 {
		return "", fmt.Errorf("%w: evaluate returned no value", ErrProtocol)
	}
	var title string
	if err := json.Unmarshal(result.Result.Value, &title); err != nil {
		return "", fmt.Errorf("%w: document.title is %s, want string", ErrProtocol, result.Result.Type)
	}
	return title, nil
}

// captureScreenshotParams is Page.captureScreenshot's documented input.
// captureBeyondViewport makes "full page" a browser-side capture instead of a
// stitched set of scrolls (and never a desktop screen grab).
type captureScreenshotParams struct {
	Format                string `json:"format"`
	CaptureBeyondViewport bool   `json:"captureBeyondViewport"`
}

type captureScreenshotResult struct {
	Data string `json:"data"`
}

// Screenshot captures the page as PNG. full asks for the whole scrollable page.
func (c *CDP) Screenshot(ctx context.Context, pageID string, full bool) ([]byte, error) {
	raw, err := c.send(WithSession(ctx, pageID), "Page.captureScreenshot", captureScreenshotParams{
		Format:                "png",
		CaptureBeyondViewport: full,
	})
	if err != nil {
		return nil, err
	}
	var result captureScreenshotResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("%w: captureScreenshot reply: %v", ErrProtocol, err)
	}
	encoded := strings.TrimSpace(result.Data)
	if encoded == "" {
		return nil, fmt.Errorf("%w: captureScreenshot returned no data", ErrProtocol)
	}
	// Tolerate the data-URL form some transports hand back verbatim.
	if index := strings.Index(encoded, ";base64,"); index >= 0 {
		encoded = encoded[index+len(";base64,"):]
	}
	png, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: screenshot is not base64: %v", ErrProtocol, err)
	}
	return png, nil
}

// closeTargetParams is Target.closeTarget's documented input.
type closeTargetParams struct {
	TargetID string `json:"targetId"`
}

type closeTargetResult struct {
	Success bool `json:"success"`
}

// Close closes the page target.
func (c *CDP) Close(ctx context.Context, pageID string) error {
	raw, err := c.send(ctx, "Target.closeTarget", closeTargetParams{TargetID: pageID})
	if err != nil {
		return err
	}
	// Older browser versions answered with an empty result; only a *negative*
	// success is treated as a failure so the skeleton works across versions.
	if len(raw) == 0 {
		return nil
	}
	var result closeTargetResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("%w: closeTarget reply: %v", ErrProtocol, err)
	}
	if !result.Success {
		return fmt.Errorf("%w: browser refused to close target %q", contracts.ErrBrowserNotFound, pageID)
	}
	return nil
}

// send is the single place a command goes out. It refuses to run without a
// transport so a misconfigured adapter fails with a typed error instead of a
// nil-pointer panic.
func (c *CDP) send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c == nil || c.transport == nil {
		return nil, fmt.Errorf("%w: no transport", contracts.ErrBrowserUnavailable)
	}
	return c.transport.Send(ctx, method, params)
}
