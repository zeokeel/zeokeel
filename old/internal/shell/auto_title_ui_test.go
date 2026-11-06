package shell

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

// waitShellTitle waits for a session row to carry want. Automatic titles are
// written asynchronously after the turn reaches idle.
func waitShellTitle(t *testing.T, st *store.Store, id, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		sess, err := st.GetSession(id)
		if err == nil && sess.Title == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	sess, err := st.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession(%q): %v", id, err)
	}
	t.Fatalf("session title = %q, want %q", sess.Title, want)
}

// TestAutoTitleShownInUI covers the live shell half of the feature: after the
// first completed turn writes an automatic title, the updated title is pushed
// to OnUpdate and the next frame renders it in both the session header and the
// sidebar row.
func TestAutoTitleShownInUI(t *testing.T) {
	// A placeholder title: a deliberately named session is never renamed by
	// the automatic titler.
	h := newLiveAppWithTitle(t, &shellProvider{}, "New Session")
	h.app.Screen = Session
	h.app.Language = LangEN

	const wantTitle = "Deploy Pipeline Fix"
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	h.ctrl.SetTitleGenerator(session.TitleFunc(func(ctx context.Context, _, _, _ string) (string, error) {
		startOnce.Do(func() { close(started) })
		select {
		case <-release:
			return wantTitle, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}))

	ctx := context.Background()
	if err := h.ctrl.Submit(ctx, h.ctrl.CurrentSessionID(), "why does the deploy pipeline keep failing?"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	m6WaitIdle(t, h.ctrl)
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("title generator never ran after the first completed turn")
	}

	// Install a listener only now: the title write must itself notify the UI,
	// which is what makes the header/sidebar change without a user action.
	var updates atomic.Int64
	h.ctrl.OnUpdate(func(*state.Snapshot) { updates.Add(1) })
	before := updates.Load()
	close(release)

	waitShellTitle(t, h.store, h.ctrl.CurrentSessionID(), wantTitle)
	deadline := time.Now().Add(10 * time.Second)
	for updates.Load() == before && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if updates.Load() == before {
		t.Fatal("automatic title update did not notify OnUpdate listeners")
	}

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if !tt.HasText(wantTitle) {
		t.Fatalf("session header missing the automatic title %q: %q", wantTitle, tt.Texts())
	}
	if !tt.HasText("Session actions for " + wantTitle) {
		t.Fatalf("sidebar still shows the old session title; want an action row for %q: %q", wantTitle, tt.Texts())
	}
	if tt.HasText("Session actions for New Session") {
		t.Fatalf("sidebar kept the placeholder title after the automatic rename: %q", tt.Texts())
	}
}
