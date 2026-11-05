package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// resetNetworkProxy clears the package-wide proxy after a test.
func resetNetworkProxy(t *testing.T) {
	t.Helper()
	SetNetworkProxy(nil)
	t.Cleanup(func() { SetNetworkProxy(nil) })
	// Tests must not inherit a developer/CI HTTP proxy: the direct and explicit
	// proxy cases assert exact routing.
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		t.Setenv(name, "")
	}
}

// TestWebFetchUsesConfiguredProxy is the FP-19 web_fetch regression: an
// explicit network.proxy routes the request through it (absolute-URI proxy
// request), while the tool still returns the proxy's response.
func TestWebFetchUsesConfiguredProxy(t *testing.T) {
	resetNetworkProxy(t)
	var (
		hits atomic.Int32
		host atomic.Value
	)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		host.Store(r.Host)
		if !strings.HasPrefix(r.URL.Path, "/page") {
			t.Errorf("proxy request path = %q, want /page", r.URL.Path)
		}
		_, _ = w.Write([]byte("proxied body"))
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	SetNetworkProxy(proxyURL)

	// allowPrivate: the test target is loopback; the proxy itself is the thing
	// under test here.
	tool := newWebFetchTool(1024, true)
	args, _ := json.Marshal(map[string]string{"url": "http://127.0.0.1:9/page"})
	out, err := tool.Invoke(context.Background(), args)
	if err != nil {
		t.Fatalf("web_fetch through proxy: %v", err)
	}
	if out != "proxied body" {
		t.Fatalf("web_fetch output = %q, want the proxy response", out)
	}
	if hits.Load() != 1 {
		t.Fatalf("proxy hits = %d, want 1", hits.Load())
	}
	if got, _ := host.Load().(string); got != "127.0.0.1:9" {
		t.Fatalf("proxy saw host %q, want 127.0.0.1:9", got)
	}
}

// TestWebFetchRejectsPrivateTargetWithProxy keeps the SSRF guard even when a
// proxy is configured: the destination host is validated before any request is
// sent, so the proxy never sees the private target.
func TestWebFetchRejectsPrivateTargetWithProxy(t *testing.T) {
	resetNetworkProxy(t)
	var hits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("should not happen"))
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	SetNetworkProxy(proxyURL)

	tool := newWebFetchTool(1024, false) // production SSRF policy
	args, _ := json.Marshal(map[string]string{"url": "http://127.0.0.1:9/private"})
	if _, err := tool.Invoke(context.Background(), args); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("private target with proxy error = %v, want the non-public rejection", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("proxy was contacted for a rejected private target (%d hits)", hits.Load())
	}
}

// TestWebFetchDirectWithoutProxy pins the default: no setting and no env proxy
// means a direct, SSRF-guarded connection (the historical behavior).
func TestWebFetchDirectWithoutProxy(t *testing.T) {
	resetNetworkProxy(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("direct body"))
	}))
	defer srv.Close()
	tool := newWebFetchTool(1024, true)
	args, _ := json.Marshal(map[string]string{"url": srv.URL})
	out, err := tool.Invoke(context.Background(), args)
	if err != nil {
		t.Fatalf("direct web_fetch: %v", err)
	}
	if out != "direct body" {
		t.Fatalf("direct web_fetch output = %q", out)
	}
}
