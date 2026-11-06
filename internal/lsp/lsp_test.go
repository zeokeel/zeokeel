package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeOperation(t *testing.T) {
	cases := []struct {
		in   string
		want Operation
	}{
		{"goToDefinition", OpGoToDefinition},
		{"gotoDefinition", OpGoToDefinition},
		{"definition", OpGoToDefinition},
		{" findReferences ", OpFindReferences},
		{"references", OpFindReferences},
		{"goToImplementation", OpGoToImplementation},
		{"GoToImplementation", OpGoToImplementation},
		{"implementation", OpGoToImplementation},
		{"hover", OpHover},
		{"HOVER", OpHover},
	}
	for _, tc := range cases {
		got, err := NormalizeOperation(tc.in)
		if err != nil {
			t.Errorf("NormalizeOperation(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeOperation(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	for _, bad := range []string{"rename", "symbols", "goToTypeDefinition"} {
		if _, err := NormalizeOperation(bad); err == nil {
			t.Errorf("NormalizeOperation(%q) succeeded, want an error", bad)
		}
	}
	_, err := NormalizeOperation("rename")
	if err == nil || !strings.Contains(err.Error(), "rename") || !strings.Contains(err.Error(), string(OpGoToDefinition)) {
		t.Errorf("unknown operation error = %v, want it to name the input and the valid operations", err)
	}
}

func TestLanguageForPath(t *testing.T) {
	cases := map[string]string{
		"main.go":                 "go",
		"pkg/dir/main.GO":         "go",
		"script.py":               "python",
		"types.ts":                "typescript",
		"view.tsx":                "typescript",
		"app.js":                  "javascript",
		"app.jsx":                 "javascript",
		"lib.rs":                  "rust",
		"Main.java":               "java",
		"config.json":             "json",
		"config.yaml":             "yaml",
		"config.yml":              "yaml",
		"main.cpp":                "cpp",
		"main.cc":                 "cpp",
		"main.c":                  "c",
		"main.h":                  "c",
		"main.hpp":                "cpp",
		"init.lua":                "lua",
		"app.rb":                  "ruby",
		"index.php":               "php",
		"Program.cs":              "csharp",
		"Main.kt":                 "kotlin",
		"main.swift":              "swift",
		"build.scala":             "scala",
		"notes.xyz":               "",
		"README":                  "",
		".gitignore":              "",
		"dir.ext/README":          "",
		"with space/trailing.txt": "",
	}
	for path, want := range cases {
		if got := LanguageForPath(path); got != want {
			t.Errorf("LanguageForPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestUTF16Len(t *testing.T) {
	cases := map[string]int{
		"":      0,
		"abc":   3,
		"中文":    2,
		"😀":     2,
		"a😀中":   4,
		"日本語ok": 5,
	}
	for in, want := range cases {
		if got := UTF16Len(in); got != want {
			t.Errorf("UTF16Len(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestValidatePosition(t *testing.T) {
	text := "😀foo\n中文target\nlast"
	cases := []struct {
		name   string
		line   int
		column int
		want   Position
	}{
		{"start", 1, 1, Position{Line: 0, Character: 0}},
		{"after emoji", 1, 3, Position{Line: 0, Character: 2}},
		{"end of first line", 1, 6, Position{Line: 0, Character: 5}},
		{"chinese line", 2, 3, Position{Line: 1, Character: 2}},
		{"last line", 3, 5, Position{Line: 2, Character: 4}},
	}
	for _, tc := range cases {
		got, err := ValidatePosition(text, tc.line, tc.column)
		if err != nil {
			t.Errorf("%s: ValidatePosition(%d,%d): %v", tc.name, tc.line, tc.column, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: ValidatePosition(%d,%d) = %+v, want %+v", tc.name, tc.line, tc.column, got, tc.want)
		}
	}
	// A lone emoji is two UTF-16 code units, so column 3 is the (valid) end
	// position. A rune-based conversion would wrongly reject it.
	if _, err := ValidatePosition("😀", 1, 3); err != nil {
		t.Errorf("ValidatePosition(emoji end column 3): %v", err)
	}
	bad := []struct {
		name   string
		line   int
		column int
		want   string
	}{
		{"zero line", 0, 1, "line"},
		{"past last line", 4, 1, "outside the file"},
		{"zero column", 1, 0, "column"},
		{"past emoji line", 1, 7, "outside line 1"},
		{"past chinese line", 2, 10, "outside line 2"},
	}
	for _, tc := range bad {
		_, err := ValidatePosition(text, tc.line, tc.column)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: ValidatePosition(%d,%d) error = %v, want it to mention %q", tc.name, tc.line, tc.column, err, tc.want)
		}
	}
	// CRLF files count lines the LSP way.
	crlf := "one\r\ntwo\r\n"
	if got, err := ValidatePosition(crlf, 2, 4); err != nil || got != (Position{Line: 1, Character: 3}) {
		t.Errorf("ValidatePosition(CRLF, 2, 4) = %+v, %v", got, err)
	}
	// An empty document still has line 1, column 1.
	if got, err := ValidatePosition("", 1, 1); err != nil || got != (Position{}) {
		t.Errorf("ValidatePosition(empty, 1, 1) = %+v, %v", got, err)
	}
}

func TestPathURIRoundTrip(t *testing.T) {
	dir := t.TempDir()
	paths := []string{
		filepath.Join(dir, "main.go"),
		filepath.Join(dir, "dir with spaces", "中文 😀.go"),
	}
	for _, path := range paths {
		uri := PathToURI(path)
		if !strings.HasPrefix(uri, "file://") {
			t.Errorf("PathToURI(%q) = %q, want a file:// URI", path, uri)
			continue
		}
		if strings.ContainsAny(uri, " #?") {
			t.Errorf("PathToURI(%q) = %q, want the path escaped", path, uri)
		}
		got, err := URIToPath(uri)
		if err != nil {
			t.Errorf("URIToPath(%q): %v", uri, err)
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			t.Fatalf("Abs(%q): %v", path, err)
		}
		if filepath.Clean(got) != filepath.Clean(abs) {
			t.Errorf("URI round trip for %q: %q -> %q -> %q", path, uri, got, abs)
		}
	}
}

func TestURIToPathRejectsNonFile(t *testing.T) {
	for _, uri := range []string{"https://example.test/x.go", "untitled:Untitled-1", "dsh-session:abc"} {
		if _, err := URIToPath(uri); err == nil {
			t.Errorf("URIToPath(%q) succeeded, want an error", uri)
		}
	}
}

func TestNormalizeHover(t *testing.T) {
	markup, _ := json.Marshal(map[string]string{"kind": "markdown", "value": "```go\nfunc Foo() int\n```"})
	if got, err := NormalizeHover(markup); err != nil || !strings.Contains(got, "func Foo() int") {
		t.Errorf("NormalizeHover(MarkupContent) = %q, %v", got, err)
	}
	plain, _ := json.Marshal(map[string]string{"kind": "plaintext", "value": "func Foo() int"})
	if got, err := NormalizeHover(plain); err != nil || got != "func Foo() int" {
		t.Errorf("NormalizeHover(plaintext) = %q, %v", got, err)
	}
	legacy, _ := json.Marshal("func Foo() int")
	if got, err := NormalizeHover(legacy); err != nil || got != "func Foo() int" {
		t.Errorf("NormalizeHover(string) = %q, %v", got, err)
	}
	marked, _ := json.Marshal([]any{"first line", map[string]string{"language": "go", "value": "func Foo() int"}})
	got, err := NormalizeHover(marked)
	if err != nil {
		t.Fatalf("NormalizeHover(MarkedString[]) error = %v", err)
	}
	if !strings.Contains(got, "first line") || !strings.Contains(got, "func Foo() int") {
		t.Errorf("NormalizeHover(MarkedString[]) = %q, want both entries", got)
	}
	for _, empty := range []json.RawMessage{nil, json.RawMessage("null"), json.RawMessage(`""`)} {
		if got, err := NormalizeHover(empty); err != nil || got != "" {
			t.Errorf("NormalizeHover(%q) = %q, %v, want empty", empty, got, err)
		}
	}
	if _, err := NormalizeHover(json.RawMessage(`42`)); err == nil {
		t.Error("NormalizeHover(42) succeeded, want an error for an unsupported shape")
	}
}

func TestJSONRPCFramingRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	first := wireMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage("7"),
		Method:  "initialize",
		Params:  json.RawMessage(`{"rootUri":"file:///tmp/x","text":"中文😀"}`),
	}
	if err := writeMessage(&buf, first); err != nil {
		t.Fatalf("writeMessage: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "Content-Length: ") {
		t.Fatalf("framed message = %q, want a Content-Length header", buf.String())
	}
	if !strings.Contains(buf.String(), "\r\n\r\n") {
		t.Fatalf("framed message = %q, want a CRLF header terminator", buf.String())
	}
	reader := bufio.NewReader(&buf)
	got, err := readMessage(reader)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if got.JSONRPC != "2.0" || got.Method != "initialize" || string(got.ID) != "7" {
		t.Fatalf("round trip = %+v", got)
	}
	var wantParams, gotParams map[string]any
	_ = json.Unmarshal(first.Params, &wantParams)
	if err := json.Unmarshal(got.Params, &gotParams); err != nil {
		t.Fatalf("round trip params: %v", err)
	}
	if gotParams["text"] != wantParams["text"] {
		t.Fatalf("round trip params = %v, want %v", gotParams, wantParams)
	}

	second := wireMessage{JSONRPC: "2.0", ID: json.RawMessage("8"), Result: json.RawMessage("null")}
	if err := writeMessage(&buf, second); err != nil {
		t.Fatalf("writeMessage(second): %v", err)
	}
	got, err = readMessage(reader)
	if err != nil {
		t.Fatalf("readMessage(second): %v", err)
	}
	if string(got.ID) != "8" || string(got.Result) != "null" {
		t.Fatalf("second round trip = %+v", got)
	}
}

func TestReadMessageRejectsMalformedFrames(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"missing content length", "X-Foo: 1\r\n\r\n{}", "Content-Length"},
		{"bad content length", "Content-Length: nope\r\n\r\n{}", "Content-Length"},
		{"negative content length", "Content-Length: -3\r\n\r\n{}", "Content-Length"},
		{"invalid json body", "Content-Length: 5\r\n\r\n{oops", "JSON"},
		{"truncated body", "Content-Length: 100\r\n\r\n{}", "Content-Length"},
	}
	for _, tc := range cases {
		_, err := readMessage(bufio.NewReader(strings.NewReader(tc.input)))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: readMessage error = %v, want it to mention %q", tc.name, err, tc.want)
		}
	}
}
