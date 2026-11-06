// Package toolcards contributes the tool-card renderers (the UI for one tool
// call). It is the example of the RegisterToolRenderer injection point: the
// module that owns a tool can ship its card, and any screen that calls
// UIRuntime.RenderTool gets it without knowing which module drew it.
package toolcards

import (
	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Config is the module's typed assembly config.
type Config struct {
	Title string `json:"title"`
}

// Module registers the demo tool renderer.
func Module() *module.Descriptor[Config] {
	return module.Define("zeokeel-ui/toolcards", func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		title := firstNonEmpty(cfg.Title, "工具调用")
		return own(ctx, func() (func(), error) {
			return runtime.RegisterToolRenderer(contracts.ToolRenderer{
				Tool:  "demo",
				ID:    "toolcards.demo-card",
				Order: 10,
				Build: func(c *ui.Context, call contracts.ToolCall) {
					theme := c.Theme()
					ui.Column(c).FillWidth().Gap(6).Padding(10).Radius(8).
						Background(theme.Surface).Children(func() {
						ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, title+": "+call.Name).Bold().FontSize(13)
							ui.Badge(c, call.Status)
							ui.Text(c, call.ID).FontSize(11).TextColor(theme.TextMuted)
						})
						if call.Summary != "" {
							ui.Text(c, call.Summary).FontSize(13)
						}
					})
				},
			})
		})
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		WithName("ZeoKeel demo tool cards")
}

// own registers one contribution and ties its unregistration to ctx's effect
// lifetime, so activation failure and module disposal leave no residue.
func own(ctx cordis.Context, register func() (func(), error)) error {
	unregister, err := register()
	if err != nil {
		return err
	}
	if _, err := ctx.Effect(func() cordis.Effect {
		return cordis.Effect{cordis.CleanupOf(unregister)}
	}); err != nil {
		unregister()
		return err
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
