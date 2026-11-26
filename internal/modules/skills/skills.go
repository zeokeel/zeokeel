// Package skills implements ZeoKeel's skills backend module: the discovery and
// reading layer for filesystem-backed agent skills, exposed to the toolkit
// (skill tool) and the settings skills page, plus the optional editor half that
// creates/deletes skills below the product data directory.
//
// It is a kernel backend module: it requires the product data directory and the
// host logger, and optionally consumes the workspace service and the settings
// store. For every call it rebuilds the root list and rescans it, so a workspace
// switch or newly created directory is visible immediately without any cache
// invalidation.
//
// Root scope is strict (S-02/S-03): when a workspace is open only its skill
// directories are scanned (<root>/skills, <root>/.agents/skills,
// <root>/.zeokeel/skills), and every global root - user-configured extra
// directories (the workspace-scoped skills.dirs.<id>), assembly directories,
// <dataDir>/skills, $ZEOKEEL_SKILLS and ~/.zeokeel/skills - is ignored. With no
// workspace open the old global list is scanned (global skills.dirs, assembly,
// dataDir, env, home). That makes workspaces fully isolated: switching folders
// swaps the whole skill set, and a skill or extra directory in another workspace
// or the data directory is never visible here.
//
// The editor writes <root>/.zeokeel/skills while a workspace is open and
// <dataDir>/skills otherwise; create/update/delete/list/warnings all use the
// same Roots() view, so an edit is immediately discoverable in its workspace.
//
// Legacy migration is explicit, not automatic: pre-existing <dataDir>/skills
// (and other global) skills remain visible only when no workspace is open. We do
// not copy or move them on activation (an implicit copy could resurrect deleted
// skills or surprise the user by writing into a repository); the skills tab shows
// this note, and "Copy to my skills" provides the deliberate path into the open
// workspace.
//
// Listing is bounded (S-05): List reads only the beginning of each SKILL.md
// (frontmatter + description + a searchable excerpt), caps the number of listed
// skills and of files scanned per root, while Read/load reads the full file. The
// frontmatter splitting rule is the rig rule (S-06): only an exact "---" line
// closes the block; an unclosed block is ordinary text.
package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"
	rigskills "github.com/zeokeel/zeokeel-rig/skills"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/skills"
	// Version is the module's semantic version.
	Version = "1.1.0"
	// DataDirName is the skill directory inside the product data directory.
	DataDirName = "skills"
	// WorkspaceDirName is the primary skill directory inside a workspace root.
	WorkspaceDirName = "skills"

	// MaxListedSkills caps one List result; extra skills are reported through
	// Warnings() instead of silently growing the model-facing list.
	MaxListedSkills = 500
	// maxSkillsScannedPerRoot caps the directory entries examined per root.
	maxSkillsScannedPerRoot = 2000
	// maxListReadBytes is how much of one SKILL.md List reads (frontmatter,
	// description, searchable excerpt); Read/load reads the whole file.
	maxListReadBytes = 8 << 10
	// MaxSkillBytes caps the full Read/load file read (larger files truncate
	// with an explicit marker).
	MaxSkillBytes = 1 << 20
	// maxWarnings caps Warnings() output.
	maxWarnings = 20
)

// workspaceSkillDirNames are the workspace-level roots scanned in order. The
// first is the product's own convention, the other two are the DeepSeek Harness
// (.agents/skills) and rig (.zeokeel/skills) conventions, so skills authored for
// either ecosystem are discovered.
var workspaceSkillDirNames = []string{
	WorkspaceDirName,
	filepath.Join(".agents", "skills"),
	filepath.Join(".zeokeel", "skills"),
}

// Config is the module's typed assembly config.
type Config struct {
	// Dirs are extra skill directories, highest precedence first. Relative
	// values are resolved against the product data directory.
	Dirs []string `json:"dirs"`
}

// Module is the skills backend descriptor. It provides contracts.KeySkills.
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
		var workspace contracts.WorkspaceService
		if service, ok := ctx.Get(contracts.KeyWorkspace); ok {
			workspace = service
		}
		var settings contracts.SettingsService
		if service, ok := ctx.Get(contracts.KeySettings); ok {
			settings = service
		}
		skillsService := newService(dataDir, cfg, logger, workspace)
		skillsService.settings = settings
		if _, err := ctx.Provide(contracts.KeySkills, contracts.SkillsService(skillsService)); err != nil {
			return fmt.Errorf("%s: provide skills service: %w", ModuleID, err)
		}
		// The same service also provides the optional write half, so the
		// settings page can create/delete data-dir skills without the toolkit
		// gaining write access through its read-only key.
		if _, err := ctx.Provide(contracts.KeySkillsEditor, contracts.SkillsEditor(skillsService)); err != nil {
			return fmt.Errorf("%s: provide skills editor: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(
			module.Provides(contracts.KeySkills),
			module.Provides(contracts.KeySkillsEditor),
		).
		AddRequires(
			module.Requires(contracts.KeyDataDir),
			module.Requires(contracts.KeyLogger),
		).
		WithName("ZeoKeel skills").
		WithVersion(Version).
		WithValidate(validateConfig)
}

// validateConfig rejects configs the service could not honour.
func validateConfig(cfg Config) error {
	for _, dir := range cfg.Dirs {
		if strings.ContainsRune(dir, 0) {
			return fmt.Errorf("%s: skills directory contains a NUL byte", ModuleID)
		}
	}
	return nil
}

// service is the contracts.SkillsService implementation.
type service struct {
	dataDir   string
	dirs      []string // normalized configured extra dirs (absolute)
	logger    contracts.Logger
	workspace contracts.WorkspaceService
	settings  contracts.SettingsService

	store *rigskills.Store

	mu sync.Mutex
}

var (
	_ contracts.SkillsService = (*service)(nil)
	_ contracts.SkillsEditor  = (*service)(nil)
	_ contracts.SkillWarnings = (*service)(nil)
	_ rig.SkillStore          = (*service)(nil)
)

// discardLogger drops skills log lines.
type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// newService normalizes the configured directories against dataDir.
func newService(dataDir string, cfg Config, logger contracts.Logger, workspace contracts.WorkspaceService) *service {
	if logger == nil {
		logger = discardLogger{}
	}
	dataDir = strings.TrimSpace(dataDir)
	configured := normalizeDirs(dataDir, cfg.Dirs)
	service := &service{
		dataDir:   dataDir,
		dirs:      configured,
		logger:    logger,
		workspace: workspace,
	}
	service.store = rigskills.New(rigskills.Options{Roots: service.Roots, DataDir: dataDir, Logger: logger})
	service.logger.Printf("%s: initialized with %d configured skill directories", ModuleID, len(configured))
	return service
}

// normalizeDirs resolves configured directory values against dataDir and
// removes empties/duplicates while preserving order.
func normalizeDirs(dataDir string, dirs []string) []string {
	configured := make([]string, 0, len(dirs))
	seen := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(dataDir, filepath.FromSlash(dir))
		}
		absolute, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		absolute = filepath.Clean(absolute)
		if seen[absolute] {
			continue
		}
		seen[absolute] = true
		configured = append(configured, absolute)
	}
	return configured
}

// List implements rig.SkillStore via the SDK store.
func (s *service) List(ctx context.Context) ([]rig.Skill, error) {
	return s.store.List(ctx)
}

// Read implements rig.SkillStore via the SDK store.
func (s *service) Read(ctx context.Context, name string) (rig.Skill, error) {
	return s.store.Read(ctx, name)
}

// Warnings implements contracts.SkillWarnings via the SDK store.
func (s *service) Warnings() []string {
	return s.store.Warnings()
}

// Roots implements contracts.SkillsService: the directories scanned for this
// call, in precedence order (workspace roots, user-configured dirs, assembly
// dirs, data dir, $ZEOKEEL_SKILLS, ~/.zeokeel/skills). Paths are absolute and
// deduplicated; missing directories are kept.
func (s *service) Roots() []string {
	roots := make([]string, 0, len(s.dirs)+8)
	seen := make(map[string]bool, len(s.dirs)+8)
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(s.dataDir, filepath.FromSlash(dir))
		}
		absolute, err := filepath.Abs(dir)
		if err != nil {
			return
		}
		absolute = filepath.Clean(absolute)
		if seen[absolute] {
			return
		}
		seen[absolute] = true
		roots = append(roots, absolute)
	}
	// Strict workspace scoping: with a folder open only that workspace's skill
	// directories and its own scoped extra directories are scanned. Global roots
	// (global settings/assembly/dataDir/env/home) stay visible only when no
	// workspace is open, so a skill or extra directory from another workspace
	// can never bleed into this one.
	if current, ok := s.currentWorkspace(); ok {
		if currentRoot := strings.TrimSpace(current.Root); currentRoot != "" {
			for _, name := range workspaceSkillDirNames {
				add(filepath.Join(currentRoot, name))
			}
			for _, dir := range s.userDirs(strings.TrimSpace(current.ID)) {
				add(dir)
			}
			return roots
		}
	}
	// 2. User-configured extra directories (global settings), then assembly
	// dirs.
	for _, dir := range s.userDirs("") {
		add(dir)
	}
	for _, dir := range s.dirs {
		add(dir)
	}
	// 3. The product data directory.
	if s.dataDir != "" {
		add(filepath.Join(s.dataDir, DataDirName))
	}
	// 4. rig default roots: $ZEOKEEL_SKILLS then ~/.zeokeel/skills.
	for _, dir := range envSkillDirs() {
		add(dir)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		add(filepath.Join(home, ".zeokeel", "skills"))
	}
	return roots
}

// userDirs reads the user-configured extra directories for one scope: the
// workspace-scoped key while a workspace is open, the global key otherwise.
func (s *service) userDirs(workspaceID string) []string {
	if s.settings == nil {
		return nil
	}
	raw := strings.TrimSpace(s.settings.Get(contracts.SkillsDirsSettingKey(workspaceID), ""))
	if raw == "" {
		return nil
	}
	fields := strings.FieldsFunc(raw, func(char rune) bool {
		return char == os.PathListSeparator || char == '\n' || char == '\r'
	})
	return normalizeDirs(s.dataDir, fields)
}

// envSkillDirs reads the rig-compatible $ZEOKEEL_SKILLS path list.
func envSkillDirs() []string {
	raw := strings.TrimSpace(os.Getenv("ZEOKEEL_SKILLS"))
	if raw == "" {
		return nil
	}
	return strings.Split(raw, string(os.PathListSeparator))
}

// currentWorkspaceRoot returns the open workspace root. An empty root is no
// workspace (the legacy global roots apply).
func (s *service) currentWorkspace() (contracts.Workspace, bool) {
	if s.workspace == nil {
		return contracts.Workspace{}, false
	}
	current, ok, err := s.workspace.Current(context.Background())
	if err != nil || !ok {
		return contracts.Workspace{}, false
	}
	return current, true
}

func (s *service) currentWorkspaceRoot() (string, bool) {
	current, ok := s.currentWorkspace()
	if !ok {
		return "", false
	}
	root := strings.TrimSpace(current.Root)
	return root, root != ""
}

// editorStore returns the store writes go to: the open workspace's
// .zeokeel/skills directory, or the data directory when no folder is open.
func (s *service) editorStore() *rigskills.Store {
	dataDir := s.dataDir
	if root, ok := s.currentWorkspaceRoot(); ok {
		dataDir = filepath.Join(root, ".zeokeel")
	}
	return rigskills.New(rigskills.Options{Roots: s.Roots, DataDir: dataDir, Logger: s.logger})
}

// --- frontmatter (shared with the editor) ---

type frontmatterPair struct {
	raw   string
	key   string
	value string
}
