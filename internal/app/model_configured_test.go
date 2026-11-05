package app

import (
	"testing"
)

// TestHasConfiguredModelProductionWiring is the P2 regression: the desktop app
// installs the per-session AgentFactory at startup, so Controller.HasAgent() is
// always true; the first-run setup card must instead key off
// HasConfiguredModel(), which follows the provider/key configuration.
func TestHasConfiguredModelProductionWiring(t *testing.T) {
	// The environment must not make the test look configured.
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("ZEOKEEL_PROVIDER_BASE_URL", "")
	t.Setenv("ZEOKEEL_PROVIDER_API_KEY", "")

	dataDir := t.TempDir()
	ctrl, cleanup, err := BuildServicesWith(dataDir, nil)
	if err != nil {
		t.Fatalf("BuildServicesWith: %v", err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		if cleanup != nil {
			_ = cleanup()
		}
	})
	// Production wiring: factory installed in startServices, model predicate set.
	a := &app{services: ctrl}
	a.installSessionAgentFactory(ctrl)
	ctrl.SetModelConfiguredFunc(a.hasConfiguredModel)

	if !ctrl.HasAgent() {
		t.Fatal("HasAgent() = false with the per-session factory installed; it must stay the turn-execution predicate")
	}
	if ctrl.HasConfiguredModel() {
		t.Fatal("HasConfiguredModel() = true on a fresh data dir with no key/model; the first-run setup card would stay hidden (P2 regression)")
	}

	st := ctrl.Store()
	// A saved provider key makes a model runnable.
	if err := st.SetSetting("provider.api_key", "sk-test-key"); err != nil {
		t.Fatal(err)
	}
	if !ctrl.HasConfiguredModel() {
		t.Fatal("HasConfiguredModel() = false after provider.api_key was saved")
	}

	// A remote endpoint without a key is NOT configured.
	if err := st.SetSetting("provider.api_key", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("provider.base_url", "https://api.example.com/v1"); err != nil {
		t.Fatal(err)
	}
	if ctrl.HasConfiguredModel() {
		t.Fatal("HasConfiguredModel() = true for a remote endpoint without a key")
	}

	// PD-02 semantics: a local keyless provider (loopback) IS usable.
	if err := st.SetSetting("provider.base_url", "http://127.0.0.1:11434/v1"); err != nil {
		t.Fatal(err)
	}
	if !ctrl.HasConfiguredModel() {
		t.Fatal("HasConfiguredModel() = false for the PD-02 keyless local provider")
	}
}

// TestHasConfiguredModelFollowsProviderSettingsAcrossRebuild keeps the predicate
// live: removing the key flips it back to false (the setup card must reappear)
// without recreating the controller.
func TestHasConfiguredModelFollowsProviderSettings(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("ZEOKEEL_PROVIDER_BASE_URL", "")
	dataDir := t.TempDir()
	ctrl, cleanup, err := BuildServicesWith(dataDir, nil)
	if err != nil {
		t.Fatalf("BuildServicesWith: %v", err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		if cleanup != nil {
			_ = cleanup()
		}
	})
	a := &app{services: ctrl}
	a.installSessionAgentFactory(ctrl)
	ctrl.SetModelConfiguredFunc(a.hasConfiguredModel)
	_ = ctrl.Store().SetSetting("provider.api_key", "sk-test-key")
	if !ctrl.HasConfiguredModel() {
		t.Fatal("configured key not reflected")
	}
	if err := ctrl.Store().SetSetting("provider.api_key", ""); err != nil {
		t.Fatal(err)
	}
	if ctrl.HasConfiguredModel() {
		t.Fatal("removing the key left HasConfiguredModel true; the setup card would stay hidden")
	}
}
