package browser

// Tool-face tests: metadata (stable names, JSON schemas, descriptions that say
// when to use), happy paths over a fake BrowserService, argument validation,
// typed backend error propagation and the module's registry wiring (register,
// duplicate tolerance, cleanup, and the no-registry case). No browser, no
// display, no network.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/hosttools"
)

// fakeBrowser is a contracts.BrowserService test double: it records the calls
// the tools make and returns whatever the test configured.
type fakeBrowser struct {
	mu sync.Mutex

	openedURLs []string
	titleIDs   []string
	shotCalls  []shotCall

	page  contracts.PageInfo
	title string
	png   []byte

	openErr  error
	titleErr error
	shotErr  error
}

type shotCall struct {
	pageID string
	full   bool
}

var _ contracts.BrowserService = (*fakeBrowser)(nil)

func (f *fakeBrowser) Open(_ context.Context, rawURL string) (contracts.PageInfo, error) {
	f.mu.Lock()
	f.openedURLs = append(f.openedURLs, rawURL)
	f.mu.Unlock()
	if f.openErr != nil {
		return contracts.PageInfo{}, f.openErr
	}
	if f.page.ID == "" {
		return contracts.PageInfo{ID: "page-1", URL: rawURL}, nil
	}
	return f.page, nil
}

func (f *fakeBrowser) Title(_ context.Context, pageID string) (string, error) {
	f.mu.Lock()
	f.titleIDs = append(f.titleIDs, pageID)
	f.mu.Unlock()
	if f.titleErr != nil {
		return "", f.titleErr
	}
	return f.title, nil
}

func (f *fakeBrowser) Screenshot(_ context.Context, pageID string, full bool) ([]byte, error) {
	f.mu.Lock()
	f.shotCalls = append(f.shotCalls, shotCall{pageID: pageID, full: full})
	f.mu.Unlock()
	if f.shotErr != nil {
		return nil, f.shotErr
	}
	return f.png, nil
}

func (f *fakeBrowser) Close(context.Context, string) error { return nil }

func (f *fakeBrowser) Status(context.Context) (contracts.BrowserStatus, error) {
	return contracts.BrowserStatus{}, nil
}

func toolNamed(t *testing.T, service contracts.BrowserService, name string) rig.Tool {
	t.Helper()
	for _, tool := range browserTools(service) {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("no tool named %q in the browser tool face", name)
	return nil
}

func invoke(t *testing.T, tool rig.Tool, arguments string) rig.ToolResult {
	t.Helper()
	result, err := tool.Invoke(context.Background(), rig.ToolCall{Name: tool.Name(), Arguments: arguments})
	if err != nil {
		t.Fatalf("%s.Invoke(%s) returned a Go error: %v", tool.Name(), arguments, err)
	}
	return result
}

func decodeContent(t *testing.T, tool rig.Tool, result rig.ToolResult, target any) {
	t.Helper()
	if err := json.Unmarshal([]byte(result.Content), target); err != nil {
		t.Fatalf("%s content %q is not JSON: %v", tool.Name(), result.Content, err)
	}
}

// --- metadata ---------------------------------------------------------------

func TestBrowserToolNamesAreStable(t *testing.T) {
	tools := browserTools(&fakeBrowser{})
	got := make([]string, 0, len(tools))
	for _, tool := range tools {
		got = append(got, tool.Name())
	}
	want := []string{"browser_open", "browser_title", "browser_screenshot"}
	if len(got) != len(want) {
		t.Fatalf("tool names = %v, want %v", got, want)
	}
	for index, name := range want {
		if got[index] != name {
			t.Errorf("tool %d = %q, want %q: names are stable identifiers shared with prompts, UI cards and snapshots", index, got[index], name)
		}
	}
	if ToolOpen != "browser_open" || ToolTitle != "browser_title" || ToolScreenshot != "browser_screenshot" {
		t.Errorf("name constants = %q/%q/%q, want browser_open/browser_title/browser_screenshot", ToolOpen, ToolTitle, ToolScreenshot)
	}
}

func TestBrowserToolSchemasDeclareRequiredArguments(t *testing.T) {
	cases := []struct {
		name     string
		required []string
		optional map[string]string
	}{
		{name: ToolOpen, required: []string{"url"}},
		{name: ToolTitle, required: []string{"pageId"}},
		{name: ToolScreenshot, required: []string{"pageId"}, optional: map[string]string{"full": "boolean"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tool := toolNamed(t, &fakeBrowser{}, testCase.name)
			schema := tool.Schema()
			if schema["type"] != "object" {
				t.Fatalf("schema type = %v, want object", schema["type"])
			}
			if schema["additionalProperties"] != false {
				t.Errorf("schema additionalProperties = %v, want false", schema["additionalProperties"])
			}
			properties, ok := schema["properties"].(map[string]any)
			if !ok {
				t.Fatalf("schema properties = %T, want a map", schema["properties"])
			}
			for name := range properties {
				described, _ := properties[name].(map[string]any)
				if strings.TrimSpace(fmt.Sprint(described["description"])) == "" {
					t.Errorf("property %q has no description", name)
				}
			}
			for name, wantType := range testCase.optional {
				property, ok := properties[name].(map[string]any)
				if !ok {
					t.Fatalf("optional property %q is missing", name)
				}
				if property["type"] != wantType {
					t.Errorf("property %q type = %v, want %s", name, property["type"], wantType)
				}
			}
			required, ok := schema["required"].([]string)
			if !ok {
				t.Fatalf("schema required = %T, want []string", schema["required"])
			}
			if len(required) != len(testCase.required) {
				t.Fatalf("required = %v, want %v", required, testCase.required)
			}
			for index, name := range testCase.required {
				if required[index] != name {
					t.Errorf("required[%d] = %q, want %q", index, required[index], name)
				}
				if _, ok := properties[name]; !ok {
					t.Errorf("required property %q is not declared in properties", name)
				}
				if _, alsoOptional := testCase.optional[name]; alsoOptional {
					t.Errorf("property %q cannot be both required and optional", name)
				}
			}
		})
	}
}

func TestBrowserToolDescriptionsSayWhenToUse(t *testing.T) {
	for _, tool := range browserTools(&fakeBrowser{}) {
		description := tool.Description()
		if len(description) < 40 {
			t.Errorf("%s description %q is too short to explain the tool", tool.Name(), description)
		}
		if !strings.Contains(description, "Use it when") {
			t.Errorf("%s description %q does not say when to use it", tool.Name(), description)
		}
		if strings.Contains(description, "TODO") {
			t.Errorf("%s description is a placeholder: %q", tool.Name(), description)
		}
	}
}

// --- happy paths ------------------------------------------------------------

func TestBrowserOpenReturnsPageIDAndURL(t *testing.T) {
	service := &fakeBrowser{page: contracts.PageInfo{ID: "T-7"}}
	tool := toolNamed(t, service, ToolOpen)
	result := invoke(t, tool, `{"url":"https://example.com/docs?x=1"}`)
	if result.IsError {
		t.Fatalf("open result = %q, want success", result.Content)
	}

	var payload struct {
		PageID string `json:"pageId"`
		URL    string `json:"url"`
	}
	decodeContent(t, tool, result, &payload)
	if payload.PageID != "T-7" {
		t.Errorf("pageId = %q, want T-7", payload.PageID)
	}
	if payload.URL != "https://example.com/docs?x=1" {
		t.Errorf("url = %q, want the opened url", payload.URL)
	}

	data, ok := result.Data.(map[string]any)
	if !ok {
		t.Fatalf("Data = %T, want a map for the host", result.Data)
	}
	if data["pageId"] != "T-7" {
		t.Errorf("Data[pageId] = %v, want T-7", data["pageId"])
	}
	if data["url"] != "https://example.com/docs?x=1" {
		t.Errorf("Data[url] = %v, want the opened url", data["url"])
	}
	if len(service.openedURLs) != 1 || service.openedURLs[0] != "https://example.com/docs?x=1" {
		t.Errorf("Open calls = %v, want exactly the requested url", service.openedURLs)
	}
}

func TestBrowserOpenFallsBackToTheRequestedURL(t *testing.T) {
	// A backend that reports no URL (an older transport) must not hide the page:
	// the tool echoes the URL it was asked to open.
	service := &fakeBrowser{page: contracts.PageInfo{ID: "T-1"}}
	tool := toolNamed(t, service, ToolOpen)
	result := invoke(t, tool, `{"url":"https://example.com/"}`)
	var payload struct {
		URL string `json:"url"`
	}
	decodeContent(t, tool, result, &payload)
	if payload.URL != "https://example.com/" {
		t.Errorf("url = %q, want the requested url", payload.URL)
	}
}

func TestBrowserTitleReturnsTitle(t *testing.T) {
	service := &fakeBrowser{title: "Example Domain"}
	tool := toolNamed(t, service, ToolTitle)
	result := invoke(t, tool, `{"pageId":"page-1"}`)
	if result.IsError {
		t.Fatalf("title result = %q, want success", result.Content)
	}

	var payload struct {
		Title string `json:"title"`
	}
	decodeContent(t, tool, result, &payload)
	if payload.Title != "Example Domain" {
		t.Errorf("title = %q, want Example Domain", payload.Title)
	}
	data, ok := result.Data.(map[string]any)
	if !ok || data["title"] != "Example Domain" || data["pageId"] != "page-1" {
		t.Errorf("Data = %+v, want {pageId: page-1, title: Example Domain}", result.Data)
	}
	if len(service.titleIDs) != 1 || service.titleIDs[0] != "page-1" {
		t.Errorf("Title calls = %v, want [page-1]", service.titleIDs)
	}
}

func TestBrowserTitleReturnsAnEmptyTitleWithoutError(t *testing.T) {
	// document.title can legitimately be empty before the document commits; an
	// empty title is a successful read, not a tool failure.
	tool := toolNamed(t, &fakeBrowser{}, ToolTitle)
	result := invoke(t, tool, `{"pageId":"page-1"}`)
	if result.IsError {
		t.Fatalf("title result = %q, want success for an empty title", result.Content)
	}
	var payload struct {
		Title string `json:"title"`
	}
	decodeContent(t, tool, result, &payload)
	if payload.Title != "" {
		t.Errorf("title = %q, want empty", payload.Title)
	}
}

func TestBrowserScreenshotAttachesThePNGAsAnImagePart(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x01, 0x02}
	service := &fakeBrowser{png: png}
	tool := toolNamed(t, service, ToolScreenshot)
	result := invoke(t, tool, `{"pageId":"page-1"}`)
	if result.IsError {
		t.Fatalf("screenshot result = %q, want success", result.Content)
	}

	// The PNG travels as an image content part (a data URL), not as base64 in
	// the model-facing text: see the note on screenshotTool.Invoke.
	if len(result.Parts) != 1 {
		t.Fatalf("Parts = %+v, want exactly one image part", result.Parts)
	}
	part := result.Parts[0]
	if part.Type != rig.PartImageURL {
		t.Fatalf("part type = %q, want %q", part.Type, rig.PartImageURL)
	}
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(part.ImageURL, prefix) {
		t.Fatalf("image url does not start with %q: %.40q", prefix, part.ImageURL)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(part.ImageURL, prefix))
	if err != nil {
		t.Fatalf("image part is not valid base64: %v", err)
	}
	if !bytes.Equal(decoded, png) {
		t.Errorf("decoded image = %v, want the PNG the browser returned", decoded)
	}

	// The text stays small: page, format and byte size, so a text-only reader
	// still knows an image exists and how big it is.
	var payload struct {
		PageID string `json:"pageId"`
		Format string `json:"format"`
		Bytes  int    `json:"bytes"`
		Full   bool   `json:"full"`
	}
	decodeContent(t, tool, result, &payload)
	if payload.PageID != "page-1" || payload.Format != "png" || payload.Bytes != len(png) || payload.Full {
		t.Errorf("screenshot content = %+v, want {page-1 png %d false}", payload, len(png))
	}
	if len(result.Content) > 200 {
		t.Errorf("screenshot content is %d bytes long, want a short summary", len(result.Content))
	}
	data, ok := result.Data.(map[string]any)
	if !ok || data["bytes"] != len(png) {
		t.Errorf("Data = %+v, want the byte count for the host", result.Data)
	}
}

func TestBrowserScreenshotPassesTheFullFlag(t *testing.T) {
	service := &fakeBrowser{png: []byte("png")}
	tool := toolNamed(t, service, ToolScreenshot)

	invoke(t, tool, `{"pageId":"page-1"}`)
	invoke(t, tool, `{"pageId":"page-1","full":true}`)

	want := []shotCall{{pageID: "page-1", full: false}, {pageID: "page-1", full: true}}
	if len(service.shotCalls) != len(want) {
		t.Fatalf("Screenshot calls = %+v, want %+v", service.shotCalls, want)
	}
	for index := range want {
		if service.shotCalls[index] != want[index] {
			t.Errorf("call %d = %+v, want %+v", index, service.shotCalls[index], want[index])
		}
	}
}

// --- argument validation ----------------------------------------------------

func TestBrowserToolsRejectMissingArguments(t *testing.T) {
	cases := []struct {
		name      string
		tool      string
		arguments string
		want      string
	}{
		{name: "open-missing-url", tool: ToolOpen, arguments: `{}`, want: "url is required"},
		{name: "open-empty-url", tool: ToolOpen, arguments: `{"url":""}`, want: "url is required"},
		{name: "open-blank-url", tool: ToolOpen, arguments: `{"url":"   \t\n"}`, want: "url is required"},
		{name: "open-null-url", tool: ToolOpen, arguments: `{"url":null}`, want: "url is required"},
		{name: "title-missing-page", tool: ToolTitle, arguments: `{}`, want: "pageId is required"},
		{name: "title-empty-page", tool: ToolTitle, arguments: `{"pageId":""}`, want: "pageId is required"},
		{name: "title-blank-page", tool: ToolTitle, arguments: `{"pageId":"  "}`, want: "pageId is required"},
		{name: "screenshot-missing-page", tool: ToolScreenshot, arguments: `{}`, want: "pageId is required"},
		{name: "screenshot-empty-page", tool: ToolScreenshot, arguments: `{"pageId":""}`, want: "pageId is required"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			service := &fakeBrowser{png: []byte("png"), title: "should not be read"}
			tool := toolNamed(t, service, testCase.tool)
			result := invoke(t, tool, testCase.arguments)
			if !result.IsError {
				t.Fatalf("%s(%s) = %q, want an error result", testCase.tool, testCase.arguments, result.Content)
			}
			if !strings.Contains(result.Content, testCase.want) {
				t.Errorf("%s(%s) = %q, want it to mention %q", testCase.tool, testCase.arguments, result.Content, testCase.want)
			}
			if !strings.Contains(result.Content, testCase.tool) {
				t.Errorf("%s error %q does not name the tool", testCase.tool, result.Content)
			}
			if len(service.openedURLs)+len(service.titleIDs)+len(service.shotCalls) != 0 {
				t.Errorf("a rejected call reached the browser: %v/%v/%v", service.openedURLs, service.titleIDs, service.shotCalls)
			}
		})
	}
}

func TestBrowserToolsRejectMalformedArguments(t *testing.T) {
	for _, tool := range browserTools(&fakeBrowser{}) {
		result := invoke(t, tool, `{"url":`)
		if !result.IsError {
			t.Fatalf("%s with truncated JSON = %q, want an error result", tool.Name(), result.Content)
		}
		if !strings.Contains(result.Content, "invalid arguments JSON") {
			t.Errorf("%s error = %q, want it to name the JSON problem", tool.Name(), result.Content)
		}
	}
}

func TestBrowserToolsReportAMissingService(t *testing.T) {
	for _, tool := range browserTools(nil) {
		arguments := `{"url":"https://example.com/","pageId":"page-1"}`
		result := invoke(t, tool, arguments)
		if !result.IsError {
			t.Fatalf("%s without a browser service = %q, want an error result", tool.Name(), result.Content)
		}
		if !strings.Contains(result.Content, "no browser service") {
			t.Errorf("%s error = %q, want it to say the service is missing", tool.Name(), result.Content)
		}
	}
}

// --- backend error propagation ----------------------------------------------

func TestBrowserToolsSurfaceBackendErrors(t *testing.T) {
	badURL := fmt.Errorf("%w: scheme %q is not http/https", contracts.ErrBrowserBadURL, "file")
	unavailable := fmt.Errorf("%w: no endpoint configured", contracts.ErrBrowserUnavailable)
	notFound := fmt.Errorf("%w: %q", contracts.ErrBrowserNotFound, "page-9")
	timeout := fmt.Errorf("%w: %v", contracts.ErrBrowserTimeout, context.DeadlineExceeded)
	denied := fmt.Errorf("%w: navigation blocked by policy", contracts.ErrBrowserPermissionDenied)

	cases := []struct {
		name      string
		tool      string
		arguments string
		service   *fakeBrowser
		err       error
		want      []string
	}{
		{
			name: "open-bad-url", tool: ToolOpen, arguments: `{"url":"file:///etc/passwd"}`,
			service: &fakeBrowser{openErr: badURL},
			want:    []string{"browser: bad url", "not http/https"},
		},
		{
			name: "open-unavailable", tool: ToolOpen, arguments: `{"url":"https://example.com/"}`,
			service: &fakeBrowser{openErr: unavailable},
			want:    []string{"browser: unavailable", "no local browser is connected"},
		},
		{
			name: "title-not-found", tool: ToolTitle, arguments: `{"pageId":"page-9"}`,
			service: &fakeBrowser{titleErr: notFound},
			want:    []string{"browser: page not found", "page-9"},
		},
		{
			name: "title-timeout", tool: ToolTitle, arguments: `{"pageId":"page-1"}`,
			service: &fakeBrowser{titleErr: timeout},
			want:    []string{"browser: timeout"},
		},
		{
			name: "screenshot-not-found", tool: ToolScreenshot, arguments: `{"pageId":"page-9"}`,
			service: &fakeBrowser{shotErr: notFound},
			want:    []string{"browser: page not found"},
		},
		{
			name: "screenshot-denied", tool: ToolScreenshot, arguments: `{"pageId":"page-1"}`,
			service: &fakeBrowser{shotErr: denied},
			want:    []string{"browser: permission denied", "do not retry"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			tool := toolNamed(t, testCase.service, testCase.tool)
			result := invoke(t, tool, testCase.arguments)
			if !result.IsError {
				t.Fatalf("%s(%s) = %q, want an error result, not a panic and not success", testCase.tool, testCase.arguments, result.Content)
			}
			for _, want := range testCase.want {
				if !strings.Contains(result.Content, want) {
					t.Errorf("%s error = %q, want it to mention %q", testCase.tool, result.Content, want)
				}
			}
			if len(result.Parts) != 0 {
				t.Errorf("%s error carried image parts: %+v", testCase.tool, result.Parts)
			}
		})
	}
}

func TestBrowserScreenshotRejectsAnEmptyImage(t *testing.T) {
	// A zero-byte "success" is a protocol failure from the model's point of
	// view: an image part with nothing in it must not be forwarded.
	tool := toolNamed(t, &fakeBrowser{}, ToolScreenshot)
	result := invoke(t, tool, `{"pageId":"page-1"}`)
	if !result.IsError {
		t.Fatalf("empty screenshot = %q, want an error result", result.Content)
	}
	if !strings.Contains(result.Content, "empty image") {
		t.Errorf("error = %q, want it to say the image was empty", result.Content)
	}
	if len(result.Parts) != 0 {
		t.Errorf("an empty screenshot produced image parts: %+v", result.Parts)
	}
}

func TestBrowserToolsHonourCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service := &fakeBrowser{openErr: fmt.Errorf("%w: %v", contracts.ErrBrowserUnavailable, context.Canceled)}
	tool := toolNamed(t, service, ToolOpen)
	if _, err := tool.Invoke(ctx, rig.ToolCall{Arguments: `{"url":"https://example.com/"}`}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Invoke(cancelled) = %v, want context.Canceled so the run stops instead of the model retrying", err)
	}
}

// --- registry wiring --------------------------------------------------------

// TestRegisteredToolsDriveTheAdapter walks the full seam the agent uses: the
// module registers its tools on the host registry, and a call through the
// registry reaches the real adapter and the CDP wire (a fake transport). It
// catches a mis-bound service, not just a mis-registered name.
func TestRegisteredToolsDriveTheAdapter(t *testing.T) {
	root := newTestRoot(t)
	registry := rigtools.NewRegistry()
	if _, err := root.Provide(hosttools.KeyRegistry, registry); err != nil {
		t.Fatalf("provide registry: %v", err)
	}
	if err := Module().Factory(root, Config{Endpoint: testEndpoint, Transport: pageTransport(t)}); err != nil {
		t.Fatalf("factory: %v", err)
	}

	call := func(name, arguments string) rig.ToolResult {
		t.Helper()
		result, err := registry.Invoke(context.Background(), rig.ToolCall{Name: name, Arguments: arguments})
		if err != nil {
			t.Fatalf("registry.Invoke(%s, %s): %v", name, arguments, err)
		}
		if result.IsError {
			t.Fatalf("registry.Invoke(%s, %s) failed: %s", name, arguments, result.Content)
		}
		return result
	}

	opened := call(ToolOpen, `{"url":"https://example.com/"}`)
	var page struct {
		PageID string `json:"pageId"`
		URL    string `json:"url"`
	}
	decodeContent(t, toolNamed(t, nil, ToolOpen), opened, &page)
	if page.PageID == "" || page.URL != "https://example.com/" {
		t.Fatalf("browser_open = %+v, want the target id and the opened url", page)
	}

	titleArguments := fmt.Sprintf(`{"pageId":%q}`, page.PageID)
	var titled struct {
		Title string `json:"title"`
	}
	decodeContent(t, toolNamed(t, nil, ToolTitle), call(ToolTitle, titleArguments), &titled)
	if titled.Title == "" {
		t.Errorf("browser_title = %+v, want the title the transport answered", titled)
	}

	shot := call(ToolScreenshot, titleArguments)
	if len(shot.Parts) != 1 || !strings.HasPrefix(shot.Parts[0].ImageURL, "data:image/png;base64,") {
		t.Errorf("browser_screenshot parts = %+v, want one PNG data-URL part", shot.Parts)
	}
}

// stubTool is a placeholder that claims a browser tool name before the module
// loads, so the duplicate-tolerance path is exercised.
type stubTool struct{ name string }

func (s stubTool) Name() string           { return s.name }
func (s stubTool) Description() string    { return "stub tool that must be replaced by the module" }
func (s stubTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (stubTool) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{Content: "stub"}, nil
}

func newTestRoot(t *testing.T) cordis.Context {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	return root
}

func registeredNames(registry *rigtools.Registry) []string {
	names := make([]string, 0)
	for _, tool := range registry.Tools() {
		names = append(names, tool.Name())
	}
	return names
}

func TestModuleRegistersBrowserToolsWithARegistry(t *testing.T) {
	root := newTestRoot(t)
	registry := rigtools.NewRegistry()
	if _, err := root.Provide(hosttools.KeyRegistry, registry); err != nil {
		t.Fatalf("provide registry: %v", err)
	}
	if err := Module().Factory(root, Config{}); err != nil {
		t.Fatalf("factory: %v", err)
	}
	names := registeredNames(registry)
	want := []string{ToolOpen, ToolScreenshot, ToolTitle} // registry sorts by name
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("registered tools = %v, want %v", names, want)
	}
	// The registered tools must be bound to the provided service, not a nil one.
	result, err := registry.Invoke(context.Background(), rig.ToolCall{
		Name: ToolOpen, Arguments: `{"url":"https://example.com/"}`,
	})
	if err != nil {
		t.Fatalf("invoke registered browser_open: %v", err)
	}
	if !result.IsError || !strings.Contains(result.Content, "browser: unavailable") {
		t.Errorf("registered browser_open = %q, want the module's disconnected service to answer (browser: unavailable)", result.Content)
	}
}

func TestModuleReplacesDuplicateBrowserToolNames(t *testing.T) {
	root := newTestRoot(t)
	registry := rigtools.NewRegistry()
	if _, err := root.Provide(hosttools.KeyRegistry, registry); err != nil {
		t.Fatalf("provide registry: %v", err)
	}
	if err := registry.Register(stubTool{name: ToolOpen}); err != nil {
		t.Fatalf("pre-register stub: %v", err)
	}
	if err := Module().Factory(root, Config{}); err != nil {
		t.Fatalf("factory: %v", err)
	}
	if registry.Len() != 3 {
		t.Fatalf("registry has %d tools (%v), want the three browser tools", registry.Len(), registeredNames(registry))
	}
	for _, tool := range registry.Tools() {
		if tool.Name() == ToolOpen {
			if strings.Contains(tool.Description(), "stub") {
				t.Errorf("browser_open is still the pre-existing stub; the module must replace it")
			}
			if !strings.Contains(tool.Description(), "Use it when") {
				t.Errorf("browser_open description = %q, want the module's real tool", tool.Description())
			}
			return
		}
	}
	t.Fatal("browser_open is not registered")
}

func TestModuleCleanupUnregistersBrowserTools(t *testing.T) {
	root := cordis.Root()
	registry := rigtools.NewRegistry()
	if _, err := root.Provide(hosttools.KeyRegistry, registry); err != nil {
		t.Fatalf("provide registry: %v", err)
	}
	if err := Module().Factory(root, Config{}); err != nil {
		t.Fatalf("factory: %v", err)
	}
	if registry.Len() != 3 {
		t.Fatalf("registry has %d tools before cleanup, want 3", registry.Len())
	}
	if err := root.DisposeAll(); err != nil {
		t.Fatalf("DisposeAll: %v", err)
	}
	if names := registeredNames(registry); len(names) != 0 {
		t.Fatalf("registry still has %v after module unload, want no browser tools", names)
	}
}

func TestModuleSkipsRegistrationWithoutARegistry(t *testing.T) {
	root := newTestRoot(t)
	if _, ok := root.Get(hosttools.KeyRegistry); ok {
		t.Fatal("test root unexpectedly has a tool registry")
	}
	if err := Module().Factory(root, Config{}); err != nil {
		t.Fatalf("factory without a registry: %v", err)
	}
	if _, ok := root.Get(hosttools.KeyRegistry); ok {
		t.Errorf("the browser module provided a tool registry; it must only consume one")
	}
	service, ok := root.Get(contracts.KeyBrowser)
	if !ok || service == nil {
		t.Fatal("KeyBrowser was not provided without a registry: the tool face is optional, the service is not")
	}
}
