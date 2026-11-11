package toolkit

// Regression tests for FR-04/FR-05 using a real rig LSP client: the toolkit
// must run the initialize handshake after starting the language server, and the
// lsp_* tools must apply the sandbox path policy (roots + deniedGlobs) and
// resolve relative paths against the workspace root.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	fakeLSPModeEnv   = "ZEOKEEL_FAKE_LSP_MODE"
	fakeLSPRecordEnv = "ZEOKEEL_FAKE_LSP_RECORD"
)

// TestMain doubles as the fake language server process: the tests re-execute
// this test binary with fakeLSPModeEnv set, so no external language server is
// needed (and no third-party dependency is added).
func TestMain(m *testing.M) {
	if mode := strings.TrimSpace(os.Getenv(fakeLSPModeEnv)); mode != "" {
		runFakeLSPServer(mode)
		return
	}
	os.Exit(m.Run())
}

// runFakeLSPServer speaks just enough LSP for the toolkit tests and records the
// method of every message it receives.
func runFakeLSPServer(mode string) {
	if mode == "crash" {
		fmt.Fprintln(os.Stderr, "fake language server: crashing on startup")
		os.Exit(3)
	}
	record := os.Getenv(fakeLSPRecordEnv)
	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	write := func(value any) {
		data, _ := json.Marshal(value)
		fmt.Fprintf(out, "Content-Length: %d\r\n\r\n", len(data))
		_, _ = out.Write(data)
		_ = out.Flush()
	}
	for {
		length, err := readLSPContentLength(in)
		if err != nil {
			return
		}
		body := make([]byte, length)
		if _, err := readFull(in, body); err != nil {
			return
		}
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(body, &message) != nil {
			continue
		}
		if record != "" && message.Method != "" {
			entry, _ := json.Marshal(map[string]any{"method": message.Method, "params": json.RawMessage(message.Params)})
			file, err := os.OpenFile(record, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err == nil {
				_, _ = file.Write(append(entry, '\n'))
				_ = file.Close()
			}
		}
		switch message.Method {
		case "initialize":
			write(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": map[string]any{
				"capabilities": map[string]any{
					"hoverProvider":          true,
					"definitionProvider":     true,
					"documentSymbolProvider": true,
				},
				"serverInfo": map[string]any{"name": "fake-lsp", "version": "1.0.0"},
			}})
		case "shutdown":
			write(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": nil})
		case "exit":
			return
		case "textDocument/documentSymbol":
			write(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": []any{}})
		case "textDocument/hover":
			write(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": map[string]any{
				"contents": map[string]any{"kind": "plaintext", "value": "hover-info"},
			}})
		case "textDocument/definition", "textDocument/references", "textDocument/implementation":
			write(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": []any{}})
		}
	}
}

// readLSPContentLength consumes the headers of one LSP frame.
func readLSPContentLength(reader *bufio.Reader) (int, error) {
	length := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return 0, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return length, nil
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			value := strings.TrimSpace(strings.ToLower(line)[len("content-length:"):])
			length, _ = strconv.Atoi(value)
		}
	}
}

// readFull fills buffer from reader.
func readFull(reader *bufio.Reader, buffer []byte) (int, error) {
	total := 0
	for total < len(buffer) {
		n, err := reader.Read(buffer[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// lspSelfCommand returns the test binary path used as the fake language server.
func lspSelfCommand(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return executable
}

// activateToolkitConfig is loadToolkit with an explicit toolkit config.
func activateToolkitConfig(t *testing.T, settings contracts.SettingsService, config map[string]any) (cordis.Context, *captureLogger) {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	logger := &captureLogger{}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); err != nil {
		t.Fatalf("provide logger: %v", err)
	}
	if _, err := root.Provide(contracts.KeySettings, settings); err != nil {
		t.Fatalf("provide settings: %v", err)
	}
	registry := module.NewRegistry()
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register module: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "toolkit", Module: "zeokeel/toolkit", Config: config,
	}})
	if err != nil {
		t.Fatalf("load toolkit: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	return root, logger
}

// fakeLSPRecords reads the recorded LSP methods/params of one test.
func fakeLSPRecords(t *testing.T, recordPath string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read LSP record: %v", err)
	}
	records := make([]map[string]any, 0, 8)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		records = append(records, entry)
	}
	return records
}

// lspMethodParams returns the params of the first recorded message with method.
func lspMethodParams(records []map[string]any, method string) (string, bool) {
	for _, entry := range records {
		if entry["method"] != method {
			continue
		}
		params := ""
		if paramsValue, ok := entry["params"]; ok && paramsValue != nil {
			encoded, err := json.Marshal(paramsValue)
			if err == nil {
				params = string(encoded)
			}
		}
		return params, true
	}
	return "", false
}

// TestToolkitLSPPerformsInitializeHandshake is the FR-04 regression: the rig
// client needs initialize before document requests, so the toolkit must run it
// when it starts the server (a spec-compliant server rejects pre-initialize
// requests).
func TestToolkitLSPPerformsInitializeHandshake(t *testing.T) {
	workspace := t.TempDir()
	sourcePath := filepath.Join(workspace, "main.go")
	if err := os.WriteFile(sourcePath, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	record := filepath.Join(t.TempDir(), "lsp-record.jsonl")
	raw, err := contracts.EncodeLSPServerConfig(contracts.LSPServerConfig{
		Name:    "fake",
		Command: lspSelfCommand(t),
		Env:     []string{fakeLSPModeEnv + "=ok", fakeLSPRecordEnv + "=" + record},
		RootURI: (&url.URL{Scheme: "file", Path: filepath.ToSlash(workspace)}).String(),
	})
	if err != nil {
		t.Fatalf("encode LSP config: %v", err)
	}
	settings := newMutableSettings(map[string]string{contracts.SettingLSPServer: raw})
	root, logger := activateToolkitConfig(t, settings, map[string]any{"root": workspace})

	registry, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	names := make(map[string]bool)
	for _, spec := range registry.Specs() {
		names[spec.Name] = true
	}
	if !names["lsp_symbols"] || !names["lsp_hover"] {
		t.Fatalf("lsp tools not registered: %v", names)
	}
	result, invokeErr := registry.Invoke(context.Background(), rig.ToolCall{
		Name:      "lsp_symbols",
		Arguments: `{"path":"` + sourcePath + `"}`,
	})
	if invokeErr != nil || result.IsError {
		t.Fatalf("lsp_symbols after initialize = %q/%v, want a successful call", result.Content, invokeErr)
	}
	records := fakeLSPRecords(t, record)
	initializeIndex := -1
	didOpenIndex := -1
	for index, entry := range records {
		if entry["method"] == "initialize" && initializeIndex < 0 {
			initializeIndex = index
		}
		if entry["method"] == "textDocument/didOpen" && didOpenIndex < 0 {
			didOpenIndex = index
		}
	}
	if initializeIndex < 0 {
		t.Fatalf("server never received initialize (methods: %v); a compliant server rejects the call", records)
	}
	if didOpenIndex >= 0 && didOpenIndex < initializeIndex {
		t.Fatalf("didOpen sent before initialize: %v", records)
	}
	if _, ok := lspMethodParams(records, "textDocument/documentSymbol"); !ok {
		t.Fatalf("documentSymbol request missing: %v", records)
	}
	if logger.contains("initialize") {
		t.Logf("lsp initialize log: %v", logger.lines)
	}
}

// TestToolkitLSPEnforcesSandboxPath is the FR-05 regression: an lsp_* call with
// a path outside the workspace (or matching deniedGlobs) is rejected before the
// file is read, while a relative workspace path resolves against the workspace
// root.
func TestToolkitLSPEnforcesSandboxPath(t *testing.T) {
	workspace := t.TempDir()
	insideRelative := "src/main.go"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(workspace, insideRelative)), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, insideRelative), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	denied := filepath.Join(workspace, "keys.secret")
	if err := os.WriteFile(denied, []byte("TOP-SECRET\n"), 0o600); err != nil {
		t.Fatalf("write denied file: %v", err)
	}
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outside, []byte("OUTSIDE\n"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	record := filepath.Join(t.TempDir(), "lsp-record.jsonl")
	raw, err := contracts.EncodeLSPServerConfig(contracts.LSPServerConfig{
		Name:    "fake",
		Command: lspSelfCommand(t),
		Env:     []string{fakeLSPModeEnv + "=ok", fakeLSPRecordEnv + "=" + record},
	})
	if err != nil {
		t.Fatalf("encode LSP config: %v", err)
	}
	settings := newMutableSettings(map[string]string{contracts.SettingLSPServer: raw})
	root, _ := activateToolkitConfig(t, settings, map[string]any{
		"root":        workspace,
		"deniedGlobs": []any{"**/*.secret"},
	})
	registry, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}

	for name, path := range map[string]string{
		"outside-root": outside,
		"denied-glob":  denied,
	} {
		result, invokeErr := registry.Invoke(context.Background(), rig.ToolCall{
			Name:      "lsp_symbols",
			Arguments: `{"path":` + lspQuoteJSON(path) + `}`,
		})
		if invokeErr != nil {
			t.Fatalf("%s: unexpected error: %v", name, invokeErr)
		}
		if !result.IsError || !strings.Contains(result.Content, "path blocked by policy") {
			t.Fatalf("%s: lsp_symbols = %q (isError=%v), want a policy rejection", name, result.Content, result.IsError)
		}
	}
	records := fakeLSPRecords(t, record)
	if params, ok := lspMethodParams(records, "textDocument/didOpen"); ok {
		if strings.Contains(params, outside) || strings.Contains(params, denied) {
			t.Fatalf("a policy-rejected path reached the language server: %s", params)
		}
	}

	// A relative workspace path is resolved against the workspace root.
	result, invokeErr := registry.Invoke(context.Background(), rig.ToolCall{
		Name:      "lsp_symbols",
		Arguments: `{"path":` + lspQuoteJSON(insideRelative) + `}`,
	})
	if invokeErr != nil || result.IsError {
		t.Fatalf("relative workspace path = %q/%v, want a successful call", result.Content, invokeErr)
	}
	records = fakeLSPRecords(t, record)
	params, ok := lspMethodParams(records, "textDocument/didOpen")
	if !ok || !strings.Contains(params, filepath.ToSlash(filepath.Join(workspace, insideRelative))) {
		t.Fatalf("relative path not resolved to the workspace root: %s", params)
	}
}

// TestToolkitLSPFailedInitializeRegistersNoTools covers the degraded path: a
// language server that dies during initialize must not fail activation and must
// not leave lsp_* tools that can only error.
func TestToolkitLSPFailedInitializeRegistersNoTools(t *testing.T) {
	raw, err := contracts.EncodeLSPServerConfig(contracts.LSPServerConfig{
		Name:    "fake",
		Command: lspSelfCommand(t),
		Env:     []string{fakeLSPModeEnv + "=crash"},
	})
	if err != nil {
		t.Fatalf("encode LSP config: %v", err)
	}
	settings := newMutableSettings(map[string]string{contracts.SettingLSPServer: raw})
	root, logger := activateToolkitConfig(t, settings, map[string]any{"root": t.TempDir()})
	registry, err := root.Require(contracts.KeyToolkit)
	if err != nil {
		t.Fatalf("require toolkit: %v", err)
	}
	for _, spec := range registry.Specs() {
		if strings.HasPrefix(spec.Name, "lsp_") {
			t.Fatalf("lsp tool %q registered after a failed initialize (want degraded: none)", spec.Name)
		}
	}
	if !logger.contains("initializ") {
		t.Fatalf("failed initialize was not logged: %v", logger.lines)
	}
}

// quoteJSON quotes one string as a JSON value.
func lspQuoteJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
