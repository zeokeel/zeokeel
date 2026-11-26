package agent

// Role administration (M3): create, override, delete — and the two refusals that
// keep "tidy the roster" from breaking live sessions.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func adminService(t *testing.T) (*presetAdmin, *fakeSettings) {
	t.Helper()
	settings := newFakeSettings(nil)
	svc := newService(Config{SystemPrompt: "base"}, nil, settings, &fakeLogger{}, newFakeStore(), fakeApprovals{})
	return &presetAdmin{service: svc}, settings
}

func TestPresetAdminCreatesARole(t *testing.T) {
	admin, settings := adminService(t)
	stored, err := admin.Put(context.Background(), contracts.AgentPreset{
		ID: "reviewer", Name: "复核员", Tools: []string{"read_file", "grep"}, Deny: []string{"bash"},
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if stored.ID != "reviewer" || stored.Name != "复核员" {
		t.Fatalf("stored = %+v", stored)
	}
	// The row the reader reads is the one the writer wrote.
	rows, err := contracts.ParseAgentPresets(settings.Get(contracts.SettingAgentPresets, ""))
	if err != nil || len(rows) != 1 || rows[0].ID != "reviewer" {
		t.Fatalf("row = (%+v, %v), want the new role", rows, err)
	}
	list, err := admin.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, preset := range list {
		if preset.ID == "reviewer" {
			found = true
		}
	}
	if !found {
		t.Fatalf("List = %+v, want the new role among the built-ins", list)
	}
}

func TestPresetAdminRefusesAContradictoryRole(t *testing.T) {
	admin, settings := adminService(t)
	_, err := admin.Put(context.Background(), contracts.AgentPreset{
		ID: "bad", Name: "坏", Tools: []string{"bash"}, Deny: []string{"bash"},
	})
	if !errors.Is(err, contracts.ErrAgentPresetInvalid) {
		t.Fatalf("err = %v, want ErrAgentPresetInvalid", err)
	}
	if row := settings.Get(contracts.SettingAgentPresets, ""); row != "" {
		t.Fatalf("row = %q, want nothing written on a refused Put", row)
	}
}

func TestPresetAdminOverridesAndRestoresABuiltin(t *testing.T) {
	admin, _ := adminService(t)
	ctx := context.Background()
	// Override the built-in lead with a narrower surface.
	if _, err := admin.Put(ctx, contracts.AgentPreset{ID: contracts.PresetLeadID, Name: "编排 lead（自定义）", Tools: []string{"read_file"}}); err != nil {
		t.Fatalf("Put override: %v", err)
	}
	got, ok, err := admin.Get(ctx, contracts.PresetLeadID)
	if err != nil || !ok || got.Name != "编排 lead（自定义）" {
		t.Fatalf("Get = (%+v, %v, %v), want the override", got, ok, err)
	}
	// Deleting the override restores the shipped role instead of deleting it.
	if err := admin.Delete(ctx, contracts.PresetLeadID); err != nil {
		t.Fatalf("Delete override: %v", err)
	}
	got, ok, err = admin.Get(ctx, contracts.PresetLeadID)
	if err != nil || !ok || got.Name == "编排 lead（自定义）" {
		t.Fatalf("Get after delete = (%+v, %v, %v), want the built-in back", got, ok, err)
	}
	if len(got.Tools) == 0 {
		t.Fatalf("restored lead = %+v, want its built-in allowlist", got)
	}
}

func TestPresetAdminRefusesToDeleteABuiltin(t *testing.T) {
	admin, _ := adminService(t)
	err := admin.Delete(context.Background(), contracts.PresetAuditorID)
	if !errors.Is(err, contracts.ErrPresetBuiltin) {
		t.Fatalf("err = %v, want ErrPresetBuiltin", err)
	}
}

func TestPresetAdminDeleteUnknown(t *testing.T) {
	admin, _ := adminService(t)
	if err := admin.Delete(context.Background(), "nope"); !errors.Is(err, contracts.ErrPresetNotFound) {
		t.Fatalf("err = %v, want ErrPresetNotFound", err)
	}
}

// A malformed row from an older build must not make the roster unrepairable: the
// first admin write replaces it rather than failing on it.
func TestPresetAdminRepairsAMalformedRow(t *testing.T) {
	settings := newFakeSettings(map[string]string{contracts.SettingAgentPresets: "{broken"})
	svc := newService(Config{SystemPrompt: "base"}, nil, settings, &fakeLogger{}, newFakeStore(), fakeApprovals{})
	admin := &presetAdmin{service: svc}
	if _, err := admin.Put(context.Background(), contracts.AgentPreset{ID: "worker", Name: "Worker"}); err != nil {
		t.Fatalf("Put over a malformed row: %v", err)
	}
	row := settings.Get(contracts.SettingAgentPresets, "")
	if strings.Contains(row, "broken") || !strings.Contains(row, "worker") {
		t.Fatalf("row = %q, want the malformed row replaced", row)
	}
}
