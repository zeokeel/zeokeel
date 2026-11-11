// Package settings contributes the model, providers, appearance, skills and
// shortcuts pages to the settings surface, rebuilt with the ZeoKeel brand design
// system.
//
// It is a UI module like ui/about: a kernel Descriptor that requires the host UI
// runtime, the settings store, the chat read model, the theme and the locale, and
// registers two contracts.SettingsPageSpec values on activation. The host owns
// the settings shell (native Sidebar + Modal); this module only composes native
// mygo controls — ui.TextInput, ui.TextArea, ui.Toggle, ui.Select, ui.Button,
// ui.PrimaryButton, ui.Icon — and styles them with internal/design tokens
// (Panel cards, the brand type scale, brand spacing/radii and the semantic
// colors). Nothing is hand-drawn.
//
// The 模型 page owns two kinds of state with two different save semantics: the
// model list (modelcatalog.models) applies immediately — every row carries its
// own provider connection and one row is the default model, whose connection is
// mirrored into chat.model/chat.baseURL/chat.apiKey — while the system prompt and
// the runtime limits are explicit-save drafts persisted by the page footer.
package settings

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// Settings page constants. ID and Section are stable contracts the host renders
// in its grouped settings sidebar; the host sorts the nav by Section rank first
// (general → 模型 → 外观 → 键盘 → 高级 → 关于) and by Order inside a section.
// Order only needs to be unique inside one section.
const (
	PageID      = "model"
	PageSection = "常用"
	PageOrder   = 10

	// AppearancePageID is the appearance page identity (theme + language).
	AppearancePageID = "appearance"
	// AppearancePageSection is the "appearance & skills" nav group.
	AppearancePageSection = "外观"
	// AppearancePageOrder places the page after the model group.
	AppearancePageOrder = 10

	// ShortcutsPageID is the keyboard-shortcut reference page.
	ShortcutsPageID = "shortcuts"
	// ShortcutsPageOrder places the page after appearance.
	ShortcutsPageOrder = 30

	// DiagnosticsPageID is the read-only diagnostics page (advanced section).
	DiagnosticsPageID = "diagnostics"
	// DiagnosticsPageSection places it in the advanced group.
	DiagnosticsPageSection = "高级"
	// DiagnosticsPageOrder places it first in the advanced group.
	DiagnosticsPageOrder = 50

	// SkillsPageID is the skills discovery/preview page.
	SkillsPageID = "skills"
	// SkillsPageSection places the skills page in the appearance group; skills
	// are how the assistant's capabilities look to the user.
	SkillsPageSection = "外观"
	// SkillsPageOrder places the page after appearance.
	SkillsPageOrder = 20
	// SkillsPageTitle is the page title and heading.
	SkillsPageTitle = "技能"
)

// shortcutRow is one line of the shortcut reference.
type shortcutRow struct {
	Action string
	Keys   string
}

// shortcutReferenceRows builds the read-only keyboard reference. H5: the two
// configurable shortcuts are edited in the section above and are not repeated
// here; this table lists the fixed window shortcuts only.
func shortcutReferenceRows(locale contracts.LocaleService, runtime contracts.UIRuntime) []shortcutRow {
	// One notation for every chord: uikit.FormatShortcut spells the command
	// modifier for the platform and joins keys with " + " (V11). Non-chord
	// entries (Enter, "type /") keep their literal key names.
	rows := []shortcutRow{
		{localized(locale, "新会话", "New session"), uikit.FormatShortcut("CmdOrCtrl+N")},
		{localized(locale, "切换右侧栏", "Toggle right panel"), uikit.FormatShortcut("CmdOrCtrl+Alt+I")},
		{localized(locale, "发送消息", "Send message"), "Enter"},
		{localized(locale, "换行", "New line"), uikit.FormatShortcut("Shift+Enter")},
		{localized(locale, "命令菜单", "Command menu"), localized(locale, "输入 /", "Type /")},
		{localized(locale, "设置", "Settings"), uikit.FormatShortcut("CmdOrCtrl+,")},
		{localized(locale, "关闭弹窗 / 菜单", "Close dialog / menu"), "Esc"},
	}
	// Registered commands carry the host's real chords (the right panel's page
	// shortcuts, for example); the reference must include them instead of
	// documenting only a hand-written subset (B11).
	if runtime != nil {
		commands := runtime.Commands()
		sort.SliceStable(commands, func(i, j int) bool {
			if commands[i].Order != commands[j].Order {
				return commands[i].Order < commands[j].Order
			}
			return commands[i].ID < commands[j].ID
		})
		for _, command := range commands {
			chord := strings.TrimSpace(command.Shortcut)
			title := strings.TrimSpace(command.Title)
			if chord == "" || title == "" {
				continue
			}
			rows = append(rows, shortcutRow{Action: title, Keys: uikit.FormatShortcut(chord)})
		}
	}
	return rows
}

// shortcutChoices maps the editable chrome shortcuts to their native Select
// options: display label -> canonical chord.
type shortcutChoice struct{ Label, Chord string }

// shortcutChoicesFor builds the editable shortcut options with labels from
// uikit.FormatShortcut, so they use the same notation as the reference table
// and the command palette. Chords whose display forms are identical on this
// platform (Cmd/B and Ctrl/B are the same physical chord off macOS) collapse to
// one option instead of listing the same label twice.
func shortcutChoicesFor(chords ...string) []shortcutChoice {
	choices := make([]shortcutChoice, 0, len(chords))
	seen := make(map[string]bool, len(chords))
	for _, chord := range chords {
		label := uikit.FormatShortcut(chord)
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		choices = append(choices, shortcutChoice{Label: label, Chord: chord})
	}
	return choices
}

func toggleSidebarChoices() []shortcutChoice {
	return shortcutChoicesFor("cmd+b", "ctrl+b", "alt+b", "cmd+shift+b")
}

func commandPaletteChoices() []shortcutChoice {
	return shortcutChoicesFor("cmd+k", "ctrl+k", "alt+k", "cmd+shift+k")
}

// shortcutDisplay returns the display label of a stored chord (falling back to
// the first choice when the value is not one of the options).
func shortcutDisplay(choices []shortcutChoice, chord string) string {
	for _, choice := range choices {
		if choice.Chord == chord {
			return choice.Label
		}
	}
	return uikit.FormatShortcut(chord)
}

// shortcutChord returns the canonical chord of a display label.
func shortcutChord(choices []shortcutChoice, label string) string {
	for _, choice := range choices {
		if choice.Label == label {
			return choice.Chord
		}
	}
	return choices[0].Chord
}

// shortcutsState is the shortcut page's local state: the confirmation of the
// destructive "restore defaults" action.
type shortcutsState struct {
	confirmRestore bool
}

// buildShortcutsPage renders the shortcut reference with native rows and the
// two editable desktop shortcuts (persisted through the host settings). Every
// control on the page applies immediately, so there is no save button.
func buildShortcutsPage(c *ui.Context, t *design.Tokens, title string, store contracts.SettingsService,
	runtime contracts.UIRuntime, locale contracts.LocaleService, st *shortcutsState) {
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		uikit.PageHeader(c, title, localized(locale,
			"键盘快捷键参考；下面两项桌面快捷键可修改并立即生效。",
			"Keyboard reference; the two desktop shortcuts below are editable and apply immediately."))
		uikit.Section(c, localized(locale, "桌面快捷键", "Desktop shortcuts"), func(*ui.Context) {
			// H23: restoring the defaults is a page action, not a form row: it
			// sits at the section's top right, away from the editable fields.
			ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
				// Restoring the defaults is recoverable, so it is not a danger
				// action (DESIGN.md §5.2); the confirmation still guards the
				// overwrite of the user's custom chords.
				if design.Apply(ui.Button(c, localized(locale, "恢复默认", "Restore defaults")),
					t.SecondaryButton()).Tooltip(localized(locale,
					"恢复两项快捷键的默认值", "Restore both default shortcuts")).Clicked() {
					st.confirmRestore = true
					c.Invalidate()
				}
			})
			toggleDisplay := shortcutDisplay(toggleSidebarChoices(),
				store.Get(contracts.SettingsUIShortcutToggleSidebar, contracts.DefaultShortcutToggleSidebar))
			ui.Row(c).FillWidth().PaddingY(design.Space2).
				AlignItems(ui.Center).Gap(design.Space3).Children(func() {
				design.Apply(ui.Text(c, localized(locale, "折叠 / 展开侧边栏", "Collapse / expand sidebar")),
					t.Text(design.TypeBody)).Grow(1)
				choice := toggleDisplay
				selectControl := ui.Select(c, &choice, shortcutChoiceLabels(toggleSidebarChoices())).
					Label(localized(locale, "侧边栏快捷键", "Sidebar shortcut"))
				if selectControl.Changed() {
					_ = store.Set(contracts.SettingsUIShortcutToggleSidebar, shortcutChord(toggleSidebarChoices(), choice))
				}
			})
			ui.Divider(c)
			paletteDisplay := shortcutDisplay(commandPaletteChoices(),
				store.Get(contracts.SettingsUIShortcutCommandPalette, contracts.DefaultShortcutCommandPalette))
			ui.Row(c).FillWidth().PaddingY(design.Space2).
				AlignItems(ui.Center).Gap(design.Space3).Children(func() {
				design.Apply(ui.Text(c, localized(locale, "打开命令面板", "Open command palette")),
					t.Text(design.TypeBody)).Grow(1)
				choice := paletteDisplay
				selectControl := ui.Select(c, &choice, shortcutChoiceLabels(commandPaletteChoices())).
					Label(localized(locale, "命令面板快捷键", "Command palette shortcut"))
				if selectControl.Changed() {
					_ = store.Set(contracts.SettingsUIShortcutCommandPalette, shortcutChord(commandPaletteChoices(), choice))
				}
			})
		})
		uikit.Section(c, localized(locale, "全部快捷键", "All shortcuts"), func(*ui.Context) {
			for index, row := range shortcutReferenceRows(locale, runtime) {
				if index > 0 {
					ui.Divider(c)
				}
				ui.Row(c).FillWidth().PaddingY(design.Space2).
					AlignItems(ui.Center).Children(func() {
					design.Apply(ui.Text(c, row.Action), t.Text(design.TypeBody)).Grow(1)
					design.Apply(ui.Text(c, row.Keys), t.Font(design.TypeXS), t.MonoFamily()).
						TextColor(t.LabelTertiary).Shrink(0)
				})
			}
		})
		// Restoring the defaults overwrites the user's custom chords: confirm it
		// with the native alert dialog.
		if st.confirmRestore {
			uikit.ConfirmDanger(c, &st.confirmRestore, uikit.ConfirmDangerOptions{
				Key:   "shortcuts.restore-defaults",
				Title: localized(locale, "恢复默认快捷键", "Restore default shortcuts"),
				Message: localized(locale, "恢复默认会覆盖你自定义的两项快捷键。",
					"Restoring the defaults overwrites your two custom shortcuts."),
				Confirm: localized(locale, "恢复默认", "Restore"),
				Cancel:  localized(locale, "取消", "Cancel"),
				OnConfirm: func() {
					_ = store.Set(contracts.SettingsUIShortcutToggleSidebar, contracts.DefaultShortcutToggleSidebar)
					_ = store.Set(contracts.SettingsUIShortcutCommandPalette, contracts.DefaultShortcutCommandPalette)
					c.Invalidate()
				},
			})
		}
	})
}

// shortcutChoiceLabels returns the Select options of a shortcut table.
func shortcutChoiceLabels(choices []shortcutChoice) []string {
	labels := make([]string, len(choices))
	for index, choice := range choices {
		labels[index] = choice.Label
	}
	return labels
}

// appearanceState is the appearance page's UI-local state.
type appearanceState struct {
	// themeMode is the segmented appearance selection: light/dark/system.
	themeMode int
	language  string
	status    string
	statusErr bool

	// Desktop integration switches (persisted immediately, applied by the
	// process host: XDG autostart entry and the single-instance lock) plus the
	// close-to-tray switch (the host hides the window instead of quitting).
	autostart      bool
	singleInstance bool
	closeToTray    bool
	// globalHotkey is the canonical accelerator draft. Theme, language and the
	// desktop switches apply at once; the hotkey is the page's one explicit-save
	// field (the host registers it at the next start; empty disables it).
	globalHotkey string
	// hotkeyText is the friendly display draft bound to the input
	// ("Cmd/Ctrl + Shift + Space"), parsed back into globalHotkey on edit.
	hotkeyText  string
	hotkeyError string
	// savingHotkey/captureText/captureFocus/captureInput are the key recorder's
	// state: while capturing, a focused text input turns pressed combinations
	// into the accelerator.
	capturing    bool
	captureText  string
	captureFocus bool
	captureInput ui.Handle
	// savedHotkey is the persisted accelerator the draft was seeded from, so the
	// page can show an "unapplied" hint and its action a dirty state.
	savedHotkey string

	// transient detects the page being shown again after another page (or the
	// closed surface), so status copy and store-backed values can refresh.
	transient transientStatus
}

// singleInstanceSwitchEnabled reads the stored single-instance setting with the
// host's default (on); only recognizable off values disable it.
func singleInstanceSwitchEnabled(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "false", "0", "no", "off":
		return false
	default:
		return true
	}
}

// buildAppearanceLabels is the resolved copy of the appearance page.
type buildAppearanceLabels struct {
	title         string
	themeLabel    string
	languageLabel string
	saveLabel     string
}

// languageOptions maps the selector's display strings to locale codes.
var languageOptions = []string{"中文", "English"}

func languageCode(choice string) string {
	if choice == "English" {
		return "en"
	}
	return "zh"
}

func languageChoice(code string) string {
	if code == "en" {
		return "English"
	}
	return "中文"
}

// Config is the module's typed assembly config.
type Config struct {
	// Title is the settings page title (default "模型").
	Title string `json:"title"`
	// ModelLabel/BaseURLLabel/APIKeyLabel are kept for existing assembly
	// configurations: the page no longer has one global connection form (every
	// model row owns its connection), so they are not rendered.
	ModelLabel   string `json:"modelLabel"`
	BaseURLLabel string `json:"baseURLLabel"`
	APIKeyLabel  string `json:"apiKeyLabel"`
	// SystemPromptLabel labels the system prompt text area (default "系统提示词").
	SystemPromptLabel string `json:"systemPromptLabel"`
	// SaveLabel labels the save button (default "保存").
	SaveLabel string `json:"saveLabel"`
	// AppearanceTitle is the appearance page title (default "外观").
	AppearanceTitle string `json:"appearanceTitle"`
	// ThemeLabel labels the dark-mode toggle (default "深色模式").
	ThemeLabel string `json:"themeLabel"`
	// LanguageLabel labels the language selector (default "界面语言").
	LanguageLabel string `json:"languageLabel"`
	// SaveAppearanceLabel labels the appearance save button (default "应用").
	SaveAppearanceLabel string `json:"saveAppearanceLabel"`
}

// moduleState is the page's UI-local state for its explicit-save fields. The
// model list is not drafted here: it is the immediate-effect control surface and
// always renders the persisted modelcatalog.models row.
type moduleState struct {
	systemPrompt string

	status    string
	statusErr bool

	// seenSystemPrompt snapshots the persisted prompt the draft was seeded from.
	// The build compares it with the current effective config so a write from
	// another surface refreshes the draft instead of the stale draft silently
	// overwriting it on the next save.
	seenSystemPrompt string

	// runtimeOpen controls the model page's collapsed 高级选项 (runtime limits):
	// the daily form stays short, the low-frequency knobs unfold on demand.
	runtimeOpen bool

	// promptInput is the persistent input identity of the system-prompt field:
	// the sync never rewrites a draft whose input holds the focus, so it cannot
	// replace text under the caret.
	promptInput ui.Handle

	// Runtime limit drafts (persisted by the page's footer Save) plus their
	// per-field inline validation messages. An empty numeric draft means "use
	// the module config", so saving it clears the settings row. The stream
	// completeness switch is immediate-write and therefore has no draft.
	maxIterations     string
	contextBudget     string
	iterationsErr     string
	contextBudgetErr  string
	seenMaxIterations string
	seenContextBudget string
	iterationsInput   ui.Handle
	budgetInput       ui.Handle
	// The runtime limits are official ui.NumberInputs; the numeric mirrors back
	// their fields, and the default switches say whether the module config
	// applies (default = no settings row, which is how "leave it to the module"
	// is stored).
	maxIterationsNum  float64
	contextBudgetNum  float64
	iterationsDefault bool
	budgetDefault     bool
	// The module-config limits are display-only defaults: they seed the numeric
	// inputs' starting point and the "use default" copy, but never become drafts
	// or settings rows unless the user turns the default switch off.
	moduleMaxIterations int
	moduleContextBudget int
}

// buildLabels is the resolved copy of one page build.
type buildLabels struct {
	title             string
	systemPromptLabel string
	saveLabel         string
}

// localized returns the copy for the current host language.
func localized(locale contracts.LocaleService, zh, en string) string {
	return uikit.T(locale, zh, en)
}

// pageDefinition is one settings page contribution with its Chinese/English
// titles. Sections stay the internal stable identifiers (they only drive the
// host's ordering); only the displayed Title follows the locale.
type pageDefinition struct {
	ID            string
	Section       string
	Order         int
	zhTitle       string
	enTitle       string
	titleOverride string
	Build         contracts.BuildFunc
	// Footer renders the page's pinned action row in the host's fixed settings
	// footer (outside the scrolling body), and Dirty reports unsaved edits so
	// the host can show the unsaved state and guard closing the surface.
	Footer contracts.BuildFunc
	Dirty  func() bool
	// Discard throws the drafts away and re-reads the saved values; the host
	// calls it when the user closes the settings with "Discard".
	Discard func()
}

// pageSyncer keeps the registered settings pages in sync with the host locale.
// The host reads page titles from the runtime registry every frame, so a
// language switch re-registers the pages (from an invisible sidebar slot built
// before the settings overlay) to make the navigation labels change on the next
// frame without restarting the module.
type pageSyncer struct {
	runtime    contracts.UIRuntime
	locale     contracts.LocaleService
	pages      []pageDefinition
	registered map[string]func()
	lang       string
}

func newPageSyncer(runtime contracts.UIRuntime, locale contracts.LocaleService) *pageSyncer {
	return &pageSyncer{runtime: runtime, locale: locale, registered: map[string]func(){}}
}

// title resolves one page title for the current locale.
func (s *pageSyncer) title(def pageDefinition) string {
	if override := strings.TrimSpace(def.titleOverride); override != "" {
		return override
	}
	return localized(s.locale, def.zhTitle, def.enTitle)
}

// register adds or replaces one page contribution.
func (s *pageSyncer) register(def pageDefinition) error {
	unregister, err := s.runtime.RegisterSettingsPage(contracts.SettingsPageSpec{
		ID:      def.ID,
		Section: def.Section,
		Title:   s.title(def),
		Order:   def.Order,
		Build:   def.Build,
		Footer:  def.Footer,
		Dirty:   def.Dirty,
		Discard: def.Discard,
	})
	if err != nil {
		return err
	}
	if old := s.registered[def.ID]; old != nil {
		old()
	}
	s.registered[def.ID] = unregister
	return nil
}

// start registers every page once (activation).
func (s *pageSyncer) start() error {
	for _, def := range s.pages {
		if err := s.register(def); err != nil {
			s.close()
			return err
		}
	}
	s.lang = s.locale.Language()
	return nil
}

// sync re-registers the pages when the host language changed since the last
// frame. It is a cheap no-op while the language is stable.
func (s *pageSyncer) sync() {
	lang := s.locale.Language()
	if lang == s.lang {
		return
	}
	for _, def := range s.pages {
		if old := s.registered[def.ID]; old != nil {
			old()
			delete(s.registered, def.ID)
		}
		if err := s.register(def); err != nil {
			continue
		}
	}
	s.lang = lang
}

// close unregisters every current page contribution.
func (s *pageSyncer) close() {
	for _, unregister := range s.registered {
		if unregister != nil {
			unregister()
		}
	}
	s.registered = map[string]func(){}
}

// diagnosticRow is one label/value line of the diagnostics page.
type diagnosticRow struct{ Label, Value string }

// diagnosticsState is the diagnostics page's UI state: whether the copy action
// just ran (so the page can show a short confirmation), the module disclosure
// and the official List state of the loaded-module collection.
type diagnosticsState struct {
	copied      bool
	modulesOpen bool
	keysOpen    bool
	list        ui.ListState
	moduleSel   int
	transient   transientStatus
}

// buildDiagnosticsPage renders the read-only runtime/storage summary (the old
// shell's diagnostics view) with a native copy action.
func buildDiagnosticsPage(c *ui.Context, t *design.Tokens, state *diagnosticsState,
	rows, keys []diagnosticRow, modules []contracts.ModuleInfo, locale contracts.LocaleService) {
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		uikit.PageHeader(c, localized(locale, "诊断", "Diagnostics"), localized(locale,
			"运行时与存储信息（只读）；可复制后附在问题报告里。",
			"Read-only runtime and storage details; copy them into a bug report."))
		uikit.Section(c, localized(locale, "运行时信息", "Runtime information"), func(*ui.Context) {
			// The property sheet is one shape everywhere (DESIGN.md §5.2
			// PropertySheet): a 160 DIP label column, the value left-aligned on
			// a 32 DIP row, a divider between rows.
			sheet := make([]uikit.KeyValue, len(rows))
			for index, row := range rows {
				sheet[index] = uikit.KeyValue{Key: row.Label, Value: row.Value}
			}
			buildPropertySheet(c, t, sheet)
			// Setting keys are a long, per-key list: a summary row plus one
			// two-column disclosure instead of a single truncated line (B13/P3).
			if len(keys) > 0 || len(rows) > 0 {
				ui.Divider(c)
				summary := fmt.Sprintf(localized(locale, "设置项 · %d 项", "Setting keys · %d"), len(keys))
				ui.Collapsible(c, summary, &state.keysOpen, func() {
					if len(keys) == 0 {
						design.Apply(ui.Text(c, localized(locale, "（空）", "(empty)")),
							t.TextMuted(design.TypeXS))
						return
					}
					ui.Column(c).FillWidth().Gap(design.Space0).Children(func() {
						for index, pair := range keys {
							if index > 0 {
								ui.Divider(c)
							}
							ui.Row(c).FillWidth().Height(design.Space8).
								AlignItems(ui.Center).Gap(design.Space3).Children(func() {
								design.Apply(ui.Text(c, pair.Label), t.MonoText(design.TypeXS)).
									TextColor(t.LabelSecondary).Width(design.Space8 * 7).
									Shrink(0).MaxLines(1).Ellipsis("…").Tooltip(pair.Label)
								design.Apply(ui.Text(c, pair.Value), t.MonoText(design.TypeXS)).
									TextColor(t.LabelPrimary).Grow(1).MaxLines(1).
									Ellipsis("…").Tooltip(pair.Value).Selectable()
							})
						}
					})
				})
			}
		})
		buildLoadedModulesSection(c, t, state, locale, modules)

		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			if ui.Button(c, localized(locale, "复制诊断信息", "Copy diagnostics")).Clicked() {
				// The copy is the whole sheet: the runtime rows and the (already
				// masked) setting keys, so a bug report carries the same detail
				// the page shows.
				var builder strings.Builder
				for _, row := range rows {
					fmt.Fprintf(&builder, "%s: %s\n", row.Label, row.Value)
				}
				for _, pair := range keys {
					fmt.Fprintf(&builder, "%s: %s\n", pair.Label, pair.Value)
				}
				c.WriteClipboard(strings.TrimRight(builder.String(), "\n"))
				state.copied = true
			}
			if state.copied {
				design.Apply(ui.Badge(c, localized(locale, "已复制", "Copied")), t.Badge()).
					TextColor(t.SuccessText)
			}
		})
	})
}

// Module registers the model and appearance settings pages.
func Module() *module.Descriptor[Config] {
	return module.Define("zeokeel-ui/settings", func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		store, err := ctx.Require(contracts.KeySettings)
		if err != nil {
			return err
		}
		chat, err := ctx.Require(contracts.KeyChat)
		if err != nil {
			return err
		}
		theme, err := ctx.Require(contracts.KeyTheme)
		if err != nil {
			return err
		}
		locale, err := ctx.Require(contracts.KeyLocale)
		if err != nil {
			return err
		}

		state := newState(chat, store)
		appearance := &appearanceState{
			themeMode: appearanceThemeMode(
				store.Get(contracts.SettingThemeMode, ""),
				store.Get(contracts.SettingsUIThemeDark, ""),
			),
			language:       locale.Language(),
			autostart:      store.Get(contracts.SettingAutostart, "false") == "true",
			singleInstance: singleInstanceSwitchEnabled(store.Get(contracts.SettingSingleInstance, "true")),
			closeToTray:    closeToTraySettingEnabled(store.Get(contracts.SettingCloseToTray, "false")),
			globalHotkey:   store.Get(contracts.SettingGlobalHotkey, ""),
			hotkeyText:     formatHotkeyDisplay(store.Get(contracts.SettingGlobalHotkey, "")),
			savedHotkey:    store.Get(contracts.SettingGlobalHotkey, ""),
		}
		shortcuts := &shortcutsState{}
		archive := &archiveState{sel: -1}
		skills := newSkillsState()
		update := newUpdateState()
		importState := newImportState()
		search := newSearchState()
		mcp := newMCPState()
		presets := newPresetsState()
		catalog := &catalogState{sel: -1}
		lsp := &lspState{sel: -1}
		notifications := &notificationsState{}
		behavior := &behaviorState{}
		memoryPage := &memoryPageState{sel: -1}
		network := newNetworkState()
		dataPrivacy := newDataPrivacyState()
		diagnostics := &diagnosticsState{}

		// Optional host services for the read-only diagnostics page.
		info, _ := ctx.Get(contracts.KeyAppInfo)
		dataDir, _ := ctx.Get(contracts.KeyDataDir)
		toolkit, _ := ctx.Get(contracts.KeyToolkit)

		modelLabels := func() buildLabels {
			return buildLabels{
				title:             firstNonEmpty(cfg.Title, localized(locale, "模型", "Model")),
				systemPromptLabel: firstNonEmpty(cfg.SystemPromptLabel, localized(locale, "系统提示词", "System prompt")),
				saveLabel: firstNonEmpty(cfg.SaveLabel, localized(locale,
					"保存提示词与运行参数", "Save prompt & runtime")),
			}
		}
		appearanceLabels := func() buildAppearanceLabels {
			return buildAppearanceLabels{
				title:         firstNonEmpty(cfg.AppearanceTitle, localized(locale, "外观", "Appearance")),
				themeLabel:    firstNonEmpty(cfg.ThemeLabel, localized(locale, "外观模式", "Appearance mode")),
				languageLabel: firstNonEmpty(cfg.LanguageLabel, localized(locale, "界面语言", "Interface language")),
				saveLabel:     firstNonEmpty(cfg.SaveAppearanceLabel, localized(locale, "应用", "Apply")),
			}
		}

		syncer := newPageSyncer(runtime, locale)
		definitions := []pageDefinition{
			{
				ID: PageID, Section: PageSection, Order: PageOrder,
				zhTitle: "模型", enTitle: "Model", titleOverride: cfg.Title,
				Build: func(c *ui.Context) {
					syncer.sync()
					buildPage(c, runtime, state, catalog, chat, modelLabels(), store, locale)
				},
				Footer: func(c *ui.Context) {
					uikit.DialogFooter(c, []uikit.DialogAction{{
						Label: modelLabels().saveLabel, Primary: true,
						OnClick: func() {
							saveModelPage(c, state, store, locale)
						},
					}})
				},
				Dirty: state.dirty,
				Discard: func() {
					state.discardDrafts(chat, store)
					catalog.discardTransient()
				},
			},
			{
				ID: DiagnosticsPageID, Section: DiagnosticsPageSection, Order: DiagnosticsPageOrder,
				zhTitle: "诊断", enTitle: "Diagnostics",
				Build: func(c *ui.Context) {
					syncer.sync()
					if diagnostics.transient.reentered(runtime) {
						// The "已复制" badge confirms one action; coming back to the
						// page starts a new visit and must not claim a fresh copy.
						diagnostics.copied = false
					}
					rows, keys := diagnosticsRows(info, dataDir, toolkit, chat, store, locale)
					modules := loadedModulesFromContext(ctx)
					buildDiagnosticsPage(c, design.Of(c), diagnostics, rows, keys, modules, locale)
				},
			},
			{
				ID: ShortcutsPageID, Section: "外观", Order: ShortcutsPageOrder,
				zhTitle: "快捷键", enTitle: "Shortcuts",
				Build: func(c *ui.Context) {
					syncer.sync()
					buildShortcutsPage(c, design.Of(c), localized(locale, "快捷键", "Shortcuts"), store, runtime, locale, shortcuts)
				},
			},
			{
				ID: AppearancePageID, Section: AppearancePageSection, Order: AppearancePageOrder,
				zhTitle: "外观", enTitle: "Appearance", titleOverride: cfg.AppearanceTitle,
				Build: func(c *ui.Context) {
					syncer.sync()
					if appearance.transient.reentered(runtime) {
						// Re-read the store-backed values and clear the transient status
						// line, so a value applied elsewhere is visible on the next visit.
						appearance.reenter(store, locale)
					}
					buildAppearancePage(c, appearance, store, theme, locale, appearanceLabels())
				},
				Footer: func(c *ui.Context) {
					uikit.DialogFooter(c, []uikit.DialogAction{{
						Label: appearanceLabels().saveLabel, Primary: true,
						OnClick: func() {
							applyAppearancePage(c, appearance, store, locale)
						},
					}})
				},
				Dirty: appearance.dirty,
				Discard: func() {
					appearance.discardHotkey(store)
				},
			},
			{
				ID: SkillsPageID, Section: SkillsPageSection, Order: SkillsPageOrder,
				zhTitle: "技能", enTitle: "Skills",
				Build: func(c *ui.Context) {
					syncer.sync()
					buildSkillsPage(c, design.Of(c), ctx, skills, locale)
				},
			},
			{
				ID: UpdatePageID, Section: UpdatePageSection, Order: UpdatePageOrder,
				zhTitle: "更新", enTitle: "Update",
				Build: func(c *ui.Context) {
					syncer.sync()
					if update.transient.reentered(runtime) {
						update.copied = false
					}
					buildUpdatePage(c, design.Of(c), ctx, runtime, update, locale)
				},
			},
			{
				ID: ImportPageID, Section: ImportPageSection, Order: ImportPageOrder,
				zhTitle: "导入", enTitle: "Import",
				Build: func(c *ui.Context) {
					syncer.sync()
					buildImportPage(c, design.Of(c), ctx, runtime, importState, locale)
				},
			},
			{
				ID: SearchPageID, Section: SearchPageSection, Order: SearchPageOrder,
				zhTitle: "搜索", enTitle: "Search",
				Build: func(c *ui.Context) {
					syncer.sync()
					buildSearchPage(c, ctx, search, locale)
				},
				Footer: func(c *ui.Context) {
					uikit.DialogFooter(c, []uikit.DialogAction{{
						Label: localized(locale, "保存", "Save"), Primary: true,
						OnClick: func() {
							if store, ok := ctx.Get(contracts.KeySettings); ok && store != nil {
								saveSearchSettings(search, store, locale)
							}
							c.Invalidate()
						},
					}})
				},
				Dirty: func() bool { return search.dirty(store) },
				Discard: func() {
					search.discardDrafts(store)
				},
			},
			{
				ID: MCPPageID, Section: MCPPageSection, Order: MCPPageOrder,
				zhTitle: "MCP", enTitle: "MCP",
				Build: func(c *ui.Context) {
					syncer.sync()
					if mcp.transient.reentered(runtime) {
						// The form's result copy belongs to one visit; the draft itself
						// is guarded by Dirty and survives.
						mcp.status = ""
						mcp.statusErr = false
						mcp.testStatus = ""
						mcp.testErr = false
					}
					buildMCPPage(c, ctx, mcp, locale)
				},
				Dirty: func() bool { return mcpDraftDirty(mcp) },
				Discard: func() {
					discardMCPDraft(mcp)
				},
			},
			{
				ID: ArchivePageID, Section: ArchivePageSection, Order: ArchivePageOrder,
				zhTitle: "归档", enTitle: "Archived",
				Build: func(c *ui.Context) {
					syncer.sync()
					buildArchivePage(c, design.Of(c), ctx, archive, locale)
				},
			},
			{
				ID: NotificationsPageID, Section: NotificationsPageSection, Order: NotificationsPageOrder,
				zhTitle: "通知", enTitle: "Notifications",
				Build: func(c *ui.Context) {
					syncer.sync()
					buildNotificationsPage(c, runtime, store, locale, notifications)
				},
			},
			{
				ID: BehaviorPageID, Section: BehaviorPageSection, Order: BehaviorPageOrder,
				zhTitle: "行为", enTitle: "Behavior",
				Build: func(c *ui.Context) {
					syncer.sync()
					buildBehaviorPage(c, runtime, store, locale, behavior)
				},
				Dirty: func() bool { return behaviorDraftDirty(behavior, store) },
				Discard: func() {
					discardBehaviorDraft(behavior, store)
				},
			},
			{
				ID: MemoryPageID, Section: MemoryPageSection, Order: MemoryPageOrder,
				zhTitle: "记忆", enTitle: "Memory",
				Build: func(c *ui.Context) {
					syncer.sync()
					buildMemoryPage(c, design.Of(c), ctx, memoryPage, store, locale)
				},
				Dirty: func() bool { return memoryDraftDirty(memoryPage) },
				Discard: func() {
					discardMemoryDraft(memoryPage)
				},
			},
			{
				ID: LSPPageID, Section: LSPPageSection, Order: LSPPageOrder,
				zhTitle: "LSP", enTitle: "LSP",
				Build: func(c *ui.Context) {
					syncer.sync()
					buildLSPPage(c, store, lsp, locale)
				},
				Dirty: func() bool { return lspDraftDirty(lsp) },
				Discard: func() {
					discardLSPDraft(lsp)
				},
			},
			{
				ID: PresetsPageID, Section: PresetsPageSection, Order: PresetsPageOrder,
				zhTitle: "预设", enTitle: "Presets",
				Build: func(c *ui.Context) {
					syncer.sync()
					buildPresetsPage(c, store, presets, locale)
				},
				Dirty: func() bool { return presetsDraftDirty(presets) },
				Discard: func() {
					discardPresetsDraft(presets)
				},
			},
			{
				ID: NetworkPageID, Section: NetworkPageSection, Order: NetworkPageOrder,
				zhTitle: "网络", enTitle: "Network",
				Build: func(c *ui.Context) {
					syncer.sync()
					buildNetworkPage(c, store, network, locale)
				},
				Footer: func(c *ui.Context) {
					uikit.DialogFooter(c, []uikit.DialogAction{{
						Label: localized(locale, "保存", "Save"), Primary: true,
						OnClick: func() {
							saveNetworkSettings(network, store, locale)
							c.Invalidate()
						},
					}})
				},
				Dirty: func() bool { return network.dirty(store) },
				Discard: func() {
					network.discardDrafts(store)
				},
			},
			{
				ID: DataPrivacyPageID, Section: DataPrivacyPageSection, Order: DataPrivacyPageOrder,
				zhTitle: "数据与隐私", enTitle: "Data & privacy",
				Build: func(c *ui.Context) {
					syncer.sync()
					buildDataPrivacyPage(c, design.Of(c), ctx, chat, store, dataPrivacy, locale)
				},
			},
		}
		syncer.pages = definitions
		if err := syncer.start(); err != nil {
			return err
		}
		if _, err := ctx.Effect(func() cordis.Effect {
			return cordis.Effect{cordis.CleanupOf(syncer.close)}
		}); err != nil {
			syncer.close()
			return err
		}
		// Invisible slot contribution: re-registers page titles when the host
		// language changed. The host sidebar slot is built before the settings
		// overlay, so a switch updates the navigation in the same frame.
		localeSync := contracts.SlotItem{
			Slot:  contracts.SlotSidebarFooter,
			ID:    "settings.locale-sync",
			Order: 1000,
			Build: func(*ui.Context) { syncer.sync() },
		}
		if err := own(ctx, func() (func(), error) {
			return runtime.RegisterSlotItem(localeSync)
		}); err != nil {
			syncer.close()
			return err
		}
		return nil
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeySettings)).
		AddRequires(module.Requires(contracts.KeyChat)).
		AddRequires(module.Requires(contracts.KeyTheme)).
		AddRequires(module.Requires(contracts.KeyLocale)).
		WithName("ZeoKeel settings pages")
}

// diagnosticsRows collects the read-only diagnostics values. Optional services
// degrade to explicit "未启用" values instead of hiding the row.
func diagnosticsRows(info contracts.AppInfo, dataDir string, toolkit *rigtools.Registry,
	chat contracts.ChatService, store contracts.SettingsService, locale contracts.LocaleService) ([]diagnosticRow, []diagnosticRow) {
	name := info.Name
	if name == "" {
		name = "ZeoKeel"
	}
	version := info.Version
	if version == "" {
		version = "dev"
	}
	rows := []diagnosticRow{
		{localized(locale, "应用", "Application"), name + " v" + version},
		{"Commit", diagnosticValue(locale, info.Commit)},
		{localized(locale, "构建时间", "Build time"), diagnosticValue(locale, info.BuildTime)},
		{localized(locale, "Go 版本", "Go version"), firstNonEmpty(info.GoVersion, runtime.Version())},
		{localized(locale, "平台", "Platform"), runtime.GOOS + "/" + runtime.GOARCH},
		{localized(locale, "数据目录", "Data directory"), firstNonEmpty(dataDir, localized(locale, "未设置", "not set"))},
		{localized(locale, "会话数据库", "Session database"), localized(locale, "zeokeel.db（数据目录）", "zeokeel.db (data directory)")},
	}
	if dataDir != "" {
		rows[6].Value = filepath.Join(dataDir, "zeokeel.db")
	}
	toolCount := 0
	if toolkit != nil {
		toolCount = len(toolkit.Specs())
	}
	rows = append(rows,
		diagnosticRow{localized(locale, "工具数", "Tools"), fmt.Sprintf("%d", toolCount)},
		diagnosticRow{localized(locale, "会话数", "Sessions"), fmt.Sprintf("%d", len(chat.Sessions()))},
	)
	// Every setting value goes through the shared credential mask, so the page,
	// its copy action and the exported report can never disagree about which
	// values are secrets. The keys come back as their own two-column list: a
	// single joined line was truncated and could not be copied per value (B13).
	keys := make([]diagnosticRow, 0)
	if all := store.All(); len(all) > 0 {
		for key, value := range all {
			keys = append(keys, diagnosticRow{Label: key, Value: maskedSettingValue(locale, key, value)})
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i].Label < keys[j].Label })
	}
	return rows, keys
}

// diagnosticValue renders one build value: empty and the assembly's raw
// "unknown" build metadata read as the friendly placeholder (the About page's
// rule, kept local because UI modules do not import each other).
func diagnosticValue(locale contracts.LocaleService, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.EqualFold(trimmed, "unknown") {
		return localized(locale, "未知", "unknown")
	}
	return trimmed
}

// runtimeSummary is the one-line effective runtime state shown under the
// collapsed 高级选项 disclosure.
func runtimeSummary(state *moduleState, store contracts.SettingsService, locale contracts.LocaleService) string {
	iterations := strings.TrimSpace(state.maxIterations)
	if iterations == "" {
		if state.moduleMaxIterations > 0 {
			iterations = fmt.Sprintf(localized(locale, "模块默认 %d", "module default %d"), state.moduleMaxIterations)
		} else {
			iterations = localized(locale, "不限制", "unlimited")
		}
	}
	budget := strings.TrimSpace(state.contextBudget)
	if budget == "" {
		if state.moduleContextBudget != 0 {
			budget = fmt.Sprintf(localized(locale, "模块默认 %d", "module default %d"), state.moduleContextBudget)
		} else {
			budget = localized(locale, "默认", "default")
		}
	}
	stream := localized(locale, "开", "on")
	if !requireFinishReasonEnabled(store) {
		stream = localized(locale, "关", "off")
	}
	return localized(locale, "最大迭代 ", "Max iterations ") + iterations +
		localized(locale, " · 上下文预算 ", " · context budget ") + budget +
		localized(locale, " · 流完整性校验 ", " · stream completeness ") + stream
}

// saveModelPage is the model page's pinned footer action: it validates the
// runtime-limit drafts, persists the drafts and reports the result in the
// page's status line. Invalid numbers keep their drafts and inline errors and
// persist nothing.
func saveModelPage(c *ui.Context, state *moduleState, store contracts.SettingsService,
	locale contracts.LocaleService) {
	if !validateRuntimeDrafts(state, locale) {
		state.status = localized(locale, "运行参数有误，未保存",
			"Runtime parameters are invalid; nothing was saved")
		state.statusErr = true
		c.Invalidate()
		return
	}
	if err := save(state, store); err != nil {
		state.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		state.statusErr = true
	} else {
		state.status = localized(locale, "已保存", "Saved")
		state.statusErr = false
	}
	c.Invalidate()
}

// applyAppearancePage is the appearance page's pinned footer action: it applies
// the explicit-save values and reports the result in the page status line.
func applyAppearancePage(c *ui.Context, state *appearanceState, store contracts.SettingsService,
	locale contracts.LocaleService) {
	if err := applyAppearance(state, store, locale); err != nil {
		state.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		state.statusErr = true
	} else {
		state.status = localized(locale, "已保存", "Saved")
		state.statusErr = false
	}
	c.Invalidate()
}

// effectiveChatConfig resolves the configuration that actually applies. The
// agent backend resolves the settings store over the assembly config, so the
// store is read first and the backend value is the fallback.
func effectiveChatConfig(chat contracts.ChatService, store contracts.SettingsService) contracts.ChatConfig {
	var effective contracts.ChatConfig
	if chat != nil {
		effective = chat.Config()
	}
	if store == nil {
		return effective
	}
	if value := strings.TrimSpace(store.Get(contracts.SettingsChatModel, "")); value != "" {
		effective.Model = value
	}
	if value := strings.TrimRight(strings.TrimSpace(store.Get(contracts.SettingsChatBaseURL, "")), "/"); value != "" {
		effective.BaseURL = value
	}
	if value := strings.TrimSpace(store.Get(contracts.SettingsChatSystemPrompt, "")); value != "" {
		effective.SystemPrompt = value
	}
	if value := strings.TrimSpace(store.Get(contracts.SettingsChatAPIKey, "")); value != "" {
		effective.APIKey = value
	}
	return effective
}

// newState seeds the explicit-save drafts from the effective chat config.
func newState(chat contracts.ChatService, store contracts.SettingsService) *moduleState {
	state := &moduleState{}
	state.seed(effectiveChatConfig(chat, store))
	state.seedRuntime(chat, store)
	return state
}

// seed replaces the prompt draft with the persisted configuration and records
// it as the snapshot the build compares against. The model list has no draft:
// the page renders the persisted row and its edits apply immediately.
func (st *moduleState) seed(cfg contracts.ChatConfig) {
	st.systemPrompt = cfg.SystemPrompt
	st.seenSystemPrompt = cfg.SystemPrompt
}

// runtimeRows reads the explicit runtime-limit settings rows: an empty value
// means the row is absent, so the module config applies.
func runtimeRows(store contracts.SettingsService) (iterations, contextBudget string) {
	if store == nil {
		return "", ""
	}
	return strings.TrimSpace(store.Get(contracts.SettingsChatMaxIterations, "")),
		strings.TrimSpace(store.Get(contracts.SettingsChatContextBudget, ""))
}

// defaultRuntimeNumber resolves one numeric input's starting number: the
// explicit settings row when present, else the module default (0 when neither
// exists). It is only a starting point; the draft stays empty while the default
// switch is on.
func defaultRuntimeNumber(row string, moduleDefault int) float64 {
	if value, err := strconv.Atoi(strings.TrimSpace(row)); err == nil {
		return float64(value)
	}
	if moduleDefault > 0 {
		return float64(moduleDefault)
	}
	return 0
}

// seedRuntime replaces the runtime-limit drafts and their persisted snapshot.
// Only the explicit settings rows seed the drafts: the module-config limits are
// the "use default" side, so they seed the numeric mirrors (the value the input
// starts from when the switch is turned off) and the explanatory copy, but never
// the drafts. Seeding the drafts from the module config made the default switch
// read as off and a plain Save pin the module value into a user settings row
// (ST-07).
func (st *moduleState) seedRuntime(chat contracts.ChatService, store contracts.SettingsService) {
	var cfg contracts.ChatConfig
	if chat != nil {
		cfg = chat.Config()
	}
	st.moduleMaxIterations = cfg.MaxIterations
	st.moduleContextBudget = cfg.ContextBudget
	st.maxIterations, st.contextBudget = runtimeRows(store)
	st.seenMaxIterations, st.seenContextBudget = st.maxIterations, st.contextBudget
	st.iterationsErr, st.contextBudgetErr = "", ""
	st.iterationsDefault = strings.TrimSpace(st.maxIterations) == ""
	st.budgetDefault = strings.TrimSpace(st.contextBudget) == ""
	st.maxIterationsNum = defaultRuntimeNumber(st.maxIterations, st.moduleMaxIterations)
	st.contextBudgetNum = defaultRuntimeNumber(st.contextBudget, st.moduleContextBudget)
}

// requireFinishReasonEnabled reports the stream-completeness switch state:
// explicit "false"/"0"/"off"/"no" disables the check, anything else (including
// the unset row, which follows the assembly default of on) keeps it enabled.
func requireFinishReasonEnabled(store contracts.SettingsService) bool {
	if store == nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(store.Get(contracts.SettingsChatRequireFinishReason, ""))) {
	case "false", "0", "off", "no":
		return false
	}
	return true
}

// validateRuntimeDrafts parses the numeric drafts and fills their inline
// errors. It reports whether both drafts are valid and can be saved.
func validateRuntimeDrafts(st *moduleState, locale contracts.LocaleService) bool {
	st.iterationsErr, st.contextBudgetErr = "", ""
	if raw := strings.TrimSpace(st.maxIterations); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			st.iterationsErr = localized(locale,
				"请输入大于 0 的整数；留空使用模块默认值。",
				"Enter an integer greater than 0; leave blank to use the module default.")
		}
	}
	if raw := strings.TrimSpace(st.contextBudget); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			st.contextBudgetErr = localized(locale,
				"请输入大于或等于 0 的整数；留空使用模块默认值。",
				"Enter an integer greater than or equal to 0; leave blank to use the module default.")
		}
	}
	return st.iterationsErr == "" && st.contextBudgetErr == ""
}

// syncFromConfig refreshes the explicit-save drafts when the persisted
// configuration changed outside this page (another settings page, a second
// window, the runtime-limit rows). It never overwrites a draft whose input
// holds the focus, so the caret and the text being typed survive; the next
// frame after the focus leaves syncs. The model list is not part of this state:
// it always renders the persisted row.
func (st *moduleState) syncFromConfig(c *ui.Context, chat contracts.ChatService, store contracts.SettingsService) {
	cfg := effectiveChatConfig(chat, store)
	iterations, contextBudget := runtimeRows(store)
	moduleCfg := contracts.ChatConfig{}
	if chat != nil {
		moduleCfg = chat.Config()
	}
	if cfg.SystemPrompt == st.seenSystemPrompt &&
		iterations == st.seenMaxIterations && contextBudget == st.seenContextBudget &&
		moduleCfg.MaxIterations == st.moduleMaxIterations &&
		moduleCfg.ContextBudget == st.moduleContextBudget {
		return
	}
	if st.promptInput.Focused(c) || st.iterationsInput.Focused(c) || st.budgetInput.Focused(c) {
		return
	}
	st.seed(cfg)
	st.seedRuntime(chat, store)
}

// discardDrafts throws the page's edits away: the drafts are re-seeded from the
// persisted configuration and the transient status state is cleared, so a
// reopened settings page shows the saved values and no "unsaved" hint.
func (st *moduleState) discardDrafts(chat contracts.ChatService, store contracts.SettingsService) {
	st.seed(effectiveChatConfig(chat, store))
	st.seedRuntime(chat, store)
	st.status = ""
	st.statusErr = false
}

// dirty reports whether the explicit-save drafts differ from the persisted
// configuration. The model list and the stream switch apply immediately and
// therefore never dirty the page.
func (st *moduleState) dirty() bool {
	return st.systemPrompt != st.seenSystemPrompt ||
		st.maxIterations != st.seenMaxIterations ||
		st.contextBudget != st.seenContextBudget
}

// statusRow renders the page's last save result. The unsaved state has one
// owner only — the host's title badge (and the close guard) — so the body stays
// silent while the drafts are dirty; any edit clears the previous result, so a
// screen can never show "未保存" next to a stale "已保存" (R8).
func statusRow(c *ui.Context, t *design.Tokens, st *moduleState, locale contracts.LocaleService) {
	if st.dirty() {
		if !st.statusErr {
			// A pending edit owns the "unsaved" state and clears the last success
			// result; a failure stays visible until the next edit.
			st.status = ""
		}
		return
	}
	if st.status == "" {
		return
	}
	ui.Row(c).FillWidth().Children(func() {
		statusBadge(c, t, st.status, st.statusErr)
	})
}

// clearSavedStatus drops the previous save result as soon as the page's drafts
// change, so a stale "已保存" never sits under an edited field.
func clearSavedStatus(st *moduleState) {
	st.status = ""
	st.statusErr = false
}

// clearAppearanceStatus is clearSavedStatus for the appearance page.
func clearAppearanceStatus(st *appearanceState) {
	st.status = ""
	st.statusErr = false
}

// statusBadge renders one page result in the product's one status anatomy: the
// failure tint for an error, success for a saved result.
func statusBadge(c *ui.Context, t *design.Tokens, text string, failed bool) {
	kind := uikit.StatusSuccess
	if failed {
		kind = uikit.StatusError
	}
	uikit.StatusBadge(c, t, kind, text)
}

// statusBadgesRow renders one page status line, hiding it while it is empty.
func statusBadgesRow(c *ui.Context, t *design.Tokens, status string, failed bool) {
	if status == "" {
		return
	}
	ui.Row(c).FillWidth().Children(func() {
		statusBadge(c, t, status, failed)
	})
}

// switchField renders one boolean setting as a native form Field: the visible
// label (which toggles the switch when clicked), the native ui.Switch with the
// same accessible name, and the description below the control. Its caller puts
// Fields in one ui.Form so their labels share the macOS-style label column, and
// handles the Changed result right where the setting lives.
// switchInline is a compact switch with its visible label for a row inside
// another field (for example "use the default" above a number input). The
// switch itself carries the Label, so assistive technology and frame tests
// target it as the control.
func switchInline(c *ui.Context, label string, value *bool) bool {
	sw := ui.Switch(c, value).Label(label)
	changed := sw.Changed()
	design.Apply(ui.Text(c, label), design.Of(c).Font(design.TypeXS)).TextColor(design.Of(c).LabelSecondary)
	return changed
}

func switchField(c *ui.Context, label, description string, value *bool) bool {
	var changed bool
	ui.Field(c, label, func() {
		// The field names the switch for assistive technology; do not set a
		// second Label on it, which would make the field's visible label
		// untargetable.
		sw := ui.Switch(c, value)
		changed = sw.Changed()
	}).Description(description)
	return changed
}

// inputLabel appends the localized "input" suffix used by native input labels.
func inputLabel(locale contracts.LocaleService, label string) string {
	return label + localized(locale, " 输入", " input")
}

// buildPage builds the model settings page. The model list is the page's model
// control surface (add/edit/remove and the default model apply immediately);
// the system prompt and the runtime limits are the only explicit-save drafts,
// persisted by the page footer's Save.
func buildPage(c *ui.Context, runtime contracts.UIRuntime, state *moduleState, catalog *catalogState,
	chat contracts.ChatService, labels buildLabels, store contracts.SettingsService,
	locale contracts.LocaleService) {
	t := design.Of(c)
	// A write from another settings surface (another window, the runtime rows)
	// refreshes the drafts before they render, so this page can never save a
	// stale configuration over it.
	state.syncFromConfig(c, chat, store)
	effective := effectiveChatConfig(chat, store)
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		uikit.PageHeader(c, labels.title, localized(locale,
			"模型列表的改动即时生效；下方「提示词与运行参数」由页脚保存。",
			"Model-list changes apply immediately; the section below is stored by the footer's Save."))

		// One page owns the model configuration: every row carries its own
		// provider connection and one row is the default model.
		buildModelCatalogSection(c, runtime, store, catalog, locale, effective.BaseURL, effective.APIKey, effective.Model)

		// Prompt and runtime knobs are the page's explicit-save section: the
		// host footer's Save is the page's one primary, and this section says so
		// in its title; the model list above applies immediately (R12).
		uikit.Section(c, localized(locale, "提示词与运行参数（页脚保存）",
			"Prompt & runtime (saved by the footer)"), func(*ui.Context) {
			ui.Field(c, labels.systemPromptLabel, func() {
				prompt := ui.TextArea(c, &state.systemPrompt).Label(inputLabel(locale, labels.systemPromptLabel)).
					Bind(&state.promptInput).FillWidth().Height(56)
				if prompt.Changed() {
					clearSavedStatus(state)
				}
			}).Description(localized(locale,
				"追加到每次对话的系统提示前；留空使用服务商默认。",
				"Prepended to every conversation's system prompt; empty uses the provider default."))
			// Runtime limits are low-frequency knobs: a native disclosure keeps the
			// section short. The numeric drafts still save with the page footer;
			// the stream switch applies immediately.
			ui.Divider(c)
			ui.Collapsible(c, localized(locale, "高级选项", "Advanced options"), &state.runtimeOpen, func() {
				ui.Row(c).FillWidth().Gap(design.Space3).AlignItems(ui.Start).Children(func() {
					ui.Column(c).FillWidth().Grow(1).Children(func() {
						ui.Field(c, localized(locale, "最大迭代次数", "Max iterations"), func() {
							if switchField(c,
								localized(locale, "使用默认上限（不限制）", "Use default limit (unlimited)"),
								localized(locale,
									"开启时删除该设置行，由模块配置决定；关闭后设置具体轮数上限。",
									"When on, the settings row is cleared and the module config decides; when off, set a concrete round limit."),
								&state.iterationsDefault) {
								clearSavedStatus(state)
								if state.iterationsDefault {
									state.maxIterations = ""
								} else {
									if state.maxIterationsNum < 1 {
										if state.moduleMaxIterations > 0 {
											state.maxIterationsNum = float64(state.moduleMaxIterations)
										} else {
											state.maxIterationsNum = 20
										}
									}
									state.maxIterations = strconv.Itoa(int(state.maxIterationsNum))
								}
							}
							if state.iterationsDefault && state.moduleMaxIterations > 0 {
								design.Apply(ui.Text(c, fmt.Sprintf(localized(locale,
									"模块默认：%d", "Module default: %d"), state.moduleMaxIterations)),
									t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
							}
							if !state.iterationsDefault {
								input := ui.NumberInput(c.Key("settings.runtime.maxIterations"),
									&state.maxIterationsNum, 1, 100000, 1).
									Label(inputLabel(locale, localized(locale, "最大迭代次数", "Max iterations"))).
									Bind(&state.iterationsInput)
								if input.Changed() {
									state.maxIterations = strconv.Itoa(int(state.maxIterationsNum))
									clearSavedStatus(state)
								}
							}
						}).Description(localized(locale,
							"模型/工具轮数上限；默认不限制，由模块配置决定。",
							"Model/tool rounds per run; unlimited by default, as the module config decides."))
						if state.iterationsErr != "" {
							uikit.ErrorLine(c, state.iterationsErr)
						}
					})
					ui.Column(c).FillWidth().Grow(1).Children(func() {
						ui.Field(c, localized(locale, "上下文预算", "Context budget"), func() {
							if switchField(c,
								localized(locale, "使用默认预算", "Use default budget"),
								localized(locale,
									"开启时删除该设置行，由模块配置决定；关闭后设置具体的 prompt token 预算，0 = 不自动压缩。",
									"When on, the settings row is cleared and the module config decides; when off, set a prompt-token budget, 0 = no automatic compaction."),
								&state.budgetDefault) {
								clearSavedStatus(state)
								if state.budgetDefault {
									state.contextBudget = ""
								} else {
									state.contextBudget = strconv.Itoa(int(state.contextBudgetNum))
								}
							}
							if state.budgetDefault && state.moduleContextBudget != 0 {
								design.Apply(ui.Text(c, fmt.Sprintf(localized(locale,
									"模块默认：%d", "Module default: %d"), state.moduleContextBudget)),
									t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
							}
							if !state.budgetDefault {
								input := ui.NumberInput(c.Key("settings.runtime.contextBudget"),
									&state.contextBudgetNum, 0, 10000000, 1000).
									Label(inputLabel(locale, localized(locale, "上下文预算", "Context budget"))).
									Bind(&state.budgetInput)
								if input.Changed() {
									state.contextBudget = strconv.Itoa(int(state.contextBudgetNum))
									clearSavedStatus(state)
								}
							}
						}).Description(localized(locale,
							"自动压缩的 prompt token 预算，0 = 不自动压缩；默认由模块配置决定。",
							"Prompt-token budget for automatic compaction; 0 = no automatic compaction. Default follows the module config."))
						if state.contextBudgetErr != "" {
							uikit.ErrorLine(c, state.contextBudgetErr)
						}
					})
				})
				ui.Form(c, func() {
					requireComplete := requireFinishReasonEnabled(store)
					if switchField(c,
						localized(locale, "流完整性校验", "Stream completeness"),
						localized(locale,
							"缺少 finish_reason 的流按截断失败处理；极简兼容服务器可关闭。",
							"A stream without a finish_reason is treated as truncated; disable it for minimal compatible servers."),
						&requireComplete) {
						clearSavedStatus(state)
						if err := store.Set(contracts.SettingsChatRequireFinishReason,
							strconv.FormatBool(requireComplete)); err != nil {
							state.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
							state.statusErr = true
						} else {
							state.status = localized(locale, "已保存", "Saved")
							state.statusErr = false
						}
						c.Invalidate()
					}
				})
			})
			if !state.runtimeOpen {
				design.Apply(ui.Text(c, runtimeSummary(state, store, locale)),
					t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
			}
		})

		// H1: the page's single primary action (save) lives in the host's
		// fixed footer; the body keeps the unsaved/saved status line.
		statusRow(c, t, state, locale)

		// Read-only effective configuration: the default model is the row the
		// composer and the request path use (the list's default), never a draft.
		models, _ := catalogModels(store)
		defaultModel := contracts.DefaultModelID(models, store)
		if defaultModel == "" {
			defaultModel = strings.TrimSpace(effective.Model)
		}
		active := strings.TrimSpace(chat.ActiveSession())
		if active == "" {
			active = localized(locale, "无活动会话", "No active session")
		}
		uikit.Section(c, localized(locale, "当前", "Current"), func(*ui.Context) {
			uikit.KeyValueRows(c, []uikit.KeyValue{
				{Key: localized(locale, "当前会话", "Current session"), Value: active},
				{Key: localized(locale, "默认模型", "Default model"),
					Value: firstNonEmpty(defaultModel,
						localized(locale, "未配置", "Not configured"))},
			})
		})
	})
}

// --- appearance page -------------------------------------------------------

// buildAppearancePage builds the theme/language/desktop page. Theme, language
// and the desktop switches apply at once (they are switches, the mygo control
// for a setting that takes effect immediately); the global hotkey is the page's
// only explicit-save field and says so next to its input.
func buildAppearancePage(c *ui.Context, state *appearanceState, store contracts.SettingsService,
	theme contracts.ThemeService, locale contracts.LocaleService, labels buildAppearanceLabels) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		uikit.PageHeader(c, labels.title, localized(locale,
			"主题、语言与桌面集成即时生效；全局热键点页脚「应用」并重启后生效。",
			"Theme, language and desktop integration apply immediately; the global hotkey applies after Apply and a restart."))

		uikit.Section(c, localized(locale, "主题", "Theme"), func(*ui.Context) {
			if buildSegmentedControl(c, t, &state.themeMode, labels.themeLabel,
				localized(locale, "浅色", "Light"),
				localized(locale, "深色", "Dark"),
				localized(locale, "跟随系统", "System"),
			) {
				applyThemeModeSelection(state, store, theme, locale)
				c.Invalidate()
			}
			design.Apply(ui.Text(c, localized(locale,
				"修改立即生效；跟随系统由桌面外观决定（需要桌面支持）。",
				"Changes apply immediately; System follows the desktop appearance (desktop support required).")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
		})

		uikit.Section(c, localized(locale, "语言", "Language"), func(*ui.Context) {
			choice := languageChoice(state.language)
			selectControl := ui.Select(c, &choice, languageOptions).Label(labels.languageLabel)
			if selectControl.Changed() {
				state.language = languageCode(choice)
				locale.SetLanguage(state.language)
				_ = store.Set(contracts.SettingsUILanguage, state.language)
				state.status = localized(locale, "语言已切换", "Language switched")
				state.statusErr = false
			}
			design.Apply(ui.Text(c, localized(locale,
				"界面文案与日期格式随语言切换，立即生效。",
				"Interface copy and date formats follow the language and apply immediately.")),
				t.TextMuted(design.TypeXXS))
		})

		uikit.Section(c, localized(locale, "桌面集成", "Desktop integration"), func(*ui.Context) {
			ui.Form(c, func() {
				if switchField(c,
					localized(locale, "开机自启动", "Launch at login"),
					localized(locale, "登录时自动启动 ZeoKeel（Linux XDG autostart）。",
						"Start ZeoKeel automatically when you log in (Linux XDG autostart)."),
					&state.autostart) {
					applyDesktopSetting(state, store, contracts.SettingAutostart,
						fmt.Sprintf("%t", state.autostart), locale)
				}
				if switchField(c,
					localized(locale, "单实例运行", "Single instance"),
					localized(locale, "关闭后允许多开；修改需要重启应用生效。",
						"When off, multiple instances may run; takes effect after restart."),
					&state.singleInstance) {
					applyDesktopSetting(state, store, contracts.SettingSingleInstance,
						fmt.Sprintf("%t", state.singleInstance), locale)
				}
				if switchField(c,
					localized(locale, "关闭时最小化到托盘", "Close to system tray"),
					localized(locale, "需要系统托盘支持；修改后重启应用生效。",
						"Requires system tray support; takes effect after restart."),
					&state.closeToTray) {
					applyDesktopSetting(state, store, contracts.SettingCloseToTray,
						fmt.Sprintf("%t", state.closeToTray), locale)
				}
			})
		})

		buildGlobalHotkeySection(c, t, state, store, locale)

		// H1: the apply action lives in the host's fixed footer; the body keeps
		// only the last save result (the title badge owns "unsaved", R8).
		if state.dirty() && !state.statusErr {
			clearAppearanceStatus(state)
		}
		statusBadgesRow(c, t, state.status, state.statusErr)
	})
}

// discardHotkey re-reads the persisted accelerator and clears the inline error
// and status, so the page reopens clean after "Discard".
func (st *appearanceState) discardHotkey(store contracts.SettingsService) {
	saved := ""
	if store != nil {
		saved = strings.TrimSpace(store.Get(contracts.SettingGlobalHotkey, ""))
	}
	st.globalHotkey = saved
	st.savedHotkey = saved
	st.hotkeyText = formatHotkeyDisplay(saved)
	st.hotkeyError = ""
	st.capturing = false
	st.captureText = ""
	st.captureFocus = false
	st.status = ""
	st.statusErr = false
}

// dirty reports whether the appearance page holds an unsaved edit: theme,
// language and the desktop switches apply at once, so only the global-hotkey
// draft can be pending (the host shows "unsaved" and guards closing for it).
func (st *appearanceState) dirty() bool {
	return strings.TrimSpace(st.globalHotkey) != strings.TrimSpace(st.savedHotkey)
}

// applyAppearance persists the page's explicit-save fields. Theme, language and
// the desktop switches already apply at once; repeating their writes here is
// idempotent and keeps the page's single "Apply" action a complete snapshot
// (it also normalizes legacy rows such as the old ui.theme.dark flag).
func applyAppearance(state *appearanceState, store contracts.SettingsService, locale contracts.LocaleService) error {
	hotkey := strings.TrimSpace(state.globalHotkey)
	if state.hotkeyError != "" {
		return errors.New(state.hotkeyError)
	}
	if !validGlobalHotkeyValue(hotkey) {
		return errors.New(globalHotkeyInvalidMessage(locale))
	}
	legacyDark := themeModeName(state.themeMode) == "dark"
	if err := store.Set(contracts.SettingsUIThemeDark, fmt.Sprintf("%t", legacyDark)); err != nil {
		return err
	}
	if err := store.Set(contracts.SettingThemeMode, themeModeName(state.themeMode)); err != nil {
		return err
	}
	if err := store.Set(contracts.SettingsUILanguage, state.language); err != nil {
		return err
	}
	if err := store.Set(contracts.SettingAutostart, fmt.Sprintf("%t", state.autostart)); err != nil {
		return err
	}
	if err := store.Set(contracts.SettingSingleInstance, fmt.Sprintf("%t", state.singleInstance)); err != nil {
		return err
	}
	if err := store.Set(contracts.SettingCloseToTray, fmt.Sprintf("%t", state.closeToTray)); err != nil {
		return err
	}
	if err := store.Set(contracts.SettingGlobalHotkey, hotkey); err != nil {
		return err
	}
	state.savedHotkey = hotkey
	state.globalHotkey = hotkey
	state.hotkeyText = formatHotkeyDisplay(hotkey)
	state.hotkeyError = ""
	return nil
}

// applyDesktopSetting persists one desktop-integration switch immediately (the
// page's switches also apply immediately) and reports success or the readable
// store error in the page's status badge. A failed write re-reads the stored
// value so the switch never lies about the persisted state.
func applyDesktopSetting(state *appearanceState, store contracts.SettingsService, key, value string, locale contracts.LocaleService) {
	if err := store.Set(key, value); err != nil {
		state.autostart = store.Get(contracts.SettingAutostart, "false") == "true"
		state.singleInstance = singleInstanceSwitchEnabled(store.Get(contracts.SettingSingleInstance, "true"))
		state.closeToTray = closeToTraySettingEnabled(store.Get(contracts.SettingCloseToTray, "false"))
		state.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		state.statusErr = true
		return
	}
	state.status = localized(locale, "桌面集成设置已更新", "Desktop integration updated")
	state.statusErr = false
}

// save writes the page's explicit-save drafts to the settings store: the system
// prompt and the runtime limits. The model list (including the default model
// mirror) is persisted by the list's own actions instead, because it applies
// immediately. The runtime "use default" switches own their rows — an on switch
// clears the row so the module config applies, an off switch writes the concrete
// draft (an explicit "0" budget stays meaningful). The effective-config snapshot
// is recorded so the page renders as clean right after a successful save.
func save(state *moduleState, store contracts.SettingsService) error {
	if err := store.Set(contracts.SettingsChatSystemPrompt, state.systemPrompt); err != nil {
		return fmt.Errorf("system prompt: %w", err)
	}
	if state.iterationsDefault {
		state.maxIterations = ""
	}
	if state.budgetDefault {
		state.contextBudget = ""
	}
	state.maxIterations = strings.TrimSpace(state.maxIterations)
	state.contextBudget = strings.TrimSpace(state.contextBudget)
	if err := store.Set(contracts.SettingsChatMaxIterations, state.maxIterations); err != nil {
		return fmt.Errorf("max iterations: %w", err)
	}
	if err := store.Set(contracts.SettingsChatContextBudget, state.contextBudget); err != nil {
		return fmt.Errorf("context budget: %w", err)
	}
	state.seenSystemPrompt = state.systemPrompt
	state.seenMaxIterations = state.maxIterations
	state.seenContextBudget = state.contextBudget
	state.iterationsErr, state.contextBudgetErr = "", ""
	return nil
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

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
