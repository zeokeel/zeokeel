package acp

// Tests for the ACP session/new workspace binding: a client (editor) sends the
// project root as params.cwd, and the agent's file/shell tools must run in that
// root instead of silently using whatever workspace the desktop last opened.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeWorkspace is a minimal contracts.WorkspaceService that records calls and
// exposes one workspace (the client's cwd).
type fakeWorkspace struct {
	root      string
	id        string
	created   bool
	opened    string
	createErr error
	openErr   error
}

var _ contracts.WorkspaceService = (*fakeWorkspace)(nil)

func (w *fakeWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	if w.root == "" {
		return nil, nil
	}
	return []contracts.Workspace{{ID: w.id, Name: filepath.Base(w.root), Root: w.root}}, nil
}

func (w *fakeWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	if w.root == "" {
		return contracts.Workspace{}, false, nil
	}
	return contracts.Workspace{ID: w.id, Root: w.root}, true, nil
}

func (w *fakeWorkspace) Create(_ context.Context, name, root string) (contracts.Workspace, error) {
	if w.createErr != nil {
		return contracts.Workspace{}, w.createErr
	}
	w.created = true
	w.root = root
	w.id = "ws-created"
	return contracts.Workspace{ID: w.id, Name: name, Root: root}, nil
}

func (w *fakeWorkspace) Open(_ context.Context, id string) (contracts.Workspace, error) {
	if w.openErr != nil {
		return contracts.Workspace{}, w.openErr
	}
	w.opened = id
	return contracts.Workspace{ID: id, Root: w.root}, nil
}

func (w *fakeWorkspace) Delete(context.Context, string) error { return nil }
func (w *fakeWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}
func (w *fakeWorkspace) ReadFile(context.Context, string, int) ([]byte, error) {
	return nil, nil
}
func (w *fakeWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

// workspaceChat adds the optional session-workspace half to the existing fake.
type workspaceChat struct {
	*fakeChat
	bindings map[string]string
}

func newWorkspaceChat() *workspaceChat {
	return &workspaceChat{fakeChat: newFakeChat(), bindings: map[string]string{}}
}

func (c *workspaceChat) SetSessionWorkspace(sessionID, workspaceID string) error {
	c.bindings[sessionID] = workspaceID
	return nil
}

func TestSessionNewHonorsClientCWD(t *testing.T) {
	root := t.TempDir()
	chat := newWorkspaceChat()
	ws := &fakeWorkspace{}
	service := newService(Config{}, chat, &fakeLogger{})
	service.workspace = ws

	frames := serveLines(t, service, initializeLine(1),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":%q}}`, root))
	sessionID := sessionIDFromFrames(t, frames, 2)

	if !ws.created {
		t.Fatalf("session/new did not register the client cwd as a workspace")
	}
	if got := chat.bindings[sessionID]; got != ws.id {
		t.Fatalf("session workspace binding = %q, want %q", got, ws.id)
	}
}

func TestSessionNewOpensExistingWorkspaceForCWD(t *testing.T) {
	root := t.TempDir()
	chat := newWorkspaceChat()
	ws := &fakeWorkspace{root: root, id: "ws-existing"}
	service := newService(Config{}, chat, &fakeLogger{})
	service.workspace = ws

	frames := serveLines(t, service, initializeLine(1),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":%q}}`, root))
	sessionID := sessionIDFromFrames(t, frames, 2)
	if ws.created {
		t.Fatalf("session/new re-registered an existing workspace")
	}
	if ws.opened != "ws-existing" {
		t.Fatalf("existing workspace was not opened: opened=%q", ws.opened)
	}
	if got := chat.bindings[sessionID]; got != "ws-existing" {
		t.Fatalf("session workspace binding = %q, want ws-existing", got)
	}
}

func TestSessionNewRejectsInvalidCWD(t *testing.T) {
	chat := newWorkspaceChat()
	service := newService(Config{}, chat, &fakeLogger{})
	service.workspace = &fakeWorkspace{}
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	frames := serveLines(t, service, initializeLine(1),
		fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":%q}}`, missing))
	result := frameByID(t, frames, 2)
	if result.Error == nil {
		t.Fatalf("session/new accepted a missing cwd, result = %s", result.Result)
	}
	if result.Error.Code != CodeInvalidParams {
		t.Fatalf("error code = %d, want invalid params (%d)", result.Error.Code, CodeInvalidParams)
	}
	if len(chat.newSessionCalls) != 0 {
		t.Fatalf("session was created despite the invalid cwd: %v", chat.newSessionCalls)
	}
}

func TestSessionNewWithoutCWDKeepsCurrentWorkspace(t *testing.T) {
	chat := newWorkspaceChat()
	ws := &fakeWorkspace{root: t.TempDir(), id: "ws-current"}
	service := newService(Config{}, chat, &fakeLogger{})
	service.workspace = ws

	frames := serveLines(t, service, initializeLine(1),
		`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{}}`)
	result := frameByID(t, frames, 2)
	if result.Error != nil {
		t.Fatalf("session/new without cwd = %v", result.Error.Message)
	}
	if ws.created || ws.opened != "" {
		t.Fatalf("session/new without cwd touched the workspace registry (created=%v opened=%q)", ws.created, ws.opened)
	}
}

// frameByID finds the response frame for one JSON-RPC id.
func frameByID(t *testing.T, frames []wireFrame, id int) wireFrame {
	t.Helper()
	for _, frame := range frames {
		if string(frame.ID) == fmt.Sprint(id) {
			return frame
		}
	}
	t.Fatalf("no response frame for id %d", id)
	return wireFrame{}
}

// sessionIDFromFrames decodes the sessionId of a session/new result.
func sessionIDFromFrames(t *testing.T, frames []wireFrame, id int) string {
	t.Helper()
	frame := frameByID(t, frames, id)
	if frame.Error != nil {
		t.Fatalf("session/new error: %s", frame.Error.Message)
	}
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(frame.Result, &result); err != nil {
		t.Fatalf("decode session/new result: %v", err)
	}
	if result.SessionID == "" {
		t.Fatalf("session/new returned no sessionId: %s", frame.Result)
	}
	return result.SessionID
}
