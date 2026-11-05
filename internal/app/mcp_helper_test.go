package app

import (
	"context"
	"fmt"
	"os"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const appMCPHelperEnv = "ZEOKEEL_APP_MCP_HELPER"

// TestMain doubles as the entry point for the fake MCP server subprocess used
// by the isolation test. stdout is the transport, so the helper exits before
// the testing framework can write to it.
func TestMain(m *testing.M) {
	if os.Getenv(appMCPHelperEnv) == "server" {
		server := sdk.NewServer(&sdk.Implementation{Name: "app-fake", Version: "0.1.0"}, nil)
		sdk.AddTool(server, &sdk.Tool{Name: "echo", Description: "Echo text."},
			func(_ context.Context, _ *sdk.CallToolRequest, in struct {
				Text string `json:"text,omitempty"`
			}) (*sdk.CallToolResult, any, error) {
				return &sdk.CallToolResult{Content: []sdk.Content{
					&sdk.TextContent{Text: "echo: " + in.Text},
				}}, nil, nil
			})
		_ = server.Run(context.Background(), &sdk.StdioTransport{})
		os.Exit(0)
	}
	if os.Getenv(appMCPHelperEnv) != "" {
		fmt.Fprintf(os.Stderr, "unknown helper mode %q\n", os.Getenv(appMCPHelperEnv))
		os.Exit(2)
	}
	os.Exit(m.Run())
}
