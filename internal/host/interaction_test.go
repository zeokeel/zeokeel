package host

// The window-owned interaction services belong to whoever runs the browser
// transport (gateway.Provide for --product serve), never to the process host.

import (
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestProvideServicesSkipsInteraction pins that the process host provides only
// data dir/settings/log: the browser transport adds the interaction services.
func TestProvideServicesSkipsInteraction(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if err := provideServices(root, t.TempDir(), settings, NewLogger("")); err != nil {
		t.Fatalf("provideServices: %v", err)
	}
	if _, ok := root.Get(contracts.KeyUserInteraction); ok {
		t.Fatal("headless host provided KeyUserInteraction")
	}
	if _, ok := root.Get(contracts.KeyArtifactPresenter); ok {
		t.Fatal("headless host provided KeyArtifactPresenter")
	}
	if _, ok := root.Get(contracts.KeyPlanReviewer); ok {
		t.Fatal("headless host provided KeyPlanReviewer")
	}
	// The Goal v2 service is provided by the goal backend module during
	// assembly, never by the process host (which runs before any assembly).
	if _, ok := root.Get(contracts.KeyGoal); ok {
		t.Fatal("headless host provided KeyGoal")
	}
}
