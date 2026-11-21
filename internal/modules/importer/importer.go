// Package importer implements ZeoKeel's DSH data import backend module: scan a
// DSH data home for session.v4.jsonl logs, preview what would be imported and
// write the conversation history/workspaces through the product store.
//
// Only the focused subset of the old shell's importer is ported: uncompressed
// session logs, user/assistant text (plus assistant reasoning) and workspace
// registration. Image/file attachments, tools and compressed (.zstd) logs are
// counted and explained but not imported.
package importer

import (
	"context"
	"fmt"
	"os"
	"strings"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"
	rigimporter "github.com/zeokeel/zeokeel-rig/session/importer"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/importer"
	// Version is the module's semantic version.
	Version = "1.0.0"
	// titleRunes mirrors the SDK's title bound (tests assert it).
	titleRunes = 60
	// defaultImportedTitle mirrors the SDK's fallback title (tests assert it).
	defaultImportedTitle = "导入的会话"
	// sessionFileName is the DSH v4 session log file name.
	sessionFileName = "session.v4.jsonl"
	// importedSessionPrefix prefixes imported session IDs.
	importedSessionPrefix = "dsh-"
)

// Config is the module's typed assembly config.
type Config struct {
	// SourceDir is the DSH data home used when Scan/Import get an empty dir
	// argument.
	SourceDir string `json:"sourceDir"`
}

// Module is the importer backend descriptor. It provides
// contracts.KeyImporter.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		store, err := ctx.Require(contracts.KeyStore)
		if err != nil {
			return fmt.Errorf("%s: require store: %w", ModuleID, err)
		}
		workspace, err := ctx.Require(contracts.KeyWorkspace)
		if err != nil {
			return fmt.Errorf("%s: require workspace: %w", ModuleID, err)
		}
		logger, err := ctx.Require(contracts.KeyLogger)
		if err != nil {
			return fmt.Errorf("%s: require logger: %w", ModuleID, err)
		}
		service := newService(cfg, store, workspace, logger)
		if _, err := ctx.Provide(contracts.KeyImporter, contracts.ImporterService(service)); err != nil {
			return fmt.Errorf("%s: provide importer service: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeyImporter)).
		AddRequires(
			module.Requires(contracts.KeyStore),
			module.Requires(contracts.KeyWorkspace),
			module.Requires(contracts.KeyLogger),
		).
		WithName("ZeoKeel DSH importer").
		WithVersion(Version).
		WithValidate(validateConfig)
}

// validateConfig rejects configs the service could not honour.
func validateConfig(cfg Config) error {
	if strings.ContainsRune(cfg.SourceDir, 0) {
		return fmt.Errorf("%s: sourceDir contains a NUL byte", ModuleID)
	}
	return nil
}

// service is the contracts.ImporterService implementation.
type service struct {
	sourceDir string
	store     contracts.Store
	workspace contracts.WorkspaceService
	logger    contracts.Logger
}

var _ contracts.ImporterService = (*service)(nil)

// discardLogger drops importer log lines.
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// newService builds the importer.
func newService(cfg Config, store contracts.Store, workspace contracts.WorkspaceService,
	logger contracts.Logger) *service {
	if logger == nil {
		logger = discardLogger{}
	}
	return &service{
		sourceDir: strings.TrimSpace(cfg.SourceDir),
		store:     store,
		workspace: workspace,
		logger:    logger,
	}
}

// scannedSession is one parsed session plus its source path relative to the
// scanned directory (for notes).
type scannedSession struct {
	session    *parsedSession
	sourcePath string
}

// resolveDir applies the default source directory and validates it.
func (s *service) resolveDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = s.sourceDir
	}
	if dir == "" {
		return "", fmt.Errorf("%s: source directory is required (set sourceDir or pass dir)", ModuleID)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("%s: source directory %q: %w", ModuleID, dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s: source directory %q is not a directory", ModuleID, dir)
	}
	return dir, nil
}

// Scan implements contracts.ImporterService: a read-only preview of the DSH
// sessions found below dir.
func (s *service) Scan(ctx context.Context, dir string) (contracts.ImportPreview, error) {
	if err := ctx.Err(); err != nil {
		return contracts.ImportPreview{}, err
	}
	resolved, err := s.resolveDir(dir)
	if err != nil {
		return contracts.ImportPreview{}, err
	}
	_, preview, err := s.scan(ctx, resolved)
	return preview, err
}

// scan discovers and parses the DSH sessions below dir through the SDK.
func (s *service) scan(ctx context.Context, dir string) ([]scannedSession, contracts.ImportPreview, error) {
	discovery, err := rigimporter.Discover(ctx, dir)
	preview := contracts.ImportPreview{
		SourceDir:  dir,
		Sessions:   discovery.Sessions,
		Messages:   discovery.Messages,
		Workspaces: discovery.Workspaces,
		Skipped:    discovery.Skipped,
		Notes:      discovery.Notes,
	}
	scanned := make([]scannedSession, 0, len(discovery.Sources))
	for _, source := range discovery.Sources {
		scanned = append(scanned, scannedSession{session: source.Session, sourcePath: source.RelativePath})
	}
	return scanned, preview, err
}

// Import implements contracts.ImporterService: it scans and writes sessions,
// history and workspaces through the product store. Sessions already imported
// are skipped; per-session failures are reported but do not abort the run.
func (s *service) Import(ctx context.Context, dir string) (contracts.ImportResult, error) {
	if err := ctx.Err(); err != nil {
		return contracts.ImportResult{}, err
	}
	resolved, err := s.resolveDir(dir)
	if err != nil {
		return contracts.ImportResult{}, err
	}
	scanned, preview, err := s.scan(ctx, resolved)
	if err != nil {
		return contracts.ImportResult{}, err
	}
	sources := make([]rigimporter.Source, 0, len(scanned))
	for _, item := range scanned {
		sources = append(sources, rigimporter.Source{Session: item.session, RelativePath: item.sourcePath})
	}
	result, err := rigimporter.Import(ctx, sources, preview.Skipped, storeTarget{store: s.store}, workspaceRegistrar{workspace: s.workspace}, s.logger)
	if err != nil {
		return contracts.ImportResult{}, err
	}
	s.logger.Printf("%s: imported %d sessions/%d messages/%d workspaces (%d skipped)",
		ModuleID, result.ImportedSessions, result.ImportedMessages, result.ImportedWorkspaces, result.Skipped)
	return contracts.ImportResult{
		ImportedSessions:   result.ImportedSessions,
		ImportedMessages:   result.ImportedMessages,
		ImportedWorkspaces: result.ImportedWorkspaces,
		Skipped:            result.Skipped,
		Errors:             result.Errors,
	}, nil
}

// storeTarget adapts contracts.Store to the SDK import target.
type storeTarget struct{ store contracts.Store }

func (t storeTarget) SessionIDs(ctx context.Context) ([]string, error) {
	metas, err := t.store.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(metas))
	for _, meta := range metas {
		ids = append(ids, meta.ID)
	}
	return ids, nil
}

func (t storeTarget) CreateSession(ctx context.Context, id, title string) error {
	return t.store.CreateSession(ctx, id, title)
}

func (t storeTarget) RenameSession(ctx context.Context, id, title string) error {
	return t.store.RenameSession(ctx, id, title)
}

func (t storeTarget) ReplaceHistory(ctx context.Context, id string, messages []rig.Message) error {
	return t.store.ReplaceHistory(ctx, id, messages)
}

func (t storeTarget) AppendEvent(ctx context.Context, event rig.SessionEvent) error {
	return t.store.AppendEvent(ctx, event)
}

// workspaceRegistrar adapts contracts.WorkspaceService.
type workspaceRegistrar struct{ workspace contracts.WorkspaceService }

func (w workspaceRegistrar) Current(ctx context.Context) (string, bool, error) {
	if w.workspace == nil {
		return "", false, nil
	}
	current, ok, err := w.workspace.Current(ctx)
	if err != nil || !ok {
		return "", false, err
	}
	return current.ID, true, nil
}

func (w workspaceRegistrar) Create(ctx context.Context, name, root string) error {
	if w.workspace == nil {
		return fmt.Errorf("workspace service unavailable")
	}
	_, err := w.workspace.Create(ctx, name, root)
	return err
}

func (w workspaceRegistrar) Open(ctx context.Context, id string) error {
	if w.workspace == nil {
		return fmt.Errorf("workspace service unavailable")
	}
	_, err := w.workspace.Open(ctx, id)
	return err
}
