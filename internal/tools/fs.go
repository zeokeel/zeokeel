package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const readFileDescription = `Read a UTF-8 text file inside the workspace and return its contents.

The path is relative to the current working directory (the workspace root by default; the working_directory tool can change it). Directories, binary files (NUL bytes or invalid UTF-8) and files larger than 1 MiB are rejected.

Args JSON schema:
{"type":"object","properties":{"path":{"type":"string","description":"Path relative to the current working directory (workspace root by default) of the file to read."}},"required":["path"],"additionalProperties":false}`

type readFileTool struct {
	sb *sandbox
}

func (t *readFileTool) Name() string        { return "read_file" }
func (t *readFileTool) Description() string { return readFileDescription }

func (t *readFileTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	path, err := t.sb.resolve(a.Path)
	if err != nil {
		return "", fmt.Errorf("read_file: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read_file: %q: %w", a.Path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("read_file: %q is a directory", a.Path)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("read_file: %q is not a regular file", a.Path)
	}
	if info.Size() > MaxReadFileBytes {
		return "", fmt.Errorf("read_file: %q is %d bytes, larger than the %d byte limit", a.Path, info.Size(), MaxReadFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read_file: %q: %w", a.Path, err)
	}
	if len(data) > MaxReadFileBytes {
		return "", fmt.Errorf("read_file: %q is larger than the %d byte limit", a.Path, MaxReadFileBytes)
	}
	if isBinary(data) {
		return "", fmt.Errorf("read_file: %q is not valid UTF-8 text (binary file)", a.Path)
	}
	return string(data), nil
}

const writeFileDescription = `Write a UTF-8 text file inside the workspace, creating parent directories as needed.

The path is relative to the current working directory (the workspace root by default; the working_directory tool can change it). Existing files are overwritten.

Args JSON schema:
{"type":"object","properties":{"path":{"type":"string","description":"Path relative to the current working directory (workspace root by default) of the file to write."},"content":{"type":"string","description":"Full file contents."}},"required":["path","content"],"additionalProperties":false}`

type writeFileTool struct {
	sb *sandbox
}

func (t *writeFileTool) Name() string        { return "write_file" }
func (t *writeFileTool) Description() string { return writeFileDescription }

func (t *writeFileTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	path, err := t.sb.resolve(a.Path)
	if err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("write_file: %q is a directory", a.Path)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("write_file: %q is not a regular file", a.Path)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("write_file: %q: %w", a.Path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("write_file: create parent directories for %q: %w", a.Path, err)
	}
	if err := os.WriteFile(path, []byte(a.Content), 0o644); err != nil {
		return "", fmt.Errorf("write_file: %q: %w", a.Path, err)
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), t.sb.relative(path)), nil
}

const editFileDescription = `Replace exactly one occurrence of old text with new text in a UTF-8 file inside the workspace.

The path is relative to the current working directory (the workspace root by default; the working_directory tool can change it). The operation fails unless old occurs exactly once. Use read_file first to check the file contents.

Args JSON schema:
{"type":"object","properties":{"path":{"type":"string","description":"Path relative to the current working directory (workspace root by default) of the file to edit."},"old":{"type":"string","description":"Exact text to replace; must occur exactly once."},"new":{"type":"string","description":"Replacement text."}},"required":["path","old","new"],"additionalProperties":false}`

type editFileTool struct {
	sb *sandbox
}

func (t *editFileTool) Name() string        { return "edit_file" }
func (t *editFileTool) Description() string { return editFileDescription }

func (t *editFileTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Path string `json:"path"`
		Old  string `json:"old"`
		New  string `json:"new"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	if a.Old == "" {
		return "", fmt.Errorf("edit_file: old text is required")
	}
	path, err := t.sb.resolve(a.Path)
	if err != nil {
		return "", fmt.Errorf("edit_file: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("edit_file: %q: %w", a.Path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("edit_file: %q is a directory", a.Path)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("edit_file: %q is not a regular file", a.Path)
	}
	if info.Size() > MaxReadFileBytes {
		return "", fmt.Errorf("edit_file: %q is %d bytes, larger than the %d byte limit", a.Path, info.Size(), MaxReadFileBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("edit_file: %q: %w", a.Path, err)
	}
	if len(data) > MaxReadFileBytes {
		return "", fmt.Errorf("edit_file: %q is larger than the %d byte limit", a.Path, MaxReadFileBytes)
	}
	if isBinary(data) {
		return "", fmt.Errorf("edit_file: %q is not valid UTF-8 text (binary file)", a.Path)
	}
	content := string(data)
	switch n := strings.Count(content, a.Old); {
	case n == 0:
		return "", fmt.Errorf("edit_file: old text not found in %q", a.Path)
	case n > 1:
		return "", fmt.Errorf("edit_file: old text occurs %d times in %q; exactly one occurrence is required", n, a.Path)
	}
	updated := strings.Replace(content, a.Old, a.New, 1)
	if err := os.WriteFile(path, []byte(updated), info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("edit_file: write %q: %w", a.Path, err)
	}
	return fmt.Sprintf("edited %s (%d bytes -> %d bytes)", t.sb.relative(path), len(data), len(updated)), nil
}

// isBinary reports whether data looks like a binary file rather than UTF-8
// text. A NUL byte or invalid UTF-8 is treated as binary.
func isBinary(data []byte) bool {
	return bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)
}
