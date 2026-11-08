package plugins

// The plugins page's built-in module section: the host's loaded kernel module
// inventory (contracts.KeyModuleLister) as a read-only, collapsed-by-default
// list. Modules are compiled into the binary, so the section only reports their
// module id and lifecycle state.

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// buildModulesSection renders the built-in module inventory as a uikit.Section;
// hidden entirely when the host provides no lister (optional product wiring).
func buildModulesSection(c *ui.Context, t *design.Tokens, st *state,
	lister contracts.ModuleLister, hasLister bool) {
	if !hasLister || lister == nil {
		return
	}
	modules := lister.Modules()
	var countLabel string
	switch {
	case len(modules) == 1:
		countLabel = st.tr("1 个模块", "1 module")
	default:
		countLabel = fmt.Sprintf(st.tr("%d 个模块", "%d modules"), len(modules))
	}
	uikit.Section(c, st.tr("内置模块", "Built-in modules"), func(c *ui.Context) {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			design.Apply(ui.Text(c, countLabel), t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
			ui.Spacer(c).Grow(1)
			design.Apply(ui.Text(c, st.tr("编译期装配，只读。", "Compiled-in, read-only.")),
				t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		})
		open := st.modulesOpen
		ui.Collapsible(c, st.tr("展开模块列表", "Show modules"), &open, func() {
			for _, module := range modules {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					design.Apply(ui.Text(c, strings.TrimSpace(module.ModuleID)), t.MonoFamily()).
						FontSize(11).TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
					state := strings.TrimSpace(module.State)
					color := t.LabelTertiary
					if state == "active" {
						color = t.SuccessText
					}
					design.Apply(ui.Badge(c, state), t.Badge()).TextColor(color)
				})
			}
		})
		st.modulesOpen = open
	})
}
