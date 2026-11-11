package toolkit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
)

// TestWebFetchAllowsPrivateAddresses pins the product decision that web_fetch
// installs no URL/address policy: loopback and private targets are fetchable
// (the tool still only accepts http/https and keeps its timeout/size caps).
func TestWebFetchAllowsPrivateAddresses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body>loopback ok</body></html>"))
	}))
	defer server.Close()

	tool := webFetcher(Config{Network: true})
	if tool == nil {
		t.Fatal("webFetcher(Network=true) = nil, want a tool")
	}
	result, err := tool.Invoke(context.Background(), rig.ToolCall{
		Name:      "web_fetch",
		Arguments: `{"url":"` + server.URL + `"}`,
	})
	if err != nil {
		t.Fatalf("web_fetch(loopback): %v", err)
	}
	if result.IsError || !strings.Contains(result.Content, "loopback ok") {
		t.Fatalf("web_fetch(loopback) = %q (error=%v), want the loopback body", result.Content, result.IsError)
	}

	if tool := webFetcher(Config{Network: false}); tool != nil {
		t.Fatal("webFetcher(Network=false) returned a tool, want nil")
	}
}
