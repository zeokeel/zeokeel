package app

import (
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/provider/openai"
)

// Outbound network proxy (FP-19).
//
// One setting, network.proxy, configures every outbound HTTP client the agent
// uses: the model provider, web_search and web_fetch. The empty default keeps
// the historical behavior (each client follows the environment proxy variables;
// web_fetch connects directly when the environment has no proxy) and the app
// never writes proxy variables itself.

// networkProxyFromSetting parses the network.proxy setting (or
// ZEOKEEL_NETWORK_PROXY). Empty means "no explicit proxy". An invalid value is
// logged and ignored so a typo cannot break model calls.
func networkProxyFromSetting(get func(key, env, def string) string) *url.URL {
	raw := strings.TrimSpace(get("network.proxy", "ZEOKEEL_NETWORK_PROXY", ""))
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		log.Printf("zeokeel: ignoring invalid network.proxy %q: use an http or https URL", raw)
		return nil
	}
	return parsed
}

// providerProxyHTTPClient mirrors the provider's own client defaults (a clone of
// the default transport, so proxy and dial behavior stay standard) with the
// configured proxy applied. The provider uses the client as provided, so the
// response-header timeout the provider would otherwise install is set here too.
func providerProxyHTTPClient(proxy *url.URL) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxy)
	if transport.ResponseHeaderTimeout == 0 {
		transport.ResponseHeaderTimeout = openai.DefaultResponseHeaderTimeout
	}
	return &http.Client{Transport: transport}
}

// webSearchProxyHTTPClient mirrors web_search's own client defaults (10s overall
// timeout) with the configured proxy applied.
func webSearchProxyHTTPClient(proxy *url.URL) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxy)
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}
}
