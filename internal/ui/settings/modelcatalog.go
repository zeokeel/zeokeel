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
)

const (
	// ModelCatalogPageID is the editable model catalog page.
	ModelCatalogPageID = "modelcatalog"
	// ModelCatalogPageSection and order place it in the 模型 group after Model.
	ModelCatalogPageSection = "模型"
	ModelCatalogPageOrder   = 15
)

// defaultCatalogModels mirrors the modelcatalog module's built-in presets; the
// page shows them when the user has not configured a list yet.
var defaultCatalogModels = []contracts.ModelInfo{
	{ID: "deepseek-chat", Name: "DeepSeek Chat", Note: "通用对话", ContextWindow: 128000},
	{ID: "deepseek-reasoner", Name: "DeepSeek Reasoner", Note: "深度推理", ContextWindow: 128000},
}

// catalogState is the add-model form draft (UI thread only).
type catalogState struct {
	id            string
	name          string
	note          string
	contextWindow string
	status        string
	err           bool
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

// buildModelCatalogPage renders the editable catalog.
func buildModelCatalogPage(c *ui.Context, store contracts.SettingsService, st *catalogState, locale contracts.LocaleService) {
	t := design.Of(c)
	models, ok := catalogModels(store)
	ui.Column(c).FillWidth().Gap(design.Space2).Children(func() {
		pageHeader(c, t, localized(locale, "模型目录", "Model catalog"), localized(locale,
			"composer 模型菜单展示的预设；保存后立即生效。",
			"Presets shown in the composer model menu; changes apply immediately."))
		if !ok {
			design.Apply(ui.Text(c, localized(locale,
				"配置解析失败：请修复模型目录设置或恢复默认。",
				"Invalid catalog setting; fix it or restore the defaults.")),
				t.Font(design.TypeXS)).TextColor(t.DangerText)
		}

		panel(c, t, func() {
			sectionTitle(c, t, localized(locale, "模型列表", "Models"))
			for index, model := range models {
				if index > 0 {
					ui.Divider(c)
				}
				model := model
				ui.Row(c).FillWidth().Padding(design.Space2, 0, design.Space2, 0).
					AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					ui.Column(c).Gap(design.Space1).Grow(1).Children(func() {
						design.Apply(ui.Text(c, firstNonEmpty(model.Name, model.ID)), t.Text(design.TypeS)).
							FontWeight(600).TextColor(t.LabelPrimary)
						ui.Row(c).Gap(design.Space2).Children(func() {
							design.Apply(ui.Text(c, model.ID), t.MonoFamily()).FontSize(11).TextColor(t.LabelSecondary)
							if note := strings.TrimSpace(model.Note); note != "" {
								design.Apply(ui.Text(c, note), t.TextMuted(design.TypeXXS))
							}
							if model.ContextWindow > 0 {
								size := formatContextWindow(model.ContextWindow)
								design.Apply(ui.Text(c, localized(locale, "上下文 "+size, size+" context")),
									t.TextMuted(design.TypeXXS))
							}
						})
					})
					if ui.Button(c, localized(locale, "删除", "Remove")).Clicked() {
						next := make([]contracts.ModelInfo, 0, len(models)-1)
						for _, candidate := range models {
							if candidate.ID != model.ID {
								next = append(next, candidate)
							}
						}
						if err := writeCatalogModels(store, next); err != nil {
							st.status, st.err = localized(locale, "保存失败：", "Save failed: ")+err.Error(), true
						} else {
							st.status, st.err = localized(locale, "已保存", "Saved"), false
						}
					}
				})
			}
		})

		panel(c, t, func() {
			sectionTitle(c, t, localized(locale, "新增模型", "Add model"))
			ui.Row(c).FillWidth().Gap(design.Space2).Children(func() {
				ui.TextInput(c, &st.name).Label(inputLabel(locale, localized(locale, "名称", "Name"))).
					Placeholder(localized(locale, "例如 DeepSeek Chat", "e.g. DeepSeek Chat")).Grow(1)
				ui.TextInput(c, &st.id).Label(inputLabel(locale, localized(locale, "模型 ID", "Model ID"))).
					Placeholder("deepseek-chat").Grow(1)
				ui.TextInput(c, &st.note).Label(inputLabel(locale, localized(locale, "备注", "Note"))).
					Placeholder(localized(locale, "可选", "optional")).Grow(1)
			})
			ui.Row(c).FillWidth().Gap(design.Space2).Children(func() {
				ui.TextInput(c, &st.contextWindow).
					Label(inputLabel(locale, localized(locale, "上下文窗口", "Context window"))).
					Placeholder("128000").Grow(1)
				design.Apply(ui.Text(c, localized(locale,
					"可选，单位 tokens；无效数字按 0（不显示）。",
					"Optional, in tokens; invalid numbers become 0 (hidden).")),
					t.TextMuted(design.TypeXXS)).Grow(1)
			})
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				if ui.PrimaryButton(c, localized(locale, "添加", "Add")).Clicked() {
					id := strings.TrimSpace(st.id)
					name := strings.TrimSpace(st.name)
					if id == "" {
						st.status, st.err = localized(locale, "模型 ID 不能为空", "Model ID is required"), true
					} else {
						dup := false
						for _, candidate := range models {
							if strings.EqualFold(candidate.ID, id) {
								dup = true
							}
						}
						if dup {
							st.status, st.err = localized(locale, "模型 ID 已存在", "Model ID already exists"), true
						} else if err := writeCatalogModels(store, append(models, contracts.ModelInfo{
							ID: id, Name: name, Note: strings.TrimSpace(st.note),
							ContextWindow: parseContextWindow(st.contextWindow),
						})); err != nil {
							st.status, st.err = localized(locale, "保存失败：", "Save failed: ")+err.Error(), true
						} else {
							st.name, st.id, st.note, st.contextWindow = "", "", "", ""
							st.status, st.err = localized(locale, "已保存", "Saved"), false
						}
					}
				}
				if ui.Button(c, localized(locale, "恢复默认", "Restore defaults")).Clicked() {
					if err := writeCatalogModels(store, nil); err != nil {
						st.status, st.err = localized(locale, "保存失败：", "Save failed: ")+err.Error(), true
					} else {
						st.status, st.err = localized(locale, "已恢复默认", "Defaults restored"), false
					}
				}
				if st.status != "" {
					color := t.Success
					if st.err {
						color = t.DangerText
					}
					design.Apply(ui.Text(c, st.status), t.Font(design.TypeXS)).TextColor(color)
				}
			})
		})
	})
}
