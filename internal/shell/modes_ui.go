package shell

import (
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/runtime"
)

// agentModeOption is one entry of the agent mode picker.
type agentModeOption struct {
	Mode  string
	Label string
	Hint  string
}

// agentModeOptions returns the picker entries in runtime.Modes order. The
// labels are the user-facing names; the values are the runtime mode strings.
func agentModeOptions() []agentModeOption {
	return []agentModeOption{
		{
			Mode:  runtime.ModeStandard,
			Label: "Standard mode",
			Hint:  "Work with code, files, and information. Suitable for most tasks, with search, editing, terminal commands, and other tools available as needed.",
		},
		{
			Mode:  runtime.ModePTC,
			Label: "PTC mode",
			Hint:  "Includes all Standard mode capabilities. Better suited to tasks that call tools in batches and then filter, organize, deduplicate, count, or summarize the results.",
		},
		{
			Mode:  runtime.ModeMinimal,
			Label: "Minimal mode",
			Hint:  "The agent uses a terminal and image reading, with context compaction. Useful for testing and comparing its basic performance.",
		},
		{
			Mode:  runtime.ModeCreator,
			Label: "Creator mode",
			Hint:  "Customize ZeoKeel through conversation. Let the agent change whitelisted settings and write reusable skills to <root>/skills/<name>/SKILL.md with the standard file tools.",
		},
	}
}

// modeUIState is the picker's interaction state. Like the other per-App
// stores, it is keyed by the App and never read by the static pixel path.
type modeUIState struct {
	open bool
	err  string
}

var modeUIStates sync.Map // map[*App]*modeUIState

// modeUI returns the picker state of a, making it on first use.
func (a *App) modeUI() *modeUIState {
	if s, ok := modeUIStates.Load(a); ok {
		return s.(*modeUIState)
	}
	actual, _ := modeUIStates.LoadOrStore(a, &modeUIState{})
	return actual.(*modeUIState)
}

// currentAgentMode returns the current session's mode, or standard in static
// mode. The controller stores the mode per session and falls back to standard.
func (a *App) currentAgentMode() string {
	if a.Ctrl == nil {
		return runtime.ModeStandard
	}
	if mode := a.Ctrl.Mode(); mode != "" {
		return mode
	}
	return runtime.ModeStandard
}

// canonicalAgentMode normalizes a persisted or displayed mode value so alias
// spellings (for example "cordis" for Creator mode) render and compare as their
// canonical mode. Unknown values are returned unchanged, which keeps the
// label/hint fallbacks and the settings highlight from matching anything.
func canonicalAgentMode(mode string) string {
	if normalized, err := runtime.NormalizeMode(mode); err == nil {
		return normalized
	}
	return mode
}

// agentModeLabel renders one mode for the picker.
func (a *App) agentModeLabel(mode string) string {
	mode = canonicalAgentMode(mode)
	if key := "modes." + mode + ".label"; hasTranslation(key) {
		return a.T(key)
	}
	for _, option := range agentModeOptions() {
		if option.Mode == mode {
			return option.Label
		}
	}
	return a.T("modes.standard.label")
}

// agentModeHint renders one mode's hint for the picker.
func (a *App) agentModeHint(mode string) string {
	mode = canonicalAgentMode(mode)
	if key := "modes." + mode + ".hint"; hasTranslation(key) {
		return a.T(key)
	}
	for _, option := range agentModeOptions() {
		if option.Mode == mode {
			return option.Hint
		}
	}
	return ""
}

// setAgentMode asks the controller to persist and apply a mode. Persistence is
// the controller's job; the app's mode switcher hook is what rebuilds the
// runtime agent. A switcher failure is shown inline instead of being dropped.
func (a *App) setAgentMode(mode string) {
	state := a.modeUI()
	state.err = ""
	if a.Ctrl == nil {
		return
	}
	// Home is a new-task surface: stage the choice for the session the Home
	// composer creates (and remember it as the new-session preset). It must
	// never rewrite the mode of the last-opened session.
	if a.backgroundScreen() == Home {
		a.homeMode = mode
		if a.Ctrl.Store() != nil {
			if err := a.Ctrl.Store().SetSetting("agent.preset", mode); err != nil {
				state.err = err.Error()
			}
		}
		return
	}
	if err := a.Ctrl.SetMode(mode); err != nil {
		state.err = err.Error()
	}
}

// agentModeMenu is the picker's popover body. Selecting an entry closes the
// popover and applies the mode; the selected entry is highlighted. The rows
// and the panel come from the shared popover menu helpers, so the picker
// matches the composer's other menus.
func (a *App) agentModeMenu(c *ui.Context, t *design.Tokens, open *bool) {
	current := a.currentAgentMode()
	if a.backgroundScreen() == Home {
		// Home chooses the preset for the next session; mark that choice, not
		// the last-opened session's mode.
		current = a.homeAgentMode()
	}
	a.popoverMenuPanel(c, t, func() {
		for _, option := range agentModeOptions() {
			option := option
			a.popoverMenuRow(c, t, a.agentModeLabel(option.Mode), a.agentModeHint(option.Mode),
				option.Mode == current, nil, func() {
					*open = false
					a.setAgentMode(option.Mode)
				})
		}
	})
}

// agentModeSummary is a compact one-line mode badge for panels that do not
// need the full picker (the team panel header uses it).
func (a *App) agentModeSummary(c *ui.Context, t *design.Tokens) {
	ui.Text(c, a.agentModeLabel(a.currentAgentMode())).FontSize(10).
		FixedLineHeight(16).TextColor(t.LabelTertiary).NoWrap()
}
