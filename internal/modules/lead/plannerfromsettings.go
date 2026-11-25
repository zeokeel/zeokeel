package lead

// PlannerFromSettings is the assembly seam: the product decides at activation
// time whether the Lead thinks with a model (when the chat settings are
// complete) or with rules (the safe default). A partially configured endpoint
// keeps the rule planner — half a configuration must never start deciding.

import (
	"context"
	"fmt"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/lib/planner"
)

// SettingsReader is the slice of the settings service this needs.
type SettingsReader interface {
	Get(key, fallback string) string
}

// PlannerFromSettings returns a ModelPlanner when the chat endpoint is fully
// configured, else the rule planner. openTasks may be nil.
func PlannerFromSettings(settings SettingsReader, openTasks func() int) (Planner, error) {
	if settings == nil {
		return RulePlanner{}, nil
	}
	baseURL := settings.Get(contracts.SettingsChatBaseURL, "")
	model := settings.Get(contracts.SettingsChatModel, "")
	if baseURL == "" || model == "" {
		return RulePlanner{}, nil
	}
	client, err := planner.New(planner.Config{
		BaseURL: baseURL,
		APIKey:  settings.Get(contracts.SettingsChatAPIKey, ""),
		Model:   model,
	})
	if err != nil {
		return nil, fmt.Errorf("lead: build model planner: %w", err)
	}
	return ModelPlanner{Model: client, OpenTasks: func(ctx context.Context) (int, error) {
		if openTasks == nil {
			return 0, nil
		}
		return openTasks(), nil
	}}, nil
}
