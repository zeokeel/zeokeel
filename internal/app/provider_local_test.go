package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/store"
)

// TestKeylessLocalProvider covers the PD-02 policy: local/loopback/private
// endpoints may run without an API key; public endpoints still require one.
func TestKeylessLocalProvider(t *testing.T) {
	local := []string{
		"http://127.0.0.1:11434/v1",
		"http://localhost:8080/v1",
		"http://localhost.localdomain/v1",
		"http://[::1]:8000/v1",
		"http://192.168.1.25:1234/v1",
		"http://10.1.2.3:8080/v1",
		"http://172.16.5.5:8080/v1",
	}
	for _, url := range local {
		if !keylessLocalProvider(url) {
			t.Errorf("keylessLocalProvider(%q) = false, want true", url)
		}
	}
	remote := []string{
		"",
		"https://api.deepseek.com/v1",
		"https://api.openai.com/v1",
		"http://example.test/v1",
		"not a url",
	}
	for _, url := range remote {
		if keylessLocalProvider(url) {
			t.Errorf("keylessLocalProvider(%q) = true, want false", url)
		}
	}
}

// TestBuildAgentKeylessLocalProvider proves the app builds an agent for a local
// provider without an API key (so Send works instead of reporting "no agent"),
// while a keyless public provider still means "not configured".
func TestBuildAgentKeylessLocalProvider(t *testing.T) {
	t.Run("local provider without key builds", func(t *testing.T) {
		st, dir := newToolStore(t)
		for key, value := range map[string]string{
			"provider.base_url": "http://127.0.0.1:11434/v1",
			"provider.model":    "llama3",
		} {
			if err := st.SetSetting(key, value); err != nil {
				t.Fatal(err)
			}
		}
		bundle, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{})
		if err != nil {
			t.Fatalf("buildAgentWithOptions: %v", err)
		}
		if bundle == nil {
			t.Fatal("local provider without an API key produced no agent")
		}
		if bundle.Close != nil {
			t.Cleanup(func() { _ = bundle.Close() })
		}
	})
	t.Run("public provider without key stays unconfigured", func(t *testing.T) {
		st, dir := newToolStore(t)
		if err := st.SetSetting("provider.base_url", "https://api.deepseek.com/v1"); err != nil {
			t.Fatal(err)
		}
		bundle, err := buildAgentWithOptions(st, dir, "instruction", registryOptions{})
		if err != nil {
			t.Fatalf("buildAgentWithOptions: %v", err)
		}
		if bundle != nil {
			if bundle.Close != nil {
				_ = bundle.Close()
			}
			t.Fatal("public provider without an API key built an agent")
		}
	})
}

// TestRunHeadlessKeylessLocalProvider is the PD-02 end-to-end check: a local
// OpenAI-compatible endpoint answers without an API key and the run succeeds.
func TestRunHeadlessKeylessLocalProvider(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZEOKEEL_DATA_DIR", dir)
	srv := sseServer(t, "local model answer")
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	for key, value := range map[string]string{
		"provider.base_url": srv.URL + "/v1",
		"provider.model":    "llama3",
		// No provider.api_key on purpose.
		"provider.retry_max_retries": "0",
	} {
		if err := st.SetSetting(key, value); err != nil {
			t.Fatalf("SetSetting(%q): %v", key, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := RunHeadless([]string{"--json", "hello local model"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	var result headlessResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !strings.Contains(result.Text, "local model answer") {
		t.Fatalf("text = %q, want the local model answer", result.Text)
	}
}
