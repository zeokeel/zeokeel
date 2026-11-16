package toolkit

import (
	"context"
	"errors"
	"strings"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type filterSkillStore struct {
	list  []rig.Skill
	roots []string
}

func (f filterSkillStore) List(context.Context) ([]rig.Skill, error) {
	return append([]rig.Skill(nil), f.list...), nil
}

func (f filterSkillStore) Read(_ context.Context, name string) (rig.Skill, error) {
	for _, skill := range f.list {
		if skill.Name == name {
			return skill, nil
		}
	}
	return rig.Skill{}, errors.New("skill not found")
}

func (f filterSkillStore) Roots() []string { return append([]string(nil), f.roots...) }

func TestFilteredSkillStoreHonoursDisabledNames(t *testing.T) {
	settings := newAuthTestSettings()
	store := filterSkillStore{
		list:  []rig.Skill{{Name: "alpha", Path: "/a"}, {Name: "Beta", Path: "/b"}},
		roots: []string{"/skills"},
	}
	filtered := filteredSkillStore{inner: store, settings: settings}
	if got, err := filtered.List(context.Background()); err != nil || len(got) != 2 {
		t.Fatalf("List with no disabled skills = %v, %v", got, err)
	}
	if err := settings.Set(contracts.SettingSkillsDisabled, `["ALPHA"]`); err != nil {
		t.Fatal(err)
	}
	got, err := filtered.List(context.Background())
	if err != nil || len(got) != 1 || got[0].Name != "Beta" {
		t.Fatalf("List filtered = %v, %v", got, err)
	}
	if _, err := filtered.Read(context.Background(), "alpha"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("Read disabled skill error = %v", err)
	}
	if skill, err := filtered.Read(context.Background(), "Beta"); err != nil || skill.Name != "Beta" {
		t.Fatalf("Read enabled skill = %+v, %v", skill, err)
	}
	if roots := filtered.Roots(); len(roots) != 1 || roots[0] != "/skills" {
		t.Fatalf("Roots = %v", roots)
	}
}
