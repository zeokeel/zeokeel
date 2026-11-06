package shell

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

// mentionQuotedPaths are the paths a quoted @"..." token must round-trip: the
// quote and backslash characters are the ones the token grammar treats
// specially, and a plain spaced path must keep working.
var mentionQuotedPaths = []string{
	`a"b.txt`,
	`a\b.txt`,
	`a"b\c.txt`,
	`plain with space.txt`,
	`a'b.txt`,
}

// TestMentionQuotedPathEscapesAndReparses pins the token grammar: a picked
// quoted token must parse back to the exact workspace path, including file names
// that contain a double quote or a backslash. Without escaping, selecting
// a"b.txt inserted @"a"b.txt" and the next "@" trigger read the query as "a".
func TestMentionQuotedPathEscapesAndReparses(t *testing.T) {
	app := &App{Language: LangEN}
	for _, path := range mentionQuotedPaths {
		closed := app.mentionToken(path, true, true)
		trigger := activeComposerTrigger(closed, utf8.RuneCountInString(closed))
		if trigger.kind != composerTriggerMention || !trigger.quoted || !trigger.closed {
			t.Fatalf("token %q parsed to %+v, want a closed quoted mention", closed, trigger)
		}
		if trigger.query != path {
			t.Fatalf("token %q parsed query %q, want %q", closed, trigger.query, path)
		}

		// The open form typed while the path is still growing round-trips too.
		open := app.mentionToken(path, true, false)
		trigger = activeComposerTrigger(open, utf8.RuneCountInString(open))
		if trigger.kind != composerTriggerMention || !trigger.quoted || trigger.closed {
			t.Fatalf("open token %q parsed to %+v, want an open quoted mention", open, trigger)
		}
		if trigger.query != path {
			t.Fatalf("open token %q parsed query %q, want %q", open, trigger.query, path)
		}
	}
}

// TestMentionFileWithQuoteSelectsAndReparses drives the real @ menu over a file
// whose name contains a double quote: the row shows the escaped token, picking
// it inserts a token that parses back to the original path, and the insertion
// never shows an unescaped spelling that would break the next trigger.
func TestMentionFileWithQuoteSelectsAndReparses(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	writeTestFile(t, h.dir, `a"b.txt`, "hello")
	h.app.draft = `@"a`
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()

	wantToken := `@"a\"b.txt"`
	if !tt.HasText(wantToken) {
		t.Fatalf("mention menu missing the escaped row %q: %v", wantToken, tt.Texts())
	}
	if tt.HasText(`@"a"b.txt"`) {
		t.Fatalf("mention menu shows the unescaped (unparseable) token: %v", tt.Texts())
	}
	if err := tt.Click(wantToken); err != nil {
		t.Fatalf("click escaped mention row: %v", err)
	}
	if got, want := h.app.draft, wantToken+" "; got != want {
		t.Fatalf("inserted draft = %q, want %q", got, want)
	}

	// The inserted token still resolves to the original workspace path.
	trigger := activeComposerTrigger(strings.TrimSuffix(h.app.draft, " "),
		draftRuneLen(strings.TrimSuffix(h.app.draft, " ")))
	if trigger.kind != composerTriggerMention || trigger.query != `a"b.txt` {
		t.Fatalf("inserted token parsed to %+v, want the original path", trigger)
	}
}
