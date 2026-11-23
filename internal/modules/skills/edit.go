package skills

// The skills editor is the SDK store's editor half; this file only maps the
// product draft type and keeps the contracts methods.

import (
	"context"

	rigskills "github.com/zeokeel/zeokeel-rig/skills"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

var _ contracts.SkillsEditor = (*service)(nil)

// CreateSkill implements contracts.SkillsEditor.
func (s *service) CreateSkill(ctx context.Context, draft contracts.SkillDraft) (contracts.Skill, error) {
	return s.editorStore().CreateSkill(ctx, rigskills.Draft{Name: draft.Name, Description: draft.Description, Body: draft.Body})
}

// UpdateSkill implements contracts.SkillsEditor.
func (s *service) UpdateSkill(ctx context.Context, name string, draft contracts.SkillDraft) (contracts.Skill, error) {
	return s.editorStore().UpdateSkill(ctx, name, rigskills.Draft{Name: draft.Name, Description: draft.Description, Body: draft.Body})
}

// DeleteSkill implements contracts.SkillsEditor.
func (s *service) DeleteSkill(ctx context.Context, name string) error {
	return s.editorStore().DeleteSkill(ctx, name)
}
