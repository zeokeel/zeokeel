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
	return headerLineVersion(t, id, cwd, origin, depth, 4)
}

func headerLineVersion(t *testing.T, id, cwd, origin string, depth, version int) string {
	return jsonLine(t, map[string]any{
		"type": "session", "version": version, "id": id, "createdAt": int64(1793788819976),
		"cwd": cwd, "origin": origin, "delegationDepth": depth, "agentPreset": "standard",
	})
}

func eventLine(t *testing.T, typ string, seq int64, data any) string {
	return jsonLine(t, map[string]any{"type": typ, "seq": seq, "time": int64(1793788819976 + seq), "data": data})
}

// userMessageEvent is one genuine human message (source.kind == "user").
func userMessageEvent(t *testing.T, seq int64, text string) string {
	return eventLine(t, "user/message", seq, map[string]any{
		"role":    "user",
		"source":  map[string]any{"kind": "user"},
		"content": []map[string]any{{"type": "text", "text": text}},
	})
}

// injectedUserMessageEvent is DSH-injected per-turn context: it arrives as a
// user/message event but its source.kind is not "user" (time-context,
// runtime-context, agent-message, ...), so it must not become a user bubble.
func injectedUserMessageEvent(t *testing.T, seq int64, kind, text string) string {
	return eventLine(t, "user/message", seq, map[string]any{
		"role":    "user",
		"source":  map[string]any{"kind": kind},
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
	if assistant.Role != "assistant" || !strings.Contains(assistant.Content, "running a tool") {
		t.Fatalf("assistant message = %+v", assistant)
	}
	if strings.Contains(assistant.Content, "[tool call]") {
		t.Fatalf("assistant content still contains a synthetic tool-call line: %q", assistant.Content)
	}
	if sess.StepAssistant != 0 || sess.SystemInjected != 0 {
		t.Fatalf("filter counters on a plain session = step %d system %d, want 0/0", sess.StepAssistant, sess.SystemInjected)
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

// ---------------------------------------------------------------------------
// PD-04 quality fixes: injected-context filtering, step-message filtering,
// skip-reason reporting and idempotency/version guards.
// ---------------------------------------------------------------------------

func imagePart() map[string]any { return map[string]any{"type": "image"} }

func TestParseSessionFiltersInjectedAndStepMessages(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	path := writeFixtureSession(t, home, "--w--", "sess-filter", "session.v4.jsonl", []string{
		headerLine(t, "sess-filter", cwd, "user", 0),
		userMessageEvent(t, 1, "real question"),
		injectedUserMessageEvent(t, 2, "time-context", "Time sampled while preparing turn 1"),
		injectedUserMessageEvent(t, 3, "runtime-context", "Current runtime context: ..."),
		// Step-level assistant event: reasoning + tool call, no visible text.
		assistantMessageEvent(t, 4, []map[string]any{
			{"type": "reasoning", "text": "thinking about the question"},
			{"type": "tool-call", "id": "call-1", "name": "bash", "arguments": `{"command":"ls"}`},
		}),
		// Final reply: visible text (+ a dropped image part).
		assistantMessageEvent(t, 5, []map[string]any{
			{"type": "text", "text": "final answer"},
			imagePart(),
		}),
	})
	sess, err := ParseSessionFile(path)
	if err != nil {
		t.Fatalf("ParseSessionFile: %v", err)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("messages = %d, want 2: %+v", len(sess.Messages), sess.Messages)
	}
	if sess.Messages[0].Role != "user" || sess.Messages[0].Content != "real question" {
		t.Fatalf("user message = %+v", sess.Messages[0])
	}
	if sess.Messages[1].Role != "assistant" || sess.Messages[1].Content != "final answer" {
		t.Fatalf("assistant message = %+v", sess.Messages[1])
	}
	if sess.SystemInjected != 2 {
		t.Fatalf("SystemInjected = %d, want 2", sess.SystemInjected)
	}
	if sess.StepAssistant != 1 {
		t.Fatalf("StepAssistant = %d, want 1", sess.StepAssistant)
	}
	if sess.Images != 1 {
		t.Fatalf("Images = %d, want 1", sess.Images)
	}
	for _, msg := range sess.Messages {
		for _, forbidden := range []string{"Time sampled", "Current runtime context", "[tool call]"} {
			if strings.Contains(msg.Content, forbidden) {
				t.Fatalf("imported content still contains %q: %q", forbidden, msg.Content)
			}
		}
	}
}

func TestImportReportSkipReasonsAndImages(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeFixtureSession(t, home, "--w--", "sess-skips", "session.v4.jsonl", []string{
		headerLine(t, "sess-skips", cwd, "user", 0),
		userMessageEvent(t, 1, "hi"),
		injectedUserMessageEvent(t, 2, "time-context", "time context"),
		injectedUserMessageEvent(t, 3, "agent-message", "agent note"),
		assistantMessageEvent(t, 4, []map[string]any{{"type": "tool-call", "id": "c1", "name": "bash", "arguments": `{}`}}),
		assistantMessageEvent(t, 5, []map[string]any{{"type": "text", "text": "answer"}, imagePart()}),
	})
	report, err := Import(context.Background(), Options{DSHHome: home, DryRun: true})
	if err != nil {
		t.Fatalf("Import dry run: %v", err)
	}
	if report.MessagesImported != 2 || report.SessionsImported != 1 {
		t.Fatalf("report = %+v", report)
	}
	want := map[string]int{
		SkipSystemInjected: 2,
		SkipStepAssistant:  1,
		SkipImagesDropped:  1,
	}
	for reason, n := range want {
		if got := report.Skips[reason]; got != n {
			t.Errorf("Skips[%s] = %d, want %d (report %+v)", reason, got, n, report.Skips)
		}
	}
	if report.ImagesDropped != 1 {
		t.Errorf("ImagesDropped = %d, want 1", report.ImagesDropped)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"skips"`) || !strings.Contains(string(raw), SkipSystemInjected) {
		t.Fatalf("report JSON does not expose skip reasons: %s", raw)
	}
}

func TestImportUnsupportedVersionIsExplicit(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	path := writeFixtureSession(t, home, "--w--", "sess-v5", "session.v5.jsonl", []string{
		headerLineVersion(t, "sess-v5", cwd, "user", 0, 5),
		userMessageEvent(t, 1, "future format"),
	})
	if _, err := ParseSessionFile(path); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("ParseSessionFile error = %v, want ErrUnsupportedVersion", err)
	}
	// The parser intentionally only discovers the version-5 file if a session
	// file name matches; rename it so Probe finds it, then check the report.
	if err := os.Rename(path, filepath.Join(filepath.Dir(path), "session.v4.jsonl")); err != nil {
		t.Fatal(err)
	}
	report, err := Import(context.Background(), Options{DSHHome: home, DryRun: true})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.SessionsFound != 1 || report.SessionsImported != 0 ||
		report.SessionsSkipped != 1 || report.SessionsFailed != 0 {
		t.Fatalf("report = %+v", report)
	}
	if report.Skips[SkipUnsupportedVersion] != 1 {
		t.Fatalf("Skips[unsupported_version] = %d, want 1 (%+v)", report.Skips[SkipUnsupportedVersion], report.Skips)
	}
	if len(report.Errors) != 1 || !strings.Contains(report.Errors[0].Error, "unsupported") {
		t.Fatalf("unsupported version not surfaced explicitly: %+v", report.Errors)
	}
}

func TestImportManifestIdempotent(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	path := writeFixtureSession(t, home, "--w--", "sess-idem", "session.v4.jsonl", []string{
		headerLine(t, "sess-idem", cwd, "user", 0),
		userMessageEvent(t, 1, "hello"),
		assistantMessageEvent(t, 2, []map[string]any{{"type": "text", "text": "hi"}}),
	})
	manifestPath := filepath.Join(t.TempDir(), "dsh-import.json")
	st := newStore(t)
	opts := Options{DSHHome: home, Store: st, ManifestPath: manifestPath}

	first, err := Import(context.Background(), opts)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if first.SessionsImported != 1 || first.MessagesImported != 2 {
		t.Fatalf("first report = %+v", first)
	}
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	manifest, err := loadManifest(manifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if len(manifest.Sessions) != 1 {
		t.Fatalf("manifest sessions = %d, want 1", len(manifest.Sessions))
	}
	for _, entry := range manifest.Sessions {
		if entry.SHA256 == "" || entry.SourceSize == 0 || entry.ZeoKeelSessionID == "" || entry.SessionID != "sess-idem" {
			t.Fatalf("manifest entry incomplete: %+v", entry)
		}
	}
	sessions, err := st.ListSessions(first.Workspaces[0].WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions after first import = %d, want 1", len(sessions))
	}

	// Second run: unchanged source must be skipped, not duplicated.
	second, err := Import(context.Background(), opts)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.SessionsImported != 0 || second.SessionsSkipped != 1 || second.Skips[SkipIdempotent] != 1 {
		t.Fatalf("second report = %+v", second)
	}
	sessions, err = st.ListSessions(first.Workspaces[0].WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("second import duplicated the session: %d sessions", len(sessions))
	}

	// Third run after the DSH session changed (continued conversation): the
	// hash differs, so the new content is imported as a fresh session.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(assistantMessageEvent(t, 3, []map[string]any{{"type": "text", "text": "follow-up"}}) + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	third, err := Import(context.Background(), opts)
	if err != nil {
		t.Fatalf("third import: %v", err)
	}
	if third.SessionsImported != 1 || third.Skips[SkipIdempotent] != 0 {
		t.Fatalf("third report = %+v", third)
	}
	sessions, err = st.ListSessions(first.Workspaces[0].WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("changed session was not re-imported: %d sessions", len(sessions))
	}
}

func TestImportManifestDryRunDoesNotWrite(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeFixtureSession(t, home, "--w--", "sess-dry", "session.v4.jsonl", []string{
		headerLine(t, "sess-dry", cwd, "user", 0),
		userMessageEvent(t, 1, "hello"),
	})
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	report, err := Import(context.Background(), Options{DSHHome: home, DryRun: true, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if report.SessionsImported != 1 {
		t.Fatalf("dry-run report = %+v", report)
	}
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote a manifest: %v", err)
	}
}

func TestImportCorruptManifestFailsBeforeWriting(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeFixtureSession(t, home, "--w--", "sess-manifest", "session.v4.jsonl", []string{
		headerLine(t, "sess-manifest", cwd, "user", 0),
		userMessageEvent(t, 1, "hello"),
	})
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(manifestPath, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := newStore(t)
	if _, err := Import(context.Background(), Options{DSHHome: home, Store: st, ManifestPath: manifestPath}); !errors.Is(err, ErrManifest) {
		t.Fatalf("Import error = %v, want ErrManifest", err)
	}
	workspaces, err := st.ListWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaces) != 0 {
		t.Fatalf("corrupt manifest still imported: %d workspaces", len(workspaces))
	}
}
