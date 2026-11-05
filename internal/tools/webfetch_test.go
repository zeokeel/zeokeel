package tools

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWebFetchTestServer(t *testing.T) {
	const hello = "hello from server"
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(hello))
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/hello", http.StatusFound)
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 5000))
	})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "missing page", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tool := newWebFetchTool(DefaultMaxOutputBytes, true)
	if got := mustInvoke(t, tool, map[string]string{"url": srv.URL + "/hello"}); got != hello {
		t.Fatalf("web_fetch = %q, want %q", got, hello)
	}
	if got := mustInvoke(t, tool, map[string]string{"url": srv.URL + "/redirect"}); got != hello {
		t.Fatalf("web_fetch after redirect = %q", got)
	}

	small := newWebFetchTool(100, true)
	out := mustInvoke(t, small, map[string]string{"url": srv.URL + "/big"})
	if len(out) > 100 {
		t.Fatalf("truncated fetch is %d bytes, want <= 100", len(out))
	}
	if !strings.Contains(out, "truncated") {
		t.Fatalf("truncated fetch has no marker: %q", out)
	}

	if _, err := invokeTool(t, tool, map[string]string{"url": srv.URL + "/missing"}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("404 error = %v", err)
	}
	for _, raw := range []string{"", "ftp://example.com/file", "file:///etc/passwd", "http:///nohost", "://bad"} {
		if _, err := invokeTool(t, tool, map[string]string{"url": raw}); err == nil {
			t.Fatalf("web_fetch accepted invalid URL %q", raw)
		}
	}
	if _, err := tool.Invoke(context.Background(), nil); err == nil {
		t.Fatal("web_fetch without url should fail")
	}
	if _, err := tool.Invoke(nil, mustArgs(t, map[string]string{"url": srv.URL + "/hello"})); err != nil {
		t.Fatalf("nil context: %v", err)
	}
}

func TestWebFetchRejectsPrivateAddresses(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("should not be reached"))
	}))
	defer srv.Close()

	tool := newWebFetchTool(DefaultMaxOutputBytes, false)
	if _, err := invokeTool(t, tool, map[string]string{"url": srv.URL + "/private"}); err == nil {
		t.Fatal("web_fetch reached a loopback httptest server")
	}
	if hits.Load() != 0 {
		t.Fatalf("web_fetch made %d requests to a private server", hits.Load())
	}

	bad := []string{
		"http://127.0.0.1/",
		"http://127.9.9.9/",
		"http://[::1]/",
		"http://10.0.0.1/",
		"http://172.16.0.1/",
		"http://192.168.1.1/",
		"http://169.254.169.254/latest/meta-data",
		"http://[fe80::1]/",
		"http://0.0.0.0/",
		"http://100.64.0.1/",
		"http://192.0.2.1/",
		"http://198.18.0.1/",
		"http://[2001:db8::1]/",
		"http://[fd00::1]/",
		"http://255.255.255.255/",
		"http://localhost/",
	}
	for _, raw := range bad {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if err := tool.validateURL(context.Background(), u); err == nil {
			t.Fatalf("validateURL accepted non-public URL %q", raw)
		}
	}

	public := []string{
		"http://8.8.8.8/",
		"https://1.1.1.1/",
		"http://93.184.216.34/",
		"http://[2606:4700:4700::1111]/",
	}
	for _, raw := range public {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if err := tool.validateURL(context.Background(), u); err != nil {
			t.Fatalf("validateURL rejected public URL %q: %v", raw, err)
		}
	}
}

func TestWebFetchRejectsPrivateRedirect(t *testing.T) {
	tool := newWebFetchTool(DefaultMaxOutputBytes, false)
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:9999/next", nil)
	if err := tool.checkRedirect(req, []*http.Request{{}}); err == nil {
		t.Fatal("checkRedirect accepted a redirect to loopback")
	}
	ftpReq := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	ftpReq.URL, _ = url.Parse("ftp://example.com/secret")
	if err := tool.checkRedirect(ftpReq, []*http.Request{{}}); err == nil {
		t.Fatal("checkRedirect accepted a non-http redirect")
	}
	tooMany := make([]*http.Request, maxWebRedirects)
	if err := tool.checkRedirect(req, tooMany); err == nil {
		t.Fatal("checkRedirect accepted too many redirects")
	}
}

func TestIsPublicAddr(t *testing.T) {
	cases := map[string]bool{
		"8.8.8.8":              true,
		"1.1.1.1":              true,
		"93.184.216.34":        true,
		"2606:4700:4700::1111": true,
		"127.0.0.1":            false,
		"10.1.2.3":             false,
		"172.20.0.1":           false,
		"192.168.0.1":          false,
		"169.254.169.254":      false,
		"100.64.0.1":           false,
		"192.0.2.10":           false,
		"198.18.0.1":           false,
		"203.0.113.9":          false,
		"255.255.255.255":      false,
		"0.0.0.0":              false,
		"::1":                  false,
		"::":                   false,
		"fe80::1":              false,
		"fd00::1":              false,
		"2001:db8::1":          false,
		"::ffff:127.0.0.1":     false,
		"224.0.0.1":            false,
	}
	for raw, want := range cases {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			t.Fatalf("parse addr %q: %v", raw, err)
		}
		if got := isPublicAddr(addr); got != want {
			t.Errorf("isPublicAddr(%s) = %v, want %v", raw, got, want)
		}
	}
	if isPublicAddr(netip.Addr{}) {
		t.Error("isPublicAddr(invalid) = true")
	}
}

func TestWebFetchBodyCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("y"), MaxFetchBytes+1000))
	}))
	defer srv.Close()

	tool := newWebFetchTool(MaxFetchBytes+1000, true)
	out, err := invokeTool(t, tool, map[string]string{"url": srv.URL + "/huge"})
	if err != nil {
		t.Fatalf("web_fetch huge: %v", err)
	}
	if len(out) > MaxFetchBytes {
		t.Fatalf("web_fetch returned %d bytes, want <= %d", len(out), MaxFetchBytes)
	}
	if !strings.Contains(out, "truncated") {
		t.Fatalf("capped web_fetch has no truncation marker")
	}
}
