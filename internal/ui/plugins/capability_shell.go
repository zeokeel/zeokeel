package plugins

// The Capabilities page skeleton: one page-level tab row whose tabs are
// parallel destination pages. The shell owns the tab identity, the chosen tab
// and the shared diagnostic area below the tabs; a tab owns its whole page body.
//
// Skills and connectors are the first two parallel tabs. The future workflow
// surface is another capabilityTabSpecs entry (one line) — it needs no shell
// change and no new screen, which is why the page is split into this skeleton
// now, before the workflow design exists.

import (
	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/lib/textutil"
)

// capabilityTabSpec is one parallel destination of the Capabilities page.
type capabilityTabSpec struct {
	// ID is the stable tab identity (the frame state indexes specs by order,
	// but the id keeps tests and future telemetry readable).
	ID string
	// LabelZh/LabelEn are the tab labels; LabelZh may come from the assembly
	// config, so a custom label is kept verbatim.
	LabelZh string
	LabelEn string
	// Build renders the tab's page body for one frame.
	Build func(c *ui.Context, t *design.Tokens, st *state)
}

// capabilityTabSpecs is the page's tab registry, in display order. A future
// workflow tab appends one capabilityTabSpec here: the shell already renders
// the tab row, binds the chosen index and owns the shared footer, so no shell
// or navigation change is needed.
func capabilityTabSpecs(cfg Config) []capabilityTabSpec {
	return []capabilityTabSpec{
		{
			ID: "skills", LabelZh: textutil.FirstNonEmptyTrimmed(cfg.SkillsSection, "技能"), LabelEn: "Skills",
			Build: buildSkillsTab,
		},
		{
			ID: "connectors", LabelZh: textutil.FirstNonEmptyTrimmed(cfg.MCPSection, "连接器"), LabelEn: "Connectors",
			Build: buildConnectorsTab,
		},
	}
}

// capabilityTabLabel localizes one spec's label for the current frame.
func (st *state) capabilityTabLabel(spec capabilityTabSpec) string {
	if st.english() {
		return spec.LabelEn
	}
	return spec.LabelZh
}

// clampCapabilityTab keeps the bound tab index inside the registry (the
// registry can shrink when an assembly disables a future tab) and returns the
// index to build.
func (st *state) clampCapabilityTab(specs []capabilityTabSpec) int {
	index := st.capabilityTab
	if index < 0 {
		index = 0
	}
	if index >= len(specs) {
		index = len(specs) - 1
	}
	if index < 0 {
		index = 0
	}
	st.capabilityTab = index
	return index
}

// buildCapabilityPage renders the skeleton: the page-level tab row stays put
// while the chosen tab's body scrolls below it. The chosen tab's own actions
// live inside its body, so the tab row carries only the parallel destinations.
func buildCapabilityPage(c *ui.Context, cfg Config, st *state) {
	t := design.Of(c)
	specs := capabilityTabSpecs(cfg)
	index := st.clampCapabilityTab(specs)

	labels := make([]string, len(specs))
	for i, spec := range specs {
		labels[i] = st.capabilityTabLabel(spec)
	}

	ui.Column(c).FillWidth().Grow(1).Gap(design.Space3).Children(func() {
		// Page-level tabs: built outside the scroll so they never scroll away.
		ui.Tabs(c, &st.capabilityTab, labels...)
		ui.Scroll(c).FillWidth().Grow(1).Children(func() {
			ui.Column(c).FillWidth().Gap(design.Space6).Children(func() {
				specs[index].Build(c, t, st)
				buildCapabilityDiagnostics(c, t, st)
			})
		})
	})
}

// buildCapabilityDiagnostics is the shared footer of the capability page: the
// compiled-in module inventory and the curated plugin settings stay available
// below every tab. They are diagnostic/settings surfaces, not a third
// capability, so they do not become a tab.
func buildCapabilityDiagnostics(c *ui.Context, t *design.Tokens, st *state) {
	ui.Divider(c)
	design.Apply(ui.Text(c, st.tr("诊断与设置", "Diagnostics & settings")),
		t.Font(design.TypeM)).FontWeight(design.WeightSemibold).TextColor(t.LabelPrimary).
		Role(ui.RoleHeading).Level(2)

	settings, hasSettings := st.ctx.Get(contracts.KeySettings)
	modulesLister, hasModules := st.ctx.Get(contracts.KeyModuleLister)
	buildModulesSection(c, t, st, modulesLister, hasModules)
	buildPluginParamsSection(c, t, st, settings, hasSettings)
}
