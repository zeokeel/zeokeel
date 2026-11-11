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
// Root precedence (S-02/S-03): the current workspace's skill directories win
// (<root>/skills, <root>/.agents/skills, <root>/.zeokeel/skills), then the
// user-configured extra directories (settings key skills.dirs), the assembly
// configured directories, <dataDir>/skills, $ZEOKEEL_SKILLS and finally
// ~/.zeokeel/skills. A same-named skill in a later root is shadowed and reported
// through Warnings() instead of disappearing silently; the settings page shows
// the warning.
//
// Listing is bounded (S-05): List reads only the beginning of each SKILL.md
// (frontmatter + description + a searchable excerpt), caps the number of listed
// skills and of files scanned per root, while Read/load reads the full file. The
// frontmatter splitting rule is the rig rule (S-06): only an exact "---" line
// closes the block; an unclosed block is ordinary text.
package skills

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"

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

	mu       sync.Mutex
	warnings []string
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
	// 1. The current workspace's skill directories (highest precedence).
	if s.workspace != nil {
		if current, ok, err := s.workspace.Current(context.Background()); err == nil && ok {
			if root := strings.TrimSpace(current.Root); root != "" {
				for _, name := range workspaceSkillDirNames {
					add(filepath.Join(root, name))
				}
			}
		}
	}
	// 2. User-configured extra directories (settings), then assembly dirs.
	for _, dir := range s.userDirs() {
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

// userDirs reads the user-configured extra directories from settings.
func (s *service) userDirs() []string {
	if s.settings == nil {
		return nil
	}
	raw := strings.TrimSpace(s.settings.Get(contracts.SettingSkillsDirs, ""))
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

// skillCandidate is one discovered skill file before it is parsed.
type skillCandidate struct {
	name string
	path string
}

// scanRoot lists the skill candidates of one root, applying the containment and
// regular-file rules. Missing roots and unreadable entries are skipped (never
// fail the whole list); a broken root is reported as a warning.
func (s *service) scanRoot(root string, warnings *[]string) []skillCandidate {
	info, err := os.Stat(root)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.addWarning(warnings, fmt.Sprintf("skill root %s is unreadable: %v", root, err))
		}
		return nil
	}
	if !info.IsDir() {
		return nil
	}
	realDir, err := filepath.EvalSymlinks(root)
	if err != nil {
		s.addWarning(warnings, fmt.Sprintf("skill root %s cannot be resolved: %v", root, err))
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.addWarning(warnings, fmt.Sprintf("skill root %s cannot be listed: %v", root, err))
		}
		return nil
	}
	candidates := make([]skillCandidate, 0, len(entries))
	for index, entry := range entries {
		if index >= maxSkillsScannedPerRoot {
			s.addWarning(warnings, fmt.Sprintf("skill root %s has more than %d entries; the rest was not scanned", root, maxSkillsScannedPerRoot))
			break
		}
		name := entry.Name()
		if entry.IsDir() {
			filePath := filepath.Join(root, name, "SKILL.md")
			fileInfo, statErr := os.Stat(filePath)
			if statErr != nil || !fileInfo.Mode().IsRegular() {
				continue
			}
			if !pathWithin(realDir, filePath) {
				continue
			}
			candidates = append(candidates, skillCandidate{name: name, path: filePath})
			continue
		}
		// Optional top-level Markdown skill (<name>.md).
		if !entry.Type().IsRegular() {
			continue
		}
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".md") {
			continue
		}
		filePath := filepath.Join(root, name)
		if !pathWithin(realDir, filePath) {
			continue
		}
		baseName := name[:len(name)-len(".md")]
		if baseName == "" {
			continue
		}
		candidates = append(candidates, skillCandidate{name: baseName, path: filePath})
	}
	return candidates
}

// List implements rig.SkillStore: name + description + a bounded body excerpt
// for every skill, root precedence first. Reads only maxListReadBytes per file
// so a large skill library does not block the frame or the tool call (S-05).
func (s *service) List(ctx context.Context) ([]rig.Skill, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	roots := s.Roots()
	warnings := make([]string, 0)
	skills := make([]rig.Skill, 0)
	seenNames := make(map[string]bool)
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, candidate := range s.scanRoot(root, &warnings) {
			if seenNames[candidate.name] {
				s.addWarning(&warnings, fmt.Sprintf("skill %q from %s is shadowed by a higher-precedence root", candidate.name, filepath.Dir(candidate.path)))
				continue
			}
			seenNames[candidate.name] = true
			if len(skills) >= MaxListedSkills {
				s.addWarning(&warnings, fmt.Sprintf("more than %d skills discovered; the rest was not listed", MaxListedSkills))
				break
			}
			excerpt, truncated, err := readSkillPrefix(candidate.path, maxListReadBytes)
			if err != nil {
				s.addWarning(&warnings, fmt.Sprintf("skill %q cannot be read: %v", candidate.name, err))
				continue
			}
			skills = append(skills, parseSkill(candidate.name, candidate.path, excerpt, truncated))
		}
	}
	s.setWarnings(warnings)
	s.logger.Printf("%s: listed %d skills from %d roots", ModuleID, len(skills), len(roots))
	return skills, nil
}

// Read implements rig.SkillStore: the full SKILL.md of one skill (first root
// wins, matching List), capped at MaxSkillBytes.
func (s *service) Read(ctx context.Context, name string) (rig.Skill, error) {
	if err := ctx.Err(); err != nil {
		return rig.Skill{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return rig.Skill{}, errors.New("skill: name is required")
	}
	for _, root := range s.Roots() {
		if err := ctx.Err(); err != nil {
			return rig.Skill{}, err
		}
		warnings := make([]string, 0)
		for _, candidate := range s.scanRoot(root, &warnings) {
			if candidate.name != name {
				continue
			}
			content, truncated, err := readSkillPrefix(candidate.path, MaxSkillBytes)
			if err != nil {
				return rig.Skill{}, fmt.Errorf("skill: read %q: %w", name, err)
			}
			return parseSkill(name, candidate.path, content, truncated), nil
		}
	}
	return rig.Skill{}, fmt.Errorf("skill: unknown skill %q (use the skill tool with action=list)", name)
}

// parseSkill parses one skill document: the rig rule for the frontmatter block
// (S-06), the description sources (frontmatter description/summary/title, a
// top-of-document description: line, the first heading, the first text line) and
// the Markdown body without the frontmatter.
func parseSkill(name, filePath, content string, truncated bool) rig.Skill {
	body := skillBody(content)
	description := skillDescription(content, body)
	description = toolutilTruncate(description, 200, "…")
	if truncated {
		body += "\n[truncated: skill file is larger than the read limit]\n"
	}
	return rig.Skill{
		Name:        name,
		Description: description,
		Path:        filePath,
		Body:        body,
	}
}

// skillBody returns the Markdown body without its frontmatter block.
func skillBody(content string) string {
	_, body := splitFrontmatter(content)
	return body
}

// frontmatterPair is one raw frontmatter line, preserved for rewrite.
type frontmatterPair struct {
	raw   string
	key   string
	value string
}

// splitFrontmatter splits a SKILL.md document into its frontmatter pairs and
// the Markdown body. It mirrors the rig rule exactly (S-06): the block only
// exists when the first line is exactly "---" and a later line is exactly
// "---"; otherwise the whole content is body and the frontmatter is nil.
func splitFrontmatter(content string) (pairs []frontmatterPair, body string) {
	content = strings.TrimPrefix(content, "\ufeff")
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, content
	}
	closing := -1
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			closing = index
			break
		}
	}
	if closing < 0 {
		// Unclosed frontmatter is ordinary text (same as rig).
		return nil, content
	}
	pairs = make([]frontmatterPair, 0, closing-1)
	for _, line := range lines[1:closing] {
		key, value, _ := strings.Cut(line, ":")
		pairs = append(pairs, frontmatterPair{
			raw:   line,
			key:   strings.ToLower(strings.TrimSpace(key)),
			value: strings.Trim(strings.TrimSpace(value), `"'`),
		})
	}
	// The guaranteed blank separator line after the closing delimiter is not
	// part of the body (the editor always writes one).
	return pairs, strings.TrimLeft(strings.Join(lines[closing+1:], "\n"), "\r\n")
}

// skillDescription resolves the description with the rig fallback chain.
func skillDescription(content, body string) string {
	pairs, _ := splitFrontmatter(content)
	meta := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		if pair.key != "" && pair.value != "" {
			meta[pair.key] = pair.value
		}
	}
	if description := firstNonEmpty(meta["description"], meta["summary"], meta["title"]); description != "" {
		return description
	}
	lines := strings.Split(body, "\n")
	if description := descriptionLine(lines); description != "" {
		return description
	}
	if description := headingLine(lines); description != "" {
		return description
	}
	return firstTextLine(lines)
}

// descriptionLine finds a top-of-body "description:" line (rig fallback 2).
func descriptionLine(lines []string) string {
	for index := 0; index < len(lines) && index < 40; index++ {
		line := strings.TrimSpace(lines[index])
		if rest, ok := strings.CutPrefix(strings.ToLower(line), "description:"); ok {
			if description := strings.TrimSpace(line[len(line)-len(rest):]); description != "" {
				return description
			}
		}
	}
	return ""
}

// headingLine finds the first "# " heading (rig fallback 3).
func headingLine(lines []string) string {
	for index := 0; index < len(lines) && index < 40; index++ {
		line := strings.TrimSpace(lines[index])
		if rest, ok := strings.CutPrefix(line, "# "); ok {
			if description := strings.TrimSpace(rest); description != "" {
				return description
			}
		}
	}
	return ""
}

// firstTextLine returns the first non-empty, non-heading, non-delimiter line
// (rig fallback 4).
func firstTextLine(lines []string) string {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" {
			continue
		}
		return trimmed
	}
	return ""
}

// firstNonEmpty returns the first non-empty trimmed value.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// readSkillPrefix reads at most limit bytes from one skill file and reports
// whether the file is larger than that.
func readSkillPrefix(path string, limit int64) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return "", false, err
	}
	truncated := int64(len(data)) > limit
	if truncated {
		data = data[:limit]
	}
	return string(data), truncated, nil
}

// toolutilTruncate truncates s to at most limit bytes on a rune boundary and
// appends suffix when it truncated.
func toolutilTruncate(s string, limit int, suffix string) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + suffix
}

// utf8RuneStart reports whether b is not a UTF-8 continuation byte.
func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// addWarning appends one unique discovery warning (bounded).
func (s *service) addWarning(warnings *[]string, message string) {
	if len(*warnings) >= maxWarnings {
		return
	}
	for _, existing := range *warnings {
		if existing == message {
			return
		}
	}
	*warnings = append(*warnings, message)
}

// setWarnings publishes this scan's warnings for the settings page.
func (s *service) setWarnings(warnings []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.warnings = append([]string(nil), warnings...)
}

// Warnings implements contracts.SkillWarnings.
func (s *service) Warnings() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.warnings...)
}

// pathWithin reports whether path (which may be a symlink) resolves inside
// realDir. It is the containment check that keeps a scan from exposing files
// outside a skill root.
func pathWithin(realDir, path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	resolved = filepath.Clean(resolved)
	realDir = filepath.Clean(realDir)
	if resolved == realDir {
		return true
	}
	return strings.HasPrefix(resolved, realDir+string(filepath.Separator))
}
