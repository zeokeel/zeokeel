package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/store"
)

// writeFixtureSession writes one DSH session JSONL file under
// <home>/sessions/<encoded>/<sid>/<name>.
func writeFixtureSession(t *testing.T, home, encoded, sid, name string, lines []string) string {
	t.Helper()
	dir := filepath.Join(home, "sessions", encoded, sid)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	content := strings.Join(lines, "\n")
	if len(lines) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func jsonLine(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func headerLine(t *testing.T, id, cwd, origin string, depth int) string {
	return jsonLine(t, map[string]any{
		"type": "session", "version": 4, "id": id, "createdAt": int64(1793788819976),
		"cwd": cwd, "origin": origin, "delegationDepth": depth, "agentPreset": "standard",
	})
}

func eventLine(t *testing.T, typ string, seq int64, data any) string {
	return jsonLine(t, map[string]any{"type": typ, "seq": seq, "time": int64(1793788819976 + seq), "data": data})
}

func userMessageEvent(t *testing.T, seq int64, text string) string {
	return eventLine(t, "user/message", seq, map[string]any{
		"role":    "user",
		"content": []map[string]any{{"type": "text", "text": text}},
	})
}

func assistantMessageEvent(t *testing.T, seq int64, parts []map[string]any) string {
	return eventLine(t, "assistant/message", seq, map[string]any{
		"turn": 1, "step": 1,
		"message": map[string]any{"role": "assistant", "content": parts},
		"usage":   map[string]any{"inputTokens": 10, "outputTokens": 5},
	})
}

func TestProbeLayoutAndCredentials(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeFixtureSession(t, home, "--tmp-x--", "sess-1", "session.v4.jsonl", []string{
		headerLine(t, "sess-1", cwd, "user", 0),
		userMessageEvent(t, 1, "hello"),
	})
	creds := "version: 1\nrecords:\n  client:\n    kind: browser\nrefs:\n  DEEPSEEK_API_KEY: secret-value-123\n  OPENAI_API_KEY: another-secret\n"
	if err := os.WriteFile(filepath.Join(home, credentialsName), []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}

	layout, err := Probe(home)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(layout.SessionFiles) != 1 {
		t.Fatalf("SessionFiles = %d, want 1 (%v)", len(layout.SessionFiles), layout.SessionFiles)
	}
	if len(layout.Workspaces) != 1 || layout.Workspaces[0].Encoded != "--tmp-x--" {
		t.Fatalf("workspaces = %+v, want the encoded bucket", layout.Workspaces)
	}
	if layout.CompressedSessions != 0 {
		t.Fatalf("CompressedSessions = %d, want 0", layout.CompressedSessions)
	}

	refs, err := InspectCredentials(layout.CredentialsPath)
	if err != nil {
		t.Fatalf("InspectCredentials: %v", err)
	}
	names := CredentialNames(refs)
	if len(names) != 2 || names[0] != "DEEPSEEK_API_KEY" || names[1] != "OPENAI_API_KEY" {
		t.Fatalf("credential names = %v, want [DEEPSEEK_API_KEY OPENAI_API_KEY]", names)
	}
	rendered := fmt.Sprintf("%+v", refs)
	for _, secret := range []string{"secret-value-123", "another-secret"} {
		if strings.Contains(rendered, secret) {
			t.Fatalf("credential VALUE leaked into the report struct: %s", rendered)
		}
	}
}

func TestParseSessionV4(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	lines := []string{
		headerLine(t, "sess-42", cwd, "user", 0),
		eventLine(t, "session/title", 1, map[string]any{"title": "first title"}),
		userMessageEvent(t, 2, "do the thing"),
		assistantMessageEvent(t, 3, []map[string]any{
			{"type": "reasoning", "text": "thinking…"},
			{"type": "text", "text": "running a tool"},
			{"type": "tool-call", "id": "call-1", "name": "bash", "arguments": `{"command":"ls"}`},
		}),
		eventLine(t, "tool/call", 4, map[string]any{"callId": "call-1", "name": "bash", "arguments": `{"command":"ls"}`}),
		eventLine(t, "tool/result", 5, map[string]any{"message": map[string]any{
			"role": "tool", "toolCallId": "call-1", "isError": false,
			"content": []map[string]any{{"type": "text", "text": "file.txt"}},
		}}),
		eventLine(t, "system/message", 6, map[string]any{"message": map[string]any{
			"role": "system", "content": []map[string]any{{"type": "text", "text": "system prompt"}},
		}}),
		eventLine(t, "session/title", 7, map[string]any{"title": "final title"}),
	}
	path := writeFixtureSession(t, home, "--w--", "sess-42", "session.v4.jsonl", lines)
	// Torn tail: a crash during append leaves a partial line without newline.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"assistant/message","data":`); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	sess, err := ParseSessionFile(path)
	if err != nil {
		t.Fatalf("ParseSessionFile: %v", err)
	}
	if sess.ID != "sess-42" || sess.Cwd != cwd || sess.Origin != "user" {
		t.Fatalf("header fields = %+v", sess)
	}
	if sess.Title != "final title" {
		t.Fatalf("title = %q, want last session/title", sess.Title)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("messages = %d, want 2: %+v", len(sess.Messages), sess.Messages)
	}
	if sess.Messages[0].Role != "user" || sess.Messages[0].Content != "do the thing" {
		t.Fatalf("user message = %+v", sess.Messages[0])
	}
	assistant := sess.Messages[1]
	if assistant.Role != "assistant" || !strings.Contains(assistant.Content, "running a tool") ||
		!strings.Contains(assistant.Content, "[tool call] bash") {
		t.Fatalf("assistant message = %+v", assistant)
	}
	if assistant.Reasoning != "thinking…" {
		t.Fatalf("reasoning = %q", assistant.Reasoning)
	}
	if sess.ToolCalls != 1 || sess.ToolResults != 1 || sess.SystemMessages != 1 {
		t.Fatalf("counters = toolCalls %d toolResults %d system %d", sess.ToolCalls, sess.ToolResults, sess.SystemMessages)
	}
	if len(sess.Warnings) == 0 {
		t.Fatalf("torn final line should produce a warning, got none")
	}
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestImportDryRunDoesNotTouchStore(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeFixtureSession(t, home, "--w--", "sess-1", "session.v4.jsonl", []string{
		headerLine(t, "sess-1", cwd, "user", 0),
		userMessageEvent(t, 1, "hello"),
		assistantMessageEvent(t, 2, []map[string]any{{"type": "text", "text": "hi"}}),
	})
	report, err := Import(context.Background(), Options{DSHHome: home, DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !report.DryRun || report.SessionsFound != 1 || report.SessionsImported != 1 || report.MessagesImported != 2 {
		t.Fatalf("dry-run report = %+v", report)
	}
	if len(report.Workspaces) != 1 || report.Workspaces[0].Cwd != cwd {
		t.Fatalf("dry-run workspaces = %+v", report.Workspaces)
	}
}

func TestImportWritesSessionsAndMessages(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeFixtureSession(t, home, "--w--", "sess-1", "session.v4.jsonl", []string{
		headerLine(t, "sess-1", cwd, "user", 0),
		eventLine(t, "session/title", 1, map[string]any{"title": "migrated chat"}),
		userMessageEvent(t, 2, "hello"),
		assistantMessageEvent(t, 3, []map[string]any{{"type": "text", "text": "hi there"}}),
	})
	st := newStore(t)
	report, err := Import(context.Background(), Options{DSHHome: home, Store: st, TitlePrefix: "[DSH] "})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.SessionsImported != 1 || report.MessagesImported != 2 || report.SessionsFailed != 0 {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Workspaces) != 1 || !report.Workspaces[0].CreatedInZeo || report.Workspaces[0].WorkspaceID == "" {
		t.Fatalf("workspace report = %+v", report.Workspaces)
	}
	sessions, err := st.ListSessions(report.Workspaces[0].WorkspaceID)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].Title != "[DSH] migrated chat" {
		t.Fatalf("sessions = %+v", sessions)
	}
	messages, err := st.ListMessages(sessions[0].ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(messages) != 2 || messages[0].Role != "user" || messages[0].Content != "hello" ||
		messages[1].Role != "assistant" || messages[1].Content != "hi there" || messages[1].TurnID != "import" {
		t.Fatalf("messages = %+v", messages)
	}
}

func TestImportSkipsSubagentAndEmptySessions(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeFixtureSession(t, home, "--w--", "child-1", "session.v4.jsonl", []string{
		headerLine(t, "child-1", cwd, "subagent", 1),
		userMessageEvent(t, 1, "child task"),
	})
	writeFixtureSession(t, home, "--w--", "empty-1", "session.v4.jsonl", []string{
		headerLine(t, "empty-1", cwd, "user", 0),
		eventLine(t, "system/message", 1, map[string]any{"message": map[string]any{"role": "system", "content": []map[string]any{{"type": "text", "text": "sys"}}}}),
	})
	st := newStore(t)
	report, err := Import(context.Background(), Options{DSHHome: home, Store: st})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.SessionsFound != 2 || report.SessionsSkipped != 2 || report.SessionsImported != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestImportSubagentWhenRequested(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeFixtureSession(t, home, "--w--", "child-1", "session.v4.jsonl", []string{
		headerLine(t, "child-1", cwd, "subagent", 1),
		userMessageEvent(t, 1, "child task"),
		assistantMessageEvent(t, 2, []map[string]any{{"type": "text", "text": "child answer"}}),
	})
	st := newStore(t)
	report, err := Import(context.Background(), Options{DSHHome: home, Store: st, IncludeSubagentSessions: true})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.SessionsImported != 1 || report.MessagesImported != 2 {
		t.Fatalf("report = %+v", report)
	}
}

func TestImportCompressedSession(t *testing.T) {
	if !zstdAvailable() {
		t.Skip("zstd binary not installed")
	}
	home := t.TempDir()
	cwd := t.TempDir()
	plain := writeFixtureSession(t, home, "--w--", "sess-z", "session.v4.jsonl", []string{
		headerLine(t, "sess-z", cwd, "user", 0),
		userMessageEvent(t, 1, "compressed hello"),
		assistantMessageEvent(t, 2, []map[string]any{{"type": "text", "text": "compressed hi"}}),
	})
	compressed := filepath.Join(filepath.Dir(plain), "session.v4.jsonl.zstd")
	cmd := exec.Command("zstd", "-q", "-f", "-o", compressed, plain)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("zstd compress fixture: %v (%s)", err, out)
	}
	if err := os.Remove(plain); err != nil {
		t.Fatal(err)
	}
	st := newStore(t)
	report, err := Import(context.Background(), Options{DSHHome: home, Store: st})
	if err != nil {
		t.Fatalf("Import compressed: %v", err)
	}
	if !report.CompressedInput || report.SessionsImported != 1 || report.MessagesImported != 2 {
		t.Fatalf("report = %+v", report)
	}
}

func TestImportMissingDSHHome(t *testing.T) {
	_, err := Import(context.Background(), Options{DSHHome: filepath.Join(t.TempDir(), "absent")})
	if !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("err = %v, want ErrNotInstalled", err)
	}
}

func TestImportRequiresStoreUnlessDryRun(t *testing.T) {
	home := t.TempDir()
	writeFixtureSession(t, home, "--w--", "s", "session.v4.jsonl", []string{
		headerLine(t, "s", t.TempDir(), "user", 0),
		userMessageEvent(t, 1, "x"),
	})
	if _, err := Import(context.Background(), Options{DSHHome: home}); !errors.Is(err, ErrNoStore) {
		t.Fatalf("err = %v, want ErrNoStore", err)
	}
}

func TestImportTornSessionDoesNotAbortRun(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	// Bad session: header is not valid JSON.
	badDir := filepath.Join(home, "sessions", "--w--", "bad")
	if err := os.MkdirAll(badDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "session.v4.jsonl"), []byte("not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFixtureSession(t, home, "--w--", "good", "session.v4.jsonl", []string{
		headerLine(t, "good", cwd, "user", 0),
		userMessageEvent(t, 1, "fine"),
	})
	st := newStore(t)
	report, err := Import(context.Background(), Options{DSHHome: home, Store: st})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.SessionsFailed != 1 || report.SessionsImported != 1 || len(report.Errors) != 1 {
		t.Fatalf("report = %+v", report)
	}
}
