package uiroot

// The host artifact bridge: the agent's present tool calls Present, the
// desktop UI module installs a handler that renders the artifact, and Present
// returns the artifact id immediately (no blocking).

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// maxArtifactHistory is how many presented artifacts the bridge retains.
const maxArtifactHistory = 50

// artifactService is the Root's contracts.ArtifactPresenter and
// contracts.ArtifactLister implementation.
type artifactService struct {
	mu      sync.Mutex
	handler func(contracts.Artifact)
	history []contracts.Artifact
	nextID  atomic.Uint64
}

var _ contracts.ArtifactLister = (*artifactService)(nil)

// newArtifactService returns an empty artifact bridge.
func newArtifactService() *artifactService { return &artifactService{} }

// SetArtifactHandler installs the display handler (nil clears it).
func (s *artifactService) SetArtifactHandler(fn func(contracts.Artifact)) {
	s.mu.Lock()
	s.handler = fn
	s.mu.Unlock()
}

// Present records one artifact and delivers it to the handler, returning its
// id. It never blocks: the UI module posts the frame invalidation itself.
func (s *artifactService) Present(ctx context.Context, title, body string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if title == "" {
		return "", fmt.Errorf("artifact: title is required")
	}
	if body == "" {
		return "", fmt.Errorf("artifact: body is required")
	}
	id := fmt.Sprintf("artifact-%d", s.nextID.Add(1))
	artifact := contracts.Artifact{
		ID:        id,
		Title:     title,
		Body:      body,
		CreatedAt: time.Now(),
	}
	s.mu.Lock()
	s.history = append(s.history, artifact)
	if len(s.history) > maxArtifactHistory {
		s.history = append([]contracts.Artifact(nil), s.history[len(s.history)-maxArtifactHistory:]...)
	}
	handler := s.handler
	s.mu.Unlock()
	if handler != nil {
		// A panicking display handler is contained: the artifact is already
		// recorded and its id is still returned to the agent tool.
		runHandlerSafely("artifact", func() { handler(artifact) })
	}
	return id, nil
}

// Artifacts implements contracts.ArtifactLister: the retained history, newest
// first. The returned slice and its elements are a copy, so callers cannot
// mutate the service state.
func (s *artifactService) Artifacts() []contracts.Artifact {
	s.mu.Lock()
	defer s.mu.Unlock()
	history := make([]contracts.Artifact, len(s.history))
	for index := range s.history {
		history[index] = s.history[len(s.history)-1-index]
	}
	// Concurrent presents append in lock-acquisition order, which may differ
	// from CreatedAt; sort by presentation time (ID breaks clock ties) so the
	// newest-first contract holds.
	sort.SliceStable(history, func(left, right int) bool {
		if history[left].CreatedAt.Equal(history[right].CreatedAt) {
			return artifactSequence(history[left].ID) > artifactSequence(history[right].ID)
		}
		return history[left].CreatedAt.After(history[right].CreatedAt)
	})
	return history
}

// artifactSequence extracts the numeric suffix of an artifact id.
func artifactSequence(id string) int {
	index := strings.LastIndexByte(id, '-')
	if index < 0 {
		return 0
	}
	value, err := strconv.Atoi(id[index+1:])
	if err != nil {
		return 0
	}
	return value
}

// ArtifactPresenter returns the host artifact bridge
// (contracts.KeyArtifactPresenter on the desktop product).
func (r *Root) ArtifactPresenter() *artifactService { return r.artifacts }
