package agent

// Tests for the explicit session provenance and the structured tool-call path
// added to the chat read model:
//   - ChatSessionInfo.Origin comes from the provenance setting the automation
//     backend writes, so the sidebar never has to guess from the title (audit
//     F6);
//   - contracts.ToolCall.Path is captured from the full tool-call arguments
//     before they are bounded, so generated-file cards survive large writes
//     (audit F4) and restored turns keep the path from the durable events.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestSessionsPopulateOriginFromSettings covers the read-model half of audit
// F6: the origin setting written by the automation backend becomes
// ChatSessionInfo.Origin (trimmed); sessions without the setting stay user
// sessions ("").
func TestSessionsPopulateOriginFromSettings(t *testing.T) {
	settings := newFakeSettings(nil)
	svc := newTestService(t, Config{}, settings, nil)

	userID, err := svc.NewSession("user chat")
	if err != nil {
		t.Fatalf("NewSession user: %v", err)
	}
	runID, err := svc.NewSession("automation run")
	if err != nil {
		t.Fatalf("NewSession run: %v", err)
	}
	childID, err := svc.NewSession("subagent child")
	if err != nil {
		t.Fatalf("NewSession child: %v", err)
	}
	// The backend writes the setting from another goroutine: a value with
	// surrounding whitespace must map to the trimmed origin.
	if err := settings.Set(contracts.SessionOriginSettingKey(runID), "  "+contracts.SessionOriginAutomation+"  "); err != nil {
		t.Fatalf("Set origin: %v", err)
	}
	if err := settings.Set(contracts.SessionOriginSettingKey(childID), contracts.SessionOriginSubagent); err != nil {
		t.Fatalf("Set origin: %v", err)
	}

	origins := make(map[string]string, len(svc.Sessions()))
	for _, info := range svc.Sessions() {
		origins[info.ID] = info.Origin
	}
	if got := origins[userID]; got != contracts.SessionOriginUser {
		t.Fatalf("user session origin = %q, want %q", got, contracts.SessionOriginUser)
	}
	if got := origins[runID]; got != contracts.SessionOriginAutomation {
		t.Fatalf("automation session origin = %q, want %q", got, contracts.SessionOriginAutomation)
	}
	if got := origins[childID]; got != contracts.SessionOriginSubagent {
		t.Fatalf("subagent session origin = %q, want %q", got, contracts.SessionOriginSubagent)
	}
}

// fileWriteProvider asks for one write_file call with the scripted arguments,
// then answers with plain text.
type fileWriteProvider struct {
	arguments string
	calls     atomic.Int32
}

func (p *fileWriteProvider) Name() string { return "file-write" }

func (p *fileWriteProvider) Stream(context.Context, rig.Request) (rig.Stream, error) {
	if p.calls.Add(1) == 1 {
		return &scriptedStream{chunks: []rig.Chunk{
			{ToolCalls: []rig.ToolCallDelta{{Index: 0, ID: "call-w", Name: "write_file", Arguments: p.arguments}}},
			{FinishReason: "tool_calls"},
		}}, nil
	}
	return &scriptedStream{chunks: []rig.Chunk{
		{Delta: "done"},
		{FinishReason: "stop"},
	}}, nil
}

// writeFileStub is a minimal registered write_file tool so the run can execute
// the call the scripted provider asked for.
type writeFileStub struct{}

func (writeFileStub) Name() string           { return "write_file" }
func (writeFileStub) Description() string    { return "Write a file." }
func (writeFileStub) Schema() map[string]any { return map[string]any{"type": "object"} }

func (writeFileStub) Invoke(context.Context, rig.ToolCall) (rig.ToolResult, error) {
	return rig.ToolResult{Content: "wrote the file"}, nil
}

// TestLiveToolCallRecordsWorkspacePath is the audit-F4 fix: a successful
// write_file whose arguments exceed the transcript bound (a whole source file)
// must still record its workspace-relative path, both on the live read model and
// in the durable tool_call event.
func TestLiveToolCallRecordsWorkspacePath(t *testing.T) {
	// A normal source-file write is far larger than the 4000-rune transcript
	// bound; the path must be captured before the arguments are cut.
	arguments := `{"path":"src/main.go","content":"` + strings.Repeat("x", maxToolArgumentsRunes+10) + `"}`
	provider := &fileWriteProvider{arguments: arguments}
	settings := newFakeSettings(map[string]string{contracts.SettingsChatModel: "file-write"})
	svc := newTestService(t, Config{}, settings, nil, writeFileStub{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }

	id, err := svc.NewSession("write it")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "write the file"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	session := waitIdle(t, svc, id)

	var call *contracts.ToolCall
	for index := range session.Messages {
		for toolIndex := range session.Messages[index].Tools {
			if session.Messages[index].Tools[toolIndex].Name == "write_file" {
				call = &session.Messages[index].Tools[toolIndex]
			}
		}
	}
	if call == nil {
		t.Fatalf("live read model carries no write_file call: %+v", session.Messages)
	}
	if call.Path != "src/main.go" {
		t.Fatalf("tool call Path = %q, want src/main.go (arguments were bounded)", call.Path)
	}
	// The transcript arguments are still bounded as before: the path is carried
	// structurally, not as an excuse to keep the whole file body.
	if got := len([]rune(call.Arguments)); got != maxToolArgumentsRunes+1 {
		t.Fatalf("bounded arguments length = %d runes, want %d", got, maxToolArgumentsRunes+1)
	}
	if !strings.HasSuffix(call.Arguments, "…") {
		t.Fatalf("bounded arguments do not carry the ellipsis: %q", call.Arguments[len(call.Arguments)-8:])
	}

	// The durable event records the path too, so a restored turn keeps it.
	store, ok := svc.store.(*fakeStore)
	if !ok {
		t.Fatalf("service store is %T, want *fakeStore", svc.store)
	}
	events, eventErr := store.Events(context.Background(), id, 0, 0)
	if eventErr != nil {
		t.Fatalf("Events: %v", eventErr)
	}
	var durablePath string
	for _, event := range events {
		if event.Type != "tool_call" {
			continue
		}
		var payload struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			continue
		}
		if payload.Path != "" {
			durablePath = payload.Path
		}
	}
	if durablePath != "src/main.go" {
		t.Fatalf("durable tool_call event path = %q, want src/main.go", durablePath)
	}
}

// TestRestoreHydratesToolCallPath covers the restore half: a durable tool_call
// event carries the path even when it has no text offset (older/partial events),
// and restore must put it back on the read model.
func TestRestoreHydratesToolCallPath(t *testing.T) {
	store := newFakeStore()
	seedToolTurn(t, store, "sess-path",
		rig.SessionEvent{Type: "tool_call", Payload: []byte(`{"id":"call-1","name":"write_file","path":"src/main.go"}`)},
	)
	svc := newPersistenceTestService(store)
	svc.restore(context.Background())

	message := restoredToolMessage(t, svc, "sess-path")
	if got := message.Tools[0].Path; got != "src/main.go" {
		t.Fatalf("restored tool call Path = %q, want src/main.go (tools = %+v)", got, message.Tools)
	}
	// An event without text_offset still leaves the legacy transcript order: the
	// path hydration must not certify a timeline it does not have.
	if message.ToolTimelineKnown {
		t.Fatal("path-only event certified the tool timeline")
	}
}

// TestToolCallPath covers the pure path-resolution rules: the path is parsed
// from the full argument JSON before bounding, converted to workspace-relative,
// and anything that cannot be a workspace file fails closed to "".
func TestToolCallPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	outside := filepath.ToSlash(filepath.Join(t.TempDir(), "elsewhere", "secret.txt"))
	inside := filepath.ToSlash(filepath.Join(root, "src", "main.go"))
	cases := []struct {
		name string
		tool string
		args string
		root string
		want string
	}{
		{
			name: "small write", tool: "write_file",
			args: `{"path":"src/main.go","content":"package main"}`,
			root: root, want: "src/main.go",
		},
		{
			name: "large write still names its file", tool: "write_file",
			args: `{"path":"src/main.go","content":"` + strings.Repeat("x", maxToolArgumentsRunes+10) + `"}`,
			root: root, want: "src/main.go",
		},
		{
			name: "file_path alias", tool: "str_replace_editor",
			args: `{"file_path":"README.md","old_string":"a","new_string":"b"}`,
			root: root, want: "README.md",
		},
		{
			name: "camel alias and dot segments", tool: "edit_file",
			args: `{"filePath":"./a/../b/c.go"}`,
			root: root, want: "b/c.go",
		},
		{
			name: "read side file tool", tool: "read_file",
			args: `{"path":"docs/x.md"}`, root: root, want: "docs/x.md",
		},
		{
			name: "absolute path inside the workspace", tool: "write_file",
			args: `{"path":"` + inside + `"}`, root: root, want: "src/main.go",
		},
		{
			name: "absolute path outside the workspace", tool: "write_file",
			args: `{"path":"` + outside + `"}`, root: root, want: "",
		},
		{
			name: "relative path escaping the workspace", tool: "write_file",
			args: `{"path":"../secret.txt"}`, root: root, want: "",
		},
		{
			name: "non file tool", tool: "bash",
			args: `{"path":"src/main.go"}`, root: root, want: "",
		},
		{
			name: "malformed arguments", tool: "write_file",
			args: `{"path":"src/main.go"`, root: root, want: "",
		},
		{
			name: "empty arguments", tool: "write_file",
			args: "", root: root, want: "",
		},
		{
			name: "relative path without a workspace", tool: "write_file",
			args: `{"path":"src/main.go"}`, root: "", want: "src/main.go",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := toolCallPath(tc.tool, tc.args, tc.root); got != tc.want {
				t.Fatalf("toolCallPath(%q, %q, %q) = %q, want %q", tc.tool, tc.args, tc.root, got, tc.want)
			}
		})
	}
}
