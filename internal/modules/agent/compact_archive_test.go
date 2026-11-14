package agent

// Tests for the fail-closed compact archive: the raw history a compaction
// replaces is preserved as a bounded durable event before the replacement.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// decodeArchiveEvents decodes the compact_archive events of a session.
func decodeArchiveEvents(t *testing.T, store *fakeStore, sessionID string) []compactArchivePayload {
	t.Helper()
	events := eventsOfType(t, store, sessionID, compactArchiveEventType)
	out := make([]compactArchivePayload, 0, len(events))
	for _, event := range events {
		var payload compactArchivePayload
		decodeEvent(t, event, &payload)
		out = append(out, payload)
	}
	return out
}

// TestCompactNowArchivesReplacedHistory covers the happy path: the archive is
// written before the replacement, carries the raw messages, and the context
// inspector exposes only the metadata.
func TestCompactNowArchivesReplacedHistory(t *testing.T) {
	compactor := &fakeCompactor{messages: compactTestMessages(2)}
	svc, store, sessionID := newCompactService(t, Config{ContextBudget: 100}, compactor, nil)
	original := compactTestMessages(4)
	if err := store.ReplaceHistory(context.Background(), sessionID, original); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	result, err := svc.CompactNow(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	archives := decodeArchiveEvents(t, store, sessionID)
	if len(archives) != 1 {
		t.Fatalf("compact_archive events = %d, want 1", len(archives))
	}
	archive := archives[0]
	if archive.Kind != "manual" || archive.MessageCount != 4 || archive.RetainedMessages != 4 ||
		archive.Truncated || archive.OmittedMessages != 0 || archive.Bytes == 0 {
		t.Fatalf("archive metadata = %+v, want the full original history", archive)
	}
	if archive.BeforeTokens != result.BeforeTokens || archive.AfterTokens != result.AfterTokens ||
		archive.Replaced != 4 || archive.Saved != 2 {
		t.Fatalf("archive audit = %+v, want the CompactResult %+v", archive, result)
	}
	decoded := make([]rig.Message, 0, len(archive.Messages))
	for _, raw := range archive.Messages {
		var message rig.Message
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatalf("decode archived message: %v", err)
		}
		decoded = append(decoded, message)
	}
	if len(decoded) != 4 {
		t.Fatalf("archived messages = %d, want 4", len(decoded))
	}
	for index := range original {
		if decoded[index].Text() != original[index].Text() || decoded[index].Role != original[index].Role {
			t.Fatalf("archived message %d = %+v, want %+v", index, decoded[index], original[index])
		}
	}
	// The archive event precedes the compaction event in the durable log.
	events, err := store.Events(context.Background(), sessionID, 0, 0)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	archiveSeq, compactSeq := int64(-1), int64(-1)
	for _, event := range events {
		switch event.Type {
		case compactArchiveEventType:
			archiveSeq = event.Seq
		case compactEvent:
			compactSeq = event.Seq
		}
	}
	if archiveSeq < 0 || compactSeq < 0 || archiveSeq > compactSeq {
		t.Fatalf("event order archive=%d compact=%d, want archive before compact", archiveSeq, compactSeq)
	}
	// The replacement still happened (archive is an audit copy, not a veto).
	history, err := store.History().Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("history after compact = %d messages, want the compacted 2", len(history))
	}

	// The inspector exposes archive metadata only (no message text).
	folded, ok := svc.SessionContext(sessionID)
	if !ok {
		t.Fatal("SessionContext reported no timeline")
	}
	found := false
	for _, event := range folded.Events {
		if event.Kind != contracts.ContextEventArchive {
			continue
		}
		found = true
		if event.ArchivedMessages != 4 || event.ArchivedBytes != archive.Bytes || event.Truncated ||
			event.Replaced != 4 || event.Saved != 4 || !strings.Contains(event.Detail, "manual archive") {
			t.Fatalf("inspector archive event = %+v, want the archive metadata", event)
		}
		if strings.Contains(event.Detail, "message-0") {
			t.Fatalf("inspector archive detail leaks message text: %q", event.Detail)
		}
	}
	if !found {
		t.Fatalf("inspector events = %+v, want the archive event", folded.Events)
	}
}

// archiveFailStore fails only the compact_archive event append.
type archiveFailStore struct {
	*fakeStore
	err error
}

func (s *archiveFailStore) AppendEvent(ctx context.Context, event rig.SessionEvent) error {
	if event.Type == compactArchiveEventType {
		if s.err != nil {
			return s.err
		}
		return errors.New("disk full")
	}
	return s.fakeStore.AppendEvent(ctx, event)
}

// TestCompactNowArchiveFailureKeepsHistory covers fail-closed: when the archive
// cannot be written the history is not replaced and the command fails.
func TestCompactNowArchiveFailureKeepsHistory(t *testing.T) {
	store := &archiveFailStore{fakeStore: newFakeStore()}
	settings := newFakeSettings(nil)
	svc := newService(Config{ContextBudget: 100}, rigtools.NewRegistry(),
		contracts.SettingsService(settings), &fakeLogger{}, store, fakeApprovals{})
	compactor := &fakeCompactor{messages: compactTestMessages(2)}
	svc.compactorFactory = func(contracts.ChatConfig) (rig.Compactor, error) { return compactor, nil }
	sessionID, err := svc.NewSession("")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	original := compactTestMessages(4)
	if err := store.ReplaceHistory(context.Background(), sessionID, original); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	if _, err := svc.CompactNow(context.Background(), sessionID); !errors.Is(err, contracts.ErrCompactArchive) {
		t.Fatalf("CompactNow with a failing archive = %v, want ErrCompactArchive", err)
	}
	history, err := store.History().Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(history) != len(original) {
		t.Fatalf("history after failed archive = %d messages, want the untouched %d", len(history), len(original))
	}
	for index := range original {
		if history[index].Text() != original[index].Text() {
			t.Fatalf("history message %d changed despite the failed archive", index)
		}
	}
	if events := eventsOfType(t, store.fakeStore, sessionID, compactEvent); len(events) != 0 {
		t.Fatalf("compact events after a failed archive = %d, want 0", len(events))
	}
}

// TestCompactArchiveTruncation covers the per-event bounds: the omitted
// messages are reported as metadata instead of silently disappearing.
func TestCompactArchiveTruncation(t *testing.T) {
	t.Run("message count bound", func(t *testing.T) {
		compactor := &fakeCompactor{messages: compactTestMessages(2)}
		svc, store, sessionID := newCompactService(t, Config{ContextBudget: 100}, compactor, nil)
		messages := make([]rig.Message, 0, compactArchiveMaxMessages+10)
		for index := 0; index < compactArchiveMaxMessages+10; index++ {
			messages = append(messages, rig.NewMessage(rig.RoleUser, fmt.Sprintf("m-%d", index)))
		}
		if err := store.ReplaceHistory(context.Background(), sessionID, messages); err != nil {
			t.Fatalf("ReplaceHistory: %v", err)
		}
		if _, err := svc.CompactNow(context.Background(), sessionID); err != nil {
			t.Fatalf("CompactNow: %v", err)
		}
		archives := decodeArchiveEvents(t, store, sessionID)
		if len(archives) != 1 {
			t.Fatalf("archives = %d, want 1", len(archives))
		}
		if !archives[0].Truncated || archives[0].RetainedMessages != compactArchiveMaxMessages ||
			archives[0].OmittedMessages != 10 || archives[0].MessageCount != compactArchiveMaxMessages+10 {
			t.Fatalf("archive = %+v, want the count bound with 10 omitted", archives[0])
		}
	})

	t.Run("byte bound", func(t *testing.T) {
		compactor := &fakeCompactor{messages: compactTestMessages(1)}
		svc, store, sessionID := newCompactService(t, Config{ContextBudget: 100}, compactor, nil)
		big := strings.Repeat("z", compactArchiveMaxBytes*3/4)
		messages := []rig.Message{
			rig.NewMessage(rig.RoleUser, big),
			rig.NewMessage(rig.RoleAssistant, big),
			rig.NewMessage(rig.RoleUser, big),
		}
		if err := store.ReplaceHistory(context.Background(), sessionID, messages); err != nil {
			t.Fatalf("ReplaceHistory: %v", err)
		}
		if _, err := svc.CompactNow(context.Background(), sessionID); err != nil {
			t.Fatalf("CompactNow: %v", err)
		}
		archives := decodeArchiveEvents(t, store, sessionID)
		if len(archives) != 1 {
			t.Fatalf("archives = %d, want 1", len(archives))
		}
		archive := archives[0]
		if !archive.Truncated || archive.Bytes > compactArchiveMaxBytes || archive.RetainedMessages >= len(messages) {
			t.Fatalf("archive = %+v, want the byte bound applied with truncation", archive)
		}
		if archive.OmittedMessages != len(messages)-archive.RetainedMessages {
			t.Fatalf("omitted = %d, want %d", archive.OmittedMessages, len(messages)-archive.RetainedMessages)
		}
	})
}

// TestCompactArchiveSurvivesRestart covers the durable audit after a restart and
// the restored context-inspector metadata.
func TestCompactArchiveSurvivesRestart(t *testing.T) {
	compactor := &fakeCompactor{messages: compactTestMessages(2)}
	svc, store, sessionID := newCompactService(t, Config{ContextBudget: 100}, compactor, nil)
	if err := store.ReplaceHistory(context.Background(), sessionID, compactTestMessages(4)); err != nil {
		t.Fatalf("ReplaceHistory: %v", err)
	}
	if _, err := svc.CompactNow(context.Background(), sessionID); err != nil {
		t.Fatalf("CompactNow: %v", err)
	}

	restarted := newService(Config{ContextBudget: 100}, rigtools.NewRegistry(),
		contracts.SettingsService(newFakeSettings(nil)), &fakeLogger{}, store, fakeApprovals{})
	restarted.restore(context.Background())
	if archives := decodeArchiveEvents(t, store, sessionID); len(archives) != 1 {
		t.Fatalf("durable archives after restart = %d, want 1", len(archives))
	}
	folded, ok := restarted.SessionContext(sessionID)
	if !ok {
		t.Fatal("restored SessionContext reported no data")
	}
	found := false
	for _, event := range folded.Events {
		if event.Kind == contracts.ContextEventArchive {
			found = true
			if event.ArchivedMessages != 4 || event.ArchivedBytes == 0 || event.Truncated {
				t.Fatalf("restored archive event = %+v, want the archive metadata", event)
			}
		}
	}
	if !found {
		t.Fatalf("restored inspector events = %+v, want the archive event", folded.Events)
	}
}
