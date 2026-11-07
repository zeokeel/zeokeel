package host

// Tests for the network-proxy environment bridge: the pure setting-to-env
// decision (validation, upper/lowercase compatibility, NO_PROXY) and applying
// it to the process environment (set on save, cleared when the setting is
// empty). The Windows/macOS GUI start path is exercised only for compilation
// here; env tests use t.Setenv so they restore the developer environment.

import (
	"os"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// proxyEnvKeys are the environment variables the host owns for outbound HTTP.
var proxyEnvKeys = []string{
	"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "NO_PROXY", "no_proxy",
}

// clearProxyEnv snapshots the six keys as empty for the duration of the test.
func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, key := range proxyEnvKeys {
		t.Setenv(key, "")
	}
}

func TestProxyEnv(t *testing.T) {
	tests := []struct {
		name    string
		proxy   string
		noProxy string
		wantSet map[string]string
		wantDel []string
		wantErr bool
	}{
		{
			name:    "empty proxy clears every owned key",
			wantDel: proxyEnvKeys,
		},
		{
			name:    "no proxy with noproxy still clears",
			noProxy: "localhost",
			wantDel: proxyEnvKeys,
		},
		{
			name:  "http proxy sets both cases",
			proxy: "http://127.0.0.1:7890",
			wantSet: map[string]string{
				"HTTP_PROXY":  "http://127.0.0.1:7890",
				"HTTPS_PROXY": "http://127.0.0.1:7890",
				"http_proxy":  "http://127.0.0.1:7890",
				"https_proxy": "http://127.0.0.1:7890",
			},
			wantDel: []string{"NO_PROXY", "no_proxy"},
		},
		{
			name:    "https proxy with noproxy",
			proxy:   "https://proxy.example.com:8443",
			noProxy: " localhost,127.0.0.1,::1 ",
			wantSet: map[string]string{
				"HTTP_PROXY":  "https://proxy.example.com:8443",
				"HTTPS_PROXY": "https://proxy.example.com:8443",
				"http_proxy":  "https://proxy.example.com:8443",
				"https_proxy": "https://proxy.example.com:8443",
				"NO_PROXY":    "localhost,127.0.0.1,::1",
				"no_proxy":    "localhost,127.0.0.1,::1",
			},
		},
		{name: "proxy is trimmed", proxy: "  http://proxy.example.com  ", wantSet: map[string]string{
			"HTTP_PROXY":  "http://proxy.example.com",
			"HTTPS_PROXY": "http://proxy.example.com",
			"http_proxy":  "http://proxy.example.com",
			"https_proxy": "http://proxy.example.com",
		}, wantDel: []string{"NO_PROXY", "no_proxy"}},
		{name: "missing scheme", proxy: "127.0.0.1:7890", wantErr: true},
		{name: "unsupported scheme", proxy: "ftp://proxy.example.com", wantErr: true},
		{name: "missing host", proxy: "http://", wantErr: true},
		{name: "garbage url", proxy: "://bad", wantErr: true},
		{name: "spaces only proxy treated as empty", proxy: "   ", wantDel: proxyEnvKeys},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			set, del, err := proxyEnv(test.proxy, test.noProxy)
			if test.wantErr {
				if err == nil {
					t.Fatalf("proxyEnv(%q) = nil error", test.proxy)
				}
				if set != nil || del != nil {
					t.Fatalf("invalid proxy returned env changes: set=%v del=%v", set, del)
				}
				return
			}
			if err != nil {
				t.Fatalf("proxyEnv(%q): %v", test.proxy, err)
			}
			for key, want := range test.wantSet {
				if got := set[key]; got != want {
					t.Fatalf("set[%s] = %q, want %q (all: %v)", key, got, want, set)
				}
			}
			for key := range set {
				if _, ok := test.wantSet[key]; !ok {
					t.Fatalf("unexpected env set %q = %q", key, set[key])
				}
			}
			for _, key := range test.wantDel {
				if !del[key] {
					t.Fatalf("del[%s] = false, want true (all: %v)", key, del)
				}
			}
			for key := range del {
				found := false
				for _, want := range test.wantDel {
					if key == want {
						found = true
					}
				}
				if !found && test.wantDel != nil {
					t.Fatalf("unexpected env delete %q", key)
				}
			}
		})
	}
}

func TestApplyProxySettingsSetsEnv(t *testing.T) {
	clearProxyEnv(t)
	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if err := settings.Set(contracts.SettingNetProxy, "http://127.0.0.1:7890"); err != nil {
		t.Fatalf("Set(proxy): %v", err)
	}
	if err := settings.Set(contracts.SettingNetNoProxy, "localhost,127.0.0.1"); err != nil {
		t.Fatalf("Set(noProxy): %v", err)
	}
	t.Cleanup(func() {
		_ = settings.Set(contracts.SettingNetProxy, "")
		_ = settings.Set(contracts.SettingNetNoProxy, "")
		_ = applyProxySettings(settings)
	})
	if err := applyProxySettings(settings); err != nil {
		t.Fatalf("applyProxySettings: %v", err)
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if got := os.Getenv(key); got != "http://127.0.0.1:7890" {
			t.Fatalf("%s = %q, want the proxy", key, got)
		}
	}
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		if got := os.Getenv(key); got != "localhost,127.0.0.1" {
			t.Fatalf("%s = %q, want the no-proxy list", key, got)
		}
	}
}

func TestApplyProxySettingsClearsTheKeysItSet(t *testing.T) {
	clearProxyEnv(t)
	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if err := settings.Set(contracts.SettingNetProxy, "http://127.0.0.1:7890"); err != nil {
		t.Fatalf("Set(proxy): %v", err)
	}
	if err := settings.Set(contracts.SettingNetNoProxy, "localhost"); err != nil {
		t.Fatalf("Set(noProxy): %v", err)
	}
	if err := applyProxySettings(settings); err != nil {
		t.Fatalf("applyProxySettings(enable): %v", err)
	}
	if err := settings.Set(contracts.SettingNetProxy, ""); err != nil {
		t.Fatalf("Set(proxy empty): %v", err)
	}
	if err := settings.Set(contracts.SettingNetNoProxy, ""); err != nil {
		t.Fatalf("Set(noProxy empty): %v", err)
	}
	if err := applyProxySettings(settings); err != nil {
		t.Fatalf("applyProxySettings(disable): %v", err)
	}
	for _, key := range proxyEnvKeys {
		if _, ok := os.LookupEnv(key); ok {
			t.Fatalf("%s = %q after clearing the settings, want it unset", key, os.Getenv(key))
		}
	}
}

func TestApplyProxySettingsInvalidLeavesEnvUntouched(t *testing.T) {
	t.Setenv("HTTP_PROXY", "before")
	t.Setenv("http_proxy", "before-lower")
	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if err := settings.Set(contracts.SettingNetProxy, "not-a-url"); err != nil {
		t.Fatalf("Set(proxy): %v", err)
	}
	if err := applyProxySettings(settings); err == nil {
		t.Fatal("applyProxySettings(invalid URL) = nil error")
	}
	if got := os.Getenv("HTTP_PROXY"); got != "before" {
		t.Fatalf("HTTP_PROXY = %q, want the previous value", got)
	}
	if got := os.Getenv("http_proxy"); got != "before-lower" {
		t.Fatalf("http_proxy = %q, want the previous value", got)
	}
}

func TestApplyProxySettingsKeepsUserEnvWhenDisabled(t *testing.T) {
	// The host only clears variables it set itself: a user's own proxy env must
	// survive a start with an empty setting.
	t.Setenv("HTTP_PROXY", "user-proxy")
	t.Setenv("NO_PROXY", "user-no-proxy")
	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if err := applyProxySettings(settings); err != nil {
		t.Fatalf("applyProxySettings: %v", err)
	}
	if got := os.Getenv("HTTP_PROXY"); got != "user-proxy" {
		t.Fatalf("HTTP_PROXY = %q, want the user value kept", got)
	}
	if got := os.Getenv("NO_PROXY"); got != "user-no-proxy" {
		t.Fatalf("NO_PROXY = %q, want the user value kept", got)
	}
}

func TestApplyProxySettingsTrimsNoProxy(t *testing.T) {
	clearProxyEnv(t)
	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if err := settings.Set(contracts.SettingNetProxy, "https://proxy.example.com"); err != nil {
		t.Fatalf("Set(proxy): %v", err)
	}
	if err := settings.Set(contracts.SettingNetNoProxy, "  a.local , b.local  "); err != nil {
		t.Fatalf("Set(noProxy): %v", err)
	}
	t.Cleanup(func() {
		_ = settings.Set(contracts.SettingNetProxy, "")
		_ = settings.Set(contracts.SettingNetNoProxy, "")
		_ = applyProxySettings(settings)
	})
	if err := applyProxySettings(settings); err != nil {
		t.Fatalf("applyProxySettings: %v", err)
	}
	if got := os.Getenv("NO_PROXY"); got != "a.local , b.local" {
		t.Fatalf("NO_PROXY = %q, want the trimmed list", got)
	}
}
