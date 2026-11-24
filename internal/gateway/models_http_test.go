package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The composer's model and thinking pickers need the catalog and each model's
// effective thinking modes. Those rules live in contracts (family table, user modes);
// the client must not re-derive them, so the gateway ships the resolved view.

func TestModelsServesCatalogWithThinkingModes(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	root := testRoot(t, chat)
	settings := newFakeSettings(map[string]string{
		contracts.SettingModelCatalogModels: `[{"id":"deepseek-v4","name":"DeepSeek V4","note":"主干"},` +
			`{"id":"qwen3","name":"Qwen 3","contextWindow":131072}]`,
		contracts.SettingsChatModel: "deepseek-v4",
	})
	if _, err := root.Provide(contracts.KeySettings, contracts.SettingsService(settings)); err != nil {
		t.Fatalf("provide settings: %v", err)
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	gw := serveGateway(t, gateway)
	defer gw.stop(t)

	status, body := filesGet(t, gw, "/api/models", true)
	if status != http.StatusOK {
		t.Fatalf("GET /api/models = %d %q", status, body)
	}
	var payload modelsDTO
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode: %v (%q)", err, body)
	}
	if payload.DefaultModel != "deepseek-v4" {
		t.Fatalf("defaultModel = %q", payload.DefaultModel)
	}
	if len(payload.Models) != 2 {
		t.Fatalf("models = %+v", payload.Models)
	}
	deepseek := payload.Models[0]
	if deepseek.ID != "deepseek-v4" || deepseek.Name != "DeepSeek V4" {
		t.Fatalf("row = %+v", deepseek)
	}
	// A deepseek-family model offers reasoning_effort modes; the client shows the
	// bilingual label without knowing the family rules.
	ids := make([]string, 0, len(deepseek.ThinkingModes))
	for _, mode := range deepseek.ThinkingModes {
		ids = append(ids, mode.ID)
		if mode.NameZH == "" || mode.NameEN == "" {
			t.Fatalf("mode %q lacks a label: %+v", mode.ID, mode)
		}
	}
	for _, want := range []string{contracts.ThinkingModeReasoningHigh, contracts.ThinkingModeDefault} {
		found := false
		for _, id := range ids {
			if id == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("deepseek modes = %v, want %q", ids, want)
		}
	}
	if payload.Models[1].ContextWindow != 131072 {
		t.Fatalf("contextWindow = %d", payload.Models[1].ContextWindow)
	}
}

func TestModelsWithoutSettingsIsEmpty(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)

	status, body := filesGet(t, gw, "/api/models", true)
	if status != http.StatusOK {
		t.Fatalf("GET = %d %q", status, body)
	}
	var payload modelsDTO
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode: %v (%q)", err, body)
	}
	if payload.Models == nil {
		t.Fatalf("empty answer must carry an array: %q", body)
	}
}

func TestModelsNeedsTheToken(t *testing.T) {
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	gw := startTestGateway(t, chat)
	defer gw.stop(t)
	if status, _ := filesGet(t, gw, "/api/models", false); status != http.StatusUnauthorized {
		t.Fatalf("without a token = %d, want 401", status)
	}
}
