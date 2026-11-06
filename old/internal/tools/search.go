package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	maxGlobEntries = 200
	maxGrepMatches = 200
)

const globDescription = `Find files and directories by glob pattern inside the workspace.

Uses filepath.Glob semantics: "*", "?" and "[...]" within a single path segment. The pattern is relative to the current working directory (the workspace root by default; the working_directory tool can change it). Results are sorted, limited to 200 entries, and printed one workspace-root-relative path per line. Symlinks that point outside the workspace are ignored.

Args JSON schema:
{"type":"object","properties":{"pattern":{"type":"string","description":"Glob pattern relative to the current working directory (workspace root by default), for example \"*.go\" or \"internal/*/*.go\"."}},"required":["pattern"],"additionalProperties":false}`

type globTool struct {
	sb *sandbox
}

func (t *globTool) Name() string        { return "glob" }
func (t *globTool) Description() string { return globDescription }

func (t *globTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Pattern string `json:"pattern"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	clean, err := cleanRelativePath(a.Pattern)
	if err != nil {
		return "", fmt.Errorf("glob: %w", err)
	}
	base := "."
	if t.sb.cwd != nil {
		if cwd := t.sb.cwd(); cwd != "" {
			base = cwd
		}
	}
	full := filepath.Join(t.sb.root, filepath.FromSlash(base), clean)
	if err := t.sb.ensureWithin(full); err != nil {
		return "", fmt.Errorf("glob: %w", err)
	}
	matches, err := filepath.Glob(full)
	if err != nil {
		return "", fmt.Errorf("glob: invalid pattern %q: %w", a.Pattern, err)
	}
	sort.Strings(matches)

	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(out) >= maxGlobEntries {
			break
		}
		real, err := filepath.EvalSymlinks(match)
		if err != nil || !t.sb.contains(real) {
			// A broken symlink or a symlink out of the root is not visible.
			continue
		}
		out = append(out, t.sb.relative(match))
	}
	return strings.Join(out, "\n"), nil
}

const grepDescription = `Search file contents with a regular expression inside the workspace.

Recursively scans text files under path and prints up to 200 matching lines as "path:line: text". The path is relative to the current working directory (the workspace root by default; the working_directory tool can change it); when omitted it defaults to that directory. Binary files are skipped. The pattern is a Go regular expression.

Args JSON schema:
{"type":"object","properties":{"pattern":{"type":"string","description":"Go regular expression to match against each line."},"path":{"type":"string","description":"Optional file or directory relative to the current working directory (workspace root by default) to search."}},"required":["pattern"],"additionalProperties":false}`

type grepTool struct {
	sb *sandbox
}

func (t *grepTool) Name() string        { return "grep" }
func (t *grepTool) Description() string { return grepDescription }

var errGrepLimit = errors.New("grep: match limit reached")

func (t *grepTool) Invoke(_ context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := decodeArgs(t.Name(), args, &a); err != nil {
		return "", err
	}
	re, err := regexp.Compile(a.Pattern)
	if err != nil {
		return "", fmt.Errorf("grep: invalid regular expression %q: %w", a.Pattern, err)
	}
	target := a.Path
	if target == "" {
		target = "."
	}
	path, err := t.sb.resolve(target)
	if err != nil {
		return "", fmt.Errorf("grep: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("grep: %q: %w", target, err)
	}

	var matches []string
	if info.IsDir() {
		err = filepath.WalkDir(path, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if len(matches) >= maxGrepMatches {
				return errGrepLimit
			}
			display := t.sb.relative(p)
			filePath := p
			if d.Type()&fs.ModeSymlink != 0 {
				// Follow a symlink only when it stays inside the root; a
				// symlinked directory is skipped to avoid loops.
				real, err := filepath.EvalSymlinks(p)
				if err != nil || !t.sb.contains(real) {
					return nil
				}
				info, err := os.Stat(real)
				if err != nil || !info.Mode().IsRegular() {
					return nil
				}
				filePath = real
			} else if !d.Type().IsRegular() {
				return nil
			}
			found, err := grepFile(filePath, display, re, maxGrepMatches-len(matches))
			if err != nil {
				// Unreadable files are skipped so one bad file cannot abort a
				// workspace-wide search.
				return nil
			}
			matches = append(matches, found...)
			return nil
		})
		if err != nil && !errors.Is(err, errGrepLimit) {
			return "", fmt.Errorf("grep: %w", err)
		}
	} else if info.Mode().IsRegular() {
		matches, err = grepFile(path, t.sb.relative(path), re, maxGrepMatches)
		if err != nil {
			return "", fmt.Errorf("grep: %q: %w", target, err)
		}
	} else {
		return "", fmt.Errorf("grep: %q is not a regular file or directory", target)
	}

	if len(matches) > maxGrepMatches {
		matches = matches[:maxGrepMatches]
	}
	return strings.Join(matches, "\n"), nil
}

// grepFile returns up to remaining "path:line: text" matches from one file.
// Binary files yield no matches. A scanner error (for example a single line
// over 1 MiB) ends the scan but keeps matches found so far.
func grepFile(path, display string, re *regexp.Regexp, remaining int) ([]string, error) {
	if remaining <= 0 {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	head := make([]byte, 8192)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	head = head[:n]
	if bytes.IndexByte(head, 0) >= 0 {
		return nil, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var out []string
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if !re.MatchString(line) {
			continue
		}
		out = append(out, fmt.Sprintf("%s:%d: %s", display, lineNo, line))
		if len(out) >= remaining {
			break
		}
	}
	return out, nil
}
