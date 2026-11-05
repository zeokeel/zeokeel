package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/state"
)

// Mention caps: files first, then sessions, matching DSH's candidate order.
// Each group is bounded on its own so a session list full of matches cannot
// hide the files.
const (
	maxMentionFiles    = 10
	maxMentionSessions = 10
	// sessionMentionPrefix labels a session row. The candidate list is a
	// []string because session_live.go and home.go (outside this change's
	// ownership) pass it through; the prefix keeps the two kinds apart
	// without changing that signature.
	sessionMentionPrefix = "session · "
)

// slashCommands is the composer command palette. Every entry starts with "/"
// and is handled locally; none of them is sent to the model. A slash line
// that names anything else falls through to the model as normal text, as
// DSH's command directory does.
var slashCommands = []struct {
	name string
	en   string
	zh   string
}{
	{"/clear", "Clear the composer draft", "清空输入框草稿"},
	{"/stop", "Stop the running turn", "停止当前回合"},
	{"/help", "List the composer commands", "列出输入框命令"},
	{"/new", "Start a new session", "新建会话"},
	{"/settings", "Open Settings", "打开设置"},
	{"/automate", "Open Automation tasks", "打开自动化任务"},
	{"/plugins", "Open Plugins", "打开插件"},
	{"/model", "Choose the model (needs the live model API)", "选择模型（需要实时模型 API）"},
}

// pendingApprovals returns the controller's pending requests, or nil in static
// mode. It is read every frame so the card follows the turn without extra
// listener plumbing.
func (a *App) pendingApprovals() []state.ApprovalRequest {
	if a.Ctrl == nil {
		return nil
	}
	return a.Ctrl.PendingApprovals()
}

// slashVisible reports whether the draft asks for the command palette: a
// non-empty draft that starts with "/" and nothing else.
func (a *App) slashVisible() bool {
	return strings.HasPrefix(strings.TrimSpace(a.draft), "/")
}

// syncComposerMenuDismissal clears an Escape dismissal whenever the draft
// changes. A comparison with the dismissed text alone was not enough: typing
// an extra character and deleting it back to the same text must show the menu
// again, because the user edited the query.
func (a *App) syncComposerMenuDismissal() {
	st := a.liveMenus()
	if !st.menuDraftSet {
		st.menuDraftSet = true
		st.menuDraft = a.draft
		return
	}
	if st.menuDraft == a.draft {
		return
	}
	st.menuDraft = a.draft
	st.mentionDismissedDraft = ""
	st.slashDismissedDraft = ""
}

// slashMenuVisible reports whether the "/" palette is derived from the draft
// and has not been dismissed with Escape for that exact draft.
func (a *App) slashMenuVisible() bool {
	return a.slashVisible() && a.liveMenus().slashDismissedDraft != a.draft
}

// visibleMentionEntries returns the "@" menu rows for the current draft, or
// nil when Escape has dismissed the menu for that draft.
func (a *App) visibleMentionEntries() []string {
	if a.liveMenus().mentionDismissedDraft == a.draft {
		return nil
	}
	return a.mentionEntries()
}

// composerPopupVisible reports whether a derived composer popup (the "@"
// mention list or the "/" command palette) is showing.
func (a *App) composerPopupVisible() bool {
	return a.slashMenuVisible() || len(a.visibleMentionEntries()) > 0
}

// dismissComposerPopup hides the visible derived popup for the current draft
// and reports whether it hid one. A later draft edit clears the dismissal
// because the recorded draft no longer matches.
func (a *App) dismissComposerPopup() bool {
	st := a.liveMenus()
	if a.slashVisible() && st.slashDismissedDraft != a.draft {
		st.slashDismissedDraft = a.draft
		return true
	}
	if entries := a.mentionEntries(); len(entries) > 0 && st.mentionDismissedDraft != a.draft {
		st.mentionDismissedDraft = a.draft
		return true
	}
	return false
}

// menuFrame centers a panel of width DIPs above the composer and runs build
// inside its token-styled surface. The width is clamped to the composer's own
// column, so an 868px approval card cannot be cut off at the window edge when
// the main column is narrower (900x600, or a docked right panel).
func (a *App) menuFrame(c *ui.Context, t *design.Tokens, width float32, build func()) {
	if limit := a.composerPopupWidthFor(c); limit > 0 && width > limit {
		width = limit
	}
	ui.Row(c).Justify(ui.Center).Children(func() {
		design.Apply(ui.Column(c), t.Panel()).Width(width).Padding(design.Space3).
			Gap(design.Space2).Children(build)
	})
}

// approvalCards draws one card per pending approval. The tool name, compact
// arguments and the two decisions are the only controls; the buttons call the
// controller, which removes the request from the pending set.
func (a *App) approvalCards(c *ui.Context, t *design.Tokens, requests []state.ApprovalRequest) {
	for _, request := range requests {
		a.menuFrame(c, t, 868, func() {
			ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				ui.Text(c, a.T("composer.approval_required")).FontSize(12).FixedLineHeight(18).
					TextColor(t.LabelTertiary).Grow(1)
				ui.Text(c, request.Name).FontSize(13).FixedLineHeight(20).
					FontWeight(600).TextColor(t.LabelPrimary)
			})
			ui.Text(c, compactArgs(request.Args)).FontSize(12).FixedLineHeight(18).
				TextColor(t.LabelSecondary).MaxLines(2).Ellipsis("...")
			ui.Row(c).Gap(design.Space2).Children(func() {
				allow := design.Apply(ui.Button(c, a.T("composer.allow")), t.PrimaryButton())
				if allow.Clicked() && a.Ctrl != nil {
					a.Ctrl.Approve(request.CallID)
				}
				deny := ui.Button(c, a.T("composer.deny")).Height(38).PaddingX(design.Space4).
					Radius(design.RadiusSM).Border(1, t.BorderL2).Background(t.BgBase).
					TextColor(t.Danger)
				if deny.Clicked() && a.Ctrl != nil {
					a.Ctrl.Deny(request.CallID)
				}
			})
		})
	}
}

// commandPalette is the "/" menu above the composer.
func (a *App) commandPalette(c *ui.Context, t *design.Tokens) {
	// runSlashCommand has no context parameter (session_live.go calls it with
	// the bare line), so keep the frame's context for the toast the command
	// may show. The palette is built on every frame whose draft starts with
	// "/", which is exactly the frame whose Enter/click runs a command.
	a.startUI().lastComposerContext = c
	a.menuFrame(c, t, 360, func() {
		ui.Text(c, a.T("composer.commands")).FontSize(11).FixedLineHeight(16).
			TextColor(t.LabelTertiary)
		for _, command := range slashCommands {
			item := ui.Button(c, command.name).Height(30).PaddingX(design.Space2).
				Radius(design.RadiusSM).TextColor(t.LabelPrimary)
			item.Tooltip(a.tr(command.en, command.zh))
			if item.Clicked() {
				a.runSlashCommand(command.name)
			}
		}
		ui.Text(c, a.tr("Unknown commands are sent to the model as text.",
			"未知命令会作为普通文本发送给模型。")).
			FontSize(11).FixedLineHeight(16).TextColor(t.LabelTertiary).
			Padding(design.Space1, design.Space2, 0, design.Space2)
	})
}

// slashCommandName returns the command name of a typed slash line. The name
// ends at the first whitespace; "/stop now" and "/stop" both resolve to
// /stop, as DSH's command directory does. Unknown names return false and the
// line falls through to the model.
func slashCommandName(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", false
	}
	name := text
	if i := strings.IndexAny(text, " \t\r\n"); i >= 0 {
		name = text[:i]
	}
	for _, command := range slashCommands {
		if command.name == name {
			return name, true
		}
	}
	return "", false
}

// isSlashCommand reports whether text names one of the palette commands.
func isSlashCommand(text string) bool {
	_, ok := slashCommandName(text)
	return ok
}

// runSlashCommand performs one command selection. The palette closes because
// every branch either clears the draft or replaces it with plain text.
// Known commands run locally; an unknown slash line is never routed here
// (submitComposer sends it as text instead).
func (a *App) runSlashCommand(line string) {
	name, ok := slashCommandName(line)
	if !ok {
		return
	}
	switch name {
	case "/clear":
		// This build has no conversation-clear API (the store is append-only),
		// so the local action is the composer draft: the state this UI owns.
		a.draft = ""
		a.toastText("Cleared the composer draft", "已清空输入框草稿")
	case "/stop":
		if a.Ctrl != nil {
			a.Ctrl.Stop()
		}
		a.draft = ""
	case "/help":
		a.draft = a.tr(
			"Commands: /clear /stop /help /new /settings /automate /plugins /model. Compaction is automatic (the runtime compacts long conversations before a turn); there is no manual command. Unknown commands are sent to the model.",
			"命令：/clear /stop /help /new /settings /automate /plugins /model。压缩是自动的（运行时会在每轮开始前压缩过长的对话）；没有手动命令。未知命令会发送给模型。")
	case "/new":
		a.draft = ""
		a.newSession()
	case "/settings":
		a.draft = ""
		a.openSettings()
	case "/automate":
		a.draft = ""
		a.Screen = Automation
	case "/plugins":
		a.draft = ""
		a.Screen = Plugins
	case "/model":
		a.draft = ""
		a.openModelPickerFromCommand()
	default:
		a.draft = ""
	}
}

// toastText shows one of this file's messages through the composer frame's
// context. The command catalog lives in i18n.go, which this change does not
// own, so new messages carry both languages here.
func (a *App) toastText(en, zh string) {
	if c := a.startUI().lastComposerContext; c != nil {
		c.Toast(a.tr(en, zh))
	}
}

// openModelPickerFromCommand is /model: it opens the composer's real model
// menu when the live model API exists. Outside Home/Session the menu is not
// mounted, so it explains where the picker lives rather than doing nothing.
func (a *App) openModelPickerFromCommand() {
	if a.Ctrl == nil {
		return
	}
	switch a.Screen {
	case Home, Session:
		a.liveMenus().modelOpen = true
	default:
		a.toastText("The model picker is available on Home and in a Session.",
			"模型选择器仅在首页和会话中可用。")
	}
}

// mentionQuery finds a trailing "@query" token. query is empty for a draft
// that ends in "@"; a space after the "@" ends the mention.
func mentionQuery(draft string) (start int, query string, ok bool) {
	start = strings.LastIndex(draft, "@")
	if start < 0 {
		return 0, "", false
	}
	query = draft[start+1:]
	if strings.ContainsAny(query, " \t\r\n") {
		return 0, "", false
	}
	return start, query, true
}

// mentionEntries lists the workspace entries and sessions whose names start
// with the text after the trailing "@": files first, then sessions, as DSH's
// reference menu orders them. Directories carry a trailing slash so the
// inserted path reads as a directory; a session row is labelled
// "session · <title>" and inserted as the plain "#<title>" reference (see
// insertSessionMention for why this build has no richer form).
func (a *App) mentionEntries() []string {
	if a.Ctrl == nil {
		return nil
	}
	_, query, ok := mentionQuery(a.draft)
	if !ok {
		return nil
	}
	prefix := strings.ToLower(query)
	out := make([]string, 0, maxMentionFiles+maxMentionSessions)
	if root := strings.TrimSpace(a.Ctrl.WorkspaceRoot()); root != "" {
		if entries, err := os.ReadDir(root); err == nil {
			for _, entry := range entries {
				name := entry.Name()
				if !strings.HasPrefix(strings.ToLower(name), prefix) {
					continue
				}
				if entry.IsDir() {
					name += "/"
				}
				out = append(out, name)
				if len(out) == maxMentionFiles {
					break
				}
			}
		}
	}
	files := len(out)
	if sessions, err := a.Ctrl.ListSessions(context.Background()); err == nil {
		for _, sess := range sessions {
			title := strings.TrimSpace(sess.Title)
			if title == "" {
				title = a.T("common.untitled")
			}
			if !strings.HasPrefix(strings.ToLower(title), prefix) {
				continue
			}
			out = append(out, sessionMentionPrefix+title)
			if len(out)-files == maxMentionSessions {
				break
			}
		}
	}
	return out
}

// mentionChoice is one selectable row of the "@" menu: a workspace path or a
// session title, with the text its selection inserts.
type mentionChoice struct {
	label   string
	value   string
	session bool
}

// mentionChoices splits the flat candidate list into rows. Files keep their
// path (and a trailing slash for a directory); sessions carry the display
// prefix and insert the documented "#<title>" reference.
func mentionChoices(entries []string) []mentionChoice {
	out := make([]mentionChoice, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry, sessionMentionPrefix) {
			title := strings.TrimPrefix(entry, sessionMentionPrefix)
			out = append(out, mentionChoice{label: entry, value: title, session: true})
			continue
		}
		out = append(out, mentionChoice{label: entry, value: entry})
	}
	return out
}

// mentionMenu lists the matching files and sessions for the trailing "@"
// token. It follows DSH's listbox capture (baseline/css/20-mention.json): the
// composer-width panel, 30px section titles, 34px option rows with an icon, a
// name and a trailing hint. Rows are RoleListItem and Focusable; the column is
// a RoleList focus group, so Tab/Up/Down reach them and Enter chooses.
func (a *App) mentionMenu(c *ui.Context, t *design.Tokens, entries []string) {
	choices := mentionChoices(entries)
	if len(choices) == 0 {
		return
	}
	st := a.liveMenus()
	key := strings.Join(entries, "\x00")
	if st.mentionKey != key {
		st.mentionKey = key
		st.mentionIndex = 0
	}
	if st.mentionIndex < 0 {
		st.mentionIndex = 0
	}
	if st.mentionIndex >= len(choices) {
		st.mentionIndex = len(choices) - 1
	}
	width := a.composerPopupWidthFor(c)
	ui.Row(c).Justify(ui.Center).Children(func() {
		design.Apply(ui.Column(c), t.Panel()).Width(width).Padding(design.Space1).Children(func() {
			list := ui.Column(c).Role(ui.RoleList).FocusGroup(ui.Vertical).
				Label(a.menuText("Mention suggestions", "提及建议"))
			list.Children(func() {
				files := 0
				sessions := 0
				for _, choice := range choices {
					if choice.session {
						sessions++
					} else {
						files++
					}
				}
				if files > 0 {
					a.mentionSectionTitle(c, t, a.tr("Files & folders", "文件和文件夹"))
				} else if sessions > 0 {
					a.mentionSectionTitle(c, t, a.tr("Sessions", "会话"))
				}
				index := 0
				for _, choice := range choices {
					if choice.session {
						continue
					}
					a.mentionRow(c, t, choice, index, st)
					index++
					if index == files && sessions > 0 {
						a.mentionSectionTitle(c, t, a.tr("Sessions", "会话"))
					}
				}
				for _, choice := range choices {
					if !choice.session {
						continue
					}
					a.mentionRow(c, t, choice, index, st)
					index++
				}
			})
		})
	})
}

// mentionSectionTitle is one 30px category label of the "@" menu, matching the
// baseline's ".sectionTitle" boxes at y=93 and y=330.
func (a *App) mentionSectionTitle(c *ui.Context, t *design.Tokens, label string) {
	ui.Row(c).Height(30).PaddingX(design.Space2).AlignItems(ui.Center).
		Label(label).Role(ui.RoleNone).Children(func() {
		ui.Text(c, label).FontSize(11).FixedLineHeight(16).TextColor(t.LabelTertiary)
	})
}

// mentionRow draws one 34px option. The row is Focusable inside the list's
// focus group: the group's arrows move the focus, and Enter or a click inserts
// the choice.
func (a *App) mentionRow(c *ui.Context, t *design.Tokens, choice mentionChoice, index int, st *liveMenuState) {
	item := ui.Row(c).Height(34).PaddingX(design.Space2).Gap(design.Space2).
		Radius(design.RadiusSM).AlignItems(ui.Center).Shrink(0).
		Label(choice.label).Role(ui.RoleListItem).Focusable()
	if index == 0 {
		item = item.Bind(&a.mentionRef)
	}
	if index == st.mentionIndex || item.Focused() {
		item = item.Background(t.BgHover)
	}
	if item.Focused() {
		st.mentionIndex = index
	}
	item.Children(func() {
		if choice.session {
			ui.Icon(c, icons.NewChat()).Size(14, 14).TextColor(t.LabelSecondary)
		} else {
			ui.Icon(c, icons.Folder()).Size(14, 14).TextColor(t.LabelSecondary)
		}
		ui.Text(c, choice.label).FontSize(13).FixedLineHeight(20).
			TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
		if choice.session {
			ui.Text(c, a.tr("Session", "会话")).FontSize(11).FixedLineHeight(16).
				TextColor(t.LabelTertiary).NoWrap()
			return
		}
		ui.Text(c, a.tr("Browse folder", "浏览文件夹")).FontSize(11).FixedLineHeight(16).
			TextColor(t.LabelTertiary).NoWrap()
		ui.Text(c, "Tab").FontSize(10).FixedLineHeight(16).TextColor(t.LabelTertiary).
			Padding(0, 4).Background(t.BgHover).Radius(design.RadiusXS)
		ui.Icon(c, icons.ChevronRight()).Size(12, 12).TextColor(t.LabelTertiary)
	})
	choose := func() {
		if choice.session {
			a.insertSessionMention(choice.value)
		} else {
			a.insertMention(choice.value)
		}
		a.composerRef.Focus(c)
	}
	item.OnClick(choose)
	if item.Focused() && item.Shortcut(0, ui.KeyEnter) {
		choose()
	}
}

// moveMention moves the "@" menu highlight by delta while the composer keeps
// the keyboard focus. The menu itself clamps the index on the next frame.
func (a *App) moveMention(entries []string, delta int) {
	st := a.liveMenus()
	st.mentionIndex += delta
	if n := len(entries); n > 0 {
		if st.mentionIndex < 0 {
			st.mentionIndex = 0
		}
		if st.mentionIndex >= n {
			st.mentionIndex = n - 1
		}
	}
}

// chooseMention inserts the highlighted choice. Enter in the composer routes
// here, so the menu works without moving the focus out of the text field.
func (a *App) chooseMention(entries []string) bool {
	choices := mentionChoices(entries)
	if len(choices) == 0 {
		return false
	}
	st := a.liveMenus()
	index := st.mentionIndex
	if index < 0 {
		index = 0
	}
	if index >= len(choices) {
		index = len(choices) - 1
	}
	choice := choices[index]
	if choice.session {
		a.insertSessionMention(choice.value)
	} else {
		a.insertMention(choice.value)
	}
	return true
}

func (a *App) insertMention(path string) {
	start, _, ok := mentionQuery(a.draft)
	if !ok {
		return
	}
	a.draft = a.draft[:start+1] + path + " "
}

// insertSessionMention inserts this build's session reference. DSH inserts
// an atomic "@[label](dsh-session:<id>)" mention, which the host resolves to
// captured session context. ZeoKeel's controller has no session-reference
// resolver (the counterpart of dsh-session-reference) and the runtime would
// treat that URI as opaque text, so the documented fallback is the plain
// "#<title>" token: readable to the model and to the user, with no false
// claim of a resolved context.
func (a *App) insertSessionMention(title string) {
	start, _, ok := mentionQuery(a.draft)
	if !ok {
		return
	}
	a.draft = a.draft[:start] + "#" + title + " "
}

// attachmentMenu is the "+" menu: Files opens the real multi-file picker and
// stages the selection as attachment chips; Commands opens the slash palette.
// A session-log viewer does not exist in this build, so no session-log entry is
// offered.
func (a *App) attachmentMenu(c *ui.Context, t *design.Tokens) {
	a.menuFrame(c, t, 240, func() {
		ui.Text(c, a.T("composer.add_to_message")).FontSize(11).FixedLineHeight(16).
			TextColor(t.LabelTertiary)
		for _, item := range []struct {
			label string
			run   func()
		}{
			{a.T("composer.files"), func() { a.chooseAttachmentFiles(c) }},
			{a.T("composer.commands"), func() { a.insertComposerToken(c, "/") }},
		} {
			item := item
			button := ui.Button(c, item.label).Height(30).PaddingX(design.Space2).
				Radius(design.RadiusSM).TextColor(t.LabelPrimary)
			if button.Clicked() {
				a.attachOpen = false
				item.run()
			}
		}
	})
}

// insertComposerToken appends a mention marker or opens the command palette
// by putting the slash at the start of the draft, then focuses the composer so
// the user can keep typing the file name or command.
func (a *App) insertComposerToken(c *ui.Context, token string) {
	switch token {
	case "@":
		if draft := a.draft; draft != "" &&
			!strings.HasSuffix(draft, " ") && !strings.HasSuffix(draft, "\n") &&
			!strings.HasSuffix(draft, "\t") {
			a.draft += " "
		}
		a.draft += "@"
	case "/":
		a.draft = "/" + a.draft
	}
	a.composerRef.Focus(c)
}

// compactArgs renders raw tool arguments as a single-line JSON string for the
// approval card. Invalid JSON falls back to its original text.
func compactArgs(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return string(raw)
	}
	return out.String()
}
