package toolkit

// Regression tests for MCP-04: every MCP tool result is bounded by the product
// output budget (maxOutputBytes, default 64KiB) with an explicit truncation
// marker, without changing the tool-level error semantics.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// bigResultMCPServer is a Streamable HTTP MCP server whose "big" tool and
// "big.txt" resource both return payload bytes.
func bigResultMCPServer(t *testing.T, payload string, isError bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var request struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(body, &request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch request.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}, "resources": map[string]any{}},
				"serverInfo":      map[string]any{"name": "big", "version": "1.0.0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []any{oneTool("big")}}
		case "tools/call":
			result = map[string]any{
				"content": []any{map[string]any{"type": "text", "text": payload}},
				"isError": isError,
			}
		case "resources/list":
			result = map[string]any{"resources": []any{map[string]any{"uri": "test://big.txt", "name": "big"}}}
		case "resources/read":
			result = map[string]any{"contents": []any{map[string]any{"uri": "test://big.txt", "text": payload}}}
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
}

// TestToolkitMCPResultsBoundedByMaxOutputBytes is the end-to-end regression:
// tools/call and resources/read payloads obey the assembly's maxOutputBytes.
func TestToolkitMCPResultsBoundedByMaxOutputBytes(t *testing.T) {
	payload := strings.Repeat("payload-", 25_000) // 200_000 bytes
	server := bigResultMCPServer(t, payload, false)
	defer server.Close()
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"big","url":"` + server.URL + `"}]`,
	})
	tools, _ := activateMCPToolkit(t, map[string]any{"root": ".", "maxOutputBytes": 1024}, settings, nil)

	for name, arguments := range map[string]string{
		"tool":     `{}`,
		"resource": `{"uri":"test://big.txt"}`,
	} {
		toolName := "mcp__big__big"
		if name == "resource" {
			toolName = "mcp__big__read_resource"
		}
		result, err := tools.Invoke(context.Background(), rig.ToolCall{Name: toolName, Arguments: arguments})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(result.Content) > 1024 {
			t.Fatalf("%s result = %d bytes, want <= 1024 (maxOutputBytes)", name, len(result.Content))
		}
		if !strings.Contains(result.Content, "[truncated:") {
			t.Fatalf("%s result lacks the truncation marker: %q", name, result.Content[:64])
		}
		if result.IsError {
			t.Fatalf("%s unexpectedly marked IsError", name)
		}
	}
}

// TestToolkitMCPErrorResultsBounded keeps the tool-level error flag on a clipped
// error payload.
func TestToolkitMCPErrorResultsBounded(t *testing.T) {
	payload := strings.Repeat("error-line\n", 20_000)
	server := bigResultMCPServer(t, payload, true)
	defer server.Close()
	settings := newMutableSettings(map[string]string{
		contracts.SettingMCPServers: `[{"name":"big","url":"` + server.URL + `"}]`,
	})
	tools, _ := activateMCPToolkit(t, map[string]any{"root": ".", "maxOutputBytes": 512}, settings, nil)
	result, err := tools.Invoke(context.Background(), rig.ToolCall{Name: "mcp__big__big", Arguments: `{}`})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !result.IsError {
		t.Fatal("large error result lost IsError")
	}
	if len(result.Content) > 512 || !strings.Contains(result.Content, "[truncated:") {
		t.Fatalf("error result = %d bytes, want <= 512 with a marker", len(result.Content))
	}
}
