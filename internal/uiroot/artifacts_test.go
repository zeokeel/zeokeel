package uiroot

// Host artifact bridge: Present trims/validates, returns ids immediately and
// delivers the artifact to the registered handler.

import (
	"context"
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
