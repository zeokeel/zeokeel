package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Artifact is one deliverable the agent presented to the user. It is metadata
// only: the tool never copies, moves or rewrites the file, so Path always
// refers to whatever the caller supplied.
type Artifact struct {
	// CallID is the runtime tool call id, filled by the controller.
	CallID string `json:"call_id,omitempty"`
	// Title is the short name shown on the card.
	Title string `json:"title"`
	// Path is a workspace-relative or absolute path the user can open.
	Path string `json:"path,omitempty"`
	// Markdown is optional inline content shown when the card is expanded.
	Markdown string `json:"markdown,omitempty"`
	// Kind classifies the artifact ("file" or "markdown"). It defaults to
	// "file" when Path is set and "markdown" otherwise.
	Kind string `json:"kind,omitempty"`
	// PresentedAt is filled by the controller from the end event.
	PresentedAt time.Time `json:"presented_at,omitempty"`
}

// ArtifactResult is the JSON document PresentTool returns. The controller
// parses it from the successful tool_end payload and records the artifact on
// the session.
type ArtifactResult struct {
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Path     string `json:"path,omitempty"`
	Markdown string `json:"markdown,omitempty"`
	Type     string `json:"type,omitempty"`
}

const (
	artifactKind     = "artifact"
	artifactFile     = "file"
	artifactMarkdown = "markdown"
)

const presentDescription = `Present a finished artifact (a file or inline Markdown) to the user.

Call it with a JSON object:
  {"title":"Test report","path":"reports/unit.md"}
  {"title":"Plan","markdown":"# Plan\n1. ...","type":"markdown"}

"title" is required. At least one of "path" and "markdown" is required. "type"
is optional and is one of file, markdown.

The tool does not copy or open the file: it records the artifact in the
session, and the live session shows a card listing the title and path. The tool
returns a JSON document {"kind":"artifact","title":...}.`

// PresentTool records presented artifacts for one agent instance. All state is
// instance-local and guarded by mu; no files are touched.
type PresentTool struct {
	mu        sync.Mutex
	artifacts []Artifact
}

// NewPresentTool returns an empty present tool.
func NewPresentTool() *PresentTool {
	return &PresentTool{}
}

// Name implements runtime.Tool.
func (t *PresentTool) Name() string { return "present" }

// Description implements runtime.Tool.
func (t *PresentTool) Description() string { return presentDescription }

// presentArgs is the decoded present argument object.
type presentArgs struct {
	Title    string `json:"title"`
	Path     string `json:"path,omitempty"`
	Markdown string `json:"markdown,omitempty"`
	Type     string `json:"type,omitempty"`
}

// Invoke implements runtime.Tool. It validates the metadata and records it; a
// non-nil error is surfaced to the model as a tool failure.
func (t *PresentTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	var in presentArgs
	if len(strings.TrimSpace(string(args))) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", fmt.Errorf("present: invalid arguments: %w", err)
		}
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return "", errors.New(`present: "title" is required`)
	}
	path := strings.TrimSpace(in.Path)
	markdown := in.Markdown
	if path == "" && strings.TrimSpace(markdown) == "" {
		return "", errors.New(`present: one of "path" or "markdown" is required`)
	}
	kind, err := parseArtifactKind(in.Type, path)
	if err != nil {
		return "", err
	}

	record := Artifact{
		Title:       title,
		Path:        path,
		Markdown:    markdown,
		Kind:        kind,
		PresentedAt: time.Now().UTC(),
	}

	t.mu.Lock()
	t.artifacts = append(t.artifacts, record)
	t.mu.Unlock()

	out, err := json.Marshal(ArtifactResult{
		Kind:     artifactKind,
		Title:    title,
		Path:     path,
		Markdown: markdown,
		Type:     kind,
	})
	if err != nil {
		return "", fmt.Errorf("present: encode result: %w", err)
	}
	return string(out), nil
}

// Artifacts returns a copy of the artifacts this tool instance presented,
// oldest first.
func (t *PresentTool) Artifacts() []Artifact {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Artifact, len(t.artifacts))
	copy(out, t.artifacts)
	return out
}

// parseArtifactKind normalizes the optional artifact type. A missing type is
// inferred from the content: a path alone is a file, inline content is
// Markdown.
func parseArtifactKind(s, path string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "":
		if path != "" {
			return artifactFile, nil
		}
		return artifactMarkdown, nil
	case artifactFile, artifactMarkdown:
		return s, nil
	default:
		return "", fmt.Errorf("present: unknown type %q (want file, markdown)", s)
	}
}
