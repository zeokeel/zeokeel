package contracts

// Tests for the execution-history read model (contracts.KeyExecutionHistory):
// the record's derived duration, the state/kind vocabulary and the service key
// the toolkit provides and the session header consumes.

import (
	"testing"
	"time"
)

func TestExecutionRecordDuration(t *testing.T) {
	start := time.Date(2026, 11, 15, 9, 0, 0, 0, time.Local)
	finished := ExecutionRecord{
		ID: "job-1", Kind: ExecutionKindJob,
		State:   ExecutionStateFinished,
		Started: start, Ended: start.Add(3 * time.Second),
	}
	if got := finished.Duration(); got != 3*time.Second {
		t.Fatalf("finished Duration() = %v, want 3s", got)
	}
	// An interrupted record has no recorded end: its lifetime is unknown, so the
	// UI must show an honest dash rather than inventing a duration.
	interrupted := ExecutionRecord{
		ID: "job-2", Kind: ExecutionKindJob,
		State:   ExecutionStateInterrupted,
		Started: start,
	}
	if got := interrupted.Duration(); got != 0 {
		t.Fatalf("interrupted Duration() = %v, want 0 (end not recorded)", got)
	}
	if got := (ExecutionRecord{State: ExecutionStateFinished}).Duration(); got != 0 {
		t.Fatalf("duration without a start = %v, want 0", got)
	}
	// A clock skew must not render a negative lifetime.
	skewed := ExecutionRecord{Started: start.Add(time.Second), Ended: start}
	if got := skewed.Duration(); got != 0 {
		t.Fatalf("negative Duration() = %v, want 0", got)
	}
}

func TestExecutionVocabulary(t *testing.T) {
	kinds := []string{ExecutionKindJob, ExecutionKindTerminal}
	seenKinds := make(map[string]bool, len(kinds))
	for _, kind := range kinds {
		if kind == "" || seenKinds[kind] {
			t.Fatalf("execution kinds must be distinct and non-empty: %v", kinds)
		}
		seenKinds[kind] = true
	}
	states := []string{ExecutionStateRunning, ExecutionStateFinished, ExecutionStateInterrupted}
	seenStates := make(map[string]bool, len(states))
	for _, state := range states {
		if state == "" || seenStates[state] {
			t.Fatalf("execution states must be distinct and non-empty: %v", states)
		}
		seenStates[state] = true
	}
}

func TestExecutionHistoryServiceKeyName(t *testing.T) {
	if got := KeyExecutionHistory.Name(); got != "zeokeel/execution-history" {
		t.Fatalf("KeyExecutionHistory.Name() = %q, want zeokeel/execution-history", got)
	}
}
