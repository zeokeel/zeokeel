package host

// Desktop host provides the interaction bridge; the headless product must not.

import (
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

func TestProvideServicesDesktopProvidesInteraction(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	uiRoot := uiroot.New(uiroot.Config{AppName: appName})
	if err := provideServices(root, t.TempDir(), settings, NewLogger(""), uiRoot); err != nil {
		t.Fatalf("provideServices: %v", err)
	}
	interaction, ok := root.Get(contracts.KeyUserInteraction)
	if !ok {
		t.Fatal("desktop host did not provide KeyUserInteraction")
	}
	if interaction == nil {
		t.Fatal("desktop host provided a nil UserInteraction")
	}
	if presenter, ok := root.Get(contracts.KeyArtifactPresenter); !ok || presenter == nil {
		t.Fatal("desktop host did not provide KeyArtifactPresenter")
	}
	if reviewer, ok := root.Get(contracts.KeyPlanReviewer); !ok || reviewer == nil {
		t.Fatal("desktop host did not provide KeyPlanReviewer")
	}
}

func TestProvideServicesHeadlessSkipsInteraction(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if err := provideServices(root, t.TempDir(), settings, NewLogger(""), nil); err != nil {
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
	if _, ok := root.Get(contracts.KeyGoalTracker); ok {
		t.Fatal("headless host provided KeyGoalTracker")
	}
}

func TestProvideServicesDesktopProvidesGoalTracker(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	uiRoot := uiroot.New(uiroot.Config{AppName: appName})
	if err := provideServices(root, t.TempDir(), settings, NewLogger(""), uiRoot); err != nil {
		t.Fatalf("provideServices: %v", err)
	}
	tracker, ok := root.Get(contracts.KeyGoalTracker)
	if !ok || tracker == nil {
		t.Fatal("desktop host did not provide KeyGoalTracker")
	}
}
