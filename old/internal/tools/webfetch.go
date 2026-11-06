package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	webFetchTimeout = 10 * time.Second
	maxWebRedirects = 10
)

const webFetchDescription = `Fetch a web page over HTTP or HTTPS and return its body as text.

Only http and https URLs are allowed. The request times out after 10 seconds, reads at most 2 MiB, and is truncated to the configured output limit. SSRF protection rejects loopback, private, link-local and other non-public addresses, including on every redirect hop.

The app's network.proxy setting applies here like it does to the model provider and web_search; with no setting, HTTP(S)_PROXY from the environment is honored and a direct connection is used otherwise. When a proxy is configured, the destination host is still resolved and validated locally on every request and redirect; the proxy itself must be trusted user configuration.

Args JSON schema:
{"type":"object","properties":{"url":{"type":"string","description":"Absolute http or https URL to fetch."}},"required":["url"],"additionalProperties":false}`

type webFetchTool struct {
	client       *http.Client
	maxOutput    int
	allowPrivate bool
}

// newWebFetchTool builds a fetch tool. allowPrivate exists so package tests can
// point the tool at an httptest server; Builtins always passes false.
func newWebFetchTool(maxOutput int, allowPrivate bool) *webFetchTool {
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutputBytes
	}
	t := &webFetchTool{maxOutput: maxOutput, allowPrivate: allowPrivate}
	t.client = t.newClient()
	return t
}

func (t *webFetchTool) Name() string        { return "web_fetch" }
func (t *webFetchTool) Description() string { return webFetchDescription }

// networkProxy is the process-wide outbound proxy the app configures from the
// network.proxy setting (FP-19). The zero value (nil URL) means "follow the
// environment proxy variables, then connect directly", which matches the model
// provider and web_search. The app calls SetNetworkProxy before every tool
// registry build, so a setting change applies to the next turn.
var networkProxy struct {
	mu  sync.RWMutex
	url *url.URL
}

// SetNetworkProxy configures the proxy every web_fetch tool uses. nil restores
// the environment/default behavior. The URL must be http or https; the app
// validates the setting before calling this.
func SetNetworkProxy(proxy *url.URL) {
	networkProxy.mu.Lock()
	networkProxy.url = proxy
	networkProxy.mu.Unlock()
}

// configuredNetworkProxy returns the explicitly configured proxy, or nil.
func configuredNetworkProxy() *url.URL {
	networkProxy.mu.RLock()
	defer networkProxy.mu.RUnlock()
	return networkProxy.url
}

// envProxyConfigured reports whether the environment asks for an HTTP proxy.
// The fetch tool only takes the environment-proxy path when a setting is
// absent; the names mirror net/http's ProxyFromEnvironment.
func envProxyConfigured() bool {
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	return false
}

func (t *webFetchTool) newClient() *http.Client {
	proxy := configuredNetworkProxy()
	useProxy := proxy != nil || envProxyConfigured()
	dialer := &net.Dialer{Timeout: webFetchTimeout}
	if !t.allowPrivate && !useProxy {
		// Direct connections keep the DNS-rebinding-safe destination check:
		// Control runs after DNS resolution with the concrete address that is
		// about to be dialed. With a proxy the dialer reaches the proxy, so the
		// target is validated by validateURL's preflight resolution instead.
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return nil
			}
			addr, err := netip.ParseAddr(host)
			if err != nil {
				return nil
			}
			if !isPublicAddr(addr) {
				return fmt.Errorf("web_fetch: refusing to connect to non-public address %s", addr)
			}
			return nil
		}
	}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          8,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   webFetchTimeout,
		ResponseHeaderTimeout: webFetchTimeout,
	}
	switch {
	case proxy != nil:
		transport.Proxy = http.ProxyURL(proxy)
	case useProxy:
		transport.Proxy = http.ProxyFromEnvironment
	default:
		transport.Proxy = nil
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       webFetchTimeout,
		CheckRedirect: t.checkRedirect,
	}
}

// checkRedirect validates every redirect hop, not just the original URL.
func (t *webFetchTool) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxWebRedirects {
		return fmt.Errorf("web_fetch: stopped after %d redirects", maxWebRedirects)
	}
	if req == nil || req.URL == nil {
		return fmt.Errorf("web_fetch: invalid redirect")
	}
	if err := t.validateURL(req.Context(), req.URL); err != nil {
		return fmt.Errorf("web_fetch: redirect to %s: %w", req.URL.Redacted(), err)
	}
	return nil
}

func (t *webFetchTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		URL string `json:"url"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.URL) == "" {
		return "", fmt.Errorf("web_fetch: url is required")
	}
	u, err := url.Parse(a.URL)
	if err != nil {
		return "", fmt.Errorf("web_fetch: invalid URL %q: %w", a.URL, err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, webFetchTimeout)
	defer cancel()
	if err := t.validateURL(ctx, u); err != nil {
		return "", fmt.Errorf("web_fetch: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("web_fetch: build request: %w", err)
	}
	req.Header.Set("User-Agent", "ZeoKeel/1.0 (web_fetch)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.8")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("web_fetch: %s: %w", u.Redacted(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		detail := strings.TrimSpace(string(snippet))
		if detail != "" {
			return "", fmt.Errorf("web_fetch: %s returned %s: %s", u.Redacted(), resp.Status, truncateBytes(detail, 512, "body truncated"))
		}
		return "", fmt.Errorf("web_fetch: %s returned %s", u.Redacted(), resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxFetchBytes+1))
	if err != nil {
		return "", fmt.Errorf("web_fetch: read response body: %w", err)
	}
	text := string(body)
	if len(body) > MaxFetchBytes {
		text = capWithNote(text, MaxFetchBytes, fmt.Sprintf("response truncated at %d bytes", MaxFetchBytes))
	}
	if len(text) > t.maxOutput {
		text = capWithNote(text, t.maxOutput, fmt.Sprintf("response truncated to %d bytes", t.maxOutput))
	}
	return text, nil
}

// validateURL enforces the scheme and address policy for one URL. Hostnames are
// resolved and every returned address must be public; literal addresses are
// checked directly.
func (t *webFetchTool) validateURL(ctx context.Context, u *url.URL) error {
	if u == nil {
		return fmt.Errorf("missing URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q; only http and https are allowed", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("URL %q has no host", u.Redacted())
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL %q has no host", u.Redacted())
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if !t.allowPrivate && !isPublicAddr(addr) {
			return fmt.Errorf("refusing to fetch non-public address %s", addr)
		}
		return nil
	}
	if t.allowPrivate {
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("cannot resolve host %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("host %q resolved to no addresses", host)
	}
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok {
			return fmt.Errorf("host %q resolved to an invalid address", host)
		}
		if !isPublicAddr(addr) {
			return fmt.Errorf("host %q resolves to non-public address %s", host, addr.Unmap())
		}
	}
	return nil
}

// nonPublicPrefixes lists address ranges that are not globally reachable but
// are not covered by the netip.Addr helpers (documentation, benchmarking,
// shared address space, reserved space and the IETF protocol block).
var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:2::/48"),
	netip.MustParsePrefix("2001:10::/28"),
	netip.MustParsePrefix("2001:20::/28"),
	netip.MustParsePrefix("2001:db8::/32"),
}

// isPublicAddr reports whether addr is a globally routable unicast address.
func isPublicAddr(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	if addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsUnspecified() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsInterfaceLocalMulticast() {
		return false
	}
	for _, prefix := range nonPublicPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}
