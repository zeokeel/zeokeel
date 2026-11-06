package shell

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/modelcatalog"
	"github.com/zeokeel/zeokeel/internal/store"
	"github.com/zeokeel/zeokeel/internal/updater"
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
//
// preset is a modelcatalog provider id or customProviderID; presetModel is the
// selected built-in model's id ("" for a hand-written custom model id).
// contextWindow is kept as text while the user types so an empty field means
// "unknown" and a partial number is not lost between frames.
type modelDraft struct {
	id, name, baseURL, apiKey, model, effort, contextWindow, preset, presetModel string
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

// customProviderID is the Add model dialog's row for an endpoint that is not
// one of the built-in catalog providers.
const customProviderID = "custom"

// dialogProviders returns the built-in provider choices the Add/Edit model
// dialog offers, in catalog order. Each choice fills the endpoint, key hint and
// its first published model, so DeepSeek and the other common providers need
// only a pasted key.
func dialogProviders() []modelcatalog.Provider {
	return modelcatalog.Providers()
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

	// Updates page state: the async check's progress and last result, the
	// release found by the check, and the download/install progress.
	updateChecking   bool
	updateMessage    string
	updateRelease    *updater.Release
	updateAvailable  bool
	updateInstalling bool
	updateInstalled  bool
	updateStaged     *updater.Staged
	// updateProgress* are written by the download goroutine and read by the
	// render; the mutex keeps the race detector quiet and the UI coherent.
	updateProgressMu         sync.Mutex
	updateProgressDownloaded int64
	updateProgressTotal      int64

	// presetPickerOpen is the live header's FP-06 preset popover.
	presetPickerOpen bool

	// FP-16 question-card tracking: which pending cards are on screen (so a
	// controller-withdrawn card can be explained) and which the user answered.
	questionSession  string
	questionSeen     map[string]bool
	questionAnswered map[string]bool

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
	parts := make([]string, 0, 4)
	if model.Model != "" {
		parts = append(parts, model.Model)
	}
	if model.BaseURL != "" {
		parts = append(parts, model.BaseURL)
	}
	if model.ReasoningEffort != "" {
		parts = append(parts, model.ReasoningEffort)
	}
	if model.ContextWindow > 0 {
		parts = append(parts, formatTokenCount(model.ContextWindow)+" ctx")
	}
	if len(parts) == 0 {
		return model.ID
	}
	return strings.Join(parts, " · ")
}

// formatTokenCount renders a token capacity compactly for a model row:
// 1000000 -> "1M", 262144 -> "262k", 512 -> "512".
func formatTokenCount(tokens int) string {
	if tokens >= 1000000 {
		value := float64(tokens) / 1000000
		if value == float64(int(value)) {
			return strconv.Itoa(int(value)) + "M"
		}
		return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(value, 'f', 1, 64), "0"), ".") + "M"
	}
	if tokens >= 1000 {
		return strconv.Itoa(tokens/1000) + "k"
	}
	return strconv.Itoa(tokens)
}

// formatContextWindowDraft renders a stored capacity for the dialog's text
// field; an unknown capacity leaves the field empty.
func formatContextWindowDraft(tokens int) string {
	if tokens <= 0 {
		return ""
	}
	return strconv.Itoa(tokens)
}

// parseContextWindowDraft reads the dialog's context-window field. An empty
// field means unknown (0); anything else must be a positive whole number.
func parseContextWindowDraft(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	tokens, err := strconv.Atoi(raw)
	if err != nil || tokens <= 0 {
		return 0, fmt.Errorf("invalid context window %q", raw)
	}
	return tokens, nil
}

// beginModelAdd opens the dialog in create mode on the built-in DeepSeek
// configuration (the catalog's first provider): endpoint, first published
// model, context window and default reasoning effort are prefilled, so a new
// user only pastes an API key. Any other provider, including a custom endpoint,
// is one pick away.
func (a *App) beginModelAdd() {
	a.resetModelProbe()
	st := a.shellUI()
	st.modelDraft = modelDraft{preset: customProviderID}
	if provider, ok := modelcatalog.LookupProvider("deepseek"); ok {
		a.applyProviderPreset(provider)
	}
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
		contextWindow: formatContextWindowDraft(model.ContextWindow),
		preset:        presetForBaseURL(model.BaseURL),
		presetModel:   presetModelFor(model.BaseURL, model.Model),
	}
	st.modelDialog = true
}

// presetForBaseURL returns the catalog provider whose Base URL matches, or the
// custom endpoint.
func presetForBaseURL(baseURL string) string {
	if provider, ok := modelcatalog.ProviderForBaseURL(baseURL); ok {
		return provider.ID
	}
	return customProviderID
}

// presetModelFor returns the built-in model id matching a saved endpoint and
// wire model, or "" for a hand-written model.
func presetModelFor(baseURL, wireModel string) string {
	provider, ok := modelcatalog.ProviderForBaseURL(baseURL)
	if !ok {
		return ""
	}
	if model, ok := modelcatalog.LookupModel(provider.ID, wireModel); ok {
		return model.ID
	}
	return ""
}

// providerPresetLabel is a provider's visible name: the catalog's product name,
// or the translated "Custom" for the custom endpoint.
func (a *App) providerPresetLabel(providerID string) string {
	if provider, ok := modelcatalog.LookupProvider(providerID); ok {
		return provider.Name
	}
	return a.T("models.provider_custom")
}

// applyProviderPreset copies a provider's endpoint and first published model
// into the dialog draft and clears a previous probe result: the old result no
// longer describes the endpoint the fields now name.
func (a *App) applyProviderPreset(provider modelcatalog.Provider) {
	st := a.shellUI()
	st.modelDraft.preset = provider.ID
	st.modelDraft.presetModel = ""
	if provider.BaseURL != "" {
		st.modelDraft.baseURL = provider.BaseURL
	}
	if len(provider.Models) > 0 {
		a.applyModelPreset(provider.Models[0])
	}
	a.resetModelProbe()
}

// applyModelPreset fills the dialog from one built-in model: the wire model id,
// the context window the catalog knows and the model's default reasoning
// effort. The display name is only replaced when it is empty or still names a
// catalog model, so a user-typed name survives repeated picks.
func (a *App) applyModelPreset(model modelcatalog.Model) {
	st := a.shellUI()
	st.modelDraft.presetModel = model.ID
	st.modelDraft.model = model.ID
	if name := strings.TrimSpace(st.modelDraft.name); name == "" || catalogModelName(name) {
		st.modelDraft.name = model.Name
	}
	st.modelDraft.contextWindow = formatContextWindowDraft(model.ContextWindow)
	st.modelDraft.effort = model.DefaultEffort
	a.resetModelProbe()
}

// catalogModelName reports whether name is a built-in model display name (so a
// previous auto-fill may be replaced without touching a user-typed name).
func catalogModelName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return true
	}
	for _, provider := range modelcatalog.Providers() {
		for _, model := range provider.Models {
			if model.Name == name || model.ID == name {
				return true
			}
		}
	}
	return false
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

// modelProviderField is the Add model dialog's provider row: choosing a
// built-in provider (DeepSeek leads) fills the endpoint, its first published
// model, that model's context window and its default reasoning effort, so the
// common cases need only a pasted key. The custom row leaves the fields alone.
func (a *App) modelProviderField(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	st := a.shellUI()
	providers := dialogProviders()
	labels := make([]string, 0, len(providers)+1)
	current := a.T("models.provider_custom")
	for _, provider := range providers {
		labels = append(labels, provider.Name)
		if provider.ID == st.modelDraft.preset {
			current = provider.Name
		}
	}
	labels = append(labels, a.T("models.provider_custom"))
	ui.Field(c, a.T("models.provider"), func() {
		sel := ui.Select(c, &current, labels)
		sel.Height(th.Space(9)).MinWidth(th.Space(36)).Radius(design.RadiusSM)
		if !sel.Changed() {
			return
		}
		if current == a.T("models.provider_custom") {
			st.modelDraft.preset = customProviderID
			st.modelDraft.presetModel = ""
			a.resetModelProbe()
			return
		}
		for _, provider := range providers {
			if provider.Name == current {
				a.applyProviderPreset(provider)
				return
			}
		}
	})
}

// modelPresetField is the Add model dialog's built-in model row, shown only
// while the chosen provider publishes model entries. Picking one fills the
// wire id, the context window and the default reasoning effort; the "Custom
// model" row lets the user type an id the catalog does not know.
func (a *App) modelPresetField(c *ui.Context, t *design.Tokens) {
	provider, ok := modelcatalog.LookupProvider(a.shellUI().modelDraft.preset)
	if !ok || len(provider.Models) == 0 {
		return
	}
	th := t.Theme()
	st := a.shellUI()
	customLabel := a.T("models.model_custom")
	labels := make([]string, 0, len(provider.Models)+1)
	current := customLabel
	for _, model := range provider.Models {
		labels = append(labels, model.Name)
		if st.modelDraft.presetModel == model.ID {
			current = model.Name
		}
	}
	labels = append(labels, customLabel)
	ui.Field(c, a.T("models.model_preset"), func() {
		sel := ui.Select(c, &current, labels)
		sel.Height(th.Space(9)).MinWidth(th.Space(36)).Radius(design.RadiusSM)
		if !sel.Changed() {
			return
		}
		if current == customLabel {
			st.modelDraft.presetModel = ""
			return
		}
		for _, model := range provider.Models {
			if model.Name == current {
				a.applyModelPreset(model)
				return
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
			a.modelPresetField(c, t)
			ui.Form(c, func() {
				a.modelField(c, t, a.T("models.name"), &st.modelDraft.name,
					"DeepSeek V4.1 Flash", true, false)
				a.modelField(c, t, a.T("models.base_url"), &st.modelDraft.baseURL,
					"https://api.deepseek.com", false, false)
				a.modelField(c, t, a.T("models.api_key"), &st.modelDraft.apiKey,
					"sk-...", false, true)
				a.modelField(c, t, a.T("models.model"), &st.modelDraft.model,
					"deepseek-flash", false, false)
				a.modelField(c, t, a.T("models.context_window"), &st.modelDraft.contextWindow,
					"1000000", false, false)
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
	model, toastKey := modelFromDraft(st.modelDraft)
	if toastKey != "" {
		c.Toast(a.T(toastKey))
		return
	}
	var saveErr error
	if model.ID == "" {
		_, saveErr = a.Ctrl.Models().Create(model)
	} else {
		_, saveErr = a.Ctrl.Models().Update(model)
	}
	if saveErr != nil {
		c.Toast(a.T("models.could_not_save_model"))
		return
	}
	a.resetModelProbe()
	st.modelDialog = false
	c.Toast(a.T("models.model_saved"))
}

// modelFromDraft validates the Add/Edit model dialog's draft and returns the
// row to persist, or the i18n key of the first field that blocks saving. The
// built-in catalog is advisory: a published model accepts only the effort
// levels it lists, while a custom endpoint stays free-form.
func modelFromDraft(draft modelDraft) (store.Model, string) {
	contextWindow, err := parseContextWindowDraft(draft.contextWindow)
	if err != nil {
		return store.Model{}, "models.invalid_context_window"
	}
	model := store.Model{
		ID:              draft.id,
		Name:            strings.TrimSpace(draft.name),
		BaseURL:         strings.TrimSpace(draft.baseURL),
		APIKey:          strings.TrimSpace(draft.apiKey),
		Model:           strings.TrimSpace(draft.model),
		ReasoningEffort: strings.TrimSpace(draft.effort),
		ContextWindow:   contextWindow,
	}
	if model.Name == "" {
		return store.Model{}, "models.model_name_is_required"
	}
	if provider, ok := modelcatalog.ProviderForBaseURL(model.BaseURL); ok {
		if catalogModel, ok := modelcatalog.LookupModel(provider.ID, model.Model); ok &&
			model.ReasoningEffort != "" && !effortSupported(catalogModel, model.ReasoningEffort) {
			return store.Model{}, "models.unsupported_reasoning_effort"
		}
	}
	return model, ""
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

// effortSupported reports whether a built-in model publishes effort.
func effortSupported(model modelcatalog.Model, effort string) bool {
	for _, supported := range model.Efforts {
		if supported == effort {
			return true
		}
	}
	return false
}

// composerEffort is the reasoning effort the composer controls right now: the
// session's explicit choice, the Home composer's staged choice, or the selected
// model's configured default.
func (a *App) composerEffort() string {
	if a.Ctrl == nil {
		return ""
	}
	switch a.backgroundScreen() {
	case Session:
		if id := a.Ctrl.CurrentSessionID(); id != "" {
			if effort := a.Ctrl.SessionReasoningEffort(id); effort != "" {
				return effort
			}
		}
	case Home:
		if effort := strings.TrimSpace(a.homeEffort); effort != "" {
			return effort
		}
	}
	if model := a.composerModel(); model != nil {
		return strings.TrimSpace(model.ReasoningEffort)
	}
	return ""
}

// composerEffortOptions returns the reasoning levels the composer offers for
// the selected model: the levels its built-in entry publishes, or every known
// level for a custom endpoint or hand-written model id.
func (a *App) composerEffortOptions() []string {
	model := a.composerModel()
	if model == nil {
		return nil
	}
	if provider, ok := modelcatalog.ProviderForBaseURL(model.BaseURL); ok {
		if catalogModel, ok := modelcatalog.LookupModel(provider.ID, model.Model); ok {
			return catalogModel.Efforts
		}
	}
	return modelcatalog.AllEfforts()
}

// applyComposerEffort records the composer's reasoning-strength choice: on Home
// it stages the level for the session the composer creates, on Session it
// persists the session's own choice (applied from the next request on). An
// empty level restores the selected model's configured default.
func (a *App) applyComposerEffort(c *ui.Context, effort string) {
	a.liveMenus().modelOpen = false
	if a.Ctrl == nil {
		return
	}
	if a.backgroundScreen() == Home {
		a.homeEffort = effort
		return
	}
	id := a.Ctrl.CurrentSessionID()
	if id == "" {
		return
	}
	if err := a.Ctrl.SetSessionReasoningEffort(id, effort); err != nil {
		if c != nil {
			c.Toast(a.T("models.could_not_select_effort"))
		}
	}
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
