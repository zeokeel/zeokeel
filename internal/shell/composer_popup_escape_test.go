package shell

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/state"
)

// TestEscapeDismissesComposerPopupsBeforeStoppingTurn covers the shortcut
// catalog's "Close overlay or menu" order: with a running turn and a visible
// "@" mention list or "/" command palette, Esc must close the menu and leave
// the turn running. Only the next Esc stops the turn.
func TestEscapeDismissesComposerPopupsBeforeStoppingTurn(t *testing.T) {
	cases := []struct {
		name  string
		draft string
		menu  string // accessible label of the visible popup
		// edit makes the derived popup visible again after the dismissal.
		edit string
	}{
		{"mention", "@", "Mention suggestions", "@l"},
		{"slash", "/", "Commands", "/c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newLiveApp(t, &shellProvider{hang: true})
			h.app.Language = LangEN
			h.app.Screen = Session
			id := h.ctrl.CurrentSessionID()
			if err := h.ctrl.Submit(context.Background(), id, "hold the turn"); err != nil {
				t.Fatalf("Submit: %v", err)
			}
			waitRunning(t, h.ctrl)
			h.app.SetSnapshot(h.ctrl.Snapshot())
			h.app.draft = tc.draft
			tt := ui.NewTester(h.app.View, 1600, 1000)
			tt.Frame()
			if _, ok := tt.Find(tc.menu); !ok {
				t.Fatalf("%q menu missing before Esc: %v", tc.menu, tt.Texts())
			}

			tt.Key(0, ui.KeyEscape)
			tt.Frame()
			if _, ok := tt.Find(tc.menu); ok {
				t.Fatalf("Esc did not close the %q menu: %v", tc.menu, tt.Texts())
			}
			if got := h.app.draft; got != tc.draft {
				t.Fatalf("Esc changed the draft to %q, want %q", got, tc.draft)
			}
			if phase := h.ctrl.Snapshot().Phase; phase != state.PhaseRunning {
				t.Fatalf("Esc stopped the running turn: phase = %v", phase)
			}

			// Editing the draft resets the dismissal: the derived menu shows
			// again and belongs to the new text.
			h.app.draft = tc.edit
			tt.Frame()
			if _, ok := tt.Find(tc.menu); !ok {
				t.Fatalf("%q menu did not return after a draft edit: %v", tc.menu, tt.Texts())
			}
			// Typing back to exactly the dismissed text is still an edit, so
			// the menu must stay visible.
			h.app.draft = tc.draft
			tt.Frame()
			if _, ok := tt.Find(tc.menu); !ok {
				t.Fatalf("%q menu hidden after returning to the dismissed text", tc.menu)
			}
		})
	}
}
