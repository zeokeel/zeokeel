package toolkit

// The agent-facing skill store honours skills.disabled. The Skills page keeps
// using the unfiltered KeySkills service so disabled skills stay visible and
// can be re-enabled; only the model-facing tool sees the filtered view.

import (
	"context"
	"fmt"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type filteredSkillStore struct {
	inner    contracts.SkillsService
	settings contracts.SettingsService
}

var _ contracts.SkillsService = filteredSkillStore{}
var _ rig.SkillStore = filteredSkillStore{}

// disabledSkillNames reads skills.disabled on every call so a UI toggle applies
// without rebuilding the toolkit.
func disabledSkillNames(settings contracts.SettingsService) map[string]bool {
	disabled := make(map[string]bool)
	if settings == nil {
		return disabled
	}
	names, err := contracts.ParseStringListJSON(settings.Get(contracts.SettingSkillsDisabled, ""))
	if err != nil {
		return disabled
	}
	for _, name := range names {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			disabled[strings.ToLower(trimmed)] = true
		}
	}
	return disabled
}

func (s filteredSkillStore) Roots() []string {
	if s.inner == nil {
		return nil
	}
	return s.inner.Roots()
}

func (s filteredSkillStore) List(ctx context.Context) ([]rig.Skill, error) {
	if s.inner == nil {
		return nil, nil
	}
	skills, err := s.inner.List(ctx)
	if err != nil {
		return nil, err
	}
	disabled := disabledSkillNames(s.settings)
	if len(disabled) == 0 {
		return skills, nil
	}
	kept := make([]rig.Skill, 0, len(skills))
	for _, skill := range skills {
		if disabled[strings.ToLower(strings.TrimSpace(skill.Name))] {
			continue
		}
		kept = append(kept, skill)
	}
	return kept, nil
}

func (s filteredSkillStore) Read(ctx context.Context, name string) (rig.Skill, error) {
	if s.inner == nil {
		return rig.Skill{}, fmt.Errorf("skill %q is not available", name)
	}
	if disabledSkillNames(s.settings)[strings.ToLower(strings.TrimSpace(name))] {
		return rig.Skill{}, fmt.Errorf("skill %q is disabled", name)
	}
	return s.inner.Read(ctx, name)
}
