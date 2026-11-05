package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeSettingGetterWithProxy is fakeSettingGetter (context_budget_test.go) plus
// the env names this file uses.
func proxySettingGetter(values map[string]string) func(key, env, def string) string {
	return fakeSettingGetter(values)
}

// TestNetworkProxyFromSetting covers the FP-19 setting parser: only http(s)
// URLs with a host are accepted; empty and invalid values mean "no explicit
// proxy" (environment/default behavior).
func TestNetworkProxyFromSetting(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]string
		want   string
	}{
		{name: "unset", values: map[string]string{}, want: ""},
		{name: "blank", values: map[string]string{"network.proxy": "   "}, want: ""},
		{name: "http", values: map[string]string{"network.proxy": "http://127.0.0.1:8080"}, want: "http://127.0.0.1:8080"},
		{name: "https", values: map[string]string{"network.proxy": "https://proxy.example.test:3128"}, want: "https://proxy.example.test:3128"},
		{name: "env fallback", values: map[string]string{"ENV:ZEOKEEL_NETWORK_PROXY": "http://env-proxy:8888"}, want: "http://env-proxy:8888"},
		{name: "socks rejected", values: map[string]string{"network.proxy": "socks5://127.0.0.1:1080"}, want: ""},
		{name: "no scheme", values: map[string]string{"network.proxy": "proxy.example.test:8080"}, want: ""},
		{name: "garbage", values: map[string]string{"network.proxy": "://bad"}, want: ""},
	}
	for _, tc := range cases {
		got := networkProxyFromSetting(proxySettingGetter(tc.values))
		if got == nil {
			if tc.want != "" {
				t.Errorf("%s: networkProxyFromSetting = nil, want %q", tc.name, tc.want)
			}
			continue
		}
		if got.String() != tc.want {
			t.Errorf("%s: networkProxyFromSetting = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestProviderProxyHTTPClientRoutesThroughProxy proves the provider client
// built for network.proxy actually sends requests to the proxy (absolute-URI
// proxying for an otherwise unresolvable provider host).
func TestProviderProxyHTTPClientRoutesThroughProxy(t *testing.T) {
	var mu atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Store(r.Host + r.URL.Path)
		fmt.Fprint(w, "proxied")
	}))
	defer srv.Close()
	proxy, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := providerProxyHTTPClient(proxy)
	resp, err := client.Get("http://provider.invalid/v1/chat/completions")
	if err != nil {
		t.Fatalf("GET through proxy: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	body := make([]byte, 16)
	n, _ := resp.Body.Read(body)
	if string(body[:n]) != "proxied" {
		t.Fatalf("body = %q, want proxied", body[:n])
	}
	if got, _ := mu.Load().(string); got != "provider.invalid/v1/chat/completions" {
		t.Fatalf("proxy saw %q, want the provider host and path", got)
	}
}

// TestWebSearchProxyHTTPClientRoutesThroughProxy covers the web_search client.
func TestWebSearchProxyHTTPClientRoutesThroughProxy(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if !strings.Contains(r.Host, "search.invalid") {
			t.Errorf("proxy host = %q, want search.invalid", r.Host)
		}
		fmt.Fprint(w, "{}")
	}))
	defer srv.Close()
	proxy, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := webSearchProxyHTTPClient(proxy)
	resp, err := client.Get("http://search.invalid/search")
	if err != nil {
		t.Fatalf("GET through proxy: %v", err)
	}
	_ = resp.Body.Close()
	if hits.Load() != 1 {
		t.Fatalf("proxy hits = %d, want 1", hits.Load())
	}
}

// TestRunHeadlessUsesNetworkProxy is the FP-19 end-to-end check: a configured
// network.proxy routes the model provider's request through the proxy while the
// configured provider URL stays unresolvable locally.
func TestRunHeadlessUsesNetworkProxy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZEOKEEL_DATA_DIR", dir)
	var host atomic.Value
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		host.Store(r.Host)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeHeadlessSSE(w, "routed through proxy")
	}))
	defer proxy.Close()

	writeHeadlessStore(t, dir, "http://provider.invalid/v1", map[string]string{
		"provider.retry_max_retries": "0",
		"network.proxy":              proxy.URL,
	})
	var stdout, stderr bytes.Buffer
	code := RunHeadless([]string{"--json", "say hi"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	var result headlessResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(result.Text, "routed through proxy") {
		t.Fatalf("text = %q, want the proxied answer", result.Text)
	}
	if got, _ := host.Load().(string); got != "provider.invalid" {
		t.Fatalf("proxy saw host %q, want the configured provider host", got)
	}
}
