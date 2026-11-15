package agent

// Tests for the compaction archive read-back (contracts.ContextArchiveService):
// the raw history a compaction replaced is readable by session + durable event
// sequence, bounded, truncated honestly, never part of the folded model view and
// never injected into a prompt.

import (
	"context"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// archiveSession compacts a scripted session and returns the service, its store,
// the session ID and the durable sequence of the one compact_archive event.
func archiveSession(t *testing.T, original []rig.Message) (*service, *fakeStore, string, int64) {
	t.Helper()
	compactor := &fakeCompactor{messages: compactTestMessages(2)}
	svc, store, sessionID := newCompactService(t, Config{ContextBudget: 100}, compactor, nil)
	if err := store.ReplaceHistory(context.Background(), sessionID, original); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	if _, err := svc.CompactNow(context.Background(), sessionID); err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	events := eventsOfType(t, store, sessionID, compactArchiveEventType)
	if len(events) != 1 {
		t.Fatalf("compact_archive events = %d, want 1", len(events))
	}
	return svc, store, sessionID, events[0].Seq
}

// TestContextArchiveReadsArchivedHistoryBySeq covers the read-back: the archived
// messages (roles, text, tool calls) come back addressed by the durable archive
// event's sequence, and the folded archive event carries that same sequence so
// the UI can address it after a restart.
func TestContextArchiveReadsArchivedHistoryBySeq(t *testing.T) {
	original := []rig.Message{
		rig.NewMessage(rig.RoleUser, "把项目跑起来"),
		{Role: rig.RoleAssistant, Content: []rig.ContentPart{rig.TextPart("我先看一下目录。")},
			ToolCalls: []rig.ToolCall{{ID: "c1", Name: "bash", Arguments: `{"command":"ls"}`}}},
		{Role: rig.RoleTool, ToolCallID: "c1", Name: "bash",
			Content: []rig.ContentPart{rig.TextPart("file-a\nfile-b")}},
		rig.NewMessage(rig.RoleAssistant, "目录里有两个文件。"),
	}
	svc, store, sessionID, seq := archiveSession(t, original)
	if seq <= 0 {
		t.Fatalf("archive event seq = %d, want the durable sequence", seq)
	}

	archive, ok := svc.ContextArchive(sessionID, seq)
	if !ok {
		t.Fatalf("ContextArchive(%q, %d) reported no archive", sessionID, seq)
	}
	if archive.Seq != seq || archive.Kind != "manual" {
		t.Fatalf("archive = %+v, want seq %d and kind manual", archive, seq)
	}
	if archive.RawMessageCount != len(original) || archive.RetainedMessages != len(original) ||
		archive.OmittedMessages != 0 || archive.Truncated {
		t.Fatalf("archive metadata = %+v, want the full original history", archive)
	}
	if archive.Bytes <= 0 {
		t.Fatalf("archive bytes = %d, want the serialized size", archive.Bytes)
	}
	if len(archive.Messages) != len(original) {
		t.Fatalf("archived messages = %d, want %d", len(archive.Messages), len(original))
	}
	if archive.Messages[0].Role != "user" || archive.Messages[0].Text != "把项目跑起来" {
		t.Fatalf("message 0 = %+v, want the user prompt", archive.Messages[0])
	}
	assistant := archive.Messages[1]
	if assistant.Role != "assistant" || !strings.Contains(assistant.Text, "我先看一下目录") {
		t.Fatalf("message 1 = %+v, want the assistant text", assistant)
	}
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].Name != "bash" ||
		!strings.Contains(assistant.ToolCalls[0].Arguments, "ls") {
		t.Fatalf("message 1 tool calls = %+v, want bash with its arguments", assistant.ToolCalls)
	}
	if archive.Messages[2].Role != "tool" || !strings.Contains(archive.Messages[2].Text, "file-a") {
		t.Fatalf("message 2 = %+v, want the tool result", archive.Messages[2])
	}

	// The folded archive event carries the durable sequence: the UI addresses the
	// read-back by it, live and after a restore replay.
	folded, ok := svc.SessionContext(sessionID)
	if !ok {
		t.Fatal("SessionContext reported no timeline")
	}
	var foldedSeq int64 = -1
	for _, event := range folded.Events {
		if event.Kind == contracts.ContextEventArchive {
			foldedSeq = event.Seq
		}
	}
	if foldedSeq != seq {
		t.Fatalf("folded archive event seq = %d, want the durable %d", foldedSeq, seq)
	}
	// The read is a copy: it never becomes part of the folded model view (the
	// folded event keeps metadata only).
	for _, event := range folded.Events {
		if event.Kind == contracts.ContextEventArchive && strings.Contains(event.Detail, "我先看一下目录") {
			t.Fatalf("folded archive event leaks the raw text: %q", event.Detail)
		}
	}
	// The store's history (what the next run sends) is the compacted history, not
	// the archived raw one: the archive is a read-only audit copy.
	history, err := store.History().Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history after compaction = %d messages, want the compacted 2", len(history))
	}
}

// TestContextArchiveUnknownSequence covers the honest miss: an unknown session,
// an unknown sequence, or a sequence that is not a compact_archive event.
func TestContextArchiveUnknownSequence(t *testing.T) {
	svc, store, sessionID, seq := archiveSession(t, compactTestMessages(3))
	if _, ok := svc.ContextArchive("missing-session", seq); ok {
		t.Fatal("ContextArchive reported an archive for an unknown session")
	}
	if _, ok := svc.ContextArchive(sessionID, seq+1000); ok {
		t.Fatal("ContextArchive reported an archive for an unknown sequence")
	}
	if _, ok := svc.ContextArchive(sessionID, 0); ok {
		t.Fatal("ContextArchive reported an archive for sequence 0")
	}
	if _, ok := svc.ContextArchive("", seq); ok {
		t.Fatal("ContextArchive reported an archive for an empty session")
	}
	// A compact event is not an archive event: its sequence must not resolve.
	compacts := eventsOfType(t, store, sessionID, compactEvent)
	if len(compacts) == 0 {
		t.Fatal("no compact event recorded")
	}
	if _, ok := svc.ContextArchive(sessionID, compacts[0].Seq); ok {
		t.Fatal("ContextArchive treated a compact event as an archive")
	}
}

// TestContextArchiveBoundsLongText covers the read bound: a huge archived text
// comes back as a bounded excerpt with the truncation marked, never silently
// shortened.
func TestContextArchiveBoundsLongText(t *testing.T) {
	long := strings.Repeat("x", contracts.ContextArchiveReadMaxTextBytes+1024)
	original := []rig.Message{
		rig.NewMessage(rig.RoleUser, long),
		{Role: rig.RoleAssistant, ToolCalls: []rig.ToolCall{{
			ID: "c1", Name: "bash", Arguments: strings.Repeat("y", contracts.ContextArchiveReadMaxArgumentsBytes+512),
		}}},
	}
	svc, _, sessionID, seq := archiveSession(t, original)
	archive, ok := svc.ContextArchive(sessionID, seq)
	if !ok {
		t.Fatalf("ContextArchive(%q, %d) reported no archive", sessionID, seq)
	}
	if len(archive.Messages) != len(original) {
		t.Fatalf("archived messages = %d, want %d", len(archive.Messages), len(original))
	}
	if got := archive.Messages[0].Text; len(got) > contracts.ContextArchiveReadMaxTextBytes {
		t.Fatalf("archived text length = %d, want the %d byte read bound", len(got), contracts.ContextArchiveReadMaxTextBytes)
	}
	if !archive.Messages[0].Truncated {
		t.Fatal("a text cut at the read bound is not marked truncated")
	}
	if got := archive.Messages[1].ToolCalls[0].Arguments; len(got) > contracts.ContextArchiveReadMaxArgumentsBytes {
		t.Fatalf("tool arguments length = %d, want the %d byte read bound", len(got), contracts.ContextArchiveReadMaxArgumentsBytes)
	}
	if !archive.Messages[1].Truncated {
		t.Fatal("arguments cut at the read bound are not marked truncated")
	}
}

// TestContextArchiveEventTruncationSurvivesRead covers the event-level bound:
// an archive that was truncated when written reports the gap in the read-back.
func TestContextArchiveEventTruncationSurvivesRead(t *testing.T) {
	original := compactTestMessages(compactArchiveMaxMessages + 5)
	svc, _, sessionID, seq := archiveSession(t, original)
	archive, ok := svc.ContextArchive(sessionID, seq)
	if !ok {
		t.Fatalf("ContextArchive(%q, %d) reported no archive", sessionID, seq)
	}
	if !archive.Truncated || archive.OmittedMessages != 5 {
		t.Fatalf("archive truncation = truncated:%v omitted:%d, want 5 omitted", archive.Truncated, archive.OmittedMessages)
	}
	if archive.RawMessageCount != compactArchiveMaxMessages+5 ||
		archive.RetainedMessages != compactArchiveMaxMessages ||
		len(archive.Messages) != compactArchiveMaxMessages {
		t.Fatalf("archive counts = raw:%d retained:%d messages:%d, want %d/%d/%d",
			archive.RawMessageCount, archive.RetainedMessages, len(archive.Messages),
			compactArchiveMaxMessages+5, compactArchiveMaxMessages, compactArchiveMaxMessages)
	}
}

// TestContextArchiveReadableAfterRestart covers the durable read-back across a
// restart: the restore replay folds the archive event's durable sequence, so the
// Context UI can still address and open the raw history after the process
// restarts (the case the live-only fold would lose).
func TestContextArchiveReadableAfterRestart(t *testing.T) {
	original := compactTestMessages(4)
	_, store, sessionID, seq := archiveSession(t, original)
	restarted := newService(Config{ContextBudget: 100}, rigtools.NewRegistry(),
		contracts.SettingsService(newFakeSettings(nil)), &fakeLogger{}, store, fakeApprovals{})
	restarted.restore(context.Background())

	folded, ok := restarted.SessionContext(sessionID)
	if !ok {
		t.Fatal("restored SessionContext reported no data")
	}
	var restoredSeq int64 = -1
	for _, event := range folded.Events {
		if event.Kind == contracts.ContextEventArchive {
			restoredSeq = event.Seq
		}
	}
	if restoredSeq != seq {
		t.Fatalf("restored archive event seq = %d, want the durable %d", restoredSeq, seq)
	}
	archive, ok := restarted.ContextArchive(sessionID, restoredSeq)
	if !ok {
		t.Fatalf("ContextArchive(%q, %d) after restart reported no archive", sessionID, restoredSeq)
	}
	if len(archive.Messages) != len(original) || archive.Messages[0].Text != original[0].Text() {
		t.Fatalf("restored archive = %+v, want the %d raw messages", archive, len(original))
	}
}
