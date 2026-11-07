// Package workspace implements ZeoKeel's workspace backend module: the durable
// project-root registry (the old shell's workspaces) and the sandboxed file
// reader/search behind the Files inspector.
//
// It is a kernel backend module like the session store: it requires the product
// data directory and the host logger, loads a JSON registry (creating an empty
// one when the file does not exist yet) and provides contracts.KeyWorkspace.
//
// The registry document is {"workspaces":[...],"currentId":"..."}; every
// mutation is written atomically (temporary file + Sync + rename in the same
// directory), so a crash cannot leave a half-written registry. File access is
// confined to the current workspace root: relative paths only, no ".." escapes,
// and symbolic links are resolved and re-checked against the canonical root.
package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/workspace"
	// Version is the module's semantic version.
	Version = "1.0.0"
	// DefaultFileName is the registry file name under the product data
	// directory.
	DefaultFileName = "workspaces.json"
	// DefaultReadLimitBytes caps one ReadFile (1 MiB).
	DefaultReadLimitBytes = 1 << 20
	// DefaultSearchDepth is how many directory levels Search descends into.
	DefaultSearchDepth = 6
	// DefaultSearchLimit caps one Search result set.
	DefaultSearchLimit = 50
	// fileMode is the registry file permission.
	fileMode = 0o600
	// dirMode is the registry directory permission.
	dirMode = 0o700
)

// Config is the module's typed assembly config.
type Config struct {
	// Path is the registry file: absolute, or relative to the product data
	// directory (default workspaces.json there).
	Path string `json:"path"`
	// ReadLimitBytes caps one ReadFile (0 = default 1 MiB).
	ReadLimitBytes int `json:"readLimitBytes"`
	// SearchDepth bounds the recursive Search directory depth (0 = default 6).
	SearchDepth int `json:"searchDepth"`
}

// Errors returned by the workspace service.
var (
	// ErrNoWorkspace reports a file operation without a current workspace.
	ErrNoWorkspace = errors.New("workspace: no current workspace")
	// ErrWorkspaceNotFound reports an unknown workspace ID.
	ErrWorkspaceNotFound = errors.New("workspace: not found")
	// ErrPathOutsideRoot reports a path escaping the workspace root.
	ErrPathOutsideRoot = errors.New("workspace: path is outside the workspace root")
	// ErrNotDirectory reports a root or directory argument that is not a
	// directory.
	ErrNotDirectory = errors.New("workspace: not a directory")
	// ErrNotFile reports a ReadFile target that is not a regular file.
	ErrNotFile = errors.New("workspace: not a regular file")
	// ErrInvalidLimit reports a negative ReadFile/Search limit.
	ErrInvalidLimit = errors.New("workspace: invalid limit")
)

// Module is the workspace backend descriptor. It provides
// contracts.KeyWorkspace.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		dataDir, err := ctx.Require(contracts.KeyDataDir)
		if err != nil {
			return fmt.Errorf("%s: require data directory: %w", ModuleID, err)
		}
		logger, err := ctx.Require(contracts.KeyLogger)
		if err != nil {
			return fmt.Errorf("%s: require logger: %w", ModuleID, err)
		}
		service, err := newService(dataDir, cfg, logger)
		if err != nil {
			return fmt.Errorf("%s: open workspace registry: %w", ModuleID, err)
		}
		if _, err := ctx.Provide(contracts.KeyWorkspace, contracts.WorkspaceService(service)); err != nil {
			return fmt.Errorf("%s: provide workspace service: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyWorkspace)).
		AddRequires(
			module.Requires(contracts.KeyDataDir),
			module.Requires(contracts.KeyLogger),
		).
		WithName("ZeoKeel workspace registry").
		WithVersion(Version).
		WithValidate(validateConfig)
}

// validateConfig rejects configs the service could not honour.
func validateConfig(cfg Config) error {
	if strings.ContainsRune(cfg.Path, 0) {
		return fmt.Errorf("%s: path contains a NUL byte", ModuleID)
	}
	if cfg.ReadLimitBytes < 0 {
		return fmt.Errorf("%s: readLimitBytes must not be negative", ModuleID)
	}
	if cfg.SearchDepth < 0 {
		return fmt.Errorf("%s: searchDepth must not be negative", ModuleID)
	}
	return nil
}

// --- persistence document --------------------------------------------------

// workspaceRecord is one workspace in the JSON registry.
type workspaceRecord struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Root      string    `json:"root"`
	CreatedAt time.Time `json:"createdAt"`
}

// registryDocument is the on-disk registry.
type registryDocument struct {
	Workspaces []workspaceRecord `json:"workspaces"`
	CurrentID  string            `json:"currentId,omitempty"`
}

// --- service ---------------------------------------------------------------

// Service is the contracts.WorkspaceService implementation.
type Service struct {
	mu     sync.Mutex
	path   string
	logger contracts.Logger

	readLimit   int
	searchDepth int

	workspaces []contracts.Workspace
	currentID  string
}

var _ contracts.WorkspaceService = (*Service)(nil)

// newService resolves the registry path and loads the existing document. A
// missing file starts an empty registry; an unreadable or malformed file is a
// startup error (wrapped, never a panic).
func newService(dataDir string, cfg Config, logger contracts.Logger) (*Service, error) {
	path, err := resolveRegistryPath(dataDir, cfg.Path)
	if err != nil {
		return nil, err
	}
	readLimit := cfg.ReadLimitBytes
	if readLimit == 0 {
		readLimit = DefaultReadLimitBytes
	}
	searchDepth := cfg.SearchDepth
	if searchDepth == 0 {
		searchDepth = DefaultSearchDepth
	}
	if logger == nil {
		logger = discardLogger{}
	}
	service := &Service{
		path:        path,
		logger:      logger,
		readLimit:   readLimit,
		searchDepth: searchDepth,
	}
	if err := service.load(); err != nil {
		return nil, err
	}
	return service, nil
}

// discardLogger drops workspace log lines.
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// resolveRegistryPath resolves Config.Path against the product data directory.
func resolveRegistryPath(dataDir, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = DefaultFileName
	}
	if strings.ContainsRune(requested, 0) {
		return "", fmt.Errorf("%s: path contains a NUL byte", ModuleID)
	}
	if filepath.IsAbs(requested) {
		return filepath.Clean(requested), nil
	}
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return "", fmt.Errorf("%s: data directory is empty", ModuleID)
	}
	return filepath.Join(dataDir, filepath.FromSlash(requested)), nil
}

// Path returns the resolved registry file path (diagnostics and tests).
func (s *Service) Path() string { return s.path }

// load reads the registry, treating a missing file as empty.
func (s *Service) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: read %q: %w", ModuleID, s.path, err)
	}
	document, err := decodeRegistry(data)
	if err != nil {
		return fmt.Errorf("%s: parse %q: %w", ModuleID, s.path, err)
	}
	workspaces := make([]contracts.Workspace, 0, len(document.Workspaces))
	for _, record := range document.Workspaces {
		if strings.TrimSpace(record.ID) == "" || strings.TrimSpace(record.Root) == "" {
			continue
		}
		workspaces = append(workspaces, contracts.Workspace{
			ID:        record.ID,
			Name:      record.Name,
			Root:      record.Root,
			CreatedAt: record.CreatedAt,
		})
	}
	s.workspaces = workspaces
	s.currentID = strings.TrimSpace(document.CurrentID)
	return nil
}

// decodeRegistry accepts the canonical object and, for convenience, a bare
// workspace array.
func decodeRegistry(data []byte) (registryDocument, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return registryDocument{}, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var records []workspaceRecord
		if err := json.Unmarshal([]byte(trimmed), &records); err != nil {
			return registryDocument{}, err
		}
		return registryDocument{Workspaces: records}, nil
	}
	var document registryDocument
	if err := json.Unmarshal([]byte(trimmed), &document); err != nil {
		return registryDocument{}, err
	}
	return document, nil
}

// persistLocked writes the registry atomically (temporary file + Sync +
// rename).
func (s *Service) persistLocked() error {
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, dirMode); err != nil {
		return fmt.Errorf("%s: create registry directory %q: %w", ModuleID, directory, err)
	}
	document := registryDocument{CurrentID: s.currentID}
	document.Workspaces = make([]workspaceRecord, 0, len(s.workspaces))
	for _, workspace := range s.workspaces {
		document.Workspaces = append(document.Workspaces, workspaceRecord{
			ID:        workspace.ID,
			Name:      workspace.Name,
			Root:      workspace.Root,
			CreatedAt: workspace.CreatedAt,
		})
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("%s: encode registry: %w", ModuleID, err)
	}
	temporary, err := os.CreateTemp(directory, ".workspaces-*.tmp")
	if err != nil {
		return fmt.Errorf("%s: create temporary registry: %w", ModuleID, err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(fileMode); err != nil {
		cleanup()
		return fmt.Errorf("%s: chmod temporary registry: %w", ModuleID, err)
	}
	if _, err := temporary.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("%s: write temporary registry: %w", ModuleID, err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("%s: sync temporary registry: %w", ModuleID, err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("%s: close temporary registry: %w", ModuleID, err)
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		_ = os.Remove(temporaryPath)
		return fmt.Errorf("%s: replace registry %q: %w", ModuleID, s.path, err)
	}
	return nil
}

// cloneWorkspace returns a copy of a workspace value.
func cloneWorkspace(workspace contracts.Workspace) contracts.Workspace { return workspace }

// cloneWorkspaces returns a copy of the workspace slice.
func cloneWorkspaces(workspaces []contracts.Workspace) []contracts.Workspace {
	return append([]contracts.Workspace(nil), workspaces...)
}

// contextErr returns ctx.Err(), tolerating a nil context.
func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// --- registry operations ---------------------------------------------------

// List implements contracts.WorkspaceService in insertion order.
func (s *Service) List(ctx context.Context) ([]contracts.Workspace, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneWorkspaces(s.workspaces), nil
}

// Current implements contracts.WorkspaceService.
func (s *Service) Current(ctx context.Context) (contracts.Workspace, bool, error) {
	if err := contextErr(ctx); err != nil {
		return contracts.Workspace{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, workspace := range s.workspaces {
		if workspace.ID == s.currentID {
			return cloneWorkspace(workspace), true, nil
		}
	}
	return contracts.Workspace{}, false, nil
}

// Create implements contracts.WorkspaceService. The root must exist and be a
// directory; an already registered canonical root is reused and becomes
// current.
func (s *Service) Create(ctx context.Context, name, root string) (contracts.Workspace, error) {
	if err := contextErr(ctx); err != nil {
		return contracts.Workspace{}, err
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return contracts.Workspace{}, fmt.Errorf("%s: root is required", ModuleID)
	}
	if strings.ContainsRune(root, 0) {
		return contracts.Workspace{}, fmt.Errorf("%s: root contains a NUL byte", ModuleID)
	}
	absolute, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return contracts.Workspace{}, fmt.Errorf("%s: resolve root %q: %w", ModuleID, root, err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return contracts.Workspace{}, fmt.Errorf("%s: workspace root %q: %w", ModuleID, root, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return contracts.Workspace{}, fmt.Errorf("%s: workspace root %q: %w", ModuleID, root, err)
	}
	if !info.IsDir() {
		return contracts.Workspace{}, fmt.Errorf("%w: %q", ErrNotDirectory, root)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = filepath.Base(canonical)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.workspaces {
		if existing.Root != canonical {
			continue
		}
		previousCurrent := s.currentID
		if existing.ID != s.currentID {
			s.currentID = existing.ID
			if err := s.persistLocked(); err != nil {
				s.currentID = previousCurrent
				return contracts.Workspace{}, err
			}
		}
		return cloneWorkspace(existing), nil
	}

	workspace := contracts.Workspace{
		ID:        s.newIDLocked(),
		Name:      name,
		Root:      canonical,
		CreatedAt: time.Now().UTC(),
	}
	previous := cloneWorkspaces(s.workspaces)
	previousCurrent := s.currentID
	s.workspaces = append(s.workspaces, workspace)
	s.currentID = workspace.ID
	if err := s.persistLocked(); err != nil {
		s.workspaces = previous
		s.currentID = previousCurrent
		return contracts.Workspace{}, err
	}
	return cloneWorkspace(workspace), nil
}

// newIDLocked generates a unique "ws-<unixnano>" ID.
func (s *Service) newIDLocked() string {
	for {
		id := fmt.Sprintf("ws-%d", time.Now().UnixNano())
		exists := false
		for _, workspace := range s.workspaces {
			if workspace.ID == id {
				exists = true
				break
			}
		}
		if !exists {
			return id
		}
	}
}

// Open implements contracts.WorkspaceService: it makes an existing workspace
// current.
func (s *Service) Open(ctx context.Context, id string) (contracts.Workspace, error) {
	if err := contextErr(ctx); err != nil {
		return contracts.Workspace{}, err
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, workspace := range s.workspaces {
		if workspace.ID != id {
			continue
		}
		previous := s.currentID
		s.currentID = workspace.ID
		if err := s.persistLocked(); err != nil {
			s.currentID = previous
			return contracts.Workspace{}, err
		}
		return cloneWorkspace(workspace), nil
	}
	return contracts.Workspace{}, fmt.Errorf("%w: %q", ErrWorkspaceNotFound, id)
}

// Delete implements contracts.WorkspaceService: the files are not touched.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	index := -1
	for position, workspace := range s.workspaces {
		if workspace.ID == id {
			index = position
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("%w: %q", ErrWorkspaceNotFound, id)
	}
	previous := cloneWorkspaces(s.workspaces)
	previousCurrent := s.currentID
	s.workspaces = append(append([]contracts.Workspace(nil), s.workspaces[:index]...), s.workspaces[index+1:]...)
	if s.currentID == id {
		s.currentID = ""
	}
	if err := s.persistLocked(); err != nil {
		s.workspaces = previous
		s.currentID = previousCurrent
		return err
	}
	return nil
}

// currentWorkspaceRoot returns the current workspace and its canonical root.
func (s *Service) currentWorkspaceRoot() (contracts.Workspace, string, error) {
	s.mu.Lock()
	workspace, ok := contracts.Workspace{}, false
	for _, candidate := range s.workspaces {
		if candidate.ID == s.currentID {
			workspace, ok = candidate, true
			break
		}
	}
	s.mu.Unlock()
	if !ok {
		return contracts.Workspace{}, "", ErrNoWorkspace
	}
	root, err := filepath.EvalSymlinks(workspace.Root)
	if err != nil {
		return contracts.Workspace{}, "", fmt.Errorf("%s: workspace root %q: %w", ModuleID, workspace.Root, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return contracts.Workspace{}, "", fmt.Errorf("%s: workspace root %q: %w", ModuleID, workspace.Root, err)
	}
	if !info.IsDir() {
		return contracts.Workspace{}, "", fmt.Errorf("%w: %q", ErrNotDirectory, workspace.Root)
	}
	return workspace, filepath.Clean(root), nil
}

// --- sandboxed file access -------------------------------------------------

// resolveRelative resolves dir/path relative to root and rejects absolute
// paths, ".." escapes and symbolic links pointing outside the root.
func resolveRelative(root, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" || requested == "." {
		return root, nil
	}
	if strings.ContainsRune(requested, 0) {
		return "", fmt.Errorf("%s: path contains a NUL byte", ModuleID)
	}
	if filepath.IsAbs(requested) {
		return "", fmt.Errorf("%w: %q is absolute", ErrPathOutsideRoot, requested)
	}
	cleaned := filepath.Clean(filepath.FromSlash(requested))
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q contains a parent-directory escape", ErrPathOutsideRoot, requested)
	}
	candidate := filepath.Join(root, cleaned)
	if !withinRoot(root, candidate) {
		return "", fmt.Errorf("%w: %q", ErrPathOutsideRoot, requested)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("%s: path %q: %w", ModuleID, requested, err)
	}
	if !withinRoot(root, resolved) {
		return "", fmt.Errorf("%w: %q resolves outside the root", ErrPathOutsideRoot, requested)
	}
	return resolved, nil
}

// withinRoot reports whether path is root or below it.
func withinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if relative == "." {
		return true
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(relative)
}

// Files implements contracts.WorkspaceService.
func (s *Service) Files(ctx context.Context, dir string) ([]contracts.FileEntry, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	_, root, err := s.currentWorkspaceRoot()
	if err != nil {
		return nil, err
	}
	directory, err := resolveRelative(root, dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(directory)
	if err != nil {
		return nil, fmt.Errorf("%s: directory %q: %w", ModuleID, dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %q", ErrNotDirectory, dir)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("%s: list %q: %w", ModuleID, dir, err)
	}
	relativeDir, err := filepath.Rel(root, directory)
	if err != nil {
		return nil, fmt.Errorf("%s: relative directory: %w", ModuleID, err)
	}
	if relativeDir == "." {
		relativeDir = ""
	}
	result := make([]contracts.FileEntry, 0, len(entries))
	for _, entry := range entries {
		if err := contextErr(ctx); err != nil {
			return nil, err
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.ToSlash(filepath.Join(relativeDir, entry.Name()))
		result = append(result, contracts.FileEntry{
			Name:    entry.Name(),
			Path:    path,
			Dir:     info.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	sortFileEntries(result)
	return result, nil
}

// sortFileEntries orders directories first, then files, each by
// case-insensitive name.
func sortFileEntries(entries []contracts.FileEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		left, right := strings.ToLower(entries[i].Name), strings.ToLower(entries[j].Name)
		if left != right {
			return left < right
		}
		return entries[i].Name < entries[j].Name
	})
}

// ReadFile implements contracts.WorkspaceService.
func (s *Service) ReadFile(ctx context.Context, path string, limit int) ([]byte, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if limit < 0 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidLimit, limit)
	}
	_, root, err := s.currentWorkspaceRoot()
	if err != nil {
		return nil, err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("%s: file path is required", ModuleID)
	}
	file, err := resolveRelative(root, path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(file)
	if err != nil {
		return nil, fmt.Errorf("%s: file %q: %w", ModuleID, path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %q", ErrNotFile, path)
	}
	if limit == 0 {
		limit = s.readLimit
	}
	handle, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("%s: open %q: %w", ModuleID, path, err)
	}
	defer handle.Close()
	data, err := io.ReadAll(io.LimitReader(handle, int64(limit)))
	if err != nil {
		return nil, fmt.Errorf("%s: read %q: %w", ModuleID, path, err)
	}
	return data, nil
}

// Search implements contracts.WorkspaceService: a case-insensitive file-name
// search below the current root, bounded by depth and result count.
func (s *Service) Search(ctx context.Context, query string, limit int) ([]contracts.FileEntry, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return []contracts.FileEntry{}, nil
	}
	if limit < 0 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidLimit, limit)
	}
	if limit == 0 {
		limit = DefaultSearchLimit
	}
	_, root, err := s.currentWorkspaceRoot()
	if err != nil {
		return nil, err
	}
	depthLimit := s.searchDepth
	results := make([]contracts.FileEntry, 0, limit)
	var walk func(directory string, depth int) error
	walk = func(directory string, depth int) error {
		if err := contextErr(ctx); err != nil {
			return err
		}
		if len(results) >= limit || depth > depthLimit {
			return nil
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			return nil // unreadable subdirectories do not fail the search
		}
		for _, entry := range entries {
			if err := contextErr(ctx); err != nil {
				return err
			}
			if len(results) >= limit {
				return nil
			}
			name := entry.Name()
			if entry.IsDir() {
				if strings.HasPrefix(name, ".") {
					continue
				}
				if err := walk(filepath.Join(directory, name), depth+1); err != nil {
					return err
				}
				continue
			}
			if !strings.Contains(strings.ToLower(name), query) {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			relative, err := filepath.Rel(root, filepath.Join(directory, name))
			if err != nil {
				continue
			}
			results = append(results, contracts.FileEntry{
				Name:    name,
				Path:    filepath.ToSlash(relative),
				Dir:     false,
				Size:    info.Size(),
				ModTime: info.ModTime(),
			})
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return nil, err
	}
	sort.SliceStable(results, func(i, j int) bool {
		left, right := strings.ToLower(results[i].Path), strings.ToLower(results[j].Path)
		if left != right {
			return left < right
		}
		return results[i].Path < results[j].Path
	})
	return results, nil
}
