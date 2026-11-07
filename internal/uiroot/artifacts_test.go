package uiroot

// Host artifact bridge: Present trims/validates, returns ids immediately and
// delivers the artifact to the registered handler.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestArtifactPresentDeliversHandler(t *testing.T) {
	root := newTestRoot(t)
	service := root.ArtifactPresenter()
	artifacts := make(chan contracts.Artifact, 4)
	service.SetArtifactHandler(func(artifact contracts.Artifact) { artifacts <- artifact })

	id, err := service.Present(context.Background(), "  Report  ", "  # body  ")
	if err != nil {
		t.Fatalf("Present: %v", err)
	}
	if id != "artifact-1" {
		t.Fatalf("first id = %q, want artifact-1", id)
	}
	artifact := <-artifacts
	if artifact.ID != id || artifact.Title != "Report" || artifact.Body != "# body" {
		t.Fatalf("artifact = %+v", artifact)
	}
	second, err := service.Present(context.Background(), "Second", "body")
	if err != nil {
		t.Fatalf("Present second: %v", err)
	}
	if second != "artifact-2" {
		t.Fatalf("second id = %q, want artifact-2", second)
	}
}

func TestArtifactPresentValidation(t *testing.T) {
	root := newTestRoot(t)
	service := root.ArtifactPresenter()
	if _, err := service.Present(context.Background(), "  ", "body"); err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatalf("Present(empty title) = %v, want title error", err)
	}
	if _, err := service.Present(context.Background(), "title", "  "); err == nil || !strings.Contains(err.Error(), "body") {
		t.Fatalf("Present(empty body) = %v, want body error", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Present(cancelled, "title", "body"); err == nil {
		t.Fatal("Present(cancelled ctx) returned nil error")
	}
	// nil context means background and still works.
	if _, err := service.Present(nil, "title", "body"); err != nil {
		t.Fatalf("Present(nil ctx): %v", err)
	}
}

func TestArtifactWithoutHandlerStillReturnsID(t *testing.T) {
	root := newTestRoot(t)
	service := root.ArtifactPresenter()
	id, err := service.Present(context.Background(), "title", "body")
	if err != nil || id == "" {
		t.Fatalf("Present without handler = %q, %v", id, err)
	}
	service.SetArtifactHandler(func(contracts.Artifact) {})
	service.SetArtifactHandler(nil)
	if _, err := service.Present(context.Background(), "title", "body"); err != nil {
		t.Fatalf("Present after clearing handler: %v", err)
	}
}

func TestArtifactConcurrentPresents(t *testing.T) {
	root := newTestRoot(t)
	service := root.ArtifactPresenter()
	var mu sync.Mutex
	seen := make(map[string]bool)
	service.SetArtifactHandler(func(artifact contracts.Artifact) {
		mu.Lock()
		defer mu.Unlock()
		if seen[artifact.ID] {
			t.Errorf("duplicate artifact id %q", artifact.ID)
		}
		seen[artifact.ID] = true
	})
	const count = 12
	var wg sync.WaitGroup
	ids := make(chan string, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := service.Present(context.Background(), "title", "body")
			if err != nil {
				t.Errorf("Present: %v", err)
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	unique := make(map[string]bool, count)
	for id := range ids {
		if unique[id] {
			t.Fatalf("duplicate returned id %q", id)
		}
		unique[id] = true
	}
	if len(unique) != count {
		t.Fatalf("ids = %d, want %d", len(unique), count)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != count {
		t.Fatalf("handler saw %d artifacts, want %d", len(seen), count)
	}
}

// --- artifact history -------------------------------------------------------

func TestArtifactHistoryNewestFirstAndCopy(t *testing.T) {
	root := newTestRoot(t)
	service := root.ArtifactPresenter()
	if history := service.Artifacts(); history == nil || len(history) != 0 {
		t.Fatalf("empty history = %#v, want a non-nil empty slice", history)
	}
	first, err := service.Present(context.Background(), "First", "first body")
	if err != nil {
		t.Fatalf("Present first: %v", err)
	}
	second, err := service.Present(context.Background(), "Second", "second body")
	if err != nil {
		t.Fatalf("Present second: %v", err)
	}
	history := service.Artifacts()
	if len(history) != 2 {
		t.Fatalf("history = %+v, want two entries", history)
	}
	if history[0].ID != second || history[0].Title != "Second" || history[0].Body != "second body" ||
		history[0].CreatedAt.IsZero() {
		t.Fatalf("newest history entry = %+v", history[0])
	}
	if history[1].ID != first || history[1].Title != "First" || history[1].CreatedAt.IsZero() {
		t.Fatalf("oldest history entry = %+v", history[1])
	}
	// Callers get a copy: mutating the slice or its elements cannot change the
	// stored history.
	history[0].Title = "Mutated"
	history[1] = contracts.Artifact{}
	again := service.Artifacts()
	if again[0].Title != "Second" || again[1].Title != "First" || again[1].ID != first {
		t.Fatalf("history mutated through Artifacts(): %+v", again)
	}
}

func TestArtifactHistoryFiltersAndLimits(t *testing.T) {
	root := newTestRoot(t)
	service := root.ArtifactPresenter()
	// Validation and cancellation failures must not enter the history.
	if _, err := service.Present(context.Background(), " ", "body"); err == nil {
		t.Fatal("Present(empty title) succeeded")
	}
	if _, err := service.Present(context.Background(), "title", " "); err == nil {
		t.Fatal("Present(empty body) succeeded")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Present(cancelled, "title", "body"); err == nil {
		t.Fatal("Present(cancelled ctx) succeeded")
	}
	if history := service.Artifacts(); len(history) != 0 {
		t.Fatalf("failed presents entered history: %+v", history)
	}
	// A nil handler still records; a panicking handler records and returns an id.
	for index := 0; index < 3; index++ {
		if _, err := service.Present(context.Background(), "nil-handler", "body"); err != nil {
			t.Fatalf("Present without handler: %v", err)
		}
	}
	service.SetArtifactHandler(func(contracts.Artifact) { panic("artifact handler boom") })
	id, err := service.Present(context.Background(), "panic-handler", "body")
	if err != nil || id == "" {
		t.Fatalf("Present with panicking handler = %q, %v", id, err)
	}
	history := service.Artifacts()
	if len(history) != 4 || history[0].ID != id || history[0].Title != "panic-handler" {
		t.Fatalf("history with panicking handler = %+v", history)
	}

	// The history is capped at 50, dropping the oldest entries.
	service.SetArtifactHandler(nil)
	for index := 0; index < maxArtifactHistory+5; index++ {
		if _, err := service.Present(context.Background(), "many", fmt.Sprintf("body %d", index)); err != nil {
			t.Fatalf("Present many %d: %v", index, err)
		}
	}
	history = service.Artifacts()
	if len(history) != maxArtifactHistory {
		t.Fatalf("history length = %d, want %d", len(history), maxArtifactHistory)
	}
	if history[0].ID != "artifact-59" || history[len(history)-1].ID != "artifact-10" {
		t.Fatalf("trimmed history bounds = first %+v last %+v, want artifact-59..artifact-10",
			history[0], history[len(history)-1])
	}
}

func TestArtifactHistoryConcurrent(t *testing.T) {
	root := newTestRoot(t)
	service := root.ArtifactPresenter()
	const count = 20
	var wg sync.WaitGroup
	ids := make(chan string, count)
	for index := 0; index < count; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := service.Present(context.Background(), "concurrent", "body")
			if err != nil {
				t.Errorf("Present: %v", err)
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	unique := make(map[string]bool, count)
	for id := range ids {
		unique[id] = true
	}
	history := service.Artifacts()
	if len(history) != count {
		t.Fatalf("history = %d, want %d", len(history), count)
	}
	seen := make(map[string]bool, count)
	for _, artifact := range history {
		if artifact.ID == "" || artifact.CreatedAt.IsZero() || seen[artifact.ID] {
			t.Fatalf("invalid/duplicate history item: %+v", artifact)
		}
		seen[artifact.ID] = true
	}
	if len(seen) != len(unique) {
		t.Fatalf("history IDs = %v, returned IDs = %v", seen, unique)
	}
}
