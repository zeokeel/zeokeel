package agent

// Role administration (M3): the CRUD half of the agent presets.
//
// Roles live in one settings row (contracts.SettingAgentPresets) that the agent
// already reads on every run, so administration is a read-modify-write of that
// row — not a second store that could disagree with the reader.
//
// Two rules that make this safe to expose to a model or a person:
//   - only USER rows are written. A built-in role can be overridden (Put), and
//     removing the override restores the shipped role; a built-in with no
//     override cannot be deleted (ErrPresetBuiltin). Otherwise "tidy the roster"
//     would silently break every session whose preset is "lead".
//   - every write goes through ValidateAgentPresets, so a row that the reader
//     would later degrade on cannot be stored in the first place.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

var _ contracts.AgentPresetService = (*presetAdmin)(nil)

// presetAdmin implements contracts.AgentPresetService over one service's
// settings store.
type presetAdmin struct{ service *service }

// List returns the effective roster: user rows merged over the built-ins.
func (a *presetAdmin) List(ctx context.Context) ([]contracts.AgentPreset, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	presets := a.service.effectiveAgentPresets()
	sort.SliceStable(presets, func(i, j int) bool {
		if presets[i].Order != presets[j].Order {
			return presets[i].Order < presets[j].Order
		}
		return presets[i].ID < presets[j].ID
	})
	return presets, nil
}

// Get returns one effective role.
func (a *presetAdmin) Get(ctx context.Context, id string) (contracts.AgentPreset, bool, error) {
	if err := ctx.Err(); err != nil {
		return contracts.AgentPreset{}, false, err
	}
	preset, ok := a.service.agentPresetByID(id)
	return preset, ok, nil
}

// Put creates or overrides one role.
func (a *presetAdmin) Put(ctx context.Context, preset contracts.AgentPreset) (contracts.AgentPreset, error) {
	if err := ctx.Err(); err != nil {
		return contracts.AgentPreset{}, err
	}
	preset.ID = strings.TrimSpace(preset.ID)
	preset.Name = strings.TrimSpace(preset.Name)
	if preset.ID == "" || preset.Name == "" {
		return contracts.AgentPreset{}, fmt.Errorf("%w: id and name are required", contracts.ErrAgentPresetInvalid)
	}
	user, err := a.userRows()
	if err != nil {
		return contracts.AgentPreset{}, err
	}
	replaced := false
	for i := range user {
		if user[i].ID == preset.ID {
			user[i] = preset
			replaced = true
			break
		}
	}
	if !replaced {
		user = append(user, preset)
	}
	if err := a.writeRows(user); err != nil {
		return contracts.AgentPreset{}, err
	}
	stored, ok := a.service.agentPresetByID(preset.ID)
	if !ok {
		return contracts.AgentPreset{}, fmt.Errorf("preset %q vanished after write", preset.ID)
	}
	return stored, nil
}

// Delete removes a user row.
func (a *presetAdmin) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return contracts.ErrPresetNotFound
	}
	user, err := a.userRows()
	if err != nil {
		return err
	}
	kept := make([]contracts.AgentPreset, 0, len(user))
	removed := false
	for _, preset := range user {
		if preset.ID == id {
			removed = true
			continue
		}
		kept = append(kept, preset)
	}
	if !removed {
		// No user row: either a shipped role (refuse, the override is the only
		// thing a delete can mean) or nothing at all.
		if a.service.isBuiltinPreset(id) {
			return contracts.ErrPresetBuiltin
		}
		return contracts.ErrPresetNotFound
	}
	return a.writeRows(kept)
}

// userRows reads the raw user rows (empty when the row is absent or malformed —
// the same degradation the reader uses).
func (a *presetAdmin) userRows() ([]contracts.AgentPreset, error) {
	if a.service.settings == nil {
		return nil, ErrSettingsUnavailable
	}
	rows, err := contracts.ParseAgentPresets(a.service.settings.Get(contracts.SettingAgentPresets, ""))
	if err != nil {
		// A malformed row is treated as "no user presets" by every reader. An
		// admin write is the moment to replace it, not to fail on it: refusing
		// here would leave the roster unrepairable from the UI.
		a.service.logger.Printf("[agent] replacing malformed %s row: %v", contracts.SettingAgentPresets, err)
		return nil, nil
	}
	return rows, nil
}

// writeRows encodes, validates and stores one row set.
func (a *presetAdmin) writeRows(rows []contracts.AgentPreset) error {
	if a.service.settings == nil {
		return ErrSettingsUnavailable
	}
	encoded, err := contracts.EncodeAgentPresets(rows)
	if err != nil {
		return err
	}
	if err := contracts.ValidateAgentPresets(encoded); err != nil {
		return fmt.Errorf("%w: %v", contracts.ErrAgentPresetInvalid, err)
	}
	return a.service.settings.Set(contracts.SettingAgentPresets, encoded)
}
