package uiroot

// The host artifact bridge: the agent's present tool calls Present, the
// desktop UI module installs a handler that renders the artifact, and Present
// returns the artifact id immediately (no blocking).

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// artifactService is the Root's contracts.ArtifactPresenter implementation.
type artifactService struct {
	mu      sync.Mutex
	handler func(contracts.Artifact)
	nextID  atomic.Uint64
}

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
	artifact := contracts.Artifact{ID: id, Title: title, Body: body}
	s.mu.Lock()
	handler := s.handler
	s.mu.Unlock()
	if handler != nil {
		handler(artifact)
	}
	return id, nil
}

// ArtifactPresenter returns the host artifact bridge
// (contracts.KeyArtifactPresenter on the desktop product).
func (r *Root) ArtifactPresenter() *artifactService { return r.artifacts }
