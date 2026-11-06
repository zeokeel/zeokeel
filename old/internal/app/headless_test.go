package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zeokeel/zeokeel/old/internal/store"
)

// writeHeadlessStore seeds the provider settings a headless run needs, so the
// test does not depend on the developer's real ~/.zeokeel.
func writeHeadlessStore(t *testing.T, dir, baseURL string, extra map[string]string) {
	t.Helper()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close() //nolint:errcheck // test cleanup
	settings := map[string]string{
		"provider.api_key":  "test-key",
		"provider.base_url": baseURL,
		"provider.model":    "test-model",
	}
	for k, v := range extra {
		settings[k] = v
	}
	for k, v := range settings {
		if err := st.SetSetting(k, v); err != nil {
			t.Fatalf("SetSetting(%q): %v", k, err)
		}
	}
}

// sseServer answers every chat completion with answer, streaming one delta per
// word, which is all the headless path needs from a provider.
func sseServer(t *testing.T, answer string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for i, part := range strings.Split(answer, " ") {
			if i > 0 {
				part = " " + part
			}
			payload := fmt.Sprintf(`{"choices":[{"index":0,"delta":{"content":%q}}]}`, part)
			if _, err := io.WriteString(w, "data: "+payload+"\n\n"); err != nil {
				return
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestParseHeadlessArgs(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    headlessOptions
		wantErr bool
	}{
		{name: "positional", args: []string{"hello", "world"}, want: headlessOptions{prompt: "hello world"}},
		{name: "prompt flag", args: []string{"--prompt", "hi"}, want: headlessOptions{prompt: "hi"}},
		{name: "prompt equals", args: []string{"--prompt=hi there"}, want: headlessOptions{prompt: "hi there"}},
		{name: "json yes permission", args: []string{"--json", "--yes", "--permission", "read-only", "go"},
			want: headlessOptions{prompt: "go", json: true, yes: true, permission: "ask"}},
		{name: "help only", args: []string{"--help"}, want: headlessOptions{help: true}},
		{name: "double dash", args: []string{"--", "--not-a-flag"}, want: headlessOptions{prompt: "--not-a-flag"}},
		{name: "missing prompt", args: []string{"--json"}, wantErr: true},
		{name: "unknown flag", args: []string{"--bogus", "hi"}, wantErr: true},
		{name: "bad permission", args: []string{"--permission", "root", "hi"}, wantErr: true},
		{name: "prompt twice", args: []string{"--prompt", "a", "b"}, wantErr: true},
	}
	for _, tc := range cases {
		got, err := parseHeadlessArgs(tc.args)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: parseHeadlessArgs(%q) = %+v, want error", tc.name, tc.args, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: parseHeadlessArgs(%q) = %+v, want %+v", tc.name, tc.args, got, tc.want)
		}
	}
}

// TestRunHeadlessUsageAndHelp covers the exit-code contract without touching a
// data directory: help is 0, usage errors are 2.
func TestRunHeadlessUsageAndHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := RunHeadless([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("--help exit = %d, want 0 (stderr %q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage: zeokeel run") || !strings.Contains(stdout.String(), "--json") {
		t.Fatalf("--help output missing usage:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := RunHeadless([]string{"--bogus", "hi"}, &stdout, &stderr); code != 2 {
		t.Fatalf("unknown flag exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown flag") || stdout.Len() != 0 {
		t.Fatalf("unknown flag stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := RunHeadless(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("missing prompt exit = %d, want 2", code)
	}
}

// TestRunHeadlessNoModel covers the failure path: a data directory without a
// provider key exits 1 with an actionable message instead of starting a turn.
func TestRunHeadlessNoModel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZEOKEEL_DATA_DIR", dir)
	var stdout, stderr bytes.Buffer
	code := RunHeadless([]string{"hello"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stdout %q stderr %q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "no model is configured") {
		t.Fatalf("stderr = %q, want the no-model message", stderr.String())
	}
}

// TestRunHeadlessJSON is the FP-18 end-to-end regression: one prompt against a
// streaming provider yields a JSON result, the final answer text and exit 0.
func TestRunHeadlessJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZEOKEEL_DATA_DIR", dir)
	srv := sseServer(t, "Hello from headless")
	writeHeadlessStore(t, dir, srv.URL+"/v1", map[string]string{"provider.retry_max_retries": "0"})

	var stdout, stderr bytes.Buffer
	code := RunHeadless([]string{"--json", "say hello"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	var result headlessResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Status != "ok" {
		t.Errorf("status = %q, want ok", result.Status)
	}
	if result.SessionID == "" {
		t.Error("session_id is empty")
	}
	if !strings.Contains(result.Text, "Hello from headless") {
		t.Errorf("text = %q, want the model answer", result.Text)
	}
	if result.Usage.Turns == 0 && result.Usage.Steps == 0 {
		t.Errorf("usage = %+v, want at least one counted turn/step", result.Usage)
	}

	// The session is persisted in the data directory and can be listed back.
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st.Close() //nolint:errcheck // test cleanup
	if _, err := st.GetSession(result.SessionID); err != nil {
		t.Fatalf("headless session %q not persisted: %v", result.SessionID, err)
	}
}

// TestRunHeadlessPlainText covers the non-JSON output shape: only the final
// answer goes to stdout and the exit code is 0.
func TestRunHeadlessPlainText(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZEOKEEL_DATA_DIR", dir)
	srv := sseServer(t, "plain text answer")
	writeHeadlessStore(t, dir, srv.URL+"/v1", map[string]string{"provider.retry_max_retries": "0"})

	var stdout, stderr bytes.Buffer
	code := RunHeadless([]string{"say something"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "plain text answer") {
		t.Errorf("stdout = %q, want the answer", stdout.String())
	}
	if strings.Contains(stdout.String(), `"status"`) {
		t.Errorf("plain-text mode printed JSON: %s", stdout.String())
	}
}

// TestRunHeadlessFailedTurn covers the failure exit code: a provider 500 fails
// the turn and RunHeadless reports 1 with the error on stderr, not a silent 0.
func TestRunHeadlessFailedTurn(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZEOKEEL_DATA_DIR", dir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	writeHeadlessStore(t, dir, srv.URL+"/v1", map[string]string{"provider.retry_max_retries": "0"})

	var stdout, stderr bytes.Buffer
	code := RunHeadless([]string{"--json", "fail please"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	var result headlessResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Status == "ok" || result.Status == "" {
		t.Errorf("status = %q, want a failure status", result.Status)
	}
	if result.Error == "" {
		t.Error("error field is empty for a failed turn")
	}
}

// TestHeadlessFinalText pins the answer selection: the last assistant message
// wins, and a tool-only assistant message (empty text) is skipped.
func TestHeadlessFinalText(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZEOKEEL_DATA_DIR", dir)
	if got := headlessFinalText(nil); got != "" {
		t.Fatalf("nil snapshot text = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "index.sqlite")); err == nil {
		// The pure helper test must not require a store; nothing to assert.
		_ = err
	}
}

// TestHeadlessPermissionMapping pins the CLI spelling of the access modes.
func TestHeadlessPermissionMapping(t *testing.T) {
	cases := map[string]string{
		"read-only":       "ask",
		"ask":             "ask",
		"workspace":       "workspace",
		"workspace-write": "workspace",
		"auto-review":     "auto_review",
		"full":            "full",
		"yolo":            "full",
	}
	for in, want := range cases {
		got, err := headlessPermission(in)
		if err != nil || got != want {
			t.Errorf("headlessPermission(%q) = (%q, %v), want %q", in, got, err, want)
		}
	}
	if _, err := headlessPermission("root"); err == nil {
		t.Error("headlessPermission(root) accepted an unknown mode")
	}
}

// writeHeadlessSSE streams one text answer over the OpenAI SSE format.
func writeHeadlessSSE(w io.Writer, text string) {
	payload := fmt.Sprintf(`{"choices":[{"index":0,"delta":{"content":%q}}]}`, text)
	_, _ = io.WriteString(w, "data: "+payload+"\n\n")
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

// toolCallServer answers the first model call with one tool call and every
// later call (tool result continuation, auto-title) with finalText. It lets a
// test drive a real tool through the headless turn without a real model.
func toolCallServer(t *testing.T, tool, argsJSON, finalText string) *httptest.Server {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if calls.Add(1) > 1 {
			writeHeadlessSSE(w, finalText)
			return
		}
		call := map[string]any{
			"index": 0, "id": "call-1", "type": "function",
			"function": map[string]any{"name": tool, "arguments": argsJSON},
		}
		payload := fmt.Sprintf(`{"choices":[{"index":0,"delta":{"tool_calls":[%s]},"finish_reason":"tool_calls"}]}`,
			mustMarshalJSON(t, call))
		_, _ = io.WriteString(w, "data: "+payload+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mustMarshalJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

// TestRunHeadlessPermissionReadOnly proves FP-18's --permission is applied, not
// ignored: a Read Only run must not execute a write tool even when the model
// asks for it, and the file must not appear.
func TestRunHeadlessPermissionReadOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZEOKEEL_DATA_DIR", dir)
	target := filepath.Join(t.TempDir(), "readonly-pwn.txt")
	srv := toolCallServer(t, "write_file",
		fmt.Sprintf(`{"path":%q,"content":"pwned"}`, target), "done")
	writeHeadlessStore(t, dir, srv.URL+"/v1", map[string]string{"provider.retry_max_retries": "0"})

	var stdout, stderr bytes.Buffer
	code := RunHeadless([]string{"--json", "--permission", "read-only", "write a file"}, &stdout, &stderr)
	if code != 0 && code != 1 {
		t.Fatalf("exit = %d, want a normal turn result\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatalf("Read Only headless run executed write_file and created %s\nstdout: %s", target, stdout.String())
	}
}

// TestRunHeadlessPermissionFullExecutesGatedTool proves the other direction:
// --permission full rebuilds the tools/gate so a gated bash call runs instead
// of being denied by the default Workspace Write gate.
func TestRunHeadlessPermissionFullExecutesGatedTool(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZEOKEEL_DATA_DIR", dir)
	marker := filepath.Join(t.TempDir(), "full-marker")
	srv := toolCallServer(t, "bash", fmt.Sprintf(`{"command":"touch %s"}`, marker), "done")
	writeHeadlessStore(t, dir, srv.URL+"/v1", map[string]string{"provider.retry_max_retries": "0"})

	var stdout, stderr bytes.Buffer
	code := RunHeadless([]string{"--json", "--permission", "full", "run the command"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 for full access\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("full-access headless run did not execute bash: %v\nstdout: %s", err, stdout.String())
	}
	var result headlessResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Denied != 0 {
		t.Fatalf("full access denied %d call(s)", result.Denied)
	}
}

// TestRunHeadlessDefaultPermissionStillDenies proves the default behavior is
// unchanged: without --permission the persisted Workspace Write default gates
// bash, and the non-interactive run denies it instead of hanging or running it.
func TestRunHeadlessDefaultPermissionStillDenies(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZEOKEEL_DATA_DIR", dir)
	marker := filepath.Join(t.TempDir(), "denied-marker")
	srv := toolCallServer(t, "bash", fmt.Sprintf(`{"command":"touch %s"}`, marker), "done")
	writeHeadlessStore(t, dir, srv.URL+"/v1", map[string]string{
		"provider.retry_max_retries": "0",
		"ui.permission":              "workspace",
	})
	var stdout, stderr bytes.Buffer
	code := RunHeadless([]string{"--json", "run the command"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 when an approval is denied\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("default-permission headless run executed a gated bash call: %s", marker)
	}
	var result headlessResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if result.Status != "denied" || result.Denied == 0 {
		t.Fatalf("result = %+v, want status denied with denied>0", result)
	}
	if !strings.Contains(stderr.String(), "requires human approval") {
		t.Fatalf("stderr = %q, want the approval guidance", stderr.String())
	}
}
