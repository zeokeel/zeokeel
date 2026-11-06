package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zeokeel/zeokeel/old/internal/mcp"
	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

const (
	mcpToolHelperEnv    = "ZEOKEEL_TOOLS_MCP_HELPER"
	mcpToolFailListsEnv = "ZEOKEEL_TOOLS_MCP_FAIL_LISTS"

	mcpToolTextURI = "file:///tool/note.txt"
	mcpToolBlobURI = "file:///tool/data.bin"
	mcpToolHugeURI = "file:///tool/huge.txt"
)

var (
	mcpToolBlob     = []byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x01}
	mcpToolHugeText = strings.Repeat("huge resource text ", 8_000)
)

// TestMain doubles as the entry point for the fake MCP server subprocess used
// by the MCP resource tool tests.
func TestMain(m *testing.M) {
	switch mode := os.Getenv(mcpToolHelperEnv); mode {
	case "":
		// Normal test run.
	case "server":
		runMCPToolHelperServer()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown MCP helper mode %q\n", mode)
		os.Exit(2)
	}
	os.Exit(m.Run())
}

func runMCPToolHelperServer() {
	// PageSize 1 makes every list request paginate, so the tools also exercise
	// the manager's cursor handling.
	server := sdk.NewServer(&sdk.Implementation{Name: "tools-helper", Version: "0.1.0"},
		&sdk.ServerOptions{PageSize: 1})
	server.AddResource(&sdk.Resource{
		URI:         mcpToolTextURI,
		Name:        "note",
		Description: "Tool test note.",
		MIMEType:    "text/plain",
		Size:        int64(len("tool note")),
	}, func(context.Context, *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{
			URI:      mcpToolTextURI,
			MIMEType: "text/plain",
			Text:     "tool note",
		}}}, nil
	})
	server.AddResource(&sdk.Resource{
		URI:         mcpToolBlobURI,
		Name:        "data",
		Description: "Tool test blob.",
		MIMEType:    "application/octet-stream",
		Size:        int64(len(mcpToolBlob)),
	}, func(context.Context, *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{
			URI:      mcpToolBlobURI,
			MIMEType: "application/octet-stream",
			Blob:     mcpToolBlob,
		}}}, nil
	})
	server.AddResource(&sdk.Resource{
		URI:         mcpToolHugeURI,
		Name:        "huge",
		Description: "Tool test resource larger than the tool cap.",
		MIMEType:    "text/plain",
	}, func(context.Context, *sdk.ReadResourceRequest) (*sdk.ReadResourceResult, error) {
		return &sdk.ReadResourceResult{Contents: []*sdk.ResourceContents{{
			URI:      mcpToolHugeURI,
			MIMEType: "text/plain",
			Text:     mcpToolHugeText,
		}}}, nil
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
			return nil, fmt.Errorf("name argument is required")
		}
		if punctuation == "" {
			punctuation = "!"
		}
		return &sdk.GetPromptResult{Messages: []*sdk.PromptMessage{
			{Role: sdk.Role("user"), Content: &sdk.TextContent{Text: "Hello, " + name + punctuation}},
			{Role: sdk.Role("assistant"), Content: &sdk.TextContent{Text: "Greeting rendered."}},
		}}, nil
	})
	server.AddPrompt(&sdk.Prompt{
		Name:        "review",
		Description: "Review a snippet.",
	}, func(context.Context, *sdk.GetPromptRequest) (*sdk.GetPromptResult, error) {
		return &sdk.GetPromptResult{Messages: []*sdk.PromptMessage{
			{Role: sdk.Role("user"), Content: &sdk.TextContent{Text: "review this"}},
		}}, nil
	})

	if os.Getenv(mcpToolFailListsEnv) != "" {
		server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
			return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
				switch method {
				case "resources/list", "prompts/list":
					return nil, fmt.Errorf("fixture: list endpoint unavailable")
				}
				return next(ctx, method, req)
			}
		})
	}

	_ = server.Run(context.Background(), &sdk.StdioTransport{})
}

func mcpToolServerConfig(t *testing.T, name string, extraEnv ...string) mcp.ServerConfig {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	env := append([]string{mcpToolHelperEnv + "=server"}, extraEnv...)
	return mcp.ServerConfig{
		Name:    name,
		Command: executable,
		Args:    []string{"-test.run=^$"},
		Env:     env,
	}
}

func newMCPToolManager(t *testing.T, servers ...mcp.ServerConfig) *mcp.Manager {
	t.Helper()
	manager := mcp.NewManager(context.Background())
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Errorf("cleanup mcp Close: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := manager.Connect(ctx, mcp.Config{Servers: servers}); err != nil {
		t.Fatalf("mcp Connect: %v", err)
	}
	return manager
}

func mcpToolSet(t *testing.T, manager *mcp.Manager) map[string]runtime.Tool {
	t.Helper()
	built := NewMCPResourceTools(MCPResourceConfig{Manager: manager})
	want := []string{"mcp_list_resources", "mcp_read_resource", "mcp_list_prompts", "mcp_get_prompt"}
	if len(built) != len(want) {
		t.Fatalf("NewMCPResourceTools returned %d tools, want %d", len(built), len(want))
	}
	out := make(map[string]runtime.Tool, len(built))
	for _, tool := range built {
		if strings.TrimSpace(tool.Description()) == "" {
			t.Errorf("tool %q has an empty description", tool.Name())
		}
		out[tool.Name()] = tool
	}
	for _, name := range want {
		if out[name] == nil {
			t.Fatalf("tool %q is missing from %v", name, out)
		}
	}
	return out
}

func decodeMCPToolJSON(t *testing.T, out string, dst any) {
	t.Helper()
	if len(out) > DefaultMaxOutputBytes {
		t.Fatalf("tool output is %d bytes, want <= %d", len(out), DefaultMaxOutputBytes)
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("tool output is not valid JSON:\n%s", out)
	}
	if err := json.Unmarshal([]byte(out), dst); err != nil {
		t.Fatalf("decode tool output: %v\n%s", err, out)
	}
}

func TestMCPResourceToolsListAndRead(t *testing.T) {
	manager := newMCPToolManager(t, mcpToolServerConfig(t, "demo"))
	tools := mcpToolSet(t, manager)

	var listed struct {
		Kind      string             `json:"kind"`
		Servers   []string           `json:"servers"`
		Count     int                `json:"count"`
		Resources []mcp.ResourceInfo `json:"resources"`
		Warnings  []string           `json:"warnings"`
		Truncated bool               `json:"truncated"`
	}
	out := mustInvoke(t, tools["mcp_list_resources"], map[string]any{})
	decodeMCPToolJSON(t, out, &listed)
	if listed.Kind != "mcp_list_resources" || listed.Count != 3 || len(listed.Servers) != 1 || listed.Servers[0] != "demo" {
		t.Fatalf("list resources = %+v", listed)
	}
	if len(listed.Warnings) != 0 || listed.Truncated {
		t.Fatalf("list resources warnings/truncated = %+v", listed)
	}
	uris := make(map[string]bool, len(listed.Resources))
	for _, resource := range listed.Resources {
		if resource.Server != "demo" {
			t.Fatalf("resource from unexpected server: %+v", resource)
		}
		uris[resource.URI] = true
	}
	for _, uri := range []string{mcpToolTextURI, mcpToolBlobURI, mcpToolHugeURI} {
		if !uris[uri] {
			t.Fatalf("resource %q missing from %+v", uri, listed.Resources)
		}
	}

	var text struct {
		Kind      string                `json:"kind"`
		Server    string                `json:"server"`
		URI       string                `json:"uri"`
		Count     int                   `json:"count"`
		Contents  []mcp.ResourceContent `json:"contents"`
		Truncated bool                  `json:"truncated"`
	}
	out = mustInvoke(t, tools["mcp_read_resource"], map[string]any{"server": "demo", "uri": mcpToolTextURI})
	decodeMCPToolJSON(t, out, &text)
	if text.Kind != "mcp_read_resource" || text.Server != "demo" || text.URI != mcpToolTextURI || text.Count != 1 {
		t.Fatalf("read text = %+v", text)
	}
	if len(text.Contents) != 1 || text.Contents[0].Text != "tool note" || text.Contents[0].MIME != "text/plain" || text.Truncated {
		t.Fatalf("read text contents = %+v", text.Contents)
	}

	out = mustInvoke(t, tools["mcp_read_resource"], map[string]any{"server": "demo", "uri": mcpToolBlobURI})
	decodeMCPToolJSON(t, out, &text)
	if len(text.Contents) != 1 {
		t.Fatalf("read blob contents = %+v", text.Contents)
	}
	decoded, err := base64.StdEncoding.DecodeString(text.Contents[0].Blob)
	if err != nil {
		t.Fatalf("blob is not base64: %v", err)
	}
	if !bytes.Equal(decoded, mcpToolBlob) {
		t.Fatalf("blob decoded = %v, want %v", decoded, mcpToolBlob)
	}

	// A 152 KB resource must be shrunk to fit the 64 KiB tool cap.
	out = mustInvoke(t, tools["mcp_read_resource"], map[string]any{"server": "demo", "uri": mcpToolHugeURI})
	decodeMCPToolJSON(t, out, &text)
	if !text.Truncated || len(text.Contents) != 1 || !text.Contents[0].Truncated {
		t.Fatalf("huge read is not marked truncated: %+v", text)
	}
	if got := len(text.Contents[0].Text); got == 0 || got >= len(mcpToolHugeText) {
		t.Fatalf("huge read text length = %d, want a non-empty truncated prefix", got)
	}
}

func TestMCPResourceToolsPrompts(t *testing.T) {
	manager := newMCPToolManager(t, mcpToolServerConfig(t, "demo"))
	tools := mcpToolSet(t, manager)

	var listed struct {
		Kind     string           `json:"kind"`
		Servers  []string         `json:"servers"`
		Count    int              `json:"count"`
		Prompts  []mcp.PromptInfo `json:"prompts"`
		Warnings []string         `json:"warnings"`
	}
	out := mustInvoke(t, tools["mcp_list_prompts"], map[string]any{})
	decodeMCPToolJSON(t, out, &listed)
	if listed.Kind != "mcp_list_prompts" || listed.Count != 2 || len(listed.Servers) != 1 || listed.Servers[0] != "demo" {
		t.Fatalf("list prompts = %+v", listed)
	}
	if len(listed.Warnings) != 0 {
		t.Fatalf("list prompts warnings = %+v", listed.Warnings)
	}
	var greet *mcp.PromptInfo
	for i := range listed.Prompts {
		if listed.Prompts[i].Name == "greet" {
			greet = &listed.Prompts[i]
		}
	}
	if greet == nil || len(greet.Arguments) != 2 || !greet.Arguments[0].Required {
		t.Fatalf("greet prompt = %+v", greet)
	}

	var got struct {
		Kind     string              `json:"kind"`
		Server   string              `json:"server"`
		Name     string              `json:"name"`
		Count    int                 `json:"count"`
		Messages []mcp.PromptMessage `json:"messages"`
	}
	out = mustInvoke(t, tools["mcp_get_prompt"], map[string]any{
		"server":    "demo",
		"name":      "greet",
		"arguments": map[string]string{"name": "Ada", "punctuation": "?"},
	})
	decodeMCPToolJSON(t, out, &got)
	if got.Kind != "mcp_get_prompt" || got.Server != "demo" || got.Name != "greet" || got.Count != 2 {
		t.Fatalf("get prompt = %+v", got)
	}
	if got.Messages[0].Role != "user" || got.Messages[0].Text != "Hello, Ada?" {
		t.Fatalf("get prompt first message = %+v", got.Messages[0])
	}
	if got.Messages[1].Role != "assistant" || got.Messages[1].Text != "Greeting rendered." {
		t.Fatalf("get prompt second message = %+v", got.Messages[1])
	}

	out = mustInvoke(t, tools["mcp_get_prompt"], map[string]any{"server": "demo", "name": "review"})
	decodeMCPToolJSON(t, out, &got)
	if got.Count != 1 || got.Messages[0].Text != "review this" {
		t.Fatalf("get review prompt = %+v", got)
	}
}

func TestMCPResourceToolsNoServers(t *testing.T) {
	empty := newMCPToolManager(t)
	tools := mcpToolSet(t, empty)
	for name, tool := range tools {
		args := json.RawMessage(`{"server":"demo","uri":"file:///x","name":"greet"}`)
		if _, err := tool.Invoke(context.Background(), args); err == nil ||
			!strings.Contains(err.Error(), "no MCP servers are connected") {
			t.Fatalf("%s no-server error = %v", name, err)
		}
	}

	unconfigured := NewMCPResourceTools(MCPResourceConfig{})
	for _, tool := range unconfigured {
		if _, err := tool.Invoke(context.Background(), nil); err == nil ||
			!strings.Contains(err.Error(), "no MCP manager configured") {
			t.Fatalf("%s nil-manager error = %v", tool.Name(), err)
		}
	}
}

func TestMCPResourceToolsFailureIsolation(t *testing.T) {
	manager := newMCPToolManager(t,
		mcpToolServerConfig(t, "good"),
		mcpToolServerConfig(t, "broken", mcpToolFailListsEnv+"=1"),
	)
	tools := mcpToolSet(t, manager)

	var listed struct {
		Kind      string             `json:"kind"`
		Count     int                `json:"count"`
		Resources []mcp.ResourceInfo `json:"resources"`
		Warnings  []string           `json:"warnings"`
	}
	out := mustInvoke(t, tools["mcp_list_resources"], map[string]any{})
	decodeMCPToolJSON(t, out, &listed)
	if listed.Count != 3 {
		t.Fatalf("list resources count = %d, want the good server's 3: %+v", listed.Count, listed.Resources)
	}
	if len(listed.Warnings) == 0 || !strings.Contains(strings.Join(listed.Warnings, "\n"), "list endpoint unavailable") {
		t.Fatalf("list resources warnings = %+v", listed.Warnings)
	}
	for _, resource := range listed.Resources {
		if resource.Server != "good" {
			t.Fatalf("resource from failing server leaked through: %+v", resource)
		}
	}

	var prompts struct {
		Kind     string           `json:"kind"`
		Count    int              `json:"count"`
		Prompts  []mcp.PromptInfo `json:"prompts"`
		Warnings []string         `json:"warnings"`
	}
	out = mustInvoke(t, tools["mcp_list_prompts"], map[string]any{})
	decodeMCPToolJSON(t, out, &prompts)
	if prompts.Count != 2 || len(prompts.Warnings) == 0 {
		t.Fatalf("list prompts = %+v", prompts)
	}
	for _, prompt := range prompts.Prompts {
		if prompt.Server != "good" {
			t.Fatalf("prompt from failing server leaked through: %+v", prompt)
		}
	}

	// Read-only requests against the healthy server are unaffected.
	out = mustInvoke(t, tools["mcp_read_resource"], map[string]any{"server": "good", "uri": mcpToolTextURI})
	var read struct {
		Count    int                   `json:"count"`
		Contents []mcp.ResourceContent `json:"contents"`
	}
	decodeMCPToolJSON(t, out, &read)
	if read.Count != 1 || read.Contents[0].Text != "tool note" {
		t.Fatalf("read after partial list failure = %+v", read)
	}
}

func TestMCPResourceToolsValidation(t *testing.T) {
	manager := newMCPToolManager(t, mcpToolServerConfig(t, "demo"))
	tools := mcpToolSet(t, manager)
	ctx := context.Background()

	cases := []struct {
		tool string
		args string
		want string
	}{
		{"mcp_read_resource", `{}`, `"server" is required`},
		{"mcp_read_resource", `{"server":"demo"}`, `"uri" is required`},
		{"mcp_read_resource", `[]`, "arguments must be a JSON object"},
		{"mcp_get_prompt", `{}`, `"server" is required`},
		{"mcp_get_prompt", `{"server":"demo"}`, `"name" is required`},
		{"mcp_get_prompt", `{"server":"demo","name":"greet"}`, "name argument is required"},
		{"mcp_get_prompt", `{"server":"missing","name":"greet"}`, "not connected"},
		{"mcp_get_prompt", `{"server":"demo","name":"missing"}`, "unknown prompt"},
		{"mcp_read_resource", `{"server":"missing","uri":"file:///x"}`, "not connected"},
		{"mcp_list_resources", `[]`, "arguments must be a JSON object"},
	}
	for _, tc := range cases {
		_, err := tools[tc.tool].Invoke(ctx, json.RawMessage(tc.args))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s(%s) error = %v, want %q", tc.tool, tc.args, err, tc.want)
		}
	}

	// A server-side prompt failure propagates as a tool error.
	if _, err := tools["mcp_get_prompt"].Invoke(ctx, json.RawMessage(`{"server":"demo","name":"greet"}`)); err == nil ||
		!strings.Contains(err.Error(), "name argument is required") {
		t.Fatalf("prompt failure error = %v", err)
	}
}
