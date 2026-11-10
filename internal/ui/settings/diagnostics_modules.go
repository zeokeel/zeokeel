package settings

// The diagnostics page's loaded-module section: the host's
// contracts.KeyModuleLister inventory (assembly entry id, module id, state) as
// a read-only expandable list capped at 40 rows. Hidden when the host provides
// no lister (e.g. a bare test harness).

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// maxDiagnosticModules caps the expanded module rows; the rest become "…".
const maxDiagnosticModules = 40

// loadedModulesFromContext reads the optional host module inventory.
func loadedModulesFromContext(kctx cordis.Context) []contracts.ModuleInfo {
	lister, ok := kctx.Get(contracts.KeyModuleLister)
	if !ok || lister == nil {
		return nil
	}
	return lister.Modules()
}

// buildLoadedModulesSection renders the count row and the expandable module
// collection; nothing is shown without a loaded snapshot. The rows are the
// official List (keyed and named by the assembly entry id, single-row selection
// for the keyboard) capped at maxDiagnosticModules; the rest stay a "…" line.
func buildLoadedModulesSection(c *ui.Context, t *design.Tokens, state *diagnosticsState,
	locale contracts.LocaleService, modules []contracts.ModuleInfo) {
	if len(modules) == 0 {
		return
	}
	design.Apply(ui.Text(c, fmt.Sprintf(localized(locale, "已加载模块 %d 个", "Loaded modules: %d"), len(modules))),
		t.Font(design.TypeXS)).FontWeight(600).TextColor(t.LabelPrimary)
	open := state.modulesOpen
	ui.Collapsible(c, localized(locale, "展开模块列表", "Show modules"), &open, func() {
		visible := modules
		if len(visible) > maxDiagnosticModules {
			visible = visible[:maxDiagnosticModules]
		}
		bindSettingsList(&state.list, &state.moduleSel, len(visible), func(index int) any {
			if index < 0 || index >= len(visible) {
				return nil
			}
			return strings.TrimSpace(visible[index].EntryID)
		}, func(index int) string {
			if index < 0 || index >= len(visible) {
				return ""
			}
			return strings.TrimSpace(visible[index].EntryID)
		})
		ui.List(c, &state.list, len(visible)).
			Label(localized(locale, "已加载模块列表", "Loaded modules list")).
			Gap(design.Space1).
			Rows(func(row ui.ListRow) {
				buildLoadedModuleRow(row.Context, t, visible[row.Index], row.Selected())
			})
		if len(modules) > maxDiagnosticModules {
			design.Apply(ui.Text(c, "…"), t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		}
	})
	state.modulesOpen = open
}

// buildLoadedModuleRow is one loaded module: the assembly entry id, the module
// id and its state badge. The official List owns the hover/choice paint, so the
// row's mono copy switches to the accent's on-color while chosen.
func buildLoadedModuleRow(c *ui.Context, t *design.Tokens, module contracts.ModuleInfo, selected bool) {
	entryColor, moduleColor := t.LabelSecondary, t.LabelPrimary
	if selected {
		entryColor, moduleColor = t.AccentText, t.AccentText
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).
		Padding(design.Space1, design.Space2).Children(func() {
		design.Apply(ui.Text(c, strings.TrimSpace(module.EntryID)),
			t.Font(design.TypeXS), t.MonoFamily()).
			TextColor(entryColor).Width(160).Shrink(0).NoWrap()
		design.Apply(ui.Text(c, strings.TrimSpace(module.ModuleID)),
			t.Font(design.TypeXS), t.MonoFamily()).
			TextColor(moduleColor).Grow(1).MaxLines(1).Ellipsis("…")
		state := strings.TrimSpace(module.State)
		kind := uikit.StatusIdle
		if state == "active" {
			kind = uikit.StatusSuccess
		}
		uikit.StatusBadge(c, t, kind, state)
	})
}
