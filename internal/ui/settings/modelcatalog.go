package settings

// Model catalog page: edit the model presets the composer model menu offers.
// The page writes the same settings row the modelcatalog module reads on every
// call, so saved changes are visible without a restart.

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// defaultCatalogModels mirrors the modelcatalog module's built-in presets; the
// page shows them when the user has not configured a list yet.
var defaultCatalogModels = contracts.DefaultModelCatalog()

// catalogState is the add-model form draft and the per-row editor state (UI
// thread only).
type catalogState struct {
	id            string
	name          string
	note          string
	contextWindow string
	// contextWindowNum mirrors the official ui.NumberInput (0 = unknown).
	contextWindowNum float64
	status           string
	err              bool

	// addFamily is the family the next added model gets. The add form infers it
	// from the typed model ID until the user picks one explicitly
	// (familyTouched), so typing "qwen…" pre-selects the Qwen family.
	addFamily     string
	familyTouched bool
	// addOpen/addErr drive the add-model modal (the list stays the page; adding
	// is an explicit action) and its inline validation error.
	addOpen bool
	addErr  string

	// The 模型列表 collection is the official List: keyed by model ID, named by
	// the model name/ID and single-row selectable for the keyboard; the inline
	// edit/remove buttons stay the row actions.
	list ui.ListState
	sel  int

	// editAdvanced expands the editor's advanced group (generation options and
	// custom thinking modes); it starts collapsed.
	editAdvanced bool
	// modeList is the official ui.List state of the custom thinking-mode rows
	// (identity follows the mode ID; empty IDs fall back to their index).
	modeList ui.ListState

	// Confirmation state of the two destructive actions.
	confirmRemove  string
	confirmRestore bool

	// editorOpen/edit drive the per-model editor modal; edit is nil while it is
	// closed.
	editorOpen bool
	edit       *catalogEditDraft
}

// catalogModels returns the effective editable list: the user's settings row
// when present/valid, otherwise the built-in defaults.
func catalogModels(store contracts.SettingsService) ([]contracts.ModelInfo, bool) {
	raw := ""
	if store != nil {
		raw = store.Get(contracts.SettingModelCatalogModels, "")
	}
	models, err := contracts.ParseModelInfoList(raw)
	if err != nil {
		return defaultCatalogModels, false
	}
	if len(models) == 0 {
		return defaultCatalogModels, true
	}
	return models, true
}

// writeCatalogModels persists the list ("[]" restores the defaults).
func writeCatalogModels(store contracts.SettingsService, models []contracts.ModelInfo) error {
	encoded, err := contracts.EncodeModelInfoList(models)
	if err != nil {
		return err
	}
	return store.Set(contracts.SettingModelCatalogModels, encoded)
}

// catalogHasModel reports whether id matches one of the effective catalog
// presets (the user's edited list, or the built-in defaults when unset). The
// match is trimmed and case-insensitive, mirroring how the composer reads the
// menu ids.
func catalogHasModel(store contracts.SettingsService, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	models, _ := catalogModels(store)
	for _, model := range models {
		if strings.EqualFold(strings.TrimSpace(model.ID), id) {
			return true
		}
	}
	return false
}

// hasModelID reports whether id matches one of the discovered model ids.
func hasModelID(ids []string, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	for _, candidate := range ids {
		if strings.EqualFold(strings.TrimSpace(candidate), id) {
			return true
		}
	}
	return false
}

// parseContextWindow parses the optional context-window draft: a malformed,
// empty or non-positive value becomes 0 (not displayed and not budgeted).
func parseContextWindow(raw string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
}

// formatContextWindow renders a token count for the catalog list: exact
// thousands as "128k", other positive values with one decimal.
func formatContextWindow(tokens int) string {
	if tokens <= 0 {
		return ""
	}
	if tokens%1000 == 0 {
		return strconv.Itoa(tokens/1000) + "k"
	}
	return strconv.FormatFloat(float64(tokens)/1000, 'f', 1, 64) + "k"
}

// buildModelCatalogSection renders the model list inside the merged 模型 page.
// Changes save at once (the list is the control surface itself); the destructive
// actions — removing a model and restoring the defaults over a custom list — ask
// for confirmation with the native alert dialog. The page owns the provider
// connection and the page footer Save; catalog edits apply immediately.
func buildModelCatalogSection(c *ui.Context, store contracts.SettingsService, st *catalogState, locale contracts.LocaleService) {
	t := design.Of(c)
	models, ok := catalogModels(store)
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		design.Apply(ui.Text(c, localized(locale,
			"对话输入框的模型菜单展示这些预设；新增/编辑/删除立即生效。",
			"Presets shown in the composer model menu; add/edit/remove applies immediately.")),
			t.TextMuted(design.TypeXXS)).MaxLines(2)
		if !ok {
			uikit.ErrorLine(c, localized(locale,
				"配置解析失败：请修复模型设置或恢复默认。",
				"Invalid catalog setting; fix it or restore the defaults."))
		}

		uikit.Section(c, localized(locale, "模型列表", "Models"), func(*ui.Context) {
			if len(models) == 0 {
				uikit.EmptyState(c, localized(locale, "模型列表为空", "The model list is empty"))
			}
			// The models are a collection: the official List keys the rows by
			// model ID, names them and owns the keyboard selection; the inline
			// edit/remove buttons keep their click semantics.
			bindSettingsList(&st.list, &st.sel, len(models), func(index int) any {
				if index < 0 || index >= len(models) {
					return nil
				}
				return models[index].ID
			}, func(index int) string {
				if index < 0 || index >= len(models) {
					return ""
				}
				return firstNonEmpty(strings.TrimSpace(models[index].Name), models[index].ID)
			})
			ui.List(c, &st.list, len(models)).
				Label(localized(locale, "模型列表", "Model list")).
				Gap(design.Space1).
				Rows(func(row ui.ListRow) {
					buildModelCatalogRow(row.Context, t, st, models[row.Index], locale, row.Selected())
				})
		})

		// The list is the page: adding a model is one explicit action that opens
		// the form in a modal, so the page stays short. Restoring the built-in
		// defaults is destructive and asks for confirmation.
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			if design.Apply(ui.PrimaryButton(c, localized(locale, "添加模型", "Add model")),
				t.PrimaryButton()).Clicked() {
				st.addOpen = true
				st.addErr = ""
				c.Invalidate()
			}
			ui.Spacer(c).Grow(1)
			if design.Apply(ui.Button(c, localized(locale, "恢复默认", "Restore defaults")),
				t.DangerButton()).Tooltip(localized(locale,
				"覆盖当前列表并恢复内置模型", "Overwrite the list with the built-in models")).Clicked() {
				st.confirmRestore = true
				c.Invalidate()
			}
		})
		if st.status != "" {
			if st.err {
				uikit.ErrorLine(c, st.status)
			} else {
				statusBadgesRow(c, t, st.status, false)
			}
		}

		if st.confirmRemove != "" {
			id := st.confirmRemove
			open := true
			switch ui.AlertDialog(c, &open,
				localized(locale, "删除模型", "Remove model"),
				localized(locale, "确认从模型目录删除「", "Remove ")+id+
					localized(locale, "」？", " from the model catalog?"),
				localized(locale, "取消", "Cancel"),
				localized(locale, "确认删除", "Confirm remove")) {
			case 1:
				next := make([]contracts.ModelInfo, 0, len(models))
				for _, candidate := range models {
					if candidate.ID != id {
						next = append(next, candidate)
					}
				}
				if err := writeCatalogModels(store, next); err != nil {
					st.status, st.err = localized(locale, "保存失败：", "Save failed: ")+err.Error(), true
				} else {
					st.status, st.err = localized(locale, "已保存", "Saved"), false
				}
				st.confirmRemove = ""
				c.Invalidate()
			}
			if !open {
				st.confirmRemove = ""
			}
		}
		if st.confirmRestore {
			open := true
			switch ui.AlertDialog(c, &open,
				localized(locale, "恢复默认模型", "Restore default models"),
				localized(locale, "恢复默认会覆盖当前的模型列表。此操作无法撤销。",
					"Restoring the defaults overwrites the current model list. This cannot be undone."),
				localized(locale, "取消", "Cancel"),
				localized(locale, "恢复默认值", "Restore defaults")) {
			case 1:
				if err := writeCatalogModels(store, nil); err != nil {
					st.status, st.err = localized(locale, "保存失败：", "Save failed: ")+err.Error(), true
				} else {
					st.status, st.err = localized(locale, "已恢复默认", "Defaults restored"), false
				}
				st.confirmRestore = false
				c.Invalidate()
			}
			if !open {
				st.confirmRestore = false
			}
		}
		if st.addOpen {
			buildCatalogAddDialog(c, t, store, models, st, locale)
		}
		if st.edit != nil {
			buildCatalogEditDialog(c, t, store, models, st, locale)
		}
	})
}

// buildModelCatalogRow renders one model-list row: the name, the ID with its
// note and context window, the family/thinking and generation summaries, and
// the inline edit/remove actions. The official List owns the row paint and the
// choice, so the selected row's copy uses the accent's on-color.
func buildModelCatalogRow(c *ui.Context, t *design.Tokens, st *catalogState,
	model contracts.ModelInfo, locale contracts.LocaleService, selected bool) {
	primary, secondary, tertiary := listRowTextColors(t, selected)
	ui.Row(c).FillWidth().Padding(design.Space1, design.Space2).
		AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		ui.Column(c).Gap(design.Space1).Grow(1).Children(func() {
			design.Apply(ui.Text(c, firstNonEmpty(model.Name, model.ID)), t.Text(design.TypeS)).
				FontWeight(600).TextColor(primary)
			ui.Row(c).Gap(design.Space2).Children(func() {
				design.Apply(ui.Text(c, model.ID), t.Font(design.TypeXS), t.MonoFamily()).
					TextColor(secondary)
				if note := strings.TrimSpace(model.Note); note != "" {
					design.Apply(ui.Text(c, localizedCatalogNote(locale, note)),
						t.Font(design.TypeXXS)).TextColor(secondary)
				}
				if model.ContextWindow > 0 {
					size := formatContextWindow(model.ContextWindow)
					design.Apply(ui.Text(c, localized(locale, "上下文 "+size, size+" context")),
						t.Font(design.TypeXXS)).TextColor(secondary)
				}
			})
			// Family and thinking configuration: the family picks the
			// built-in mode list, so show its effective value (an empty stored
			// family is inferred from the model ID).
			design.Apply(ui.Text(c, catalogThinkingSummary(locale, model)),
				t.Font(design.TypeXXS)).TextColor(tertiary)
			if generation := catalogGenerationSummary(locale, model); generation != "" {
				design.Apply(ui.Text(c, generation),
					t.Font(design.TypeXXS)).TextColor(tertiary)
			}
		})
		if design.Apply(ui.Button(c, localized(locale, "编辑", "Edit")),
			t.SecondaryButton()).
			Label(localized(locale, "编辑模型 ", "Edit model ") + model.ID).
			Tooltip(localized(locale, "编辑系列与思考配置",
				"Edit the family and thinking configuration")).Clicked() {
			st.edit = newCatalogEditDraft(model)
			st.editorOpen = true
			st.editAdvanced = false
			c.Invalidate()
		}
		if design.Apply(ui.Button(c, localized(locale, "删除", "Remove")),
			t.DangerButton()).Clicked() {
			st.confirmRemove = model.ID
			c.Invalidate()
		}
	})
}

// localizedCatalogNote localizes the built-in default notes, which are stored
// as data; user notes pass through unchanged.
func localizedCatalogNote(locale contracts.LocaleService, note string) string {
	switch note {
	case "通用对话":
		return localized(locale, note, "General chat")
	case "深度推理":
		return localized(locale, note, "Deep reasoning")
	default:
		return note
	}
}

// addCatalogModel validates and appends one model to the persisted catalog. A
// failure stays inline in the add-model modal; success closes it and reports on
// the page, where the list updates immediately.
func addCatalogModel(st *catalogState, store contracts.SettingsService,
	models []contracts.ModelInfo, locale contracts.LocaleService) bool {
	id := strings.TrimSpace(st.id)
	name := strings.TrimSpace(st.name)
	if id == "" {
		st.addErr = localized(locale, "模型 ID 不能为空", "Model ID is required")
		return false
	}
	for _, candidate := range models {
		if strings.EqualFold(candidate.ID, id) {
			st.addErr = localized(locale, "模型 ID 已存在", "Model ID already exists")
			return false
		}
	}
	if err := writeCatalogModels(store, append(models, contracts.ModelInfo{
		ID: id, Name: name, Note: strings.TrimSpace(st.note),
		ContextWindow: parseContextWindow(st.contextWindow),
		Family:        catalogAddFamily(st),
	})); err != nil {
		st.addErr = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		return false
	}
	st.name, st.id, st.note, st.contextWindow = "", "", "", ""
	st.contextWindowNum = 0
	st.addFamily, st.familyTouched = "", false
	st.addOpen, st.addErr = false, ""
	st.status, st.err = localized(locale, "已保存", "Saved"), false
	return true
}

// buildCatalogAddDialog renders the add-model form in a native modal: the same
// optional fields as before, with the family inferred from the typed model ID
// until the user chooses one. It closes on a successful add or on cancel.
func buildCatalogAddDialog(c *ui.Context, t *design.Tokens, store contracts.SettingsService,
	models []contracts.ModelInfo, st *catalogState, locale contracts.LocaleService) {
	ui.Modal(c, &st.addOpen, func() {
		design.Apply(ui.Column(c).Width(design.DialogWidthL).Gap(design.Space3), func(element ui.Element) ui.Element {
			return element.PaddingX(design.Space5).PaddingY(design.Space2)
		}).Children(func() {
			design.Apply(ui.Text(c, localized(locale, "新增模型", "Add model")),
				t.Font(design.TypeL)).FontWeight(600).TextColor(t.LabelPrimary)
			design.Apply(ui.Text(c, localized(locale,
				"添加后立即出现在模型菜单中。",
				"The model appears in the composer menu immediately.")),
				t.TextMuted(design.TypeXS)).MaxLines(1)
			// Enter in any single-line field adds the model, as in a desktop
			// dialog (mygo text-input.md: Submitted is Enter in a text input).
			submitted := false
			ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "名称", "Name"), func() {
						name := ui.TextInput(c, &st.name).
							Label(inputLabel(locale, localized(locale, "名称", "Name"))).
							Placeholder(localized(locale, "例如 DeepSeek Chat", "e.g. DeepSeek Chat")).FillWidth()
						if name.Submitted() {
							submitted = true
						}
					})
				})
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "模型 ID", "Model ID"), func() {
						id := ui.TextInput(c, &st.id).
							Label(inputLabel(locale, localized(locale, "模型 ID", "Model ID"))).
							Placeholder("deepseek-chat").FillWidth()
						if id.Submitted() {
							submitted = true
						}
					})
				})
			})
			ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "备注", "Note"), func() {
						note := ui.TextInput(c, &st.note).
							Label(inputLabel(locale, localized(locale, "备注", "Note"))).
							Placeholder(localized(locale, "可选", "optional")).FillWidth()
						if note.Submitted() {
							submitted = true
						}
					})
				})
				ui.Column(c).FillWidth().Grow(1).Children(func() {
					ui.Field(c, localized(locale, "上下文窗口", "Context window"), func() {
						// Official number input: 0 means the window is unknown
						// (mygo number-input.md), typed values apply only in range.
						window := ui.NumberInput(c.Key("catalog.add.contextWindow"),
							&st.contextWindowNum, 0, 10000000, 1000).
							Label(inputLabel(locale, localized(locale, "上下文窗口", "Context window")))
						if window.Changed() {
							st.contextWindow = strconv.Itoa(int(st.contextWindowNum))
						}
					})
				})
			})
			ui.Field(c, localized(locale, "模型系列", "Model family"), func() {
				choice := catalogFamilyLabel(locale, catalogAddFamily(st))
				selector := ui.Select(c.Key("catalog.addFamily"), &choice,
					catalogFamilyOptions(locale)).
					Label(localized(locale, "模型系列菜单", "Model family menu"))
				if selector.Changed() {
					if family := catalogFamilyValue(locale, choice); family != "" {
						st.addFamily, st.familyTouched = family, true
					}
					c.Invalidate()
				}
			}).Description(localized(locale,
				"决定内置的思考模式；按模型 ID 自动推断，可手动修改。",
				"Selects the built-in thinking modes; inferred from the model ID, editable."))
			if st.addErr != "" {
				uikit.ErrorLine(c, st.addErr)
			}
			if submitted {
				addCatalogModel(st, store, models, locale)
				c.Invalidate()
			}
			uikit.DialogFooter(c, []uikit.DialogAction{
				{Label: localized(locale, "取消", "Cancel"), OnClick: func() {
					st.addOpen, st.addErr = false, ""
					c.Invalidate()
				}},
				{Label: localized(locale, "添加", "Add"), Primary: true, OnClick: func() {
					addCatalogModel(st, store, models, locale)
					c.Invalidate()
				}},
			})
		})
	})
	if !st.addOpen {
		// Escape or a backdrop click dismissed the modal without saving.
		st.addErr = ""
	}
}
