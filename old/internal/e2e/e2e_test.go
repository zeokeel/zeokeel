//go:build e2e

// Package e2e holds the opt-in real-model end-to-end tests. They are build
// tagged so the ordinary `go test ./...` never touches the network; run them
// through scripts/e2e-model.sh, which loads the API key without echoing it.
package e2e

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/provider/openai"
	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// TestRealModel runs one short turn through the real provider, Controller and
// Agent over a temporary store. It verifies the streamed assistant text and
// the provider-reported usage, and skips when no API key is configured.
func TestRealModel(t *testing.T) {
	apiKey := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	if apiKey == "" {
		t.Skip("DEEPSEEK_API_KEY is not set; skipping the real-model E2E")
	}
	baseURL := strings.TrimSpace(os.Getenv("ZEOKEEL_PROVIDER_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("ZEOKEEL_PROVIDER_MODEL"))

	provider, err := openai.New(openai.Config{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
	})
	if err != nil {
		t.Fatalf("openai.New: %v", err)
	}

	agent, err := runtime.New(runtime.Config{
		Name:          "zeokeel-e2e",
		Instruction:   "You are a terse test assistant. Reply with one short sentence.",
		ModelName:     model,
		Provider:      provider,
		MaxIterations: 4,
	})
	if err != nil {
		t.Fatalf("runtime.New: %v", err)
	}

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	broker := state.NewBroker()
	projector := state.NewProjector(context.Background(), broker)
	t.Cleanup(func() {
		projector.Close()
		broker.Close()
	})
	ctrl, err := session.New(session.Config{
		Store:     st,
		Broker:    broker,
		Projector: projector,
		Agent:     agent,
	})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	t.Cleanup(func() { _ = ctrl.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	id, err := ctrl.CreateSession(ctx, "real-model-e2e")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := ctrl.Submit(ctx, id, "Reply with one short sentence about the sky."); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	ctrl.Wait()

	snap := ctrl.Snapshot()
	if snap.Phase == state.PhaseFailed {
		msg := "unknown error"
		if snap.LastError != nil && snap.LastError.Text != "" {
			msg = snap.LastError.Text
		}
		t.Fatalf("real-model turn failed: %s", msg)
	}

	text := assistantText(snap)
	if strings.TrimSpace(text) == "" {
		t.Fatalf("assistant text is empty; snapshot: %+v", snap)
	}
	usage := snap.Usage
	if usage.In <= 0 || usage.Out <= 0 {
		t.Fatalf("usage was not reported: %+v", usage)
	}
	t.Logf("real-model turn: assistant text length=%d usage in=%d out=%d cache_hit=%d phase=%s",
		len(text), usage.In, usage.Out, usage.CacheHit, snap.Phase)
}

// assistantText concatenates the assistant messages of a snapshot.
func assistantText(snap *state.Snapshot) string {
	if snap == nil {
		return ""
	}
	var b strings.Builder
	for _, msg := range snap.Messages {
		if msg.Role == state.RoleAssistant {
			b.WriteString(msg.Text)
		}
	}
	return b.String()
}
