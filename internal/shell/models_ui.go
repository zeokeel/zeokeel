package shell

import (
	"fmt"
	"strings"
	"sync"

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
	id, name, baseURL, apiKey, model, effort string
}

// automationDraft is the edit state of the Add/Edit automation dialog.
type automationDraft struct {
	id, title, prompt, cron string
	enabled                 bool
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
}

// modelsEmpty is the empty model list with its call to action.
func (a *App) modelsEmpty(c *ui.Context, t *design.Tokens) {
	th := t.Theme()
	ui.Column(c).Padding(th.Space(8), 0).AlignItems(ui.Center).
		Gap(th.Space(2)).Children(func() {
		settingsText(c, a.T("models.no_models_configured"), design.TypeBody, t.LabelPrimary)
		settingsText(c, a.T("models.add_a_model_to_choose_it_from_the_composer"),
			design.TypeXS, t.LabelTertiary)
		a.modelsAddButton(c, t)
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
			a.modelAction(c, t, a.T("models.delete"), func() { a.deleteModel(c, model) })
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

// beginModelAdd opens the dialog in create mode.
func (a *App) beginModelAdd() {
	st := a.shellUI()
	st.modelDraft = modelDraft{}
	st.modelDialog = true
}

// beginModelEdit opens the dialog prefilled with model.
func (a *App) beginModelEdit(model store.Model) {
	st := a.shellUI()
	st.modelDraft = modelDraft{
		id: model.ID, name: model.Name, baseURL: model.BaseURL,
		apiKey: model.APIKey, model: model.Model, effort: model.ReasoningEffort,
	}
	st.modelDialog = true
}

// modelDialog is the Add/Edit model form: the native mygo Form lays the
// labels beside the controls, and the buttons stay native.
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
			ui.Row(c).Justify(ui.End).Gap(th.Space(2)).
				Padding(th.Space(2), 0, 0, 0).Children(func() {
				if ui.Button(c, a.T("models.cancel")).Clicked() {
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

// deleteModel removes a model; deleting the default clears it.
func (a *App) deleteModel(c *ui.Context, model store.Model) {
	if a.Ctrl == nil {
		return
	}
	if err := a.Ctrl.Models().Delete(model.ID); err != nil {
		c.Toast(a.T("models.could_not_delete_model"))
		return
	}
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
