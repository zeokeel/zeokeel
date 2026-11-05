package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The fixture server advertises enough resources and prompts to exercise cursor
// paging: the helper runs with PageSize 2, so the five resources and four
// prompts below always require multiple pages.
const (
	testFailListsEnv = "ZEOKEEL_MCP_TEST_FAIL_LISTS"

	testTextURI   = "file:///fixtures/note.txt"
	testBlobURI   = "file:///fixtures/data.bin"
	testLargeURI  = "file:///fixtures/large.txt"
	testBulkURI   = "file:///fixtures/bulk"
	testBrokenURI = "file:///fixtures/broken"
)

var (
	testText      = "hello resource"
	testBlob      = []byte{0x00, 0x01, 0x02, 0xff, 'h', 'i'}
	testLargeText = strings.Repeat("large resource line\n", 20_000)
	testBulkText  = strings.Repeat("b", 250_000)
)

// registerResourcePromptFixtures adds the resources and prompts used by the
// resources_test.go suite to the fake stdio server.
func registerResourcePromptFixtures(server *sdk.Server) {
	server.AddResource(&sdk.Resource{
		URI:         testTextURI,
		Name:        "note",
		Description: "A small text note.",
		MIMEType:    "text/plain",
		Size:        int64(len(testText)),
	}, func(context.Context, *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{
			URI:      testTextURI,
			MIMEType: "text/plain",
			Text:     testText,
		}}}, nil
	})
	server.AddResource(&sdk.Resource{
		URI:         testBlobURI,
		Name:        "data",
		Description: "A small binary blob.",
		MIMEType:    "application/octet-stream",
		Size:        int64(len(testBlob)),
	}, func(context.Context, *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{
			URI:      testBlobURI,
			MIMEType: "application/octet-stream",
			Blob:     testBlob,
		}}}, nil
	})
	server.AddResource(&sdk.Resource{
		URI:         testLargeURI,
		Name:        "large",
		Description: "A text resource larger than the per-item cap.",
		MIMEType:    "text/plain",
	}, func(context.Context, *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{
			URI:      testLargeURI,
			MIMEType: "text/plain",
			Text:     testLargeText,
		}}}, nil
	})
	server.AddResource(&sdk.Resource{
		URI:         testBulkURI,
		Name:        "bulk",
		Description: "Several content items larger than the total cap.",
		MIMEType:    "text/plain",
	}, func(context.Context, *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		contents := make([]*sdk.ResourceContents, 0, 5)
		for i := 0; i < 5; i++ {
			contents = append(contents, &sdk.ResourceContents{
				URI:      fmt.Sprintf("%s#%d", testBulkURI, i),
				MIMEType: "text/plain",
				Text:     testBulkText,
			})
		}
		return &sdk.ReadResourceResult{Contents: contents}, nil
	})
	server.AddResource(&sdk.Resource{
		URI:         testBrokenURI,
		Name:        "broken",
		Description: "A resource whose read handler always fails.",
		MIMEType:    "text/plain",
	}, func(context.Context, *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		return nil, errors.New("fixture: resource unavailable")
	})

	server.AddPrompt(&sdk.Prompt{
		Name:        "greet",
		Description: "Greet a person by name.",
		Arguments: []*sdk.PromptArgument{
			{Name: "name", Description: "Person to greet.", Required: true},
			{Name: "punctuation", Description: "Optional trailing punctuation."},
		},
	}, func(_ context.Context, req *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		var name, punctuation string
		if req.Params != nil {
			name = req.Params.Arguments["name"]
			punctuation = req.Params.Arguments["punctuation"]
		}
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("name argument is required")
		}
		if punctuation == "" {
			punctuation = "!"
		}
		return &sdk.GetPromptResult{
			Description: "greeting",
			Messages: []*sdk.PromptMessage{
				{Role: sdk.Role("user"), Content: &sdk.TextContent{Text: "Hello, " + name + punctuation}},
				{Role: sdk.Role("assistant"), Content: &sdk.TextContent{Text: "Greeting rendered."}},
			},
		}, nil
	})
	server.AddPrompt(&sdk.Prompt{
		Name:        "review",
		Description: "Review a snippet.",
	}, func(context.Context, *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		return &sdk.GetPromptResult{Messages: []*sdk.PromptMessage{
			{Role: sdk.Role("user"), Content: &sdk.TextContent{Text: "review this"}},
		}}, nil
	})
	server.AddPrompt(&sdk.Prompt{
		Name:        "fail",
		Description: "A prompt whose handler always fails.",
	}, func(context.Context, *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		return nil, errors.New("intentional prompt failure")
	})
	server.AddPrompt(&sdk.Prompt{
		Name:        "slow",
		Description: "A prompt that waits for cancellation.",
	}, func(ctx context.Context, _ *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Second):
			return &sdk.GetPromptResult{Messages: []*sdk.PromptMessage{
				{Role: sdk.Role("user"), Content: &sdk.TextContent{Text: "too late"}},
			}}, nil
		}
	})

	// A "broken" helper still connects (tools/list works) but every list
	// request fails, which lets tests prove per-server failure isolation.
	if os.Getenv(testFailListsEnv) != "" {
		server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
			return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
				switch method {
				case "resources/list", "prompts/list":
					return nil, errors.New("fixture: list endpoint unavailable")
				}
				return next(ctx, method, req)
			}
		})
	}
}

func failingHelperConfig(t *testing.T, name string) ServerConfig {
	t.Helper()
	cfg := helperConfig(t, name, "server")
	cfg.Env = append(cfg.Env, testFailListsEnv+"=1")
	return cfg
}

func connectTestManager(t *testing.T, servers ...ServerConfig) *Manager {
	t.Helper()
	manager := NewManager(context.Background())
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Errorf("cleanup Close: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := manager.Connect(ctx, Config{Servers: servers}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return manager
}

func findResource(t *testing.T, resources []ResourceInfo, uri string) ResourceInfo {
	t.Helper()
	for _, resource := range resources {
		if resource.URI == uri {
			return resource
		}
	}
	t.Fatalf("resource %q not found in %+v", uri, resources)
	return ResourceInfo{}
}

func TestMCPResourcesListAndRead(t *testing.T) {
	manager := connectTestManager(t, helperConfig(t, "demo", "server"))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	resources, err := manager.ListResources(ctx)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	// Five resources at PageSize 2 require three pages.
	if len(resources) != 5 {
		t.Fatalf("ListResources returned %d resources, want 5: %+v", len(resources), resources)
	}
	for i := 1; i < len(resources); i++ {
		if resources[i-1].Server > resources[i].Server ||
			(resources[i-1].Server == resources[i].Server && resources[i-1].URI > resources[i].URI) {
			t.Fatalf("resources are not sorted by server/URI: %+v", resources)
		}
	}
	note := findResource(t, resources, testTextURI)
	if note.Server != "demo" || note.Name != "note" || note.MIME != "text/plain" || note.Size != int64(len(testText)) {
		t.Fatalf("note resource = %+v", note)
	}

	contents, err := manager.ReadResource(ctx, "demo", testTextURI)
	if err != nil {
		t.Fatalf("ReadResource text: %v", err)
	}
	if len(contents) != 1 || contents[0].Text != testText || contents[0].Truncated {
		t.Fatalf("text contents = %+v", contents)
	}
	if contents[0].Server != "demo" || contents[0].URI != testTextURI || contents[0].MIME != "text/plain" {
		t.Fatalf("text content metadata = %+v", contents[0])
	}

	contents, err = manager.ReadResource(ctx, "demo", testBlobURI)
	if err != nil {
		t.Fatalf("ReadResource blob: %v", err)
	}
	if len(contents) != 1 {
		t.Fatalf("blob contents = %+v", contents)
	}
	decoded, err := base64.StdEncoding.DecodeString(contents[0].Blob)
	if err != nil {
		t.Fatalf("blob is not base64: %v", err)
	}
	if !bytes.Equal(decoded, testBlob) {
		t.Fatalf("blob decoded = %v, want %v", decoded, testBlob)
	}
	if contents[0].Text != "" || contents[0].Truncated {
		t.Fatalf("blob content = %+v", contents[0])
	}

	// Per-item cap: 400 KB of text becomes 256 KB.
	contents, err = manager.ReadResource(ctx, "demo", testLargeURI)
	if err != nil {
		t.Fatalf("ReadResource large: %v", err)
	}
	if len(contents) != 1 || !contents[0].Truncated {
		t.Fatalf("large content = %+v", contents)
	}
	if got := len(contents[0].Text); got != MaxResourceContentBytes {
		t.Fatalf("large content length = %d, want %d", got, MaxResourceContentBytes)
	}

	// Total cap: five 250 KB items become exactly 1 MiB of raw text.
	contents, err = manager.ReadResource(ctx, "demo", testBulkURI)
	if err != nil {
		t.Fatalf("ReadResource bulk: %v", err)
	}
	if len(contents) != 5 {
		t.Fatalf("bulk contents = %d, want 5", len(contents))
	}
	total := 0
	for _, content := range contents {
		total += len(content.Text) + len(content.Blob)
	}
	if total != MaxResourceTotalBytes {
		t.Fatalf("bulk raw bytes = %d, want %d", total, MaxResourceTotalBytes)
	}
	if !contents[len(contents)-1].Truncated {
		t.Fatalf("last bulk content is not marked truncated: %+v", contents[len(contents)-1])
	}
	if contents[0].Truncated {
		t.Fatalf("first bulk content is marked truncated: %+v", contents[0])
	}

	// A failing read handler is reported as an error naming the server.
	if _, err := manager.ReadResource(ctx, "demo", testBrokenURI); err == nil ||
		!strings.Contains(err.Error(), "demo") || !strings.Contains(err.Error(), "resource unavailable") {
		t.Fatalf("broken resource error = %v", err)
	}
	if _, err := manager.ReadResource(ctx, "demo", "file:///fixtures/unknown"); err == nil {
		t.Fatal("unregistered resource URI was read successfully")
	}
	if _, err := manager.ReadResource(ctx, "missing", testTextURI); err == nil ||
		!strings.Contains(err.Error(), "not connected") {
		t.Fatalf("unknown server error = %v", err)
	}
	if _, err := manager.ReadResource(ctx, "  ", testTextURI); err == nil {
		t.Fatal("empty server accepted")
	}
	if _, err := manager.ReadResource(ctx, "demo", "  "); err == nil {
		t.Fatal("empty URI accepted")
	}
}

func TestMCPPromptsListAndGet(t *testing.T) {
	manager := connectTestManager(t, helperConfig(t, "demo", "server"))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	prompts, err := manager.ListPrompts(ctx)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	// Four prompts at PageSize 2 require two pages.
	if len(prompts) != 4 {
		t.Fatalf("ListPrompts returned %d prompts, want 4: %+v", len(prompts), prompts)
	}
	for i := 1; i < len(prompts); i++ {
		if prompts[i-1].Server > prompts[i].Server ||
			(prompts[i-1].Server == prompts[i].Server && prompts[i-1].Name > prompts[i].Name) {
			t.Fatalf("prompts are not sorted by server/name: %+v", prompts)
		}
	}
	var greet *PromptInfo
	for i := range prompts {
		if prompts[i].Name == "greet" {
			greet = &prompts[i]
		}
	}
	if greet == nil {
		t.Fatalf("greet prompt not found: %+v", prompts)
	}
	if greet.Server != "demo" || greet.Description != "Greet a person by name." || len(greet.Arguments) != 2 {
		t.Fatalf("greet prompt = %+v", greet)
	}
	if greet.Arguments[0].Name != "name" || !greet.Arguments[0].Required {
		t.Fatalf("greet name argument = %+v", greet.Arguments[0])
	}
	if greet.Arguments[1].Name != "punctuation" || greet.Arguments[1].Required {
		t.Fatalf("greet punctuation argument = %+v", greet.Arguments[1])
	}

	messages, err := manager.GetPrompt(ctx, "demo", "greet", map[string]string{"name": "Ada"})
	if err != nil {
		t.Fatalf("GetPrompt greet: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("greet messages = %+v", messages)
	}
	if messages[0].Role != "user" || messages[0].Text != "Hello, Ada!" {
		t.Fatalf("greet first message = %+v", messages[0])
	}
	if messages[1].Role != "assistant" || messages[1].Text != "Greeting rendered." {
		t.Fatalf("greet second message = %+v", messages[1])
	}

	messages, err = manager.GetPrompt(ctx, "demo", "greet", map[string]string{"name": "Ada", "punctuation": "?"})
	if err != nil {
		t.Fatalf("GetPrompt greet with punctuation: %v", err)
	}
	if messages[0].Text != "Hello, Ada?" {
		t.Fatalf("punctuation message = %+v", messages[0])
	}

	messages, err = manager.GetPrompt(ctx, "demo", "review", nil)
	if err != nil {
		t.Fatalf("GetPrompt review: %v", err)
	}
	if len(messages) != 1 || messages[0].Text != "review this" {
		t.Fatalf("review messages = %+v", messages)
	}

	if _, err := manager.GetPrompt(ctx, "demo", "greet", nil); err == nil ||
		!strings.Contains(err.Error(), "name argument is required") {
		t.Fatalf("missing required argument error = %v", err)
	}
	if _, err := manager.GetPrompt(ctx, "demo", "fail", nil); err == nil ||
		!strings.Contains(err.Error(), "intentional prompt failure") {
		t.Fatalf("failing prompt error = %v", err)
	}
	if _, err := manager.GetPrompt(ctx, "demo", "unknown", nil); err == nil {
		t.Fatal("unknown prompt was rendered")
	}
	if _, err := manager.GetPrompt(ctx, "missing", "greet", nil); err == nil ||
		!strings.Contains(err.Error(), "not connected") {
		t.Fatalf("unknown prompt server error = %v", err)
	}
	if _, err := manager.GetPrompt(ctx, " ", "greet", nil); err == nil {
		t.Fatal("empty prompt server accepted")
	}
	if _, err := manager.GetPrompt(ctx, "demo", " ", nil); err == nil {
		t.Fatal("empty prompt name accepted")
	}

	// The per-request timeout cancels a server handler that never returns.
	manager.requestTimeout = 200 * time.Millisecond
	start := time.Now()
	if _, err := manager.GetPrompt(ctx, "demo", "slow", nil); err == nil {
		t.Fatal("slow prompt returned without a timeout")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("slow prompt took %s, want the request timeout to apply", elapsed)
	}
	if DefaultRequestTimeout != 10*time.Second {
		t.Fatalf("DefaultRequestTimeout = %s, want 10s", DefaultRequestTimeout)
	}
}

func TestMCPResourcePromptFailureIsolation(t *testing.T) {
	manager := connectTestManager(t,
		helperConfig(t, "good", "server"),
		failingHelperConfig(t, "broken"),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	resources, err := manager.ListResources(ctx)
	if err == nil {
		t.Fatal("ListResources succeeded despite a failing server")
	}
	if !strings.Contains(err.Error(), `server "broken"`) || !strings.Contains(err.Error(), "list endpoint unavailable") {
		t.Fatalf("ListResources error = %v", err)
	}
	if len(resources) != 5 {
		t.Fatalf("ListResources returned %d resources from the good server, want 5: %+v", len(resources), resources)
	}
	for _, resource := range resources {
		if resource.Server != "good" {
			t.Fatalf("resource from a failing server leaked through: %+v", resource)
		}
	}

	prompts, err := manager.ListPrompts(ctx)
	if err == nil {
		t.Fatal("ListPrompts succeeded despite a failing server")
	}
	if !strings.Contains(err.Error(), `server "broken"`) {
		t.Fatalf("ListPrompts error = %v", err)
	}
	if len(prompts) != 4 {
		t.Fatalf("ListPrompts returned %d prompts from the good server, want 4: %+v", len(prompts), prompts)
	}
	for _, prompt := range prompts {
		if prompt.Server != "good" {
			t.Fatalf("prompt from a failing server leaked through: %+v", prompt)
		}
	}

	// Non-list requests on the broken server still work; only its list
	// endpoints are failing.
	if _, err := manager.ReadResource(ctx, "broken", testTextURI); err != nil {
		t.Fatalf("ReadResource on broken server: %v", err)
	}
	if _, err := manager.GetPrompt(ctx, "broken", "review", nil); err != nil {
		t.Fatalf("GetPrompt on broken server: %v", err)
	}
}

func TestMCPResourceNoServers(t *testing.T) {
	manager := NewManager(context.Background())
	defer manager.Close()

	resources, err := manager.ListResources(nil)
	if err != nil || resources != nil {
		t.Fatalf("ListResources on empty manager = (%v, %v), want (nil, nil)", resources, err)
	}
	prompts, err := manager.ListPrompts(nil)
	if err != nil || prompts != nil {
		t.Fatalf("ListPrompts on empty manager = (%v, %v), want (nil, nil)", prompts, err)
	}
	if _, err := manager.ReadResource(context.Background(), "demo", testTextURI); err == nil ||
		!strings.Contains(err.Error(), "not connected") {
		t.Fatalf("ReadResource on empty manager error = %v", err)
	}

	var nilManager *Manager
	if _, err := nilManager.ListResources(context.Background()); err == nil {
		t.Fatal("nil Manager.ListResources returned no error")
	}
	if _, err := nilManager.ReadResource(context.Background(), "demo", testTextURI); err == nil {
		t.Fatal("nil Manager.ReadResource returned no error")
	}
	if _, err := nilManager.ListPrompts(context.Background()); err == nil {
		t.Fatal("nil Manager.ListPrompts returned no error")
	}
	if _, err := nilManager.GetPrompt(context.Background(), "demo", "greet", nil); err == nil {
		t.Fatal("nil Manager.GetPrompt returned no error")
	}
}

func TestMCPResourceConcurrent(t *testing.T) {
	manager := connectTestManager(t, helperConfig(t, "demo", "server"))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := manager.ListResources(ctx); err != nil {
				errCh <- fmt.Errorf("ListResources: %w", err)
			}
			if _, err := manager.ListPrompts(ctx); err != nil {
				errCh <- fmt.Errorf("ListPrompts: %w", err)
			}
			if _, err := manager.ReadResource(ctx, "demo", testTextURI); err != nil {
				errCh <- fmt.Errorf("ReadResource: %w", err)
			}
			if _, err := manager.GetPrompt(ctx, "demo", "greet", map[string]string{"name": "Ada"}); err != nil {
				errCh <- fmt.Errorf("GetPrompt: %w", err)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
