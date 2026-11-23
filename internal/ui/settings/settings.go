// Package settings contributes the whole settings surface: model, behavior &
// limits, appearance (with shortcuts), notifications, network & search, LSP,
// memory, archive, data & privacy (with DSH import) and the About hub (build
// info, update, diagnostics). Capability surfaces (skills and MCP connectors)
// live on the Capabilities screen, not here.
//
// It is a UI module: a kernel Descriptor that requires the host UI runtime, the
// settings store, the chat read model, the theme and the locale, and registers
// the uinative.SettingsPageSpec values on activation. The host owns the settings
// shell (native Sidebar + Modal); this module only composes native mygo controls
// — ui.TextInput, ui.TextArea, ui.Toggle, ui.Select, ui.Button,
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
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/contracts/uinative"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/lib/textutil"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

// Settings page constants. ID and Section are stable contracts the host renders
// in its grouped settings sidebar; the host sorts the nav by Section rank
// (常用 → 界面 → 连接 → 数据 → 关于) and by Order inside a section. Order only
// needs to be unique inside one section.
const (
	PageID      = "model"
	PageSection = "常用"
	PageOrder   = 10

	// AppearancePageID is the appearance page identity (theme, language,
	// shortcuts).
	AppearancePageID = "appearance"
	// AppearancePageOrder places the page first in the 界面 group.
	AppearancePageOrder = 10
)

// Page orders inside a group. The page registry below is what the host groups
// by; the orders are local to their section.
const (
	// Order inside 集成: LSP joins Search/Network (integration surfaces) after
	// those entries.
	lspOrderInIntegrations = 50

	// Order inside 数据.
	notificationsOrderInData = 10
	archiveOrderInData       = 30
	dataPrivacyOrderInData   = 40
)

// shortcutRow is one line of the shortcut reference.
type shortcutRow struct {
	Action string
	Keys   string
}

// shortcutReferenceRows builds the read-only keyboard reference. H5: the two
// configurable shortcuts are edited in the section above and are not repeated
// here; this table lists the fixed window shortcuts only.
func shortcutReferenceRows(locale contracts.LocaleService, runtime uinative.UIRuntime) []shortcutRow {
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
	runtime uinative.UIRuntime, locale contracts.LocaleService, st *shortcutsState, showHeader bool) {
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		if showHeader {
			uikit.PageHeader(c, title, "")
		}
		// The section's one utility action sits on the section's title row
		// (uikit.SectionAction, B-spec §3.4): restoring the defaults is
		// recoverable, so it is not a danger action (DESIGN.md §5.2); the
		// confirmation still guards the overwrite of the user's custom chords.
		uikit.SectionAction(c, localized(locale, "桌面快捷键", "Desktop shortcuts"),
			func(c *ui.Context) {
				if design.Apply(ui.Button(c, localized(locale, "恢复默认", "Restore defaults")),
					t.SecondaryButton()).Tooltip(localized(locale,
					"恢复两项快捷键的默认值", "Restore both default shortcuts")).Clicked() {
					st.confirmRestore = true
					c.Invalidate()
				}
			},
			func(c *ui.Context) {
				toggleDisplay := shortcutDisplay(toggleSidebarChoices(),
					store.Get(contracts.SettingsUIShortcutToggleSidebar, contracts.DefaultShortcutToggleSidebar))
				paletteDisplay := shortcutDisplay(commandPaletteChoices(),
					store.Get(contracts.SettingsUIShortcutCommandPalette, contracts.DefaultShortcutCommandPalette))
				// One table shape: a hairline between the two rows, declared once
				// (A1 F-05), and the two pickers on the 320 DIP control column.
				ui.Column(c).FillWidth().Dividers(1, t.BorderL2).Children(func() {
					ui.Row(c).FillWidth().PaddingY(design.Space2).
						AlignItems(ui.Center).Gap(design.Space3).Children(func() {
						design.Apply(ui.Text(c, localized(locale, "折叠 / 展开侧边栏", "Collapse / expand sidebar")),
							t.Text(design.TypeBody)).Grow(1)
						choice := toggleDisplay
						selectControl := ui.Select(c, &choice, shortcutChoiceLabels(toggleSidebarChoices())).
							Label(localized(locale, "侧边栏快捷键", "Sidebar shortcut")).
							Width(controlWidth).Shrink(0)
						if selectControl.Changed() {
							_ = store.Set(contracts.SettingsUIShortcutToggleSidebar,
								shortcutChord(toggleSidebarChoices(), choice))
						}
					})
					ui.Row(c).FillWidth().PaddingY(design.Space2).
						AlignItems(ui.Center).Gap(design.Space3).Children(func() {
						design.Apply(ui.Text(c, localized(locale, "打开命令面板", "Open command palette")),
							t.Text(design.TypeBody)).Grow(1)
						choice := paletteDisplay
						selectControl := ui.Select(c, &choice, shortcutChoiceLabels(commandPaletteChoices())).
							Label(localized(locale, "命令面板快捷键", "Command palette shortcut")).
							Width(controlWidth).Shrink(0)
						if selectControl.Changed() {
							_ = store.Set(contracts.SettingsUIShortcutCommandPalette,
								shortcutChord(commandPaletteChoices(), choice))
						}
					})
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
	// process host: the XDG autostart entry and the close-to-tray behavior).
	// Single instance is always on, so it has no switch here.
	autostart   bool
	closeToTray bool
	// globalHotkey is the canonical accelerator draft. Theme, language and the
	// desktop switches apply at once; the hotkey is the page's one explicit-save
	// field (the host registers it at the next start; empty disables it).
	globalHotkey string
	// hotkeyText is the friendly display draft bound to the input
	// ("Cmd/Ctrl+Shift+Space"), parsed back into globalHotkey on edit.
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
	// conflictArmed gates the two-step save of a conflicting global hotkey
	// (UX-11): the first Save arms the confirmation, the second Save writes.
	// Editing the draft disarms it, so a new conflict needs a fresh confirmation.
	conflictArmed bool

	// transient detects the page being shown again after another page (or the
	// closed surface), so status copy and store-backed values can refresh.
	transient transientStatus
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
	// SaveAppearanceLabel labels the appearance save button (default "保存").
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

	// Runtime limit draft (persisted by the page's footer Save) plus its
	// inline validation message. An empty numeric draft means "use the module
	// config", so saving it clears the settings row; a value persists the row.
	// The iteration cap has no draft: it is assembly-level product definition,
	// not a user knob (the chat.maxIterations row was removed in v0.2.50).
	contextBudget     string
	contextBudgetErr  string
	seenContextBudget string
	budgetInput       ui.Handle
	// The module-config limit is a display-only default: it names the fallback
	// in the empty draft's placeholder/description and the page summary, but
	// never becomes a draft or settings row on its own.
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
// host's ordering); the displayed Title follows the locale. A module-declared
// section (one the host has no copy or rank for, as 数据与通知) is localized at
// registration by pageSyncer.register: the host renders the section value as the
// group title, and the syncer re-registers on a language switch, so the group
// title follows the locale like the page titles do.
type pageDefinition struct {
	ID            string
	Section       string
	Order         int
	zhTitle       string
	enTitle       string
	titleOverride string
	Build         uinative.BuildFunc
	// Footer renders the page's pinned action row in the host's fixed settings
	// footer (outside the scrolling body), and Dirty reports unsaved edits so
	// the host can show the unsaved state and guard closing the surface.
	Footer uinative.BuildFunc
	Dirty  func() bool
	// Discard throws the drafts away and re-reads the saved values; the host
	// calls it when the user closes the settings with "Discard".
	Discard func()
	// keywords/searchItems declare the page's search terms and searchable items.
	// They are functions because the labels and aliases follow the current
	// language, and the syncer re-registers the page on a language switch: the
	// values are resolved per registration, so the search never reads a stale
	// translation.
	keywords    func() []string
	searchItems func() []uinative.SettingsSearchItem
	// reveal is the page's search-locate hook: the host calls it with the hit
	// item's anchor key before the page builds (uinative.SettingsPageSpec
	// .Reveal). A page with searchable items binds its regions in Build and
	// sets up a *pageSearch here.
	reveal func(c *ui.Context, anchorKey string)
}

// pageSyncer keeps the registered settings pages in sync with the host locale.
// The host reads page titles from the runtime registry every frame, so a
// language switch re-registers the pages (from an invisible sidebar slot built
// before the settings overlay) to make the navigation labels change on the next
// frame without restarting the module.
type pageSyncer struct {
	runtime    uinative.UIRuntime
	locale     contracts.LocaleService
	pages      []pageDefinition
	registered map[string]func()
	lang       string
}

func newPageSyncer(runtime uinative.UIRuntime, locale contracts.LocaleService) *pageSyncer {
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
	var keywords []string
	if def.keywords != nil {
		keywords = def.keywords()
	}
	var searchItems []uinative.SettingsSearchItem
	if def.searchItems != nil {
		searchItems = def.searchItems()
	}
	unregister, err := s.runtime.RegisterSettingsPage(uinative.SettingsPageSpec{
		ID:          def.ID,
		Section:     s.sectionKey(def.Section),
		Title:       s.title(def),
		Order:       def.Order,
		Build:       def.Build,
		Footer:      def.Footer,
		Dirty:       def.Dirty,
		Discard:     def.Discard,
		Keywords:    keywords,
		SearchItems: searchItems,
		Reveal:      def.reveal,
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

// sectionKey resolves the section value a page registers under. The groups are
// stable keys the host localizes (常用 / 界面 / 连接 / 数据 / 关于); a module with
// its own group registers the display text and the host prints it verbatim.
func (s *pageSyncer) sectionKey(section string) string {
	return section
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
// shell's diagnostics view) with a native copy action. buildRows are the About
// page's build-facts table: shown once above (not repeated in the runtime
// sheet) but included in the copy, so a bug report still carries the version,
// commit and Go build.
func buildDiagnosticsPage(c *ui.Context, t *design.Tokens, state *diagnosticsState,
	buildRows, rows, keys []diagnosticRow, modules []contracts.ModuleInfo, locale contracts.LocaleService, showHeader bool) {
	ui.Column(c).FillWidth().Gap(design.Space4).Children(func() {
		if showHeader {
			uikit.PageHeader(c, localized(locale, "诊断", "Diagnostics"), "")
		}
		uikit.Section(c, localized(locale, "运行时信息", "Runtime information"), func(body *ui.Context) {
			// The property sheet is one shape everywhere (DESIGN.md §5.2
			// PropertySheet): a 160 DIP label column, the value left-aligned on
			// a 32 DIP row, a divider between rows.
			sheet := make([]uikit.PropertyRow, len(rows))
			for index, row := range rows {
				sheet[index] = uikit.PropertyRow{Label: row.Label, Value: row.Value}
			}
			uikit.PropertySheet(body, uikit.PropertySheetOptions{
				Rows: sheet, Key: "diagnostics.runtime", Locale: locale,
			})
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
					// One table shape: rows separated by hairline dividers declared
					// a tooltip is an overlay that takes the first Escape).
					ui.Column(c).FillWidth().Dividers(1, t.BorderL1).Children(func() {
						for _, pair := range keys {
							pair := pair
							ui.Row(c).FillWidth().Height(design.Space8).
								AlignItems(ui.Center).Gap(design.Space3).Children(func() {
								design.Apply(ui.Text(c, pair.Label), t.MonoText(design.TypeXS)).
									TextColor(t.LabelSecondary).Width(design.Space8 * 7).
									Shrink(0).MaxLines(1).Ellipsis("…")
								design.Apply(ui.Text(c, pair.Value), t.MonoText(design.TypeXS)).
									TextColor(t.LabelPrimary).Grow(1).MaxLines(1).
									Ellipsis("…").Selectable()
							})
						}
					})
				})
			}
		})
		buildLoadedModulesSection(c, t, state, locale, modules)

		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			if ui.Button(c, localized(locale, "复制诊断信息", "Copy diagnostics")).Clicked() {
				// The copy is the whole page: the build facts, the runtime rows
				// and the (already masked) setting keys, so a bug report carries
				// the same detail the page shows.
				var builder strings.Builder
				for _, row := range buildRows {
					fmt.Fprintf(&builder, "%s: %s\n", row.Label, row.Value)
				}
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
		runtime, err := ctx.Require(uinative.KeyUIRuntime)
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
			language:     locale.Language(),
			autostart:    store.Get(contracts.SettingAutostart, "false") == "true",
			closeToTray:  closeToTraySettingEnabled(store.Get(contracts.SettingCloseToTray, "false")),
			globalHotkey: store.Get(contracts.SettingGlobalHotkey, ""),
			hotkeyText:   formatHotkeyDisplay(store.Get(contracts.SettingGlobalHotkey, "")),
			savedHotkey:  store.Get(contracts.SettingGlobalHotkey, ""),
		}
		shortcuts := &shortcutsState{}
		archive := &archiveState{sel: -1}
		update := newUpdateState()
		importState := newImportState()
		search := newSearchState()
		about := &aboutState{}
		catalog := &catalogState{sel: -1}
		lsp := &lspState{sel: -1}
		notifications := &notificationsState{}
		behavior := &behaviorState{}
		// Optional workspace backend: the Behavior page shows the current work
		// folder with its native-picker action when the product provides one.
		workFolder := newWorkFolderState(ctx)
		network := newNetworkState()
		capabilities := newCapabilitiesState()
		dataPrivacy := newDataPrivacyState()
		diagnostics := &diagnosticsState{}

		// Optional host app identity for the About hub.
		info, _ := ctx.Get(contracts.KeyAppInfo)
		// Optional OS sandbox service: the Behavior page shows its switch and
		// live status; a stripped assembly without the module still renders.
		sandboxService, _ := ctx.Get(contracts.KeySandbox)

		modelLabels := func() buildLabels {
			return buildLabels{
				title:             textutil.FirstNonEmpty(cfg.Title, localized(locale, "模型", "Model")),
				systemPromptLabel: textutil.FirstNonEmpty(cfg.SystemPromptLabel, localized(locale, "系统提示词", "System prompt")),
				saveLabel: textutil.FirstNonEmpty(cfg.SaveLabel, localized(locale,
					"保存", "Save")),
			}
		}
		appearanceLabels := func() buildAppearanceLabels {
			return buildAppearanceLabels{
				title:         textutil.FirstNonEmpty(cfg.AppearanceTitle, localized(locale, "外观", "Appearance")),
				themeLabel:    textutil.FirstNonEmpty(cfg.ThemeLabel, localized(locale, "外观模式", "Appearance mode")),
				languageLabel: textutil.FirstNonEmpty(cfg.LanguageLabel, localized(locale, "界面语言", "Interface language")),
				saveLabel:     textutil.FirstNonEmpty(cfg.SaveAppearanceLabel, localized(locale, "保存", "Save")),
			}
		}

		// Search-locate state of the pages that declare searchable items: the
		// page owns its region anchors and the transient highlight (see
		// page_search.go); the host calls Reveal with the hit item's anchor key
		// before the page builds.
		modelSearch := &pageSearch{}
		appearanceSearch := &pageSearch{}
		notificationsSearch := &pageSearch{}
		behaviorSearch := &pageSearch{}

		syncer := newPageSyncer(runtime, locale)
		definitions := []pageDefinition{
			{
				ID: PageID, Section: PageSection, Order: PageOrder,
				zhTitle: "模型", enTitle: "Model", titleOverride: cfg.Title,
				keywords:    modelPageKeywords,
				searchItems: func() []uinative.SettingsSearchItem { return modelPageSearchItems(locale) },
				reveal:      modelSearch.reveal,
				Build: func(c *ui.Context) {
					syncer.sync()
					buildPage(c, runtime, state, catalog, chat, modelLabels(), store, locale, modelSearch)
				},
				Footer: func(c *ui.Context) {
					uikit.PageFooter(c, uikit.PageFooterOptions{
						Key: "settings.footer.model",
						Primary: uikit.DialogAction{
							Label:    modelLabels().saveLabel,
							Disabled: !state.dirty(),
							OnClick: func() {
								saveModelPage(c, state, store, locale)
							},
						},
					})
				},
				Dirty: state.dirty,
				Discard: func() {
					state.discardDrafts(chat, store)
					catalog.discardTransient()
				},
			},
			{
				ID: BehaviorPageID, Section: "常用", Order: BehaviorPageOrder,
				zhTitle: "行为与限额", enTitle: "Behavior & limits",
				keywords: func() []string {
					return append(append([]string{}, behaviorPageKeywords()...), capabilitiesPageKeywords()...)
				},
				searchItems: func() []uinative.SettingsSearchItem { return behaviorPageSearchItems(locale) },
				reveal:      behaviorSearch.reveal,
				Build: func(c *ui.Context) {
					syncer.sync()
					buildBehaviorPage(c, runtime, store, locale, behavior, sandboxService, chat, capabilities, workFolder, behaviorSearch)
				},
				Footer: func(c *ui.Context) {
					uikit.PageFooter(c, uikit.PageFooterOptions{
						Key: "settings.footer.behavior",
						Secondary: []uikit.DialogAction{
							{Label: localized(locale, "恢复默认值", "Restore defaults"), OnClick: func() {
								capabilities.restoreDefaults(locale)
								c.Invalidate()
							}},
						},
						Primary: uikit.DialogAction{
							Label:    localized(locale, "保存", "Save"),
							Disabled: !capabilities.dirty(store),
							OnClick: func() {
								capabilities.save(store, locale)
								c.Invalidate()
							},
						},
					})
				},
				Dirty: func() bool {
					return behaviorDraftDirty(behavior, store) || capabilities.dirty(store)
				},
				Discard: func() {
					discardBehaviorDraft(behavior, store)
					capabilities.discardDrafts(store)
				},
			},
			{
				ID: AppearancePageID, Section: "界面", Order: AppearancePageOrder,
				zhTitle: "外观", enTitle: "Appearance", titleOverride: cfg.AppearanceTitle,
				keywords: func() []string {
					return append(append([]string{}, appearancePageKeywords()...), shortcutsPageKeywords()...)
				},
				searchItems: func() []uinative.SettingsSearchItem { return appearancePageSearchItems(locale) },
				reveal:      appearanceSearch.reveal,
				Build: func(c *ui.Context) {
					syncer.sync()
					if appearance.transient.reentered(runtime) {
						appearance.reenter(store, locale)
					}
					buildAppearancePage(c, appearance, store, theme, runtime, locale,
						appearanceLabels(), shortcuts, appearanceSearch)
				},
				Footer: func(c *ui.Context) {
					uikit.PageFooter(c, uikit.PageFooterOptions{
						Key: "settings.footer.appearance",
						Primary: uikit.DialogAction{
							Label:    appearanceLabels().saveLabel,
							Disabled: !appearance.dirty(),
							OnClick: func() {
								applyAppearancePage(c, appearance, store, locale)
							},
						},
					})
				},
				Dirty: appearance.dirty,
				Discard: func() {
					appearance.discardHotkey(store)
				},
			},
			{
				ID: NotificationsPageID, Section: "界面", Order: notificationsOrderInData,
				zhTitle: "通知", enTitle: "Notifications",
				keywords:    notificationsPageKeywords,
				searchItems: func() []uinative.SettingsSearchItem { return notificationsPageSearchItems(locale) },
				reveal:      notificationsSearch.reveal,
				Build: func(c *ui.Context) {
					syncer.sync()
					buildNotificationsPage(c, runtime, store, locale, notifications,
						systemNotificationsSupported(ctx), notificationsSearch)
				},
			},
			{
				ID: NetworkPageID, Section: "连接", Order: NetworkPageOrder,
				zhTitle: "网络与搜索", enTitle: "Network & search",
				keywords: func() []string {
					return append(append([]string{}, networkPageKeywords()...), searchPageKeywords()...)
				},
				Build: func(c *ui.Context) {
					syncer.sync()
					buildNetworkPage(c, ctx, store, runtime, network, search, locale)
				},
				Footer: func(c *ui.Context) {
					uikit.PageFooter(c, uikit.PageFooterOptions{
						Key: "settings.footer.network",
						Primary: uikit.DialogAction{
							Label:    localized(locale, "保存", "Save"),
							Disabled: !network.dirty(store) && !search.dirty(store),
							OnClick: func() {
								saveNetworkSettings(network, store, locale)
								// The two halves share one footer: a rejected proxy
								// must not let the search half write a partial save.
								if !network.statusErr {
									saveSearchSettings(search, store, locale)
								}
								c.Invalidate()
							},
						},
					})
				},
				Dirty: func() bool { return network.dirty(store) || search.dirty(store) },
				Discard: func() {
					network.discardDrafts(store)
					search.discardDrafts(store)
				},
			},
			{
				ID: LSPPageID, Section: "连接", Order: lspOrderInIntegrations,
				zhTitle: "LSP", enTitle: "LSP",
				keywords: lspPageKeywords,
				Build: func(c *ui.Context) {
					syncer.sync()
					buildLSPPage(c, store, lsp, locale)
				},
				// The add form's submit is the page's one primary and lives in
				// the host's fixed footer (rule 14): the old in-section button
				// scrolled out of sight on a short window. Validation stays on
				// click, so an empty draft reports the missing name/command
				// instead of silently doing nothing.
				Footer: func(c *ui.Context) {
					if store == nil {
						return
					}
					uikit.PageFooter(c, uikit.PageFooterOptions{
						Key: "settings.footer.lsp",
						Primary: uikit.DialogAction{
							Label: localized(locale, "添加", "Add"),
							OnClick: func() {
								addLSPServer(lsp, store, locale)
								c.Invalidate()
							},
						},
					})
				},
				Dirty: func() bool { return lspDraftDirty(lsp) },
				Discard: func() {
					discardLSPDraft(lsp)
				},
			},
			{
				ID: ArchivePageID, Section: "数据", Order: archiveOrderInData,
				zhTitle: "归档", enTitle: "Archived",
				keywords: archivePageKeywords,
				Build: func(c *ui.Context) {
					syncer.sync()
					buildArchivePage(c, design.Of(c), ctx, archive, locale)
				},
			},
			{
				ID: DataPrivacyPageID, Section: "数据", Order: dataPrivacyOrderInData,
				zhTitle: "数据与隐私", enTitle: "Data & privacy",
				keywords: func() []string {
					all := append([]string{}, dataPrivacyPageKeywords()...)
					all = append(all, importPageKeywords()...)
					// The opt-in file-hiding field (2026-11 audit RG-10).
					return append(all, "文件隐藏", "隐藏文件", "deniedGlobs", "glob", "隐私", "privacy", ".env")
				},
				Build: func(c *ui.Context) {
					syncer.sync()
					buildDataPrivacyPage(c, design.Of(c), ctx, runtime, chat, store, dataPrivacy, importState, locale)
				},
				Footer: func(c *ui.Context) {
					uikit.PageFooter(c, uikit.PageFooterOptions{
						Key: "settings.footer.data-privacy",
						Primary: uikit.DialogAction{
							Label:    localized(locale, "保存", "Save"),
							Disabled: !deniedGlobsDirty(dataPrivacy, store),
							OnClick: func() {
								saveDeniedGlobs(dataPrivacy, store, locale)
								c.Invalidate()
							},
						},
					})
				},
				Dirty: func() bool { return deniedGlobsDirty(dataPrivacy, store) },
				Discard: func() {
					discardDeniedGlobs(dataPrivacy, store)
				},
			},
			{
				ID: "about", Section: "关于", Order: 10,
				zhTitle: "关于", enTitle: "About",
				keywords: func() []string {
					all := append([]string{}, updatePageKeywords()...)
					all = append(all, diagnosticsPageKeywords()...)
					all = append(all, "关于", "about", "版本", "version", "许可", "license", "架构", "architecture")
					return all
				},
				Build: func(c *ui.Context) {
					syncer.sync()
					if update.transient.reentered(runtime) {
						update.copied = false
					}
					if diagnostics.transient.reentered(runtime) {
						diagnostics.copied = false
					}
					buildAboutHubPage(c, ctx, info, update, diagnostics, about, locale)
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
		localeSync := uinative.SlotItem{
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
		AddRequires(module.Requires(uinative.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeySettings)).
		AddRequires(module.Requires(contracts.KeyChat)).
		AddRequires(module.Requires(contracts.KeyTheme)).
		AddRequires(module.Requires(contracts.KeyLocale)).
		WithName("ZeoKeel settings pages")
}

// diagnosticsRows collects the read-only runtime/storage diagnostics values.
// Build identity (name/version/commit/build time/Go/platform) lives once in the
// About page's build-facts table (buildFactRows), not repeated here. Optional
// services degrade to explicit "未启用" values instead of hiding the row. The
// tool surface is the narrow contracts.ToolCatalog (names + count), never the
// concrete rig registry, so this UI module imports no backend tool framework
// (AR-06).
func diagnosticsRows(dataDir string, catalog contracts.ToolCatalog,
	chat contracts.ChatService, store contracts.SettingsService, locale contracts.LocaleService) ([]diagnosticRow, []diagnosticRow) {
	rows := []diagnosticRow{
		{localized(locale, "数据目录", "Data directory"), textutil.FirstNonEmpty(dataDir, localized(locale, "未设置", "not set"))},
		{localized(locale, "会话数据库", "Session database"), localized(locale, "zeokeel.db（数据目录）", "zeokeel.db (data directory)")},
	}
	if dataDir != "" {
		rows[1].Value = filepath.Join(dataDir, "zeokeel.db")
	}
	toolCount := 0
	toolNames := localized(locale, "未启用", "not enabled")
	if catalog != nil {
		toolCount = catalog.Len()
		if names := catalog.Names(); len(names) > 0 {
			toolNames = strings.Join(names, ", ")
		}
	}
	rows = append(rows,
		diagnosticRow{localized(locale, "工具数", "Tools"), fmt.Sprintf("%d", toolCount)},
		diagnosticRow{localized(locale, "工具清单", "Tool names"), toolNames},
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

// runtimeSummary is the one-line effective runtime state shown under the
// collapsed 高级选项 disclosure. An empty draft names the module-config fallback
// instead of a concrete value. Iterations are not summarized: the cap is
// assembly-level (unlimited by default) and has no user knob.
func runtimeSummary(state *moduleState, locale contracts.LocaleService) string {
	budget := strings.TrimSpace(state.contextBudget)
	if budget == "" {
		if state.moduleContextBudget != 0 {
			budget = fmt.Sprintf(localized(locale, "模块默认 %d", "module default %d"), state.moduleContextBudget)
		} else {
			budget = localized(locale, "默认", "default")
		}
	}
	return localized(locale, "上下文预算 ", "Context budget ") + budget
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
// the explicit-save values and reports the result in the page status line. A
// global hotkey that conflicts with a chrome shortcut needs a second
// confirmation: the first Save arms it and writes nothing, the second Save
// overwrites (UX-11, personal-scale ruling — warning + 二次确认, not a block).
func applyAppearancePage(c *ui.Context, state *appearanceState, store contracts.SettingsService,
	locale contracts.LocaleService) {
	if action := globalHotkeyConflict(locale, state.globalHotkey, store); action != "" {
		if !state.conflictArmed {
			state.conflictArmed = true
			state.status = localized(locale,
				"与「"+action+"」快捷键冲突：再次点「保存」确认覆盖。",
				"Conflicts with the "+action+" shortcut: press Save again to confirm overwriting it.")
			state.statusErr = true
			c.Invalidate()
			return
		}
	}
	state.conflictArmed = false
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

// runtimeRows reads the explicit runtime-limit settings row: an empty value
// means the row is absent, so the module config applies. The iteration cap is
// not a settings row (assembly-level only, v0.2.50 removal).
func runtimeRows(store contracts.SettingsService) (contextBudget string) {
	if store == nil {
		return ""
	}
	return strings.TrimSpace(store.Get(contracts.SettingsChatContextBudget, ""))
}

// runtimeDefaultHint names the module-config fallback of an empty runtime draft:
// the concrete module default when it has one, else the field's own
// "unlimited"/"default" wording. It is shown as the empty input's placeholder
// and in the field's description.
func runtimeDefaultHint(locale contracts.LocaleService, moduleDefault int, fallbackZh, fallbackEn string) string {
	if moduleDefault > 0 {
		return fmt.Sprintf(localized(locale, "留空使用模块默认 %d",
			"Leave blank for the module default %d"), moduleDefault)
	}
	return localized(locale, fallbackZh, fallbackEn)
}

// seedRuntime replaces the runtime-limit draft and its persisted snapshot.
// Only the explicit settings row seeds the draft: the module-config limit is a
// display-only fallback named by the empty draft's placeholder/description, so
// it never seeds the draft. An empty draft therefore keeps the module config
// applying until the user types a concrete value.
func (st *moduleState) seedRuntime(chat contracts.ChatService, store contracts.SettingsService) {
	var cfg contracts.ChatConfig
	if chat != nil {
		cfg = chat.Config()
	}
	st.moduleContextBudget = cfg.ContextBudget
	st.contextBudget = runtimeRows(store)
	st.seenContextBudget = st.contextBudget
	st.contextBudgetErr = ""
}

// validateRuntimeDrafts parses the numeric draft and fills its inline error.
// It reports whether the draft is valid and can be saved. An empty draft is
// valid: it means "use the module config".
func validateRuntimeDrafts(st *moduleState, locale contracts.LocaleService) bool {
	st.contextBudgetErr = ""
	if raw := strings.TrimSpace(st.contextBudget); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			st.contextBudgetErr = localized(locale,
				"请输入大于或等于 0 的整数；留空使用模块默认值。",
				"Enter an integer greater than or equal to 0; leave blank to use the module default.")
		}
	}
	return st.contextBudgetErr == ""
}

// syncFromConfig refreshes the explicit-save drafts when the persisted
// configuration changed outside this page (another settings page, a second
// window, the runtime-limit rows). It never overwrites a draft whose input
// holds the focus, so the caret and the text being typed survive; the next
// frame after the focus leaves syncs. The model list is not part of this state:
// it always renders the persisted row.
func (st *moduleState) syncFromConfig(c *ui.Context, chat contracts.ChatService, store contracts.SettingsService) {
	cfg := effectiveChatConfig(chat, store)
	contextBudget := runtimeRows(store)
	moduleCfg := contracts.ChatConfig{}
	if chat != nil {
		moduleCfg = chat.Config()
	}
	if cfg.SystemPrompt == st.seenSystemPrompt &&
		contextBudget == st.seenContextBudget &&
		moduleCfg.ContextBudget == st.moduleContextBudget {
		return
	}
	if st.promptInput.Focused(c) || st.budgetInput.Focused(c) {
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
// configuration. The model list applies immediately and therefore never dirties
// the page.
func (st *moduleState) dirty() bool {
	return st.systemPrompt != st.seenSystemPrompt ||
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

// inputLabel appends the localized "input" suffix used by native input labels.
//
// Convention: EVERY text field and text area in the settings surface and the
// plugins dialogs names itself through this helper (or its plugins twin
// inputLabel/skillInputLabel), so the accessible name is uniform — "代理地址 输入"
// on the settings pages reads the same as "URL 输入" in the connector form. The
// ui.Field caption stays the human label ("代理地址"); the suffix marks the
// control itself, which is what a screen reader announces on focus. A switch, a
// select or a search field is not an input and keeps its bare name.
func inputLabel(locale contracts.LocaleService, label string) string {
	return label + localized(locale, " 输入", " input")
}

// rowActionLabel names one row's inline action for assistive technology:
// "记忆操作：<行名>" / "Memory actions: <row>". A page of identical ⋯ buttons was
// indistinguishable to a screen reader and to scripted interaction, which had to
// fall back to "the same-named control nearest the row" (2026-11-18 review). The
// visible glyph does not change.
func rowActionLabel(locale contracts.LocaleService, action, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return action
	}
	return action + localized(locale, "：", ": ") + name
}

// buildPage builds the model settings page. The model list is the page's model
// control surface (add/edit/remove and the default model apply immediately);
// the system prompt and the runtime limits are the only explicit-save drafts,
// persisted by the page footer's Save.
func buildPage(c *ui.Context, runtime uinative.UIRuntime, state *moduleState, catalog *catalogState,
	chat contracts.ChatService, labels buildLabels, store contracts.SettingsService,
	locale contracts.LocaleService, search *pageSearch) {
	t := design.Of(c)
	// The runtime limits live behind the collapsed 高级选项 disclosure: a search
	// hit on one of them must open it before the fields build (contracts
	// .SettingsPageSpec.Reveal runs before Build), so the reveal can scroll the
	// field into view in this frame.
	if search.targetedAt("model.context-budget") || search.targetedAt("model.max-iterations") {
		state.runtimeOpen = true
	}
	// A write from another settings surface (another window, the runtime rows)
	// refreshes the drafts before they render, so this page can never save a
	// stale configuration over it.
	state.syncFromConfig(c, chat, store)
	effective := effectiveChatConfig(chat, store)
	ui.Column(c).FillWidth().Gap(design.SpaceScreen).Children(func() {
		uikit.PageHeader(c, labels.title, "")

		// One page owns the model configuration: every row carries its own
		// provider connection and one row is the default model. Prices and the
		// per-provider key fields belong to this list, so a search for "价格" or
		// "密钥" reveals the list itself.
		search.region(c, "model.catalog", func(c *ui.Context) {
			buildModelCatalogSection(c, runtime, store, catalog, locale, effective.BaseURL, effective.APIKey, effective.Model)
		})

		// Prompt and runtime knobs are the page's explicit-save section: the
		// host footer's Save is the page's one primary. The title names the
		// content only; the save mechanism is the section's description, not
		// title chrome (the model list above applies immediately, R12).
		uikit.Section(c, localized(locale, "提示词与运行参数", "Prompt & runtime"), func(*ui.Context) {
			design.Apply(ui.Text(c, localized(locale,
				"本节草稿由页脚「保存」统一写入。",
				"The footer Save writes this section's drafts.")),
				t.TextMuted(design.TypeXXS)).MaxLines(2)
			search.region(c, "model.system-prompt", func(c *ui.Context) {
				ui.Field(c, labels.systemPromptLabel, func() {
					prompt := ui.TextArea(c, &state.systemPrompt).Label(inputLabel(locale, labels.systemPromptLabel)).
						Bind(&state.promptInput).FillWidth().Height(56)
					if prompt.Changed() {
						clearSavedStatus(state)
					}
				}).Description(localized(locale,
					"追加到每次对话的系统提示前；留空使用服务商默认。",
					"Prepended to every conversation's system prompt; empty uses the provider default."))
			})
			// The runtime limit is a low-frequency knob: a native disclosure
			// keeps the section short. The draft saves with the page footer, and
			// an empty draft means "use the module config" (no settings row).
			// Iterations have no knob: the cap is assembly-level (v0.2.50).
			ui.Divider(c)
			ui.Collapsible(c, localized(locale, "高级选项", "Advanced options"), &state.runtimeOpen, func() {
				search.region(c, "model.context-budget", func(c *ui.Context) {
					budgetHint := runtimeDefaultHint(locale, state.moduleContextBudget,
						"留空使用模块默认（0 = 不自动压缩）",
						"Leave blank for the module default (0 = no automatic compaction)")
					budgetLabel := localized(locale, "上下文预算", "Context budget")
					ui.Field(c, budgetLabel, func() {
						input := ui.TextInput(c, &state.contextBudget).
							Label(inputLabel(locale, budgetLabel)).
							Placeholder(budgetHint).
							Bind(&state.budgetInput).FillWidth()
						if input.Changed() {
							clearSavedStatus(state)
						}
					}).Description(localized(locale,
						"自动压缩的 prompt token 预算；", "Prompt-token budget for automatic compaction; ") +
						budgetHint + localized(locale, "。", "."))
					if state.contextBudgetErr != "" {
						uikit.ErrorLine(c, state.contextBudgetErr)
					}
				})
			})
			if !state.runtimeOpen {
				design.Apply(ui.Text(c, runtimeSummary(state, locale)),
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
		// The row names the conversation, never its internal id: settings is a
		// product surface, and sess-1794… is a storage handle (2026-11-18 review).
		sessionRow := sessionTitleRow(chat, locale)
		rows := []uikit.PropertyRow{}
		if sessionRow != "" {
			rows = append(rows, uikit.PropertyRow{
				Label: localized(locale, "当前会话", "Current session"), Value: sessionRow,
			})
		}
		rows = append(rows, uikit.PropertyRow{
			Label: localized(locale, "默认模型", "Default model"),
			Value: textutil.FirstNonEmpty(defaultModel,
				localized(locale, "未配置", "Not configured")),
		})
		uikit.Section(c, localized(locale, "当前", "Current"), func(body *ui.Context) {
			uikit.PropertySheet(body, uikit.PropertySheetOptions{
				Rows: rows, Key: "runtime.current", Locale: locale,
			})
		})
	})
}

// sessionTitleRow returns the active conversation's display title for the
// read-only 当前 section, or "" when no session is active (the row is omitted
// rather than showing an empty id). The id is deliberately never rendered: the
// settings surface speaks product language, not storage handles.
func sessionTitleRow(chat contracts.ChatService, locale contracts.LocaleService) string {
	if chat == nil {
		return ""
	}
	id := strings.TrimSpace(chat.ActiveSession())
	if id == "" {
		return ""
	}
	title := ""
	if infos := chat.Sessions(); infos != nil {
		for _, info := range infos {
			if info.ID == id {
				title = info.Title
				break
			}
		}
	}
	if strings.TrimSpace(title) == "" {
		if session, ok := chat.Session(id); ok {
			title = session.Title
		}
	}
	return textutil.FirstNonEmptyTrimmed(title,
		localized(locale, "未命名会话", "Untitled conversation"))
}

// --- appearance page -------------------------------------------------------

// buildAppearancePage builds the theme/language/desktop page. Theme, language
// and the desktop switches apply at once (they are switches, the mygo control
// for a setting that takes effect immediately); the global hotkey is the page's
// only explicit-save field and says so next to its input.
func buildAppearancePage(c *ui.Context, state *appearanceState, store contracts.SettingsService,
	theme contracts.ThemeService, runtime uinative.UIRuntime, locale contracts.LocaleService,
	labels buildAppearanceLabels, shortcuts *shortcutsState, search *pageSearch) {
	t := design.Of(c)
	ui.Column(c).FillWidth().Gap(design.SpaceScreen).Children(func() {
		uikit.PageHeader(c, labels.title, "")

		search.region(c, "appearance.theme", func(c *ui.Context) {
			uikit.Section(c, localized(locale, "主题", "Theme"), func(*ui.Context) {
				if buildSegmentedControl(c, t, &state.themeMode, labels.themeLabel,
					localized(locale, "浅色", "Light"),
					localized(locale, "深色", "Dark"),
					localized(locale, "跟随系统", "System"),
				) {
					applyThemeModeSelection(state, store, theme, locale)
					c.Invalidate()
				}
			})
		})

		search.region(c, "appearance.language", func(c *ui.Context) {
			uikit.Section(c, localized(locale, "语言", "Language"), func(*ui.Context) {
				choice := languageChoice(state.language)
				// The language picker is a form control: the 320 DIP column, not
				// the page's full measure (B-spec §3.4.4).
				selectControl := ui.Select(c, &choice, languageOptions).
					Label(labels.languageLabel).Width(controlWidth)
				if selectControl.Changed() {
					state.language = languageCode(choice)
					locale.SetLanguage(state.language)
					_ = store.Set(contracts.SettingsUILanguage, state.language)
					state.status = localized(locale, "语言已切换", "Language switched")
					state.statusErr = false
				}
			})
		})

		search.region(c, "appearance.desktop", func(c *ui.Context) {
			uikit.Section(c, localized(locale, "桌面集成", "Desktop integration"), func(*ui.Context) {
				// One row per switch (label left, switch trailing): a shared
				// Form label column right-aligned the two labels against each
				// other and orphaned the tray description under the control.
				if uikit.SwitchRow(c, uikit.SwitchRowOptions{
					Label: localized(locale, "开机自启动", "Launch at login"),
					Value: &state.autostart,
				}) {
					applyDesktopSetting(state, store, contracts.SettingAutostart,
						fmt.Sprintf("%t", state.autostart), locale)
				}
				if uikit.SwitchRow(c, uikit.SwitchRowOptions{
					Label: localized(locale, "关闭时最小化到托盘", "Close to system tray"),
					Description: localized(locale, "关闭窗口后应用继续在托盘运行，任务不中断。修改后重启应用生效。",
						"Keep running in the system tray after closing the window, so tasks continue. Takes effect after a restart."),
					Value: &state.closeToTray,
				}) {
					applyDesktopSetting(state, store, contracts.SettingCloseToTray,
						fmt.Sprintf("%t", state.closeToTray), locale)
				}
			})
		})

		search.region(c, "appearance.hotkey", func(c *ui.Context) {
			buildGlobalHotkeySection(c, t, state, store, locale)
		})

		// H1: the apply action lives in the host's fixed footer; the body keeps
		// only the last save result (the title badge owns "unsaved", R8).
		if state.dirty() && !state.statusErr {
			clearAppearanceStatus(state)
		}
		statusBadgesRow(c, t, state.status, state.statusErr)

		// Keyboard configuration and the reference used to be a separate
		// settings page; it is one section of the appearance page now.
		buildShortcutsPage(c, t, localized(locale, "快捷键", "Shortcuts"), store, runtime, locale, shortcuts, false)
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
	st.conflictArmed = false
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
		state.closeToTray = closeToTraySettingEnabled(store.Get(contracts.SettingCloseToTray, "false"))
		state.status = localized(locale, "保存失败：", "Save failed: ") + err.Error()
		state.statusErr = true
		return
	}
	state.status = localized(locale, "桌面集成设置已更新", "Desktop integration updated")
	state.statusErr = false
}

// save writes the page's explicit-save drafts to the settings store: the system
// prompt and the optional context budget. The model list (including the default
// model mirror) is persisted by the list's own actions instead, because it
// applies immediately. An empty runtime draft writes an empty row, which means
// "use the module config"; a value persists that row (an explicit "0" budget
// stays meaningful). The effective-config snapshot is recorded so the page
// renders as clean right after a successful save.
func save(state *moduleState, store contracts.SettingsService) error {
	if err := store.Set(contracts.SettingsChatSystemPrompt, state.systemPrompt); err != nil {
		return fmt.Errorf("system prompt: %w", err)
	}
	state.contextBudget = strings.TrimSpace(state.contextBudget)
	if err := store.Set(contracts.SettingsChatContextBudget, state.contextBudget); err != nil {
		return fmt.Errorf("context budget: %w", err)
	}
	state.seenSystemPrompt = state.systemPrompt
	state.seenContextBudget = state.contextBudget
	state.contextBudgetErr = ""
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
