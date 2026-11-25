package inbox_test

// TDD for durability: the inbox is the product's memory of "someone asked for
// this". A restart must not silently drop it, dedupe must still work across a
// restart (otherwise a restart turns one request into two), and a corrupt file
// must fail loudly instead of starting empty.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
)

func openAt(t *testing.T, dir string) *inbox.Service {
	t.Helper()
	svc, err := inbox.Open(dir, inbox.Config{Now: time.Now, MaxPerMinute: 1000})
	if err != nil {
		t.Fatalf("Open(%s): %v", dir, err)
	}
	return svc
}

func TestOpenEmptyDir(t *testing.T) {
	dir := t.TempDir()
	svc := openAt(t, dir)
	items, err := svc.List(context.Background(), contracts.InboxFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("items = %d, want 0", len(items))
	}
}

func TestPersistedItemsSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	first := openAt(t, dir)
	stored, _, err := first.Add(context.Background(), contracts.InboxItem{
		Source: "wechat", Channel: "dm", Sender: "owner", DedupKey: "k1",
		Title: "查退款率", Body: "上周", Priority: 4,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := first.Triage(context.Background(), []string{stored.ID}, contracts.InboxActionTask, []string{"task-1"}); err != nil {
		t.Fatalf("Triage: %v", err)
	}

	second := openAt(t, dir)
	restored, err := second.Get(context.Background(), stored.ID)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if restored.Title != "查退款率" || restored.Priority != 4 {
		t.Fatalf("restored = %+v, want the stored fields", restored)
	}
	if restored.Status != contracts.InboxStatusTask || len(restored.TaskIDs) != 1 || restored.TaskIDs[0] != "task-1" {
		t.Fatalf("restored status/taskIDs = %q/%v, want task/[task-1]", restored.Status, restored.TaskIDs)
	}
	if restored.Count != 1 || restored.ReceivedAt.IsZero() {
		t.Fatalf("restored count/timestamps = %d/%v", restored.Count, restored.ReceivedAt)
	}
}

func TestDedupeStillWorksAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	first := openAt(t, dir)
	if _, _, err := first.Add(context.Background(), contracts.InboxItem{
		Source: "wechat", DedupKey: "same", Title: "a",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	second := openAt(t, dir)
	stored, merged, err := second.Add(context.Background(), contracts.InboxItem{
		Source: "wechat", DedupKey: "same", Title: "b",
	})
	if err != nil {
		t.Fatalf("Add after reopen: %v", err)
	}
	if !merged {
		t.Fatal("dedupe did not survive the restart (would double the work)")
	}
	if stored.Count != 2 {
		t.Fatalf("count = %d after restart merge, want 2", stored.Count)
	}
}

func TestTriageStatusSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	first := openAt(t, dir)
	stored, _, _ := first.Add(context.Background(), contracts.InboxItem{
		Source: "email", DedupKey: "m1", Title: "mail",
	})
	if _, err := first.Triage(context.Background(), []string{stored.ID}, contracts.InboxActionIgnored, nil); err != nil {
		t.Fatalf("Triage: %v", err)
	}

	second := openAt(t, dir)
	restored, err := second.Get(context.Background(), stored.ID)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if restored.Status != contracts.InboxStatusIgnored {
		t.Fatalf("status = %q after reopen, want ignored", restored.Status)
	}
}

func TestCorruptFileFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "inbox.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	if _, err := inbox.Open(dir, inbox.Config{Now: time.Now}); !errors.Is(err, inbox.ErrCorruptStore) {
		t.Fatalf("Open err = %v, want ErrCorruptStore", err)
	}
}

func TestPersistLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	svc := openAt(t, dir)
	if _, _, err := svc.Add(context.Background(), contracts.InboxItem{Source: "wechat", DedupKey: "k"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".tmp" {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
}
