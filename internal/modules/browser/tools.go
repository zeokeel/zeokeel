// The browser tool face: the model-facing tools that expose the local browser
// (plan §9.3) to the agent. Three tools, one page-oriented sequence: open a URL,
// then name the returned page id for its title or a screenshot.
//
// The tools are a thin adaptation layer over contracts.BrowserService and own no
// browser policy: URL validation lives in cdp.go (before anything is sent), the
// timeout and the error mapping live in adapter.go, and page bookkeeping stays
// in the adapter. What this file owns is the model-facing contract — stable
// names, JSON schemas, argument validation, typed-error text and the shape of
// the result — so a tool call can never panic the agent or reach the browser
// with a blank argument.
package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Model-facing tool names. They are stable identifiers: the agent system
// prompt, the UI tool cards and session snapshots may refer to them by name, so
// they do not change with the implementation.
const (
	ToolOpen       = "browser_open"
	ToolTitle      = "browser_title"
	ToolScreenshot = "browser_screenshot"
)

// browserTools returns the tool face bound to service, in registration order.
// A nil service still yields the three tools: each one answers with a readable
// error result, which keeps the tool list stable across a product without a
// browser instead of silently removing capabilities the prompt may mention.
func browserTools(service contracts.BrowserService) []rig.Tool {
	return []rig.Tool{
		&openTool{browser: service},
		&titleTool{browser: service},
		&screenshotTool{browser: service},
	}
}

// --- browser_open -----------------------------------------------------------

// openTool opens a URL as a new tab in the user's own browser.
type openTool struct {
	browser contracts.BrowserService
}

// Name implements rig.Tool.
func (t *openTool) Name() string { return ToolOpen }

// Description implements rig.Tool.
func (t *openTool) Description() string {
	return "Open an http/https URL as a new tab in the user's local browser (the window is visible to the user) and return the page id. " +
		"Use it when the task needs a live web page: a site the user asked about, a page they must finish by hand (a login or a form), or a page you want to screenshot. " +
		"Other browser tools take the returned pageId. Only http/https is accepted; the URL is checked before anything is sent to the browser."
}

// Schema implements rig.Tool.
func (t *openTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"url": stringProperty("Absolute http/https URL to open (a bare host or another scheme is rejected)."),
	}, "url")
}

// Invoke implements rig.Tool.
func (t *openTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	var args struct {
		URL string `json:"url"`
	}
	if err := decodeArguments(call, &args); err != nil {
		return errorResult("%s: %v", ToolOpen, err), nil
	}
	rawURL := strings.TrimSpace(args.URL)
	if rawURL == "" {
		return errorResult("%s: url is required: pass an absolute http/https URL", ToolOpen), nil
	}
	if t.browser == nil {
		return errorResult("%s: no browser service is available in this product", ToolOpen), nil
	}
	page, err := t.browser.Open(ctx, rawURL)
	if err != nil {
		return browserFailure(ctx, ToolOpen, err)
	}
	// PageInfo.URL is the URL the browser was asked to open; a backend that
	// leaves it empty still must not hide what this call did.
	opened := page.URL
	if opened == "" {
		opened = rawURL
	}
	return jsonResult(
		ToolOpen,
		pageResult{PageID: page.ID, URL: opened},
		map[string]any{"pageId": page.ID, "url": opened},
	), nil
}

// pageResult is browser_open's model-facing result.
type pageResult struct {
	PageID string `json:"pageId"`
	URL    string `json:"url"`
}

// --- browser_title ----------------------------------------------------------

// titleTool reads a page's document.title.
type titleTool struct {
	browser contracts.BrowserService
}

// Name implements rig.Tool.
func (t *titleTool) Name() string { return ToolTitle }

// Description implements rig.Tool.
func (t *titleTool) Description() string {
	return "Read the current document.title of a page opened with browser_open. " +
		"Use it when you need to confirm which page is loaded (after a navigation, or before acting on a tab) without capturing an image. " +
		"An empty title is a successful read: the document may not have a title yet. Pass only page ids returned by browser_open; unknown ids are reported as an error."
}

// Schema implements rig.Tool.
func (t *titleTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"pageId": stringProperty("Page id returned by browser_open."),
	}, "pageId")
}

// Invoke implements rig.Tool.
func (t *titleTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	var args struct {
		PageID string `json:"pageId"`
	}
	if err := decodeArguments(call, &args); err != nil {
		return errorResult("%s: %v", ToolTitle, err), nil
	}
	pageID := strings.TrimSpace(args.PageID)
	if pageID == "" {
		return errorResult("%s: pageId is required: pass the id browser_open returned", ToolTitle), nil
	}
	if t.browser == nil {
		return errorResult("%s: no browser service is available in this product", ToolTitle), nil
	}
	title, err := t.browser.Title(ctx, pageID)
	if err != nil {
		return browserFailure(ctx, ToolTitle, err)
	}
	return jsonResult(
		ToolTitle,
		titleResult{PageID: pageID, Title: title},
		map[string]any{"pageId": pageID, "title": title},
	), nil
}

// titleResult is browser_title's model-facing result. Title stays a string even
// when empty, so the model never has to tell "no title yet" from "null".
type titleResult struct {
	PageID string `json:"pageId"`
	Title  string `json:"title"`
}

// --- browser_screenshot -----------------------------------------------------

// screenshotTool captures a page as PNG.
type screenshotTool struct {
	browser contracts.BrowserService
}

// Name implements rig.Tool.
func (t *screenshotTool) Name() string { return ToolScreenshot }

// Description implements rig.Tool.
func (t *screenshotTool) Description() string {
	return "Capture a page opened with browser_open as a PNG image and attach it to the result so you can see the page. " +
		"Use it when the answer depends on what the page looks like (layout, a rendered chart, a visual check) rather than on its text. " +
		"Set full=true to capture the whole scrollable page instead of the visible viewport."
}

// Schema implements rig.Tool.
func (t *screenshotTool) Schema() map[string]any {
	return objectSchema(map[string]any{
		"pageId": stringProperty("Page id returned by browser_open."),
		"full":   boolProperty("Capture the whole scrollable page instead of the visible viewport (default false)."),
	}, "pageId")
}

// Invoke implements rig.Tool.
//
// The PNG travels as a ToolResult image content part (rig.ImagePart with a
// data: URL), not as base64 inside Content. The Rig agent forwards Parts to the
// model as a real image, so a vision model can actually look at the page, while
// Content stays a short JSON summary (page, format, byte size) instead of
// megabytes of base64 that would eat the context window. The byte size in
// Content is what a text-only reader needs to know an image exists.
func (t *screenshotTool) Invoke(ctx context.Context, call rig.ToolCall) (rig.ToolResult, error) {
	var args struct {
		PageID string `json:"pageId"`
		Full   bool   `json:"full"`
	}
	if err := decodeArguments(call, &args); err != nil {
		return errorResult("%s: %v", ToolScreenshot, err), nil
	}
	pageID := strings.TrimSpace(args.PageID)
	if pageID == "" {
		return errorResult("%s: pageId is required: pass the id browser_open returned", ToolScreenshot), nil
	}
	if t.browser == nil {
		return errorResult("%s: no browser service is available in this product", ToolScreenshot), nil
	}
	png, err := t.browser.Screenshot(ctx, pageID, args.Full)
	if err != nil {
		return browserFailure(ctx, ToolScreenshot, err)
	}
	if len(png) == 0 {
		// A zero-byte "success" is useless to the caller and must not be
		// forwarded as an empty image part.
		return errorResult("%s: the browser returned an empty image for page %q", ToolScreenshot, pageID), nil
	}
	result := jsonResult(
		ToolScreenshot,
		screenshotResult{PageID: pageID, Format: "png", Bytes: len(png), Full: args.Full},
		map[string]any{"pageId": pageID, "format": "png", "bytes": len(png), "full": args.Full},
	)
	if result.IsError {
		return result, nil
	}
	result.Parts = []rig.ContentPart{
		rig.ImagePart("data:image/png;base64,"+base64.StdEncoding.EncodeToString(png), ""),
	}
	return result, nil
}

// screenshotResult is browser_screenshot's model-facing text result.
type screenshotResult struct {
	PageID string `json:"pageId"`
	Format string `json:"format"`
	Bytes  int    `json:"bytes"`
	Full   bool   `json:"full"`
}

// --- shared helpers ---------------------------------------------------------

// decodeArguments decodes one call's raw arguments; an empty argument string is
// the empty object. A decode failure is returned as text, never as a panic.
func decodeArguments(call rig.ToolCall, target any) error {
	raw := strings.TrimSpace(call.Arguments)
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("invalid arguments JSON: %v", err)
	}
	return nil
}

// jsonResult renders payload as the model-facing text and keeps data as the
// structured result for the host. Both describe the same call; the host must not
// have to parse the text.
func jsonResult(tool string, payload any, data map[string]any) rig.ToolResult {
	encoded, err := json.Marshal(payload)
	if err != nil {
		// Only the local struct shapes above reach this path; keep the failure
		// model-readable instead of dropping the result.
		return errorResult("%s: encode tool result: %v", tool, err)
	}
	return rig.ToolResult{Content: string(encoded), Data: data}
}

// browserFailure turns a backend error into a model-facing error result. A
// cancelled or expired caller context is returned as a Go error instead, so the
// run stops rather than reporting a tool the model might retry in a loop.
func browserFailure(ctx context.Context, tool string, err error) (rig.ToolResult, error) {
	if ctxErr := contextError(ctx); ctxErr != nil {
		return rig.ToolResult{}, ctxErr
	}
	return errorResult("%s: %s", tool, describeBrowserError(err)), nil
}

// describeBrowserError adds the one thing the model cannot infer from the typed
// error alone: whether retrying can help. The typed error text stays in the
// message so a host reading tool results still sees which error fired.
func describeBrowserError(err error) string {
	switch {
	case errors.Is(err, contracts.ErrBrowserBadURL):
		return err.Error() + " (the URL must be an absolute http/https URL; nothing was sent to the browser)"
	case errors.Is(err, contracts.ErrBrowserUnavailable):
		return err.Error() + " (no local browser is connected; ask the user to start it or configure the endpoint)"
	case errors.Is(err, contracts.ErrBrowserPermissionDenied):
		return err.Error() + " (the browser refused this page; do not retry the same call)"
	default:
		return err.Error()
	}
}

// contextError returns the context's error, tolerating a nil context.
func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// errorResult is a model-facing tool failure.
func errorResult(format string, args ...any) rig.ToolResult {
	return rig.ToolResult{Content: fmt.Sprintf(format, args...), IsError: true}
}

// objectSchema is the JSON Schema object advertised for one tool.
func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// stringProperty declares one string argument.
func stringProperty(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// boolProperty declares one boolean argument.
func boolProperty(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}
