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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	result := contracts.ImportResult{Skipped: preview.Skipped}
	metas, err := s.store.Sessions(ctx)
	if err != nil {
		return contracts.ImportResult{}, fmt.Errorf("%s: list existing sessions: %w", ModuleID, err)
	}
	existing := make(map[string]bool, len(metas))
	for _, meta := range metas {
		existing[meta.ID] = true
	}
	registeredWorkspaces := make(map[string]bool)
	// BE-04 (2026-11 audit): workspace.Create makes the registered root
	// current, so importing used to silently move the user's current workspace
	// to the last imported cwd (unbound sessions follow Current for their tool
	// root). Save the current workspace and restore it after the registration
	// loop. With no current workspace there is no user project to protect: the
	// first imported root stays current (the workspace contract has no
	// "register without switching" API).
	previousCurrentID := ""
	if s.workspace != nil {
		if current, ok, currentErr := s.workspace.Current(ctx); currentErr == nil && ok {
			previousCurrentID = current.ID
		}
	}
	registeredAny := false
	for _, item := range scanned {
		if err := ctx.Err(); err != nil {
			return contracts.ImportResult{}, err
		}
		session := item.session
		sessionID := importedSessionPrefix + session.ID
		if existing[sessionID] {
			result.Skipped++
			continue
		}
		title := importedTitle(session.Messages)
		if err := s.store.CreateSession(ctx, sessionID, title); err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("%s: create session: %v", item.sourcePath, err))
			continue
		}
		if err := s.store.RenameSession(ctx, sessionID, title); err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("%s: rename session: %v", item.sourcePath, err))
		}
		messages := importedRigMessages(session.Messages)
		if err := s.store.ReplaceHistory(ctx, sessionID, messages); err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("%s: replace history: %v", item.sourcePath, err))
			continue
		}
		s.appendImportEvent(ctx, sessionID, session.ID, &result)
		result.ImportedSessions++
		result.ImportedMessages += len(messages)
		existing[sessionID] = true
		if session.Cwd != "" && !registeredWorkspaces[session.Cwd] {
			registeredWorkspaces[session.Cwd] = true
			if info, statErr := os.Stat(session.Cwd); statErr == nil && info.IsDir() {
				if s.workspace == nil {
					result.Errors = append(result.Errors,
						fmt.Sprintf("%s: register workspace %q: workspace service unavailable", item.sourcePath, session.Cwd))
				} else if _, err := s.workspace.Create(ctx, filepath.Base(session.Cwd), session.Cwd); err != nil {
					result.Errors = append(result.Errors,
						fmt.Sprintf("%s: register workspace %q: %v", item.sourcePath, session.Cwd, err))
				} else {
					result.ImportedWorkspaces++
					registeredAny = true
				}
			}
		}
	}
	// Restore the user's current workspace after the registrations switched it
	// (BE-04). A failed restore is logged, never fatal: the import succeeded.
	if previousCurrentID != "" && registeredAny {
		if _, err := s.workspace.Open(ctx, previousCurrentID); err != nil {
			s.logger.Printf("%s: restore current workspace %q after import: %v", ModuleID, previousCurrentID, err)
		}
	}
	s.logger.Printf("%s: imported %d sessions/%d messages/%d workspaces (%d skipped)",
		ModuleID, result.ImportedSessions, result.ImportedMessages, result.ImportedWorkspaces, result.Skipped)
	return result, nil
}

// appendImportEvent records an optional durable "import" event.
func (s *service) appendImportEvent(ctx context.Context, sessionID, sourceID string,
	result *contracts.ImportResult) {
	payload, err := json.Marshal(map[string]string{"source": sourceID})
	if err != nil {
		return
	}
	if err := s.store.AppendEvent(ctx, rig.SessionEvent{
		SessionID: sessionID,
		Type:      "import",
		Time:      time.Now().UTC(),
		Payload:   payload,
	}); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("%s: append import event: %v", sessionID, err))
	}
}
