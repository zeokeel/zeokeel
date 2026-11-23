package gateway

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

// TestEveryDataRouteRequiresAuth is the "no leaky handler" assertion: every
// route that reads or writes data refuses an unauthenticated request, and the
// only unauthenticated route is the static client bundle.
//
// The route table is written out here on purpose: adding a data route without
// adding it to this list (and to the switch in server.go) is a review error the
// test cannot catch, but a route that forgets its auth check fails immediately.
func TestEveryDataRouteRequiresAuth(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)
	// Serve a bundle so a leaky static-style 200 would be visible.
	gw.gateway.webFS = fstest.MapFS{
		"index.html":  &fstest.MapFile{Data: []byte("<!doctype html>ok")},
		"BUNDLE_INFO": &fstest.MapFile{Data: []byte("sha256 x")},
		".keep":       &fstest.MapFile{Data: []byte("marker")},
	}

	// No token at all (a browser always sends Origin; this one does not, so the
	// missing bearer is what must be refused).
	cases := []struct {
		method string
		path   string
		body   string
		want   []int
	}{
		{method: http.MethodGet, path: "/ws", want: []int{http.StatusUnauthorized}},
		{method: http.MethodPost, path: "/quit", want: []int{http.StatusUnauthorized}},
		{method: http.MethodPost, path: "/api/chat/run", body: `{"sessionId":"s1","text":"hi","clientRunId":"r"}`, want: []int{http.StatusUnauthorized}},
		{method: http.MethodPost, path: "/api/unknown", want: []int{http.StatusUnauthorized}},
		{method: http.MethodGet, path: "/api/unknown", want: []int{http.StatusUnauthorized}},
	}
	for _, testCase := range cases {
		request, err := http.NewRequest(testCase.method, "http://"+gw.address+testCase.path, strings.NewReader(testCase.body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("%s %s: %v", testCase.method, testCase.path, err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		ok := false
		for _, want := range testCase.want {
			if response.StatusCode == want {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("%s %s without a token = %d, want one of %v", testCase.method, testCase.path, response.StatusCode, testCase.want)
		}
	}

	// Build bookkeeping inside the embed tree is not served as an asset.
	for _, path := range []string{"/BUNDLE_INFO", "/.keep"} {
		response, err := http.Get("http://" + gw.address + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", path, response.StatusCode)
		}
	}

	// A foreign Origin is refused even when the token is correct.
	request, err := http.NewRequest(http.MethodPost, "http://"+gw.address+"/api/chat/run",
		strings.NewReader(`{"sessionId":"s1","text":"hi","clientRunId":"r"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Origin", "http://evil.example")
	request.Header.Set("Authorization", "Bearer "+gw.info.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST /api/chat/run: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign-origin run = %d, want 403", response.StatusCode)
	}

	// The static bundle is the one route without a token, and a non-GET method
	// on a client route is refused instead of answered with the page.
	staticResponse, err := http.Get("http://" + gw.address + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	_, _ = io.Copy(io.Discard, staticResponse.Body)
	_ = staticResponse.Body.Close()
	if staticResponse.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d, want 200 (static is public by design)", staticResponse.StatusCode)
	}
	postResponse, err := http.Post("http://"+gw.address+"/c/s1", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("POST /c/s1: %v", err)
	}
	_ = postResponse.Body.Close()
	if postResponse.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /c/s1 = %d, want 405", postResponse.StatusCode)
	}
}

// TestSecurityHeadersArePresent pins the response hardening (§3.2) and the one
// deliberate relaxation (style-src for antd's CSS-in-JS). script-src must stay
// 'self': the client renders model output.
func TestSecurityHeadersArePresent(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)
	gw.gateway.webFS = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html>ok")}}

	response, err := http.Get("http://" + gw.address + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	for name, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := response.Header.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	csp := response.Header.Get("Content-Security-Policy")
	for _, directive := range []string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"object-src 'none'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
		"form-action 'self'",
		"connect-src 'self'",
	} {
		if !strings.Contains(csp, directive) {
			t.Fatalf("CSP %q is missing %q", csp, directive)
		}
	}
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") || strings.Contains(csp, "unsafe-eval") {
		t.Fatalf("script-src was relaxed: %q", csp)
	}
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("the gateway must not send CORS headers, got %q", got)
	}
}
