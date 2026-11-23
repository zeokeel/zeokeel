package host

// The desktop notifier belonged to the window host; the browser transport
// provides its own (gateway.Provide). The process host provides neither.

import (
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestHostDoesNotProvideNotifier(t *testing.T) {
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	settings, err := NewSettings(t.TempDir())
	if err != nil {
		t.Fatalf("NewSettings: %v", err)
	}
	if err := provideServices(root, t.TempDir(), settings, NewLogger("")); err != nil {
		t.Fatalf("provideServices: %v", err)
	}
	if _, ok := root.Get(contracts.KeyNotifier); ok {
		t.Fatal("headless host provided KeyNotifier")
	}
}
