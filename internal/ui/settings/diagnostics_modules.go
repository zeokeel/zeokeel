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
// list; nothing is shown without a loaded snapshot.
func buildLoadedModulesSection(c *ui.Context, t *design.Tokens, state *diagnosticsState,
	locale contracts.LocaleService, modules []contracts.ModuleInfo) {
	if len(modules) == 0 {
		return
	}
	design.Apply(ui.Text(c, fmt.Sprintf(localized(locale, "已加载模块 %d 个", "Loaded modules: %d"), len(modules))),
		t.Font(design.TypeXS)).FontWeight(600).TextColor(t.LabelPrimary)
	open := state.modulesOpen
	ui.Collapsible(c, localized(locale, "展开模块列表", "Show modules"), &open, func() {
		for index, module := range modules {
			if index >= maxDiagnosticModules {
				design.Apply(ui.Text(c, "…"), t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
				return
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				design.Apply(ui.Text(c, strings.TrimSpace(module.EntryID)), t.MonoFamily()).
					FontSize(11).TextColor(t.LabelSecondary).Width(160).Shrink(0).NoWrap()
				design.Apply(ui.Text(c, strings.TrimSpace(module.ModuleID)), t.MonoFamily()).
					FontSize(11).TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
				state := strings.TrimSpace(module.State)
				color := t.LabelTertiary
				if state == "active" {
					color = t.Success
				}
				design.Apply(ui.Badge(c, state), t.Badge()).TextColor(color)
			})
		}
	})
	state.modulesOpen = open
}
