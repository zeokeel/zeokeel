package contracts

import (
	"context"

	cordis "github.com/zeokeel/zeokeel-kernel"
	rig "github.com/zeokeel/zeokeel-rig"
)

// Skill is one filesystem-backed agent skill document (SKILL.md).
type Skill = rig.Skill

// SkillsService exposes the discovered skills to the UI and to the agent's
// skill tool: the rig SkillStore surface plus the roots that were scanned.
type SkillsService interface {
	rig.SkillStore
	// Roots returns the skill directories scanned for this call, in
	// precedence order (configured dirs, then data dir, then workspace).
	Roots() []string
}

// SkillWarnings is the optional diagnostics half of the skills service: it
// reports discovery problems (a skill shadowed by an earlier root, a scan cap
// hit) so the settings page can show them instead of silently hiding a skill.
type SkillWarnings interface {
	Warnings() []string
}

// SettingSkillsDirs is the user-configurable extra skill directory list
// (os.PathListSeparator separated; relative paths resolve against the product
// data directory). It is scanned after the workspace roots and before the data
// directory, so a user-installed skill library overrides the shipped one.
const SettingSkillsDirs = "skills.dirs"

// KeySkills is provided by the skills backend module and consumed by the
// toolkit (skill tool) and the settings skills page.
var KeySkills = cordis.NewServiceKey[SkillsService]("zeokeel/skills")

// SkillDraft is one new skill submitted by the settings page: Name is the
// directory/slug, Description is optional frontmatter copy and Body is the
// Markdown content (both empty-name and empty-body drafts are rejected).
type SkillDraft struct {
	Name        string
	Description string
	Body        string
}

// SkillsEditor is the optional write half of the skills backend: the settings
// page creates/deletes SKILL.md skills below <dataDir>/skills through it. Only
// the product data directory is writable; configured/workspace roots stay
// read-only (DeleteSkill rejects them).
type SkillsEditor interface {
	// CreateSkill writes <dataDir>/skills/<slug>/SKILL.md and returns the
	// created skill. A name that already exists is rejected.
	CreateSkill(ctx context.Context, draft SkillDraft) (Skill, error)
	// UpdateSkill rewrites one existing data-dir skill's SKILL.md. The draft
	// name must match the existing directory name (the name is not changed);
	// skills from other roots are rejected.
	UpdateSkill(ctx context.Context, name string, draft SkillDraft) (Skill, error)
	// DeleteSkill removes one data-dir skill directory. Skills from other roots
	// are rejected.
	DeleteSkill(ctx context.Context, name string) error
}

// KeySkillsEditor is provided by the skills module alongside KeySkills; it is a
// separate key so read-only consumers (the toolkit) do not depend on write
// access. The settings page looks it up optionally and hides the create/delete
// actions when it is absent.
var KeySkillsEditor = cordis.NewServiceKey[SkillsEditor]("zeokeel/skills/editor")
