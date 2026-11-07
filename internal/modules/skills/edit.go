package skills

// The skills editor half of the module: the settings page creates and deletes
// SKILL.md skills below <dataDir>/skills through contracts.SkillsEditor. Only
// the product data directory is writable: configured directories and the
// workspace stay read-only, so a UI action cannot touch another root's files.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// maxSkillNameRunes bounds a skill slug (its directory name).
const maxSkillNameRunes = 64

// compile-time check: the skills service also implements the optional editor.
var _ contracts.SkillsEditor = (*service)(nil)

// CreateSkill implements contracts.SkillsEditor: it validates the draft, writes
// <dataDir>/skills/<slug>/SKILL.md with name/description frontmatter and
// returns the created skill. The service rebuilds a rig FileStore per call
// (there is no cache), so the new skill is immediately visible to List/Read and
// the toolkit. An existing name is rejected instead of overwritten.
func (s *service) CreateSkill(ctx context.Context, draft contracts.SkillDraft) (contracts.Skill, error) {
	if err := contextErr(ctx); err != nil {
		return contracts.Skill{}, err
	}
	name, err := normalizeSkillName(draft.Name)
	if err != nil {
		return contracts.Skill{}, err
	}
	body := strings.TrimSpace(draft.Body)
	if body == "" {
		return contracts.Skill{}, errors.New("skill body is required")
	}
	description := normalizeSkillDescription(draft.Description)
	baseDir, err := s.dataDirSkillsDir()
	if err != nil {
		return contracts.Skill{}, err
	}
	targetDir := filepath.Join(baseDir, name)
	if !pathWithinDir(baseDir, targetDir) {
		return contracts.Skill{}, fmt.Errorf("skill %q escapes the data directory", name)
	}
	if _, err := os.Lstat(targetDir); err == nil {
		return contracts.Skill{}, fmt.Errorf("skill %q already exists", name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return contracts.Skill{}, fmt.Errorf("stat skill directory: %w", err)
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return contracts.Skill{}, fmt.Errorf("create skills directory: %w", err)
	}
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		return contracts.Skill{}, fmt.Errorf("create skill directory: %w", err)
	}
	path := filepath.Join(targetDir, "SKILL.md")
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s", name, description, body)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		_ = os.Remove(targetDir)
		return contracts.Skill{}, fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.RemoveAll(targetDir)
		return contracts.Skill{}, fmt.Errorf("write %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		_ = os.RemoveAll(targetDir)
		return contracts.Skill{}, fmt.Errorf("close %s: %w", path, err)
	}
	// The umask may have narrowed the mode; the file's mode is part of the
	// skill-format contract.
	if err := os.Chmod(path, 0o644); err != nil {
		_ = os.RemoveAll(targetDir)
		return contracts.Skill{}, fmt.Errorf("chmod %s: %w", path, err)
	}
	s.logger.Printf("%s: created skill %q at %s", ModuleID, name, path)
	return contracts.Skill{Name: name, Description: description, Path: path, Body: body}, nil
}

// DeleteSkill implements contracts.SkillsEditor: it deletes one skill directory
// below <dataDir>/skills. Names that are unsafe, absent, or that belong to
// another root (configured/workspace) are rejected with a readable error; a
// directory without SKILL.md is not treated as a skill and is left alone.
func (s *service) DeleteSkill(ctx context.Context, name string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	normalized, err := normalizeSkillName(name)
	if err != nil {
		return err
	}
	baseDir, err := s.dataDirSkillsDir()
	if err != nil {
		return err
	}
	targetDir := filepath.Join(baseDir, normalized)
	if !pathWithinDir(baseDir, targetDir) {
		return fmt.Errorf("skill %q escapes the data directory", normalized)
	}
	info, err := os.Lstat(targetDir)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("skill %q not found in the data directory", normalized)
	}
	if err != nil {
		return fmt.Errorf("stat skill directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("skill %q is a symlink; refusing to delete it", normalized)
	}
	if !info.IsDir() {
		return fmt.Errorf("skill %q is not a skill directory", normalized)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "SKILL.md")); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("skill %q not found in the data directory", normalized)
	} else if err != nil {
		return fmt.Errorf("stat skill file: %w", err)
	}
	// Defense in depth: resolve symlinked parents and confirm the real target
	// still lives below the real data-dir skills directory.
	if realBase, err := filepath.EvalSymlinks(baseDir); err == nil {
		if realTarget, err := filepath.EvalSymlinks(targetDir); err == nil && !pathWithinDir(realBase, realTarget) {
			return fmt.Errorf("skill %q resolves outside the data directory", normalized)
		}
	}
	if err := os.RemoveAll(targetDir); err != nil {
		return fmt.Errorf("delete skill directory: %w", err)
	}
	s.logger.Printf("%s: deleted skill %q", ModuleID, normalized)
	return nil
}

// contextErr reports a canceled context (nil contexts count as background).
func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// dataDirSkillsDir returns the absolute <dataDir>/skills directory.
func (s *service) dataDirSkillsDir() (string, error) {
	dataDir := strings.TrimSpace(s.dataDir)
	if dataDir == "" {
		return "", errors.New("no data directory for the skills editor")
	}
	return filepath.Abs(filepath.Join(dataDir, DataDirName))
}

// normalizeSkillName validates/trims a skill slug: 1-64 characters of ASCII
// letters/digits, Chinese characters, '-' or '_'. Everything else (paths,
// dots, spaces, newlines) is rejected, so a name is always one safe directory
// component below <dataDir>/skills.
func normalizeSkillName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("skill name is required")
	}
	if utf8.RuneCountInString(name) > maxSkillNameRunes {
		return "", fmt.Errorf("skill name is too long (max %d characters)", maxSkillNameRunes)
	}
	for _, char := range name {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '-' || char == '_':
		case unicode.Is(unicode.Han, char):
		default:
			return "", fmt.Errorf("skill name %q contains an unsupported character %q (use letters, digits, Chinese, '-' or '_')", name, char)
		}
	}
	return name, nil
}

// normalizeSkillDescription folds an optional description into one safe
// frontmatter line (no newlines that could inject YAML keys).
func normalizeSkillDescription(raw string) string {
	return strings.Join(strings.Fields(raw), " ")
}

// pathWithinDir reports whether path is dir itself or below it (after cleaning;
// both paths must already be absolute).
func pathWithinDir(dir, path string) bool {
	relative, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	if relative == "." {
		return true
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
