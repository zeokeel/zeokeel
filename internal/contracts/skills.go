package contracts

import (
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

// KeySkills is provided by the skills backend module and consumed by the
// toolkit (skill tool) and the settings skills page.
var KeySkills = cordis.NewServiceKey[SkillsService]("zeokeel/skills")
