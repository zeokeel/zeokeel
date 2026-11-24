package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Workspace file access for the right inspector (R8–R10) and the composer's `@`
// file menu.
//
// The routes existed in the client from the start and the acceptance table marked
// them done, but the gateway never served them: `GET /api/files/tree` answered 404,
// so the file tree rendered empty and `@` silently offered nothing. This file is the
// missing half; the workspace service already owns every filesystem rule (root
// scoping, limits), so the transport only shapes and bounds its answers.

const (
	// filesTreeMaxNodes bounds one tree walk. A monorepo must not turn the
	// inspector into a multi-megabyte response.
	filesTreeMaxNodes = 4000
	// filesTreeMaxDepth bounds how deep the walk goes (defensive against symlink
	// loops; the workspace service lists one level at a time).
	filesTreeMaxDepth = 12
	// filesContentMaxBytes is the preview cap; a longer file reports truncated.
	filesContentMaxBytes = 256 << 10
)

// filesSkipDirs are directories no preview needs and every workspace has: walking
// them would spend the node budget before reaching real sources.
var filesSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".cache":       true,
	".gocache":     true,
	".venv":        true,
	"dist":         true,
	"build":        true,
	"__pycache__":  true,
}

type fileNodeDTO struct {
	Key     string `json:"key"`
	Path    string `json:"path"`
	Title   string `json:"title"`
	IsLeaf  bool   `json:"isLeaf"`
	Changed bool   `json:"changed,omitempty"`
}

type fileTreeDTO struct {
	Root  string        `json:"root"`
	Nodes []fileNodeDTO `json:"nodes"`
}

type fileContentDTO struct {
	Path      string `json:"path"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

type fileDiffDTO struct {
	Path      string `json:"path"`
	Unified   string `json:"unified"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// handleFiles serves /api/files/{tree,content,diff} for the current workspace.
func (g *Gateway) handleFiles(w http.ResponseWriter, r *http.Request) {
	if err := g.authorizeRequest(r, true); err != nil {
		status := http.StatusUnauthorized
		if err.Code == codeBadOrigin || err.Data["name"] == "bad_host" {
			status = http.StatusForbidden
		}
		writeProtocolError(w, status, err)
		return
	}
	workspace, ok := g.root.Get(contracts.KeyWorkspace)
	if !ok || workspace == nil {
		writeProtocolError(w, http.StatusServiceUnavailable, errNoWorkspace())
		return
	}
	ctx := r.Context()
	switch r.URL.Path {
	case "/api/files/tree":
		g.filesTree(ctx, w, workspace)
	case "/api/files/content":
		g.filesContent(ctx, w, r, workspace)
	case "/api/files/diff":
		g.filesDiff(ctx, w, r, workspace)
	default:
		writeProtocolError(w, http.StatusNotFound, errMethodNotFound(r.URL.Path))
	}
}

// filesTree returns the workspace tree as a flat node list, marking the files that
// have uncommitted changes (the inspector's change marker).
func (g *Gateway) filesTree(ctx context.Context, w http.ResponseWriter, workspace contracts.WorkspaceService) {
	current, ok, err := workspace.Current(ctx)
	if err != nil {
		writeProtocolError(w, http.StatusInternalServerError, internalError(err))
		return
	}
	if !ok {
		writeProtocolError(w, http.StatusServiceUnavailable, errNoWorkspace())
		return
	}
	changed := map[string]bool{}
	if differ, ok := workspace.(contracts.WorkspaceDiffer); ok {
		if changes, err := differ.Changes(ctx); err == nil {
			for _, change := range changes {
				changed[change.Path] = true
			}
		}
	}
	nodes := make([]fileNodeDTO, 0, 64)
	walkWorkspace(ctx, workspace, "", 0, &nodes, changed)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path < nodes[j].Path })
	writeJSON(w, http.StatusOK, fileTreeDTO{Root: current.Root, Nodes: nodes})
}

func walkWorkspace(ctx context.Context, workspace contracts.WorkspaceService, dir string, depth int,
	nodes *[]fileNodeDTO, changed map[string]bool) {
	if depth >= filesTreeMaxDepth || len(*nodes) >= filesTreeMaxNodes {
		return
	}
	entries, err := workspace.Files(ctx, dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if len(*nodes) >= filesTreeMaxNodes {
			return
		}
		if entry.Dir && filesSkipDirs[entry.Name] {
			continue
		}
		*nodes = append(*nodes, fileNodeDTO{
			Key:     entry.Path,
			Path:    entry.Path,
			Title:   entry.Name,
			IsLeaf:  !entry.Dir,
			Changed: !entry.Dir && changed[entry.Path],
		})
		if entry.Dir {
			walkWorkspace(ctx, workspace, entry.Path, depth+1, nodes, changed)
		}
	}
}

// filesContent serves one file's text. A binary file is a 415 rather than a screen
// of replacement characters; a path that escapes the workspace is a 400 before the
// service ever sees it.
func (g *Gateway) filesContent(ctx context.Context, w http.ResponseWriter, r *http.Request,
	workspace contracts.WorkspaceService) {
	target := strings.TrimSpace(r.URL.Query().Get("path"))
	if target == "" {
		writeProtocolError(w, http.StatusBadRequest, errInvalidParams("path", "is required"))
		return
	}
	if escapesWorkspace(target) {
		writeProtocolError(w, http.StatusBadRequest, errInvalidParams("path", "escapes the workspace"))
		return
	}
	data, err := workspace.ReadFile(ctx, target, filesContentMaxBytes)
	if err != nil {
		writeProtocolError(w, http.StatusNotFound, internalError(err))
		return
	}
	if isBinary(data) {
		writeProtocolError(w, http.StatusUnsupportedMediaType,
			protocolErrorWithMessage(codeInvalidParams, "binary file: the preview shows text only"))
		return
	}
	writeJSON(w, http.StatusOK, fileContentDTO{
		Path:      target,
		Text:      string(data),
		Truncated: len(data) >= filesContentMaxBytes,
	})
}

// filesDiff serves the uncommitted unified diff of one file.
func (g *Gateway) filesDiff(ctx context.Context, w http.ResponseWriter, r *http.Request,
	workspace contracts.WorkspaceService) {
	target := strings.TrimSpace(r.URL.Query().Get("path"))
	if target == "" {
		writeProtocolError(w, http.StatusBadRequest, errInvalidParams("path", "is required"))
		return
	}
	differ, ok := workspace.(contracts.WorkspaceDiffer)
	if !ok {
		writeProtocolError(w, http.StatusServiceUnavailable,
			protocolErrorWithMessage(codeCapabilityUnsupported, "workspace diff unavailable"))
		return
	}
	changes, err := differ.Changes(ctx)
	if err != nil {
		writeProtocolError(w, http.StatusInternalServerError, internalError(err))
		return
	}
	for _, change := range changes {
		if change.Path != target {
			continue
		}
		additions, deletions := countDiffLines(change.Diff)
		writeJSON(w, http.StatusOK, fileDiffDTO{
			Path:      target,
			Unified:   change.Diff,
			Additions: additions,
			Deletions: deletions,
		})
		return
	}
	writeProtocolError(w, http.StatusNotFound, errInvalidParams("path", "has no uncommitted changes"))
}

// escapesWorkspace reports whether a client-supplied path leaves the workspace.
func escapesWorkspace(target string) bool {
	if strings.HasPrefix(target, "/") || strings.Contains(target, "\\") {
		return true
	}
	cleaned := path.Clean(target)
	return cleaned == ".." || strings.HasPrefix(cleaned, "../")
}

// isBinary reports whether the bytes cannot be shown as text.
func isBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	if bytes := data; len(bytes) > 8192-1 {
		if strings.IndexByte(string(bytes[:8192]), 0) >= 0 {
			return true
		}
	} else if strings.IndexByte(string(bytes), 0) >= 0 {
		return true
	}
	return !utf8.Valid(data) && len(data) > 0 && !looksLikeTextLosingInvalid(data)
}

// looksLikeTextLosingInvalid keeps a mostly-text file (a stray byte) previewable
// instead of refusing it: only a file that is predominantly invalid is binary.
func looksLikeTextLosingInvalid(data []byte) bool {
	invalid := 0
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			invalid++
		}
		data = data[size:]
	}
	return invalid*10 < len(data)
}

// countDiffLines counts added and removed lines in one unified diff.
func countDiffLines(diff string) (additions, deletions int) {
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			continue
		case strings.HasPrefix(line, "+"):
			additions++
		case strings.HasPrefix(line, "-"):
			deletions++
		}
	}
	return additions, deletions
}

// errNoWorkspace is the honest answer when no workspace is open yet.
func errNoWorkspace() *protocolError {
	return protocolErrorWithMessage(codeCapabilityUnsupported, "no workspace is open")
}

// writeJSON writes one JSON success body.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// protocolErrorWithMessage builds a protocol error carrying the given sentence.
func protocolErrorWithMessage(code int, message string) *protocolError {
	return &protocolError{Code: code, Message: message}
}

var _ = errors.New
