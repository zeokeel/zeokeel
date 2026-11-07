package host

// Tests (written before the implementation, TDD) for the desktop notifier
// wiring: the desktop host provides KeyNotifier, the headless host does not.

import (
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

func TestDesktopHostProvidesNotifier(t *testing.T) {
	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if err := provideServices(root, t.TempDir(), settings, NewLogger(""), uiRoot); err != nil {
		t.Fatalf("provideServices: %v", err)
	}
	notifier, ok := root.Get(contracts.KeyNotifier)
	if !ok || notifier == nil {
		t.Fatal("desktop host did not provide KeyNotifier")
	}
	if got := notifier.Notify(contracts.NotifyInfo, "hello"); got.ID == "" {
		t.Fatalf("desktop notifier Notify = %+v, want an id", got)
	}
}

func TestHeadlessHostDoesNotProvideNotifier(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if err := provideServices(root, t.TempDir(), settings, NewLogger(""), nil); err != nil {
		t.Fatalf("provideServices: %v", err)
	}
	if _, ok := root.Get(contracts.KeyNotifier); ok {
		t.Fatal("headless host provided KeyNotifier")
	}
}
