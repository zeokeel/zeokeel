// Package skills implements ZeoKeel's skills backend module: the discovery and
// reading layer for filesystem-backed agent skills, exposed to the toolkit
// (skill tool) and the settings skills page.
//
// It is a kernel backend module: it requires the product data directory and the
// host logger, and optionally consumes the workspace service. For every call it
// rebuilds the root list (configured dirs first, then <dataDir>/skills, then
// <workspace root>/skills) and wraps them in a fresh rig skill.FileStore, so a
// workspace switch or newly created directory is visible immediately without any
// cache invalidation.
package skills

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"
	"github.com/zeokeel/zeokeel-rig/tools/skill"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// ModuleID is the stable module identity used by assembly documents.
	ModuleID = "zeokeel/skills"
	// Version is the module's semantic version.
	Version = "1.0.0"
	// DataDirName is the skill directory inside the product data directory.
	DataDirName = "skills"
	// WorkspaceDirName is the skill directory inside a workspace root.
	WorkspaceDirName = "skills"
)

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
		skillsService := newService(dataDir, cfg, logger, workspace)
		if _, err := ctx.Provide(contracts.KeySkills, contracts.SkillsService(skillsService)); err != nil {
			return fmt.Errorf("%s: provide skills service: %w", ModuleID, err)
		}
		return nil
	}).
		AddProvides(module.Provides(contracts.KeySkills)).
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
}

var (
	_ contracts.SkillsService = (*service)(nil)
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
	configured := make([]string, 0, len(cfg.Dirs))
	for _, dir := range cfg.Dirs {
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
		configured = append(configured, filepath.Clean(absolute))
	}
	service := &service{
		dataDir:   dataDir,
		dirs:      configured,
		logger:    logger,
		workspace: workspace,
	}
	service.logger.Printf("%s: initialized with %d configured skill directories", ModuleID, len(configured))
	return service
}

// Roots implements contracts.SkillsService: the directories scanned for this
// call, in precedence order: configured dirs, <dataDir>/skills, then the current
// workspace's <root>/skills. Paths are absolute and deduplicated; missing
// directories are kept (the rig FileStore ignores them, matching its own Dirs
// semantics).
func (s *service) Roots() []string {
	roots := make([]string, 0, len(s.dirs)+2)
	seen := make(map[string]bool, len(s.dirs)+2)
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
	for _, dir := range s.dirs {
		add(dir)
	}
	if s.dataDir != "" {
		add(filepath.Join(s.dataDir, DataDirName))
	}
	if s.workspace != nil {
		workspace, ok, err := s.workspace.Current(context.Background())
		if err == nil && ok {
			if root := strings.TrimSpace(workspace.Root); root != "" {
				add(filepath.Join(root, WorkspaceDirName))
			}
		}
	}
	return roots
}

// List implements rig.SkillStore. It builds a fresh rig FileStore for the
// current roots, so directory changes take effect immediately.
func (s *service) List(ctx context.Context) ([]rig.Skill, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	roots := s.Roots()
	store := skill.NewFileStore(roots...)
	skills, err := store.List(ctx)
	if err != nil {
		s.logger.Printf("%s: list %d roots: %v", ModuleID, len(roots), err)
		return nil, err
	}
	for index := range skills {
		skills[index].Body = stripFrontmatter(skills[index].Body)
	}
	s.logger.Printf("%s: listed %d skills from %d roots", ModuleID, len(skills), len(roots))
	return skills, nil
}

// stripFrontmatter removes a leading "---\n...\n---" YAML block from a skill
// body: the frontmatter carries name/description (the store parsed them
// already), and the rendered body should start at the Markdown content.
func stripFrontmatter(body string) string {
	if !strings.HasPrefix(body, "---") {
		return body
	}
	rest := strings.TrimPrefix(body, "---")
	rest = strings.TrimLeft(rest, "\r")
	if !strings.HasPrefix(rest, "\n") {
		return body
	}
	rest = rest[1:]
	if closing := strings.Index(rest, "\n---"); closing >= 0 {
		end := closing + len("\n---")
		if end < len(rest) && rest[end] == '\r' {
			end++
		}
		if end < len(rest) && rest[end] == '\n' {
			end++
		}
		return strings.TrimLeft(rest[end:], "\r\n")
	}
	return body
}

// Read implements rig.SkillStore.
func (s *service) Read(ctx context.Context, name string) (rig.Skill, error) {
	if err := ctx.Err(); err != nil {
		return rig.Skill{}, err
	}
	roots := s.Roots()
	store := skill.NewFileStore(roots...)
	result, err := store.Read(ctx, name)
	if err != nil {
		return rig.Skill{}, err
	}
	result.Body = stripFrontmatter(result.Body)
	return result, nil
}
