package memory

// Tests for the deterministic memory consolidation (caps/retention): expiry,
// the exact cutoff boundary, the over-cap newest-first trim, their combination,
// unlimited policy, empty stores and Remove failures.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// newConsolidationStore builds a store over a temp file with the given entries.
func newConsolidationStore(t *testing.T) *Store {
	t.Helper()
	store, err := newStore(t.TempDir(), Config{})
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	return store
}

// addEntries adds memories with explicit creation times (insertion order kept).
func addEntries(t *testing.T, store *Store, entries ...contracts.Memory) {
	t.Helper()
	for _, entry := range entries {
		if _, err := store.Add(context.Background(), entry); err != nil {
			t.Fatalf("Add(%+v): %v", entry, err)
		}
	}
}

func memoryAt(id, text string, created time.Time) contracts.Memory {
	return contracts.Memory{ID: id, Text: text, Scope: "global", Created: created}
}

func TestConsolidateExpiredAndBoundary(t *testing.T) {
	store := newConsolidationStore(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	addEntries(t, store,
		memoryAt("old", "too old", now.Add(-2*time.Hour)),
		memoryAt("boundary", "exactly at cutoff", now.Add(-time.Hour)),
		memoryAt("fresh", "fresh", now.Add(-30*time.Minute)),
	)
	report, err := store.consolidate(context.Background(), contracts.MemoryPolicy{MaxAge: time.Hour}, now)
	if err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	if report.RemovedExpired != 1 || report.RemovedOverCap != 0 || report.Remaining != 2 {
		t.Fatalf("report = %+v, want expired 1 over-cap 0 remaining 2", report)
	}
	entries, _ := store.List(context.Background())
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want boundary+fresh", entries)
	}
	ids := map[string]bool{}
	for _, entry := range entries {
		ids[entry.ID] = true
	}
	if ids["old"] || !ids["boundary"] || !ids["fresh"] {
		t.Fatalf("remaining ids = %v, want boundary+fresh", ids)
	}
}

func TestConsolidateOverCapKeepsNewest(t *testing.T) {
	store := newConsolidationStore(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	addEntries(t, store,
		memoryAt("one", "one", now.Add(-5*time.Hour)),
		memoryAt("two", "two", now.Add(-4*time.Hour)),
		memoryAt("three", "three", now.Add(-3*time.Hour)),
		memoryAt("four", "four", now.Add(-2*time.Hour)),
		memoryAt("five", "five", now.Add(-time.Hour)),
	)
	report, err := store.consolidate(context.Background(), contracts.MemoryPolicy{MaxEntries: 2}, now)
	if err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	if report.RemovedExpired != 0 || report.RemovedOverCap != 3 || report.Remaining != 2 {
		t.Fatalf("report = %+v, want over-cap 3 remaining 2", report)
	}
	entries, _ := store.List(context.Background())
	if len(entries) != 2 || entries[0].ID != "four" || entries[1].ID != "five" {
		t.Fatalf("remaining = %+v, want four+five (newest preserved)", entries)
	}
}

func TestConsolidateCombinedExpiryAndCap(t *testing.T) {
	store := newConsolidationStore(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	addEntries(t, store,
		memoryAt("expired-1", "a", now.Add(-10*24*time.Hour)),
		memoryAt("expired-2", "b", now.Add(-9*24*time.Hour)),
		memoryAt("kept-1", "c", now.Add(-2*time.Hour)),
		memoryAt("kept-2", "d", now.Add(-time.Hour)),
	)
	report, err := store.consolidate(context.Background(),
		contracts.MemoryPolicy{MaxEntries: 1, MaxAge: 7 * 24 * time.Hour}, now)
	if err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	if report.RemovedExpired != 2 || report.RemovedOverCap != 1 || report.Remaining != 1 {
		t.Fatalf("report = %+v, want expired 2 over-cap 1 remaining 1", report)
	}
	entries, _ := store.List(context.Background())
	if len(entries) != 1 || entries[0].ID != "kept-2" {
		t.Fatalf("remaining = %+v, want kept-2", entries)
	}
}

func TestConsolidateUnlimitedAndEmpty(t *testing.T) {
	store := newConsolidationStore(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	report, err := store.consolidate(context.Background(), contracts.MemoryPolicy{}, now)
	if err != nil || report != (contracts.MemoryConsolidationReport{}) {
		t.Fatalf("empty report = %+v/%v, want zero", report, err)
	}
	addEntries(t, store,
		memoryAt("old", "old", now.Add(-100*24*time.Hour)),
		memoryAt("new", "new", now),
	)
	report, err = store.consolidate(context.Background(), contracts.MemoryPolicy{}, now)
	if err != nil {
		t.Fatalf("unlimited consolidate: %v", err)
	}
	if report != (contracts.MemoryConsolidationReport{Remaining: 2}) {
		t.Fatalf("unlimited report = %+v, want remaining 2 only", report)
	}
	// A nil context behaves like the background context.
	if _, err := store.consolidate(nil, contracts.MemoryPolicy{}, now); err != nil {
		t.Fatalf("nil ctx consolidate: %v", err)
	}
}

func TestConsolidateRemoveFailureReturnsError(t *testing.T) {
	store := newConsolidationStore(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	addEntries(t, store, memoryAt("old", "old", now.Add(-2*time.Hour)))
	// Point the store at a directory: Remove's atomic rename fails, so the
	// entry cannot be deleted.
	directory := t.TempDir()
	store.path = directory
	report, err := store.consolidate(context.Background(), contracts.MemoryPolicy{MaxAge: time.Hour}, now)
	if err == nil || !strings.Contains(err.Error(), "could not be removed") {
		t.Fatalf("consolidate error = %v, want a readable removal failure", err)
	}
	if report.Remaining != 1 {
		t.Fatalf("failure report = %+v, want remaining 1", report)
	}
	if report.RemovedExpired != 0 {
		t.Fatalf("failure report = %+v, want no successful expiry removal", report)
	}
}

func TestConsolidateContextError(t *testing.T) {
	store := newConsolidationStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Consolidate(ctx, contracts.MemoryPolicy{MaxEntries: 1}); err == nil {
		t.Fatal("Consolidate(cancelled) = nil error")
	}
}
