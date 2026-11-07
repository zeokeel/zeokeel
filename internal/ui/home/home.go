// Package home contributes the Home screen. It is a UI module: a kernel
// Descriptor that requires the host UI runtime (and any read models it needs),
// registers its contributions on activation and hands the unregister functions
// to the kernel effect so disposal unwinds them in reverse order.
//
// Home is a composition screen. It owns only the page skeleton and the default
// states for its three injection points (home.hero, home.composer and
// home.recent); other modules may replace each region. The layout follows the
// brand design system: a centered hero with the ZeoKeel mark and wordmark over a
// raised composer card, followed by the recent-session cards and the tool-card
// demo. Every control is a mygo native control styled through internal/design
// (Panel, PrimaryButton, Badge, type scale); nothing is hand-drawn.
package home

import (
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/brand"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
)

// Config is the module's typed assembly config.
type Config struct {
	Title               string `json:"title"`
	Greeting            string `json:"greeting"`
	ComposerPlaceholder string `json:"composerPlaceholder"`
	RecentTitle         string `json:"recentTitle"`
	RecentEmpty         string `json:"recentEmpty"`
}

// heroTitle is the brand wordmark in the hero.
const heroTitle = "ZeoKeel"

// homeColumnWidth caps the centered content column so the composer reads as a
// card on a wide window instead of a full-width bar.
const homeColumnWidth float32 = 720

// Module registers the Home screen and its command.
func Module() *module.Descriptor[Config] {
	return module.Define("zeokeel-ui/home", func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		nav, err := ctx.Require(contracts.KeyNavigator)
		if err != nil {
			return err
		}
		info, err := ctx.Require(contracts.KeyAppInfo)
		if err != nil {
			return err
		}
		// Chat is optional so the Home screen still loads in products without
		// the agent backend; with it the composer creates a real session.
		chat, _ := ctx.Get(contracts.KeyChat)
		title := firstNonEmpty(cfg.Title, "首页")
		greeting := firstNonEmpty(cfg.Greeting, "编译期模块化的桌面 AI agent。")
		composerPlaceholder := firstNonEmpty(cfg.ComposerPlaceholder, "向 ZeoKeel 提问…")

		screen := contracts.ScreenSpec{
			ID:    "home",
			Title: title,
			Order: 10,
			Build: func(c *ui.Context) {
				tokens := design.Of(c)
				ui.Column(c).FillWidth().Grow(1).Gap(design.Space6).Children(func() {
					// The brand column: centered vertically in the free space,
					// capped at the readable width.
					ui.Column(c).FillWidth().Grow(1).Justify(ui.Center).AlignItems(ui.Center).
						Children(func() {
							ui.Column(c).FillWidth().MaxWidth(homeColumnWidth).Gap(design.Space6).
								Children(func() {
									region(c, runtime, contracts.SlotHomeHero, func(c *ui.Context) {
										buildHero(c, tokens, greeting, info, chat == nil)
									})
									region(c, runtime, contracts.SlotHomeComposer, func(c *ui.Context) {
										buildComposer(c, tokens, chat, nav, runtime, composerPlaceholder)
									})
								})
						})
				})
			},
		}
		if err := own(ctx, func() (func(), error) { return runtime.RegisterScreen(screen) }); err != nil {
			return err
		}
		return own(ctx, func() (func(), error) {
			return runtime.RegisterCommand(contracts.CommandSpec{
				ID:    "home.settings",
				Title: "设置",
				Hint:  "打开设置",
				Order: 10,
				Run:   func() error { nav.ToggleSettings(); return nil },
			})
		})
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyNavigator)).
		AddRequires(module.Requires(contracts.KeyAppInfo)).
		WithName("ZeoKeel Home screen")
}

// buildHero is the default Home hero: the brand mark and wordmark over the
// tagline and the build badges, centered with generous whitespace.
func buildHero(c *ui.Context, t *design.Tokens, greeting string, info contracts.AppInfo, showFallbackHint bool) {
	ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(design.Space3).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(design.Space3).Children(func() {
			ui.Image(c, brand.Mark(t.Dark)).Size(48, 48)
			design.Apply(ui.Text(c, heroTitle), t.Text(design.TypeXL)).FontWeight(600)
		})
		design.Apply(ui.Text(c, greeting), t.TextMuted(design.TypeM))
		ui.Row(c).Gap(design.Space2).Children(func() {
			design.Apply(ui.Badge(c, "v"+firstNonEmpty(info.Version, "dev")), t.Badge())
			if goVersion := strings.TrimSpace(info.GoVersion); goVersion != "" {
				design.Apply(ui.Badge(c, goVersion), t.Badge())
			}
		})
		// Default hero hint; replaced when another module contributes home.hero
		// (see region). With a chat backend the product Home is not an empty
		// state, so the hint is only shown in chat-less assemblies.
		if showFallbackHint {
			design.Apply(ui.Text(c, "这是 home 的默认 Hero 空态。"), t.TextMuted(design.TypeXS))
		}
	})
}

// buildComposer is the default composer entry: a raised brand panel with the
// placeholder and the primary "start a new conversation" action.
func buildComposer(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, runtime contracts.UIRuntime, placeholder string) {
	panel := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space3).
		Padding(design.Space4)
	design.Apply(panel, t.Panel())
	panel.Children(func() {
		design.Apply(ui.Text(c, placeholder), t.TextMuted(design.TypeS)).Grow(1).MaxLines(1).Ellipsis("…")
		start := ui.ButtonBase(c).Height(38).PaddingX(design.Space4).Radius(design.RadiusMD)
		design.Apply(start, t.PrimaryButton())
		if start.Pressed() || start.Hovered() {
			start = start.Background(t.BgHover)
		}
		start.Label("开始新对话")
		start.Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
				ui.Icon(c, icons.NewChat()).Size(16, 16).TextColor(t.LabelPrimary)
				design.Apply(ui.Text(c, "开始新对话"), t.Text(design.TypeS)).FontWeight(500)
			})
		})
		if start.Clicked() && chat != nil {
			if _, err := chat.NewSession(""); err == nil {
				_ = nav.Push("session")
				runtime.Invalidate()
			}
		}
	})
}

// region renders one injection point. When nothing is registered yet it draws
// the module's default state instead of an empty hole.
func region(c *ui.Context, runtime contracts.UIRuntime, slot string, fallback contracts.BuildFunc) {
	for _, name := range runtime.Slots() {
		if name == slot {
			runtime.RenderSlot(c, slot)
			return
		}
	}
	if fallback != nil {
		fallback(c)
	}
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
