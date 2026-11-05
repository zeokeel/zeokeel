package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zeokeel/zeokeel/internal/runtime"
)

const helperEnv = "ZEOKEEL_MCP_TEST_HELPER"

// TestMain doubles as the entry point for the fake MCP server subprocess. The
// parent test process re-executes this test binary with helperEnv set; the
// helper never runs the test suite, and exits 0 so stdout is free of testing
// framework output (stdout is the MCP transport).
func TestMain(m *testing.M) {
	switch mode := os.Getenv(helperEnv); mode {
	case "":
		// Normal test run.
	case "server":
		runHelperServer()
		os.Exit(0)
	case "sleep":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", mode)
		os.Exit(2)
	}
	os.Exit(m.Run())
}

func runHelperServer() {
	// PageSize is deliberately tiny so the resources/prompts tests exercise
	// cursor pagination.
	server := sdk.NewServer(&sdk.Implementation{Name: "fake-mcp-server", Version: "0.1.0"},
		&sdk.ServerOptions{PageSize: 2})
	sdk.AddTool(server, &sdk.Tool{Name: "echo", Description: "Echo text back to the caller."},
		func(_ context.Context, _ *sdk.CallToolRequest, in struct {
			Text string `json:"text,omitempty"`
		}) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{
				&sdk.TextContent{Text: "echo: " + in.Text},
			}}, nil, nil
		})
	sdk.AddTool(server, &sdk.Tool{Name: "fail", Description: "Always fails."},
		func(_ context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
			return nil, nil, errors.New("intentional failure")
		})
	// env exposes what the child process actually inherited, so the scrub test
	// can prove provider secrets are not visible to a configured server.
	sdk.AddTool(server, &sdk.Tool{Name: "env", Description: "Read an environment variable."},
		func(_ context.Context, _ *sdk.CallToolRequest, in struct {
			Name string `json:"name"`
		}) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{
				&sdk.TextContent{Text: os.Getenv(in.Name)},
			}}, nil, nil
		})
	registerResourcePromptFixtures(server)
	// Run returns an error when the client closes the connection; the process
	// has done its job either way, so the error is ignored here.
	_ = server.Run(context.Background(), &sdk.StdioTransport{})
}

func helperConfig(t *testing.T, name, mode string) ServerConfig {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return ServerConfig{
		Name:    name,
		Command: executable,
		Args:    []string{"-test.run=^$"},
		Env:     []string{helperEnv + "=" + mode},
	}
}

func TestManagerStdio(t *testing.T) {
	manager := NewManager(context.Background())
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Errorf("cleanup Close: %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := manager.Connect(ctx, Config{Servers: []ServerConfig{helperConfig(t, "demo", "server")}}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if got, want := manager.Servers(), []string{"demo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Servers() = %v, want %v", got, want)
	}

	tools := manager.Tools()
	if got, want := toolNames(tools), []string{"mcp__demo__echo", "mcp__demo__env", "mcp__demo__fail"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Tools() names = %v, want %v", got, want)
	}

	echo := findTool(t, tools, "mcp__demo__echo")
	if !strings.Contains(echo.Description(), "Echo text back") {
		t.Errorf("echo description = %q", echo.Description())
	}
	if !strings.Contains(echo.Description(), "Args JSON schema") {
		t.Errorf("echo description lacks input schema: %q", echo.Description())
	}
	out, err := echo.Invoke(ctx, json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("echo Invoke: %v", err)
	}
	if out != "echo: hi" {
		t.Fatalf("echo output = %q", out)
	}

	// Missing arguments become {} and reach the server as an empty object.
	out, err = echo.Invoke(ctx, nil)
	if err != nil {
		t.Fatalf("echo with nil args: %v", err)
	}
	if out != "echo: " {
		t.Fatalf("echo with nil args = %q", out)
	}

	// Only JSON objects are valid tool arguments.
	if _, err := echo.Invoke(ctx, json.RawMessage(`[1,2]`)); err == nil || !strings.Contains(err.Error(), "JSON object") {
		t.Fatalf("array args error = %v", err)
	}
	if _, err := echo.Invoke(ctx, json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid JSON args accepted")
	}

	fail := findTool(t, tools, "mcp__demo__fail")
	if _, err := fail.Invoke(ctx, json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "intentional failure") {
		t.Fatalf("failure tool error = %v", err)
	}

	// Reconnecting an existing server name is a per-server error.
	if err := manager.Connect(ctx, Config{Servers: []ServerConfig{helperConfig(t, "demo", "server")}}); err == nil || !strings.Contains(err.Error(), "already connected") {
		t.Fatalf("duplicate connect error = %v", err)
	}

	if err := manager.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := manager.Tools(); len(got) != 0 {
		t.Fatalf("Tools() after Close = %v", toolNames(got))
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := manager.Connect(ctx, Config{}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Connect after Close error = %v", err)
	}
	if out, err := echo.Invoke(ctx, nil); err == nil {
		t.Fatalf("Invoke after Close = %q, want error", out)
	}
}

func TestManagerPerServerFailure(t *testing.T) {
	manager := NewManager(context.Background())
	t.Cleanup(func() { _ = manager.Close() })

	bad := ServerConfig{Name: "broken", Command: filepath.Join(t.TempDir(), "definitely-missing-binary")}
	good := helperConfig(t, "good", "server")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	err := manager.Connect(ctx, Config{Servers: []ServerConfig{bad, good}})
	if err == nil {
		t.Fatal("Connect succeeded despite a broken server")
	}
	if !strings.Contains(err.Error(), `server "broken"`) {
		t.Fatalf("error does not identify the broken server: %v", err)
	}
	if strings.Contains(err.Error(), `server "good"`) {
		t.Fatalf("error incorrectly blames the good server: %v", err)
	}
	if got, want := manager.Servers(), []string{"good"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Servers() = %v, want %v", got, want)
	}
	tools := manager.Tools()
	if len(tools) != 3 {
		t.Fatalf("good server tools = %v", toolNames(tools))
	}
	echo := findTool(t, tools, "mcp__good__echo")
	if _, err := echo.Invoke(ctx, json.RawMessage(`{"text":"still works"}`)); err != nil {
		t.Fatalf("good server after partial failure: %v", err)
	}
}

func TestManagerConnectTimeout(t *testing.T) {
	manager := NewManager(context.Background())
	manager.connectTimeout = 5 * time.Second // the caller's shorter context must win
	manager.terminate = 100 * time.Millisecond
	t.Cleanup(func() { _ = manager.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := manager.Connect(ctx, Config{Servers: []ServerConfig{helperConfig(t, "sleepy", "sleep")}})
	if err == nil {
		t.Fatal("Connect succeeded against a server that never handshakes")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("timeout error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Connect took %s, should honor the context timeout", elapsed)
	}
	if got := manager.Tools(); len(got) != 0 {
		t.Fatalf("timed-out server exposed tools: %v", toolNames(got))
	}
	if got := manager.Servers(); len(got) != 0 {
		t.Fatalf("timed-out server is listed: %v", got)
	}
}

func TestManagerInvalidConfigs(t *testing.T) {
	manager := NewManager(context.Background())
	t.Cleanup(func() { _ = manager.Close() })

	missing := filepath.Join(t.TempDir(), "missing-command")
	err := manager.Connect(context.Background(), Config{Servers: []ServerConfig{
		{Name: "", Command: missing},
		{Name: "no-command"},
		{Name: "bad name", Command: missing},
		{Name: "bad-env", Command: missing, Env: []string{"NO_EQUALS"}},
		{Name: "dup", Command: missing},
		{Name: "dup", Command: missing},
	}})
	if err == nil {
		t.Fatal("Connect accepted invalid server configs")
	}
	for _, want := range []string{
		"name is required",
		"command is required",
		`invalid name "bad name"`,
		`env entry "NO_EQUALS"`,
		`server "dup": duplicate server name`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if got := manager.Tools(); len(got) != 0 {
		t.Fatalf("invalid configs exposed tools: %v", toolNames(got))
	}
}

func TestEmptyConfig(t *testing.T) {
	manager := NewManager(context.Background())
	defer manager.Close()
	if err := manager.Connect(context.Background(), Config{}); err != nil {
		t.Fatalf("Connect(empty): %v", err)
	}
	if got := manager.Tools(); len(got) != 0 {
		t.Fatalf("empty manager has tools: %v", toolNames(got))
	}
	if got := manager.Servers(); len(got) != 0 {
		t.Fatalf("empty manager has servers: %v", got)
	}
}

func TestToolAdapterNameAndDescription(t *testing.T) {
	adapter, err := newToolAdapter("demo", &sdk.Tool{
		Name:        "search",
		Description: "Search things.",
		InputSchema: map[string]any{"type": "object"},
	}, nil)
	if err != nil {
		t.Fatalf("newToolAdapter: %v", err)
	}
	if adapter.Name() != "mcp__demo__search" {
		t.Fatalf("Name() = %q", adapter.Name())
	}
	if !strings.Contains(adapter.Description(), "Search things.") {
		t.Fatalf("Description() = %q", adapter.Description())
	}
	if !strings.Contains(adapter.Description(), `"type":"object"`) {
		t.Fatalf("Description() lacks schema: %q", adapter.Description())
	}
	if _, err := newToolAdapter("demo", &sdk.Tool{Name: "bad name"}, nil); err == nil {
		t.Fatal("tool name with whitespace accepted")
	}
}

func TestFormatResult(t *testing.T) {
	res := &sdk.CallToolResult{Content: []sdk.Content{
		&sdk.TextContent{Text: "first"},
		&sdk.ImageContent{MIMEType: "image/png", Data: []byte("aGVsbG8=")},
		&sdk.ResourceLink{Name: "file", URI: "file:///tmp/a"},
	}}
	got, err := formatResult(res)
	if err != nil {
		t.Fatalf("formatResult: %v", err)
	}
	for _, want := range []string{"first", "[image content: image/png", "[resource link: file <file:///tmp/a>]"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatted result %q does not contain %q", got, want)
		}
	}

	// Structured content is used when there are no content blocks.
	got, err = formatResult(&sdk.CallToolResult{StructuredContent: map[string]any{"ok": true}})
	if err != nil {
		t.Fatalf("formatResult structured: %v", err)
	}
	if !strings.Contains(got, `"ok": true`) {
		t.Fatalf("structured output = %q", got)
	}
}

func TestMergeEnv(t *testing.T) {
	base := []string{"KEEP=1", "OVERRIDE=old"}
	got := mergeEnv(base, []string{"OVERRIDE=new", "ADDED=2"})
	want := []string{"KEEP=1", "OVERRIDE=new", "ADDED=2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeEnv = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(mergeEnv(base, nil), base) {
		t.Fatal("mergeEnv with no overrides changed the environment")
	}
}

func toolNames(tools []runtime.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name())
	}
	return names
}

func findTool(t *testing.T, tools []runtime.Tool, name string) runtime.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

// TestMCPEnvironmentIsScrubbed proves a configured MCP server cannot read the
// provider key or another ZeoKeel secret from the inherited environment, while
// variables the operator explicitly passes through ServerConfig.Env still work.
func TestMCPEnvironmentIsScrubbed(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-provider-secret")
	t.Setenv("ZEOKEEL_WEBSEARCH_API_KEY", "ws-secret")

	manager := NewManager(context.Background())
	t.Cleanup(func() { _ = manager.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := manager.Connect(ctx, Config{Servers: []ServerConfig{helperConfig(t, "demo", "server")}}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	env := findTool(t, manager.Tools(), "mcp__demo__env")
	for _, name := range []string{"DEEPSEEK_API_KEY", "ZEOKEEL_WEBSEARCH_API_KEY"} {
		raw, _ := json.Marshal(map[string]string{"name": name})
		out, err := env.Invoke(ctx, raw)
		if err != nil {
			t.Fatalf("env %s: %v", name, err)
		}
		if strings.TrimSpace(out) != "" {
			t.Fatalf("MCP child inherited %s=%q", name, out)
		}
	}
	raw, _ := json.Marshal(map[string]string{"name": helperEnv})
	out, err := env.Invoke(ctx, raw)
	if err != nil {
		t.Fatalf("env %s: %v", helperEnv, err)
	}
	if strings.TrimSpace(out) != "server" {
		t.Fatalf("explicit server.Env was not passed through: %q", out)
	}
}
