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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/importer"
	// Version is the module's semantic version.
	Version = "1.0.0"
	// sessionFileName is the DSH v4 session log file name.
	sessionFileName = "session.v4.jsonl"
	// importedSessionPrefix prefixes imported session IDs.
	importedSessionPrefix = "dsh-"
	// titleRunes bounds an imported session title.
	titleRunes = 60
	// defaultImportedTitle is used when a session has no user message.
	defaultImportedTitle = "导入的会话"
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

// scan walks <dir>/sessions and parses every uncompressed session.v4.jsonl.
func (s *service) scan(ctx context.Context, dir string) ([]scannedSession, contracts.ImportPreview, error) {
	preview := contracts.ImportPreview{SourceDir: dir}
	sessionsRoot := filepath.Join(dir, "sessions")
	info, err := os.Stat(sessionsRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, preview, nil
	}
	if err != nil {
		return nil, preview, fmt.Errorf("%s: sessions directory %q: %w", ModuleID, sessionsRoot, err)
	}
	if !info.IsDir() {
		return nil, preview, fmt.Errorf("%s: %q is not a directory", ModuleID, sessionsRoot)
	}
	workspaceDirs := make(map[string]bool)
	scanned := make([]scannedSession, 0)
	walkErr := filepath.WalkDir(sessionsRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name := strings.ToLower(entry.Name())
		relative := relativeSourcePath(dir, path)
		if strings.HasSuffix(name, ".zstd") {
			preview.Skipped++
			preview.Notes = append(preview.Notes,
				fmt.Sprintf("skipped compressed session %s (zstd is not supported)", relative))
			return nil
		}
		if name != sessionFileName {
			return nil
		}
		parsed, err := parseSessionFile(path)
		if err != nil {
			preview.Skipped++
			if errors.Is(err, ErrUnsupportedVersion) {
				preview.Notes = append(preview.Notes,
					fmt.Sprintf("unsupported session version in %s: %v", relative, err))
			} else {
				preview.Notes = append(preview.Notes, fmt.Sprintf("skipped %s: %v", relative, err))
			}
			return nil
		}
		if parsed.DroppedAttachments > 0 {
			preview.Skipped += parsed.DroppedAttachments
			preview.Notes = append(preview.Notes,
				fmt.Sprintf("%s: %d image/file attachment(s) not imported", relative, parsed.DroppedAttachments))
		}
		if parsed.Cwd != "" {
			if cwdInfo, statErr := os.Stat(parsed.Cwd); statErr == nil && cwdInfo.IsDir() {
				workspaceDirs[parsed.Cwd] = true
			}
		}
		preview.Sessions++
		preview.Messages += len(parsed.Messages)
		scanned = append(scanned, scannedSession{session: parsed, sourcePath: relative})
		return nil
	})
	if walkErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, preview, ctxErr
		}
		return nil, preview, fmt.Errorf("%s: walk %q: %w", ModuleID, sessionsRoot, walkErr)
	}
	preview.Workspaces = len(workspaceDirs)
	return scanned, preview, nil
}

// relativeSourcePath renders a session path relative to the DSH home.
func relativeSourcePath(root, path string) string {
	if relative, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(relative)
	}
	return filepath.ToSlash(path)
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
				}
			}
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

// importedTitle derives the session title from the first user message.
func importedTitle(messages []parsedMessage) string {
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		runes := []rune(text)
		if len(runes) > titleRunes {
			runes = runes[:titleRunes]
		}
		return string(runes)
	}
	return defaultImportedTitle
}

// importedRigMessages rebuilds the durable rig history.
func importedRigMessages(messages []parsedMessage) []rig.Message {
	out := make([]rig.Message, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case "user":
			out = append(out, rig.NewMessage(rig.RoleUser, message.Text))
		case "assistant":
			assistant := rig.NewMessage(rig.RoleAssistant, message.Text)
			assistant.Reasoning = message.Reasoning
			out = append(out, assistant)
		}
	}
	return out
}
