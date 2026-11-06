package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The fake language server runs as a subprocess of this same test binary: the
// test binary re-executes itself with LSPTEST_MODE set, TestMain dispatches to
// runFakeLSPServer, and stdout/stdin become the JSON-RPC transport. No `go run`
// and no external server binary are needed.
const (
	fakeLSPModeEnv     = "LSPTEST_MODE"
	fakeLSPExpectLine  = "LSPTEST_EXPECT_LINE"
	fakeLSPExpectChar  = "LSPTEST_EXPECT_CHAR"
	fakeLSPExpectText  = "LSPTEST_EXPECT_TEXT"
	fakeLSPExpectLang  = "LSPTEST_EXPECT_LANGUAGE"
	fakeLSPExitTimeout = 60 * time.Second
)

func TestMain(m *testing.M) {
	if mode := strings.TrimSpace(os.Getenv(fakeLSPModeEnv)); mode != "" {
		runFakeLSPServer(mode)
		return // runFakeLSPServer always exits the process
	}
	os.Exit(m.Run())
}

type fakeLSPDoc struct {
	uri      string
	language string
	text     string
	rootURI  string
	rootPath string
}

// runFakeLSPServer is the fake server's main loop. It speaks Content-Length
// framed JSON-RPC on stdin/stdout and never writes anything else to stdout.
func runFakeLSPServer(mode string) {
	if mode == "crash" {
		fmt.Fprintln(os.Stderr, "fake language server crashed on startup")
		os.Exit(3)
	}
	in := bufio.NewReader(os.Stdin)
	out := os.Stdout
	doc := &fakeLSPDoc{}
	answered := map[string]bool{}
	// In server_request mode the definition response is held until the client
	// has answered our two server-initiated requests; the request and the
	// answers may race on the wire, so the pending id is completed lazily.
	var pendingDefinition json.RawMessage
	serverRequestMode := mode == "server_request"
	respondPendingDefinition := func() {
		if pendingDefinition == nil || !answered["101"] || !answered["102"] {
			return
		}
		writeFakeResult(out, pendingDefinition, fakeDefinitionLocations(doc))
		pendingDefinition = nil
	}
	for {
		msg, err := readMessage(in)
		if err != nil {
			os.Exit(0)
		}
		if msg.Method == "" {
			// A response to one of our server-initiated requests.
			var id int
			_ = json.Unmarshal(msg.ID, &id)
			answered[strconv.Itoa(id)] = true
			respondPendingDefinition()
			continue
		}
		switch msg.Method {
		case "initialize":
			if mode == "hang" {
				time.Sleep(fakeLSPExitTimeout)
				os.Exit(0)
			}
			var params struct {
				RootURI string `json:"rootUri"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			doc.rootURI = params.RootURI
			if params.RootURI != "" {
				if path, err := URIToPath(params.RootURI); err == nil {
					doc.rootPath = path
				}
			}
			capabilities := map[string]any{
				"definitionProvider":     true,
				"referencesProvider":     true,
				"implementationProvider": true,
				"hoverProvider":          true,
				"positionEncoding":       "utf-16",
			}
			if mode == "nocap" {
				capabilities["definitionProvider"] = false
				capabilities["implementationProvider"] = false
			}
			writeFakeResult(out, msg.ID, map[string]any{
				"capabilities": capabilities,
				"serverInfo":   map[string]any{"name": "fake-lsp", "version": "0.1.0"},
			})
		case "initialized":
			if mode == "server_request" {
				writeFakeRequest(out, json.RawMessage("101"), "workspace/configuration",
					map[string]any{"items": []any{map[string]any{}}})
				writeFakeRequest(out, json.RawMessage("102"), "client/registerCapability",
					map[string]any{"registrations": []any{}})
			}
		case "textDocument/didOpen":
			var params struct {
				TextDocument struct {
					URI        string `json:"uri"`
					LanguageID string `json:"languageId"`
					Text       string `json:"text"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(msg.Params, &params)
			doc.uri = params.TextDocument.URI
			doc.language = params.TextDocument.LanguageID
			doc.text = params.TextDocument.Text
		case "textDocument/definition":
			if mode == "crash_mid" {
				fmt.Fprintln(os.Stderr, "fake language server crashed during the request")
				os.Exit(4)
			}
			if mode == "rpc_error" {
				writeFakeError(out, msg.ID, -32603, "internal explosion")
				continue
			}
			if mode == "null_result" {
				writeFakeResult(out, msg.ID, nil)
				continue
			}
			if problem := checkFakeExpectations(doc, msg.Params); problem != "" {
				writeFakeError(out, msg.ID, -32000, problem)
				continue
			}
			if serverRequestMode && (!answered["101"] || !answered["102"]) {
				pendingDefinition = msg.ID
				continue
			}
			writeFakeResult(out, msg.ID, fakeDefinitionLocations(doc))
		case "textDocument/references":
			writeFakeResult(out, msg.ID, fakeReferenceLocations(doc))
		case "textDocument/implementation":
			writeFakeResult(out, msg.ID, map[string]any{
				"targetUri": doc.uri,
				"targetRange": map[string]any{
					"start": map[string]int{"line": 3, "character": 0},
					"end":   map[string]int{"line": 3, "character": 4},
				},
				"targetSelectionRange": map[string]any{
					"start": map[string]int{"line": 3, "character": 0},
					"end":   map[string]int{"line": 3, "character": 4},
				},
			})
		case "textDocument/hover":
			if mode == "legacy_hover" {
				writeFakeResult(out, msg.ID, map[string]any{
					"contents": []any{"first line", map[string]string{"language": "go", "value": "func Foo() int"}},
				})
				continue
			}
			writeFakeResult(out, msg.ID, map[string]any{
				"contents": map[string]string{
					"kind":  "markdown",
					"value": "```go\nfunc Foo(x int) int\n```\n\nDocs for Foo.",
				},
			})
		case "shutdown":
			writeFakeResult(out, msg.ID, nil)
		case "exit":
			os.Exit(0)
		default:
			if len(msg.ID) > 0 {
				writeFakeResult(out, msg.ID, nil)
			}
		}
	}
}

func fakeDefinitionLocations(doc *fakeLSPDoc) []map[string]any {
	sibling := ""
	if doc.rootPath != "" {
		sibling = PathToURI(filepath.Join(doc.rootPath, "sibling.go"))
	}
	return []map[string]any{
		{"uri": doc.uri, "range": fakeRange(1, 2, 1, 5)},
		{"uri": sibling, "range": fakeRange(0, 0, 0, 3)},
	}
}

func fakeReferenceLocations(doc *fakeLSPDoc) []map[string]any {
	return []map[string]any{
		{"uri": doc.uri, "range": fakeRange(0, 0, 0, 1)},
		{"uri": doc.uri, "range": fakeRange(1, 1, 1, 2)},
		{"uri": doc.uri, "range": fakeRange(2, 2, 2, 3)},
	}
}

func fakeRange(startLine, startChar, endLine, endChar int) map[string]any {
	return map[string]any{
		"start": map[string]int{"line": startLine, "character": startChar},
		"end":   map[string]int{"line": endLine, "character": endChar},
	}
}

func writeFakeResult(out *os.File, id json.RawMessage, result any) {
	raw := json.RawMessage("null")
	if result != nil {
		if encoded, err := json.Marshal(result); err == nil {
			raw = encoded
		}
	}
	_ = writeMessage(out, wireMessage{JSONRPC: "2.0", ID: id, Result: raw})
}

func writeFakeError(out *os.File, id json.RawMessage, code int, message string) {
	_ = writeMessage(out, wireMessage{JSONRPC: "2.0", ID: id, Error: &wireError{Code: code, Message: message}})
}

func writeFakeRequest(out *os.File, id json.RawMessage, method string, params any) {
	raw, _ := json.Marshal(params)
	_ = writeMessage(out, wireMessage{JSONRPC: "2.0", ID: id, Method: method, Params: raw})
}

// checkFakeExpectations lets a test pin what the client sent without an extra
// out-of-band channel: the values travel as environment variables of the fake
// server process.
func checkFakeExpectations(doc *fakeLSPDoc, params json.RawMessage) string {
	var query struct {
		Position struct {
			Line      int `json:"line"`
			Character int `json:"character"`
		} `json:"position"`
	}
	_ = json.Unmarshal(params, &query)
	if want := strings.TrimSpace(os.Getenv(fakeLSPExpectText)); want != "" && !strings.Contains(doc.text, want) {
		return fmt.Sprintf("didOpen text does not contain %q: %q", want, doc.text)
	}
	if want := strings.TrimSpace(os.Getenv(fakeLSPExpectLang)); want != "" && doc.language != want {
		return fmt.Sprintf("didOpen language = %q, want %q", doc.language, want)
	}
	if want := strings.TrimSpace(os.Getenv(fakeLSPExpectLine)); want != "" {
		wantLine, err := strconv.Atoi(want)
		if err != nil || query.Position.Line != wantLine {
			return fmt.Sprintf("position.line = %d, want %s", query.Position.Line, want)
		}
	}
	if want := strings.TrimSpace(os.Getenv(fakeLSPExpectChar)); want != "" {
		wantChar, err := strconv.Atoi(want)
		if err != nil || query.Position.Character != wantChar {
			return fmt.Sprintf("position.character = %d, want %s", query.Position.Character, want)
		}
	}
	return ""
}
