package shell

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestExportFileContent drives the session-header "Download session log"
// action through the save-dialog seam and checks the JSONL that lands on disk:
// a session record, message records and event records.
func TestExportFileContent(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	id := h.ctrl.CurrentSessionID()
	if id == "" {
		t.Fatal("no current session")
	}
	if err := h.ctrl.Submit(ctx, id, "export file"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdle(t, h.ctrl)
	h.ctrl.Wait()

	path := filepath.Join(t.TempDir(), "session-log.jsonl")
	var suggested string
	h.app.SaveSessionLog = func(defaultPath string) (string, error) {
		suggested = defaultPath
		return path, nil
	}

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("More actions"); err != nil {
		t.Fatalf("click More actions: %v", err)
	}
	if err := tt.Click("Download session log"); err != nil {
		t.Fatalf("click Download session log: %v", err)
	}
	if !tt.HasText("Session log saved to") {
		t.Fatalf("download did not report the saved path: %v", tt.Texts())
	}
	if !strings.HasSuffix(suggested, ".jsonl") || !strings.Contains(suggested, id) {
		t.Fatalf("suggested save path = %q, want a .jsonl name containing %q", suggested, id)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Fatal("export does not end with a newline")
	}
	counts := make(map[string]int)
	var contents []string
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d is not JSON: %v (%q)", i+1, err, line)
		}
		kind, _ := rec["type"].(string)
		counts[kind]++
		if content, ok := rec["content"].(string); ok {
			contents = append(contents, content)
		}
	}
	if counts["session"] != 1 || counts["message"] < 2 || counts["event"] < 1 {
		t.Fatalf("export record counts = %+v, want 1 session and messages/events", counts)
	}
	joined := strings.Join(contents, "\n")
	if !strings.Contains(joined, "export file") || !strings.Contains(joined, "done") {
		t.Fatalf("exported message contents = %q, want the session transcript", contents)
	}
}

// TestForkSessionMenuOpensFork drives the Session header's More actions entry:
// forking creates the copy and selects it in the live UI.
func TestForkSessionMenuOpensFork(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	ctx := context.Background()
	srcID := h.ctrl.CurrentSessionID()
	if srcID == "" {
		t.Fatal("no current session")
	}
	if err := h.ctrl.Submit(ctx, srcID, "fork this"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitIdle(t, h.ctrl)
	h.ctrl.Wait()
	srcMessages := len(h.ctrl.Snapshot().Messages)

	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Frame()
	if err := tt.Click("More actions"); err != nil {
		t.Fatalf("click More actions: %v", err)
	}
	if !tt.HasText("Fork session") {
		t.Fatalf("More actions missing Fork session: %v", tt.Texts())
	}
	if err := tt.Click("Fork session"); err != nil {
		t.Fatalf("click Fork session: %v", err)
	}

	forkID := h.ctrl.CurrentSessionID()
	if forkID == "" || forkID == srcID {
		t.Fatalf("current session after fork = %q, want a fresh session", forkID)
	}
	sess, err := h.ctrl.CurrentSession()
	if err != nil {
		t.Fatalf("CurrentSession: %v", err)
	}
	if !strings.HasSuffix(sess.Title, " (fork)") {
		t.Fatalf("fork title = %q, want the source title with (fork)", sess.Title)
	}
	snap := h.app.Snapshot()
	if snap == nil || snap.SessionID != forkID {
		t.Fatalf("app snapshot = %+v, want fork %q", snap, forkID)
	}
	if len(snap.Messages) != srcMessages {
		t.Fatalf("fork snapshot messages = %d, want %d", len(snap.Messages), srcMessages)
	}
	if !tt.HasText(sess.Title) {
		t.Fatalf("header does not show the fork title %q: %v", sess.Title, tt.Texts())
	}
}
