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
	"context"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/brand"
	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/uikit"
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

// Product-default copy. The assembly may carry the Chinese default in its
// config (desktop.json does); comparing against these defaults is what lets an
// English UI fall back to English instead of showing the configured Chinese
// string.
const (
	defaultGreetingZH    = "编译期模块化的桌面 AI agent。"
	defaultGreetingEN    = "A compile-time modular desktop AI agent."
	defaultPlaceholderZH = "问点什么…"
	defaultPlaceholderEN = "Ask anything…"
)

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
		// The host's build info remains a declared dependency of every product
		// module; the Home screen itself no longer prints it (About owns the
		// version): the front page only carries the product's one-line value.
		if _, err := ctx.Require(contracts.KeyAppInfo); err != nil {
			return err
		}
		// Chat is optional so the Home screen still loads in products without
		// the agent backend; with it the composer creates a real session.
		chat, _ := ctx.Get(contracts.KeyChat)
		title := firstNonEmpty(cfg.Title, "首页")
		// The locale is optional: copy is generated per frame so a language
		// switch applies on the next frame (zh stays the default).
		locale, _ := ctx.Get(contracts.KeyLocale)
		tr := func(zh, en string) string { return uikit.T(locale, zh, en) }
		greeting := cfg.Greeting
		composerPlaceholder := cfg.ComposerPlaceholder
		composerState := &homeComposerState{}

		// The home command lives in the host command palette, whose titles are
		// fixed at registration: keep it registered in the current language and
		// re-register when the language changes (the session commands' pattern).
		var unregisterCommand func()
		commandLang := ""
		registerCommand := func() error {
			if unregisterCommand != nil {
				unregisterCommand()
				unregisterCommand = nil
			}
			unregister, err := runtime.RegisterCommand(contracts.CommandSpec{
				ID:    "home.settings",
				Title: uikit.T(locale, "设置", "Settings"),
				Hint:  uikit.T(locale, "打开设置", "Open settings"),
				Order: 10,
				Run:   func() error { nav.ToggleSettings(); return nil },
			})
			if err != nil {
				return err
			}
			unregisterCommand = unregister
			commandLang = uikit.Language(locale)
			return nil
		}
		if err := registerCommand(); err != nil {
			return err
		}
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(func() {
				if unregisterCommand != nil {
					unregisterCommand()
					unregisterCommand = nil
				}
			})}
		}); err != nil {
			if unregisterCommand != nil {
				unregisterCommand()
			}
			return err
		}

		screen := contracts.ScreenSpec{
			ID:    "home",
			Title: title,
			Order: 10,
			Build: func(c *ui.Context) {
				// A language switch re-registers the palette command for the next
				// frame; the registration itself is a UI-thread side effect of the
				// current frame, guarded by the language check.
				if uikit.Language(locale) != commandLang {
					_ = registerCommand()
				}
				greetingText := strings.TrimSpace(greeting)
				if greetingText == "" || (uikit.Language(locale) == "en" && greetingText == defaultGreetingZH) {
					greetingText = tr(defaultGreetingZH, defaultGreetingEN)
				}
				placeholderText := strings.TrimSpace(composerPlaceholder)
				if placeholderText == "" || (uikit.Language(locale) == "en" && placeholderText == defaultPlaceholderZH) {
					placeholderText = tr(defaultPlaceholderZH, defaultPlaceholderEN)
				}
				tokens := design.Of(c)
				ui.Column(c).FillWidth().Grow(1).Gap(design.Space6).Children(func() {
					// The brand column: centered vertically in the free space,
					// capped at the readable width.
					ui.Column(c).FillWidth().Grow(1).Justify(ui.Center).AlignItems(ui.Center).
						Children(func() {
							ui.Column(c).FillWidth().MaxWidth(homeColumnWidth).Gap(design.Space6).
								Children(func() {
									region(c, runtime, contracts.SlotHomeHero, func(c *ui.Context) {
										buildHero(c, tokens, greetingText, chat == nil, tr)
									})
									buildSetupCard(c, tokens, chat, nav, tr)
									region(c, runtime, contracts.SlotHomeComposer, func(c *ui.Context) {
										buildHomeComposer(c, tokens, runtime, nav, chat, ctx, composerState, placeholderText, tr)
									})
									buildRecentSessions(c, tokens, runtime, nav, chat, tr)
									buildNextSteps(c, tokens, runtime, nav, chat, ctx, composerState, tr)
								})
						})
				})
			},
		}
		return own(ctx, func() (func(), error) { return runtime.RegisterScreen(screen) })
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyNavigator)).
		AddRequires(module.Requires(contracts.KeyAppInfo)).
		WithName("ZeoKeel Home screen")
}

// buildSetupCard is the first-run guidance card from the old shell's
// homeSetupCard: while no model is configured, Home says what is missing and
// offers the action that fixes it instead of letting the first send fail. It is
// the non-empty-state form of the guidance: with no conversations yet,
// buildNextSteps carries the same "Configure model" action in the empty state.
func buildSetupCard(c *ui.Context, t *design.Tokens, chat contracts.ChatService,
	nav contracts.Navigator, tr func(zh, en string) string) {
	if chat == nil {
		return
	}
	cfg := chat.Config()
	if cfg.Model != "" || len(homeVisibleSessions(chat)) == 0 {
		return
	}
	panel := ui.Column(c).FillWidth().Gap(design.Space2).Padding(design.Space3)
	design.Apply(panel, t.Panel())
	panel.Children(func() {
		ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Sparkles()).Size(16, 16).TextColor(t.Accent)
			design.Apply(ui.Text(c, tr("完成 ZeoKeel 的初始设置", "Finish setting up ZeoKeel")), t.Font(design.TypeS)).
				FontWeight(600).TextColor(t.LabelPrimary).Grow(1)
		})
		ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			design.Apply(ui.Text(c, tr("尚未配置模型。添加服务商与密钥后才能发送消息。",
				"No model configured yet. Add a provider and API key before sending messages.")),
				t.TextMuted(design.TypeXS)).Grow(1).MaxLines(2)
			if ui.Button(c, tr("配置模型", "Configure model")).Clicked() {
				nav.ToggleSettings()
			}
		})
	})
}

// homeVisibleSessions lists the conversations the sidebar would show: archived
// sessions belong to the settings archive page and never appear on Home.
func homeVisibleSessions(chat contracts.ChatService) []contracts.ChatSessionInfo {
	if chat == nil {
		return nil
	}
	visible := make([]contracts.ChatSessionInfo, 0, 8)
	for _, session := range chat.Sessions() {
		if session.Archived {
			continue
		}
		visible = append(visible, session)
	}
	return visible
}

// homeRecentLimit is how many recent conversations Home lists.
const homeRecentLimit = 5

// buildRecentSessions is Home's recent-conversation list: the newest few
// conversations (archived excluded) so the user can resume without hunting
// through the sidebar. Clicking one activates it and opens the conversation.
func buildRecentSessions(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	nav contracts.Navigator, chat contracts.ChatService, tr func(zh, en string) string) {
	visible := homeVisibleSessions(chat)
	if len(visible) == 0 {
		return
	}
	sort.SliceStable(visible, func(left, right int) bool {
		if !visible[left].UpdatedAt.Equal(visible[right].UpdatedAt) {
			return visible[left].UpdatedAt.After(visible[right].UpdatedAt)
		}
		return visible[left].ID < visible[right].ID
	})
	if len(visible) > homeRecentLimit {
		visible = visible[:homeRecentLimit]
	}
	ui.Column(c).FillWidth().Gap(design.Space1).Children(func() {
		design.Apply(ui.Text(c, tr("最近会话", "Recent chats")), t.SectionLabel()).NoWrap().MaxLines(1)
		for _, session := range visible {
			session := session
			title := strings.TrimSpace(session.Title)
			if title == "" {
				title = tr("未命名会话", "Untitled session")
			}
			c.Key("home/recent/" + session.ID)
			row := ui.ButtonBase(c).FillWidth().Height(36).PaddingX(design.Space2).
				Radius(design.RadiusSM).Label(title)
			switch {
			case row.Pressed():
				row = row.Background(t.BgSelected)
			case row.Hovered():
				row = row.Background(t.BgHover)
			}
			row.Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
					ui.Icon(c, icons.Message()).Size(16, 16).TextColor(t.LabelSecondary)
					design.Apply(ui.Text(c, title), t.Text(design.TypeS)).
						TextColor(t.LabelSecondary).Grow(1).MaxLines(1).Ellipsis("…")
					if !session.UpdatedAt.IsZero() {
						design.Apply(ui.Text(c, uikit.FormatDateTime(nil, session.UpdatedAt)),
							t.TextMuted(design.TypeXXXS)).NoWrap()
					}
				})
			})
			row.OnClick(func() {
				_ = chat.SetActiveSession(session.ID)
				runtime.Post(func() { _ = nav.Push("session") })
				runtime.Invalidate()
			})
		}
	})
}

// buildNextSteps is Home's empty state: with no conversations yet, the front
// page offers the actions that get a first chat started (configure a model,
// open a workspace, start the conversation) instead of leaving the composer as
// the only entry. Only the steps that still need doing are shown.
func buildNextSteps(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	nav contracts.Navigator, chat contracts.ChatService, kctx cordis.Context,
	st *homeComposerState, tr func(zh, en string) string) {
	if chat == nil || len(homeVisibleSessions(chat)) > 0 {
		return
	}
	modelConfigured := strings.TrimSpace(chat.Config().Model) != ""
	var workspaceService contracts.WorkspaceService
	if raw, ok := kctx.Get(contracts.KeyWorkspace); ok {
		workspaceService, _ = raw.(contracts.WorkspaceService)
	}
	workspaceOpen := false
	if workspaceService != nil {
		if current, ok, err := workspaceService.Current(context.Background()); err == nil && ok && strings.TrimSpace(current.ID) != "" {
			workspaceOpen = true
		}
	}

	ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
		design.Apply(ui.Text(c, tr(
			"还没有会话。开始第一次对话，或先完成下面的准备：",
			"No conversations yet. Start the first one, or get these ready first:")),
			t.TextMuted(design.TypeXXS)).MaxLines(2)
		ui.Row(c).AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			if !modelConfigured {
				configure := design.Apply(ui.Button(c, tr("配置模型", "Configure model")), t.SecondaryButton())
				if configure.Clicked() {
					nav.ToggleSettings()
				}
			}
			if workspaceService != nil && !workspaceOpen {
				addWorkspace := design.Apply(ui.Button(c, tr("添加工作区", "Add workspace")), t.SecondaryButton())
				if addWorkspace.Clicked() {
					addHomeWorkspace(workspaceService, st, tr)
					runtime.Invalidate()
				}
			}
			start := design.Apply(ui.PrimaryButton(c, tr("新对话", "New chat")), t.PrimaryButton())
			if start.Clicked() {
				if _, err := chat.NewSession(""); err == nil {
					_ = nav.Push("session")
					runtime.Invalidate()
				}
			}
		})
		if st.nextStepErr != "" {
			uikit.ErrorLine(c, st.nextStepErr)
		}
	})
}

// buildHero is the default Home hero: the brand mark and wordmark over one
// user-facing value line. Build metadata (version, Go version) belongs to About,
// not to the product's front page.
func buildHero(c *ui.Context, t *design.Tokens, greeting string, showFallbackHint bool,
	tr func(zh, en string) string) {
	ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(design.Space3).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(design.Space3).Children(func() {
			ui.Image(c, brand.Mark(t.Dark)).Size(48, 48)
			design.Apply(ui.Text(c, heroTitle), t.Text(design.TypeXL)).FontWeight(600)
		})
		design.Apply(ui.Text(c, greeting), t.TextMuted(design.TypeM))
		// Default hero hint; replaced when another module contributes home.hero
		// (see region). With a chat backend the product Home is not an empty
		// state, so the hint is only shown in chat-less assemblies.
		if showFallbackHint {
			design.Apply(ui.Text(c, tr("这是 home 的默认 Hero 空态。",
				"This is Home's default hero empty state.")), t.TextMuted(design.TypeXS))
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
