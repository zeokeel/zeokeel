package shell

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/store"
)

// settingsTab identifies the selected page of the live Settings modal.
type settingsTab int

const (
	settingsTabGeneral settingsTab = iota
	settingsTabModels
	settingsTabBuiltinPlugins
	settingsTabAgentPresets
)

// modelDraft is the edit state of the Add/Edit model dialog.
type modelDraft struct {
	id, name, baseURL, apiKey, model, effort, preset string
}

// modelProbeState is the Add model dialog's connection check as the frame
// paints it: running, the outcome, and the one-line message next to the
// button. It lives on App (not shellUIState) so the async probe can write it
// while the view goroutine reads it.
type modelProbeState struct {
	running bool
	ok      bool
	message string
}

// providerPreset fills the Add model dialog's wire fields for one known
// OpenAI-compatible provider. name is the product name (the same in every
// language); an empty name is the custom endpoint, whose label is translated.
type providerPreset struct {
	id      string
	name    string
	baseURL string
	model   string
	keyHint string
}

// providerPresets are the service presets the Add model dialog offers. They
// only pre-fill Base URL and Model; the key stays whatever the user types.
var providerPresets = []providerPreset{
	{id: "custom", keyHint: "sk-..."},
	{id: "deepseek", name: "DeepSeek", baseURL: "https://api.deepseek.com", model: "deepseek-chat", keyHint: "sk-..."},
	{id: "openai", name: "OpenAI", baseURL: "https://api.openai.com/v1", model: "gpt-4o-mini", keyHint: "sk-..."},
	{id: "ollama", name: "Ollama", baseURL: "http://localhost:11434/v1", model: "llama3.2", keyHint: ""},
}

// probeModelConnection is the "Test connection" check: an OpenAI-compatible
// GET {base}/models, the cheapest request that separates a bad URL from a bad
// key without spending tokens. It is a variable so tests never touch a
// network. The controller's provider client already retries turns; this probe
// is deliberately one short attempt.
var probeModelConnection = func(ctx context.Context, baseURL, apiKey string) error {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return fmt.Errorf("empty base URL")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("invalid base URL %q", baseURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if key := strings.TrimSpace(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // probe response
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	return nil
}

// automationDraft is the edit state of the Add/Edit automation dialog. The
// binding fields (workspace/permission/model) scope each run's session; empty
// means the app default.
type automationDraft struct {
	id, title, prompt, cron        string
	workspaceID, permission, model string
	enabled                        bool
}

// shellUIState is the live interaction state the shell keeps per App but
// cannot add to App itself: app.go is owned by other changes. Like the motion
// and liveUIState stores, it is keyed by the App and never read by the static
// pixel path.
type shellUIState struct {
	settingsTab settingsTab

	modelDialog bool
	modelDraft  modelDraft

	automationDialog bool
	automationDraft  automationDraft
	automationFilter string // all, enabled, inactive
	automationTab    int
	automationQuery  string

	// rightNewTabOpen is the right panel's "New tab" menu: Workspace files or
	// New terminal. It mirrors DSH's new-tab picker instead of silently
	// resetting the current tab.
	rightNewTabOpen bool
	// rightTabIndex mirrors App.rightTab for the TabsBase selection pointer.
	rightTabIndex int

	// queued holds one message per session that was sent while a turn was
	// running and the Send behavior is Queue. The view submits it when the
	// turn reaches idle.
	queued map[string]string

	// railWorkspaceOpen is the collapsed rail's per-workspace session flyout
	// (one bool per workspace id, so each workspace icon owns its popover).
	railWorkspaceOpen map[string]*bool

	// textDrafts are the in-progress values of the settings text rows (the
	// proxy URL today), keyed by settings key, so typing does not rebuild the
	// value from the store on every frame.
	textDrafts map[string]string
	// confirmOpen/confirmKind are the shared destructive-confirmation dialog
	// the Data & privacy page opens (reset settings, delete conversations).
	confirmOpen bool
	confirmKind string

	// Draft persistence bookkeeping (PD-10): the last values written per
	// session and when, so typing writes the store at most every
	// draftPersistDelay and a switch writes immediately.
	draftPersisted   map[string]string
	draftPersistedAt time.Time

	// PD-07 management page state: the MCP JSON editor draft (seeded from the
	// settings store on first open) and the AGENTS.md editor.
	mcpLoaded     bool
	mcpDraft      string
	agentsEditing bool
	agentsDraft   string
	agentsErr     string

	// PD-06 automation page state: the dialog's three binding selectors and
	// which task cards have their run history expanded.
	automationWorkspaceOpen  bool
	automationPermissionOpen bool
	automationModelOpen      bool
	automationRunsOpen       map[string]bool

	// importOpen is the PD-04 "Import from DSH" instructions dialog.
	importOpen bool

	// Updates page state: the async check's progress and last result.
	updateChecking bool
	updateMessage  string

	// presetPickerOpen is the live header's FP-06 preset popover.
	presetPickerOpen bool

	// sessionCache is the AR-08 session-list cache: the view renders it while
	// a background goroutine refreshes it.
	sessionCache *sessionListCache

	// AR-08 session-open async: the id being opened and the completed results
	// waiting for the UI thread.
	sessionOpeningID   string
	sessionOpenResults chan sessionOpenResult
}

// shellUIStates maps each App to its shell UI state.
var shellUIStates sync.Map // map[*App]*shellUIState

// shellUI returns the shell interaction state of a, making it on first use.
func (a *App) shellUI() *shellUIState {
	if s, ok := shellUIStates.Load(a); ok {
		return s.(*shellUIState)
	}
	s := &shellUIState{automationFilter: "all"}
	actual, _ := shellUIStates.LoadOrStore(a, s)
	return actual.(*shellUIState)
}

// modelsSettingsPage is Settings > Models: the brand page heading, the
// persisted model list as native cards, and the add/edit/delete actions.
func (a *App) modelsSettingsPage(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Column(c).Gap(th.Space(4)).Children(func() {
		ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).
			Gap(th.Space(4)).Children(func() {
			ui.Column(c).Grow(1).Gap(th.Space(1)).Children(func() {
				settingsText(c, a.T("models.models"), design.TypeL, t.LabelPrimary).FontWeight(600)
				settingsText(c, a.T("models.choose_the_model_the_agent_uses_by_default"),
					design.TypeXS, t.LabelTertiary)
			})
			a.modelsAddButton(c, t)
		})

		if a.Ctrl == nil {
			settingsText(c, a.T("models.models_require_the_live_session_service"),
				design.TypeXXS, t.LabelTertiary)
			return
		}

		models, err := a.Ctrl.Models().List()
		if err != nil {
			settingsText(c, a.T("models.could_not_load_models"), design.TypeXXS, t.Danger)
			return
		}
		if len(models) == 0 {
			a.modelsEmpty(c, t)
			return
		}
		ui.Column(c).Gap(th.Space(2)).Children(func() {
			for _, model := range models {
				a.modelRow(c, t, model)
			}
		})
	})
	a.modelDialog(c, t)
	a.modelDeleteDialog(c, t)
}

// modelsEmpty is the empty model list with its call to action. The action is
// secondary here: the page header already carries the one primary "Add model"
// button, and two filled primaries in one viewport read as two competing
// actions (UX-07).
func (a *App) modelsEmpty(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Column(c).Padding(th.Space(8), 0).AlignItems(ui.Center).
		Gap(th.Space(2)).Children(func() {
		settingsText(c, a.T("models.no_models_configured"), design.TypeBody, t.LabelPrimary)
		settingsText(c, a.T("models.add_a_model_to_choose_it_from_the_composer"),
			design.TypeXS, t.LabelTertiary)
		label := a.T("models.add_model")
		button := ui.Button(c, label).Height(th.Space(9)).Label(label)
		button.OnClick(func() { a.beginModelAdd() })
	})
}

// modelsAddButton is the native brand "Add model" action: a primary button so
// keyboard and screen-reader users reach the model editor like any other.
func (a *App) modelsAddButton(c *ui.Context, t *design.Tokens) {
	label := a.T("models.add_model")
	el := ui.PrimaryButton(c, label).Height(t.Theme().Space(9)).Label(label)
	el.OnClick(func() { a.beginModelAdd() })
}

// modelRow is one model card: name, wire model id, base URL, the default
// marker and the native row actions.
func (a *App) modelRow(c *ui.Context, t *design.Tokens, model store.Model) {
	th := t.Theme()
	ui.Row(c).Padding(th.Space(3), 0).Gap(th.Space(2)).
		Radius(th.Radius).Border(1, t.BorderL2).Background(t.BgRaised).
		AlignItems(ui.Center).
		Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
					settingsText(c, modelDisplayName(model), design.TypeBody, t.LabelPrimary).
						FontWeight(500).MaxLines(1)
					if model.Default {
						ui.Row(c).Height(18).PaddingX(6).Radius(design.RadiusMax).
							Background(t.Accent.Alpha(0.10)).AlignItems(ui.Center).
							Children(func() {
								settingsText(c, a.T("models.default"), design.TypeXXXS, t.Accent)
							})
					}
				})
				settingsText(c, modelDetail(model), design.TypeXXS, t.LabelTertiary).MaxLines(1)
			})
			// Set default is only offered on non-default rows, as DSH's
			// baseline does (the default row shows Edit). A no-op action that
			// toasted success was worse than no action at all.
			if !model.Default {
				a.modelAction(c, t, a.T("models.set_default"), func() { a.setDefaultModel(c, model) })
			}
			a.modelAction(c, t, a.T("models.edit"), func() { a.beginModelEdit(model) })
			a.modelAction(c, t, a.T("models.delete"), func() { a.beginModelDelete(model) })
		})
}

// modelAction is a native row action: Set default/Edit/Delete keep their
// accessible labels, keyboard focus and activation.
func (a *App) modelAction(c *ui.Context, t *design.Tokens, label string, fn func()) {
	el := ui.Button(c, label).Height(t.Theme().Space(8)).Label(label)
	el.OnClick(fn)
}

// modelDisplayName is the human name of a model, with fallbacks for a row
// written by hand.
func modelDisplayName(model store.Model) string {
	if strings.TrimSpace(model.Name) != "" {
		return strings.TrimSpace(model.Name)
	}
	if strings.TrimSpace(model.Model) != "" {
		return strings.TrimSpace(model.Model)
	}
	return model.ID
}

// modelDetail is the second line of a model row.
func modelDetail(model store.Model) string {
	parts := make([]string, 0, 3)
	if model.Model != "" {
		parts = append(parts, model.Model)
	}
	if model.BaseURL != "" {
		parts = append(parts, model.BaseURL)
	}
	if model.ReasoningEffort != "" {
		parts = append(parts, model.ReasoningEffort)
	}
	if len(parts) == 0 {
		return model.ID
	}
	return strings.Join(parts, " · ")
}

// beginModelAdd opens the dialog in create mode. The provider preset starts on
// the custom endpoint (fields empty) so nothing is prefilled behind the user's
// back; choosing a preset fills Base URL and Model.
func (a *App) beginModelAdd() {
	a.resetModelProbe()
	st := a.shellUI()
	st.modelDraft = modelDraft{preset: "custom"}
	st.modelDialog = true
}

// beginModelEdit opens the dialog prefilled with model. The preset row matches
// the saved Base URL when it is one of the known providers, so editing shows
// which service the model came from.
func (a *App) beginModelEdit(model store.Model) {
	a.resetModelProbe()
	st := a.shellUI()
	st.modelDraft = modelDraft{
		id: model.ID, name: model.Name, baseURL: model.BaseURL,
		apiKey: model.APIKey, model: model.Model, effort: model.ReasoningEffort,
		preset: presetForBaseURL(model.BaseURL),
	}
	st.modelDialog = true
}

// presetForBaseURL returns the provider preset whose Base URL matches, or the
// custom endpoint.
func presetForBaseURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return "custom"
	}
	for _, preset := range providerPresets {
		if preset.baseURL != "" && strings.TrimRight(preset.baseURL, "/") == base {
			return preset.id
		}
	}
	return "custom"
}

// providerPresetLabel is a preset's visible name: the product name, or the
// translated "Custom" for the custom endpoint.
func (a *App) providerPresetLabel(preset providerPreset) string {
	if preset.name != "" {
		return preset.name
	}
	return a.T("models.provider_custom")
}

// applyProviderPreset copies a preset's wire fields into the dialog draft and
// clears a previous probe result: the old result no longer describes the
// endpoint the fields now name.
func (a *App) applyProviderPreset(preset providerPreset) {
	st := a.shellUI()
	st.modelDraft.preset = preset.id
	if preset.baseURL != "" {
		st.modelDraft.baseURL = preset.baseURL
	}
	if preset.model != "" {
		st.modelDraft.model = preset.model
	}
	a.resetModelProbe()
}

// resetModelProbe cancels a running "Test connection" and clears its result;
// the dialog calls it when it opens, closes or points at different fields.
func (a *App) resetModelProbe() {
	if a.modelProbeCancel != nil {
		a.modelProbeCancel()
		a.modelProbeCancel = nil
	}
	a.setModelProbe(modelProbeState{})
}

// currentModelProbe reads the probe state; the probe goroutine writes it, so
// the frame and the goroutine share it through modelProbeMu.
func (a *App) currentModelProbe() modelProbeState {
	a.modelProbeMu.Lock()
	defer a.modelProbeMu.Unlock()
	return a.modelProbe
}

// setModelProbe replaces the probe state under modelProbeMu.
func (a *App) setModelProbe(state modelProbeState) {
	a.modelProbeMu.Lock()
	a.modelProbe = state
	a.modelProbeMu.Unlock()
}

// testModelConnection starts the probe for the current draft fields: the
// request runs off the view goroutine with a short timeout, and the frame
// paints App.modelProbe. The messages are resolved before the goroutine starts
// because a.T follows the UI language, which the view goroutine owns.
func (a *App) testModelConnection(c *ui.Context) {
	st := a.shellUI()
	draft := st.modelDraft
	base := strings.TrimSpace(draft.baseURL)
	model := strings.TrimSpace(draft.model)
	if base == "" || model == "" {
		a.setModelProbe(modelProbeState{message: a.T("models.test_needs_url_and_model")})
		if c != nil {
			c.Invalidate()
		}
		return
	}
	a.resetModelProbe()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	a.modelProbeCancel = cancel
	a.setModelProbe(modelProbeState{running: true, message: a.T("models.testing_connection")})
	if c != nil {
		c.Invalidate()
	}
	okMessage := a.T("models.connection_ok")
	failTemplate := a.T("models.connection_failed")
	apiKey := draft.apiKey
	go func() {
		err := probeModelConnection(ctx, base, apiKey)
		cancel()
		if err != nil {
			a.setModelProbe(modelProbeState{message: fmt.Sprintf(failTemplate, err.Error())})
		} else {
			a.setModelProbe(modelProbeState{ok: true, message: okMessage})
		}
		if c != nil {
			c.Invalidate()
		}
	}()
}

// modelProviderField is the Add model dialog's provider preset row: choosing
// DeepSeek/OpenAI/Ollama fills Base URL and Model so the common cases need only
// a key. The custom row leaves both fields alone.
func (a *App) modelProviderField(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	st := a.shellUI()
	labels := make([]string, 0, len(providerPresets))
	current := ""
	for _, preset := range providerPresets {
		label := a.providerPresetLabel(preset)
		labels = append(labels, label)
		if preset.id == st.modelDraft.preset {
			current = label
		}
	}
	if current == "" {
		current = a.T("models.provider_custom")
	}
	ui.Field(c, a.T("models.provider"), func() {
		sel := ui.Select(c, &current, labels)
		sel.Height(th.Space(9)).MinWidth(th.Space(36)).Radius(design.RadiusSM)
		if sel.Changed() {
			for _, preset := range providerPresets {
				if a.providerPresetLabel(preset) == current {
					a.applyProviderPreset(preset)
					break
				}
			}
		}
	})
}

// modelDialog is the Add/Edit model form: the native mygo Form lays the
// labels beside the controls, and the buttons stay native. The provider
// presets and the connection test live above the form's Save/Cancel so the
// user can check a URL and key before persisting them (PD-02).
func (a *App) modelDialog(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	st := a.shellUI()
	ui.Modal(c, &st.modelDialog, func() {
		ui.Column(c).Width(th.Space(120)).Gap(th.Space(4)).Children(func() {
			title := a.T("models.add_model")
			if st.modelDraft.id != "" {
				title = a.T("models.edit_model")
			}
			settingsText(c, title, design.TypeL, t.LabelPrimary).FontWeight(600)
			a.modelProviderField(c, t)
			ui.Form(c, func() {
				a.modelField(c, t, a.T("models.name"), &st.modelDraft.name,
					"DeepSeek V4.1 Flash", true, false)
				a.modelField(c, t, a.T("models.base_url"), &st.modelDraft.baseURL,
					"https://api.deepseek.com", false, false)
				a.modelField(c, t, a.T("models.api_key"), &st.modelDraft.apiKey,
					"sk-...", false, true)
				a.modelField(c, t, a.T("models.model"), &st.modelDraft.model,
					"deepseek-v4.1-flash", false, false)
				a.modelField(c, t, a.T("models.reasoning_effort"), &st.modelDraft.effort,
					a.T("common.high"), false, false)
			})
			ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				probe := a.currentModelProbe()
				testLabel := a.T("models.test_connection")
				if probe.running {
					testLabel = a.T("models.testing_connection")
				}
				button := ui.Button(c, testLabel).Height(th.Space(8)).Label(testLabel)
				button.OnClick(func() { a.testModelConnection(c) })
				if msg := probe.message; msg != "" {
					color := t.LabelTertiary
					switch {
					case probe.running:
					case probe.ok:
						color = t.Success
					default:
						color = t.Danger
					}
					ui.Text(c, msg).FontSize(11).FixedLineHeight(16).
						TextColor(color).MaxLines(2).Ellipsis("…")
				}
			})
			ui.Row(c).Justify(ui.End).Gap(th.Space(2)).
				Padding(th.Space(2), 0, 0, 0).Children(func() {
				if ui.Button(c, a.T("models.cancel")).Clicked() {
					a.resetModelProbe()
					st.modelDialog = false
				}
				if ui.PrimaryButton(c, a.T("models.save")).Clicked() {
					a.confirmModel(c)
				}
			})
		})
	})
}

// modelField is one labelled text input in the native model form. secret
// fields (the API key) are masked so the stored credential is never painted on
// screen.
func (a *App) modelField(c *ui.Context, t *design.Tokens, label string, value *string, placeholder string, autofocus, secret bool) {
	ui.Field(c, label, func() {
		input := ui.TextInput(c, value).Label(label).Placeholder(placeholder)
		if secret {
			input = input.Password()
		}
		if autofocus {
			input.AutoFocus()
		}
	})
}

// confirmModel validates and persists the model dialog.
func (a *App) confirmModel(c *ui.Context) {
	st := a.shellUI()
	if a.Ctrl == nil {
		st.modelDialog = false
		return
	}
	draft := st.modelDraft
	model := store.Model{
		ID:              draft.id,
		Name:            strings.TrimSpace(draft.name),
		BaseURL:         strings.TrimSpace(draft.baseURL),
		APIKey:          strings.TrimSpace(draft.apiKey),
		Model:           strings.TrimSpace(draft.model),
		ReasoningEffort: strings.TrimSpace(draft.effort),
	}
	if model.Name == "" {
		c.Toast(a.T("models.model_name_is_required"))
		return
	}
	var err error
	if model.ID == "" {
		_, err = a.Ctrl.Models().Create(model)
	} else {
		_, err = a.Ctrl.Models().Update(model)
	}
	if err != nil {
		c.Toast(a.T("models.could_not_save_model"))
		return
	}
	a.resetModelProbe()
	st.modelDialog = false
	c.Toast(a.T("models.model_saved"))
}

// setDefaultModel marks a model as the selected default.
func (a *App) setDefaultModel(c *ui.Context, model store.Model) {
	if a.Ctrl == nil {
		return
	}
	if err := a.Ctrl.Models().SetDefault(model.ID); err != nil {
		c.Toast(a.T("models.could_not_select_model"))
		return
	}
	c.Toast(a.T("models.default_model_updated"))
}

// beginModelDelete opens the delete confirmation for model (PD-18): deleting
// the default model changes what every session falls back to, so it must not
// happen on a single click.
func (a *App) beginModelDelete(model store.Model) {
	a.modelDeleteID = model.ID
	a.modelDeleteName = modelDisplayName(model)
	a.modelDeleteOpen = true
}

// modelDeleteDialog is the model row's destructive confirmation, matching the
// Automation task delete: Cancel takes the focus, Esc and the backdrop dismiss
// it, and the danger button is never the default action.
func (a *App) modelDeleteDialog(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Modal(c, &a.modelDeleteOpen, func() {
		ui.Column(c).Width(th.Space(105)).Gap(th.Space(3)).Children(func() {
			settingsText(c, a.T("models.delete_model_title"), design.TypeM, t.LabelPrimary).
				FontWeight(600)
			message := a.T("models.delete_model_named")
			settingsText(c, fmt.Sprintf(message, a.modelDeleteName), design.TypeXS,
				t.LabelSecondary).MaxLines(3)
			ui.Row(c).Gap(th.Space(2)).Justify(ui.End).Children(func() {
				if ui.Button(c, a.T("models.cancel")).AutoFocus().Clicked() {
					a.modelDeleteOpen = false
				}
				if ui.Button(c, a.T("models.delete_model_confirm")).TextColor(t.Danger).Clicked() {
					a.confirmModelDelete(c)
				}
			})
		})
	})
}

// confirmModelDelete removes the confirmed model. A failure keeps the dialog
// open and reports through the toast so the user can retry or cancel.
func (a *App) confirmModelDelete(c *ui.Context) {
	if a.Ctrl == nil {
		a.modelDeleteOpen = false
		return
	}
	if err := a.Ctrl.Models().Delete(a.modelDeleteID); err != nil {
		c.Toast(a.T("models.could_not_delete_model"))
		return
	}
	a.modelDeleteOpen = false
	a.modelDeleteID = ""
	a.modelDeleteName = ""
	c.Toast(a.T("models.model_deleted"))
}

// composerModelID is the model the current composer controls: the session's
// own choice on Session, the Home/new-task choice on Home, else the global
// default.
func (a *App) composerModelID() string {
	if a.Ctrl != nil {
		switch a.backgroundScreen() {
		case Session:
			if id := a.Ctrl.CurrentSessionID(); id != "" {
				return a.Ctrl.SessionModelOrDefault(id)
			}
		case Home:
			return a.homeModelID()
		}
	}
	if a.Ctrl == nil {
		return ""
	}
	if id, err := a.Ctrl.Models().DefaultID(); err == nil {
		return strings.TrimSpace(id)
	}
	return ""
}

// composerModel returns the selected model record for the composer, or nil.
func (a *App) composerModel() *store.Model {
	if a.Ctrl == nil {
		return nil
	}
	if id := a.composerModelID(); id != "" {
		if model, err := a.Ctrl.Models().Get(id); err == nil && model != nil {
			return model
		}
	}
	model, err := a.defaultModel()
	if err != nil {
		return nil
	}
	return model
}

// composerModelLabel is the composer badge's model name.
func (a *App) composerModelLabel() string {
	if model := a.composerModel(); model != nil {
		if name := modelDisplayName(*model); name != "" {
			return name
		}
	}
	return a.T("models.no_model")
}

// DefaultModelLabel is the badge text for the selected default model. In the
// static shell it keeps the baseline's demo label; a live shell without a
// default says so instead of guessing.
func (a *App) DefaultModelLabel() string {
	if a.Ctrl == nil {
		return "DeepSeek-V4.1-Flash"
	}
	if label := a.Ctrl.DefaultModelLabel(); label != "" {
		return label
	}
	return a.T("models.no_model")
}

// ModelBadge is the composer's model badge. Session renderers call it for the
// live composer; it reads the selected default from the controller.
func (a *App) ModelBadge(c *ui.Context, t *design.Tokens) {
	a.modelBadge(c, t)
}

// modelBadge draws the composer model selector: the selected default's name,
// its reasoning effort when set, and the chevron.
func (a *App) modelBadge(c *ui.Context, t *design.Tokens) {
	ui.Row(c).Height(28).Padding(0, design.Space1, 0, design.Space2).
		Gap(design.Space1).AlignItems(ui.Center).Children(func() {
		settingsText(c, a.DefaultModelLabel(), design.TypeXS, t.LabelSecondary).MaxLines(1)
		if model, err := a.defaultModel(); err == nil && model.ReasoningEffort != "" {
			ui.Row(c).Padding(1, design.Space2).Radius(design.RadiusMax).
				Background(t.Accent.Alpha(0.10)).Children(func() {
				settingsText(c, model.ReasoningEffort, design.TypeXXXS, t.Accent)
			})
		}
		ui.Image(c, settingsIconChevron).Size(14, 14).TextColor(t.LabelTertiary)
	})
}

// defaultModel reads the selected default model, if any.
func (a *App) defaultModel() (*store.Model, error) {
	if a.Ctrl == nil {
		return nil, fmt.Errorf("shell: static app has no model store")
	}
	return a.Ctrl.DefaultModel()
}
