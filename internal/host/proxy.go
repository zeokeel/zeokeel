package host

// Network proxy bridge: the settings page writes net.proxy/net.noProxy; the
// host applies them to the process environment at startup, so outbound HTTP
// clients (and child processes) honour the proxy. A restart applies changes.
// The host only removes environment variables it set itself, so a user's own
// HTTP(S)_PROXY/NO_PROXY environment survives a launch with the setting empty.

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// proxyEnvironmentKeys is the set of variables the host owns for HTTP(S) proxy
// configuration: both cases are set because tools differ in which they read.
var proxyEnvironmentKeys = []string{
	"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "NO_PROXY", "no_proxy",
}

var (
	// proxyApplied records the keys/valeurs this process set, so disabling the
	// setting can remove exactly them and nothing else.
	proxyAppliedMu sync.Mutex
	proxyApplied   = map[string]string{}
)

// proxyEnv turns the two settings into environment changes: the keys to set
// with their values and the keys to clear. An empty proxy disables the explicit
// proxy (clear all owned keys); a non-empty proxy must be an http(s) URL with a
// host. Invalid input returns an error and no changes.
func proxyEnv(proxy, noProxy string) (map[string]string, map[string]bool, error) {
	proxy = strings.TrimSpace(proxy)
	noProxy = strings.TrimSpace(noProxy)
	if proxy == "" {
		return nil, allProxyEnvKeys(), nil
	}
	if !validProxyURL(proxy) {
		return nil, nil, fmt.Errorf("invalid proxy URL %q: enter an http(s) URL with a host", proxy)
	}
	set := map[string]string{
		"HTTP_PROXY":  proxy,
		"HTTPS_PROXY": proxy,
		"http_proxy":  proxy,
		"https_proxy": proxy,
	}
	del := make(map[string]bool)
	if noProxy == "" {
		del["NO_PROXY"] = true
		del["no_proxy"] = true
	} else {
		set["NO_PROXY"] = noProxy
		set["no_proxy"] = noProxy
	}
	return set, del, nil
}

// allProxyEnvKeys returns a delete set for every owned variable.
func allProxyEnvKeys() map[string]bool {
	del := make(map[string]bool, len(proxyEnvironmentKeys))
	for _, key := range proxyEnvironmentKeys {
		del[key] = true
	}
	return del
}

// validProxyURL reports whether raw is an http(s) URL with a host.
func validProxyURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return false
	}
	return parsed.Hostname() != ""
}

// applyProxySettings applies the stored settings to the process environment.
// It validates before touching anything, records what it set (so the next empty
// setting clears exactly those keys), and never returns a partial application
// after a validation error.
func applyProxySettings(settings *Settings) error {
	if settings == nil {
		return nil
	}
	set, del, err := proxyEnv(
		settings.Get(contracts.SettingNetProxy, ""),
		settings.Get(contracts.SettingNetNoProxy, ""),
	)
	if err != nil {
		return err
	}
	proxyAppliedMu.Lock()
	defer proxyAppliedMu.Unlock()
	for key := range del {
		if _, ours := proxyApplied[key]; !ours {
			continue
		}
		if err := os.Unsetenv(key); err != nil {
			return fmt.Errorf("clear %s: %w", key, err)
		}
		delete(proxyApplied, key)
	}
	for key, value := range set {
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s: %w", key, err)
		}
		proxyApplied[key] = value
	}
	return nil
}
