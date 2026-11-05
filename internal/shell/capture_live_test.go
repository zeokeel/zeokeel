package shell

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/pixeltest"
	"github.com/zeokeel/zeokeel/internal/state"
)

// TestCaptureLiveSession renders the live session screen with one finished
// turn so the real UI (not the static fallback) can be compared with DSH.
func TestCaptureLiveSession(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	id, err := h.ctrl.CreateSession(ctx, "纯 Go MyGo+Eino AI agent 规划")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := h.ctrl.OpenSession(ctx, id); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if err := h.ctrl.Submit(ctx, id, "选择 a，不指定字体，用系统的，单独启动一个实例，然后探测，要整全了"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snap := h.ctrl.Snapshot()
		if snap != nil && snap.Phase == state.PhaseIdle {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)
	dir := "../../artifacts/live"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, screen := range map[string]Screen{"session": Session, "home": Home} {
		app := &App{Ctrl: h.ctrl, Screen: screen}
		img := ui.Render(app.View, 1600, 1000, 1)
		if img == nil {
			t.Fatalf("%s: nil render", name)
		}
		if err := pixeltest.SavePNG(filepath.Join(dir, name+".png"), img); err != nil {
			t.Fatal(err)
		}
	}
}
