package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/sessionreference"
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
	sessionMentionPrefix = "session \u00b7 "
)

// sessionMentionEntry is how the flat "@" candidate list carries a session row
// through the []string signature the two composers share: a JSON object after
// sessionMentionPrefix keeps the title (which may contain any character), the
// row's relative-time detail and the session id unambiguous.
type sessionMentionEntry struct {
	// Title is the row label: the session title, falling back to the
	// localized "Untitled".
	Title string `json:"title"`
	// Label is the session's own title ("" when untitled); the inserted
	// canonical mention falls back to the session id for it, as DSH does.
	Label string `json:"label"`
	// Detail is the row's right column: the relative time, prefixed with the
	// workspace name for a session outside the current workspace.
	Detail string `json:"detail"`
	// ID is the session id the inserted canonical mention encodes.
	ID string `json:"id"`
}

// encodeSessionMention renders one session candidate for mentionEntries.
func encodeSessionMention(entry sessionMentionEntry) string {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return ""
	}
	return sessionMentionPrefix + string(encoded)
}

// decodeSessionMention parses one session candidate; ok is false for a file
// path or for a damaged encoding.
func decodeSessionMention(candidate string) (sessionMentionEntry, bool) {
	if !strings.HasPrefix(candidate, sessionMentionPrefix) {
		return sessionMentionEntry{}, false
	}
	var entry sessionMentionEntry
	if err := json.Unmarshal([]byte(strings.TrimPrefix(candidate, sessionMentionPrefix)), &entry); err != nil {
		return sessionMentionEntry{}, false
	}
	return entry, true
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

// slashVisible reports whether the token under the composer caret asks for
// the command palette: it must start with "/" and be live at the caret.
func (a *App) slashVisible() bool {
	return a.composerTrigger().kind == composerTriggerSlash
}

// closeComposerPopovers dismisses every composer-owned popover: the
// permission and model menus, the composer More menu, the attachment menu and
// the derived "/" and "@" lists. Opening another surface (Settings, Home, a
// plugin page) or another window calls it, so a menu cannot outlive the screen
// that owns it (UX-04).
func (a *App) closeComposerPopovers() {
	st := a.liveMenus()
	st.permissionOpen = false
	st.modelOpen = false
	st.moreOpen = false
	a.attachOpen = false
	// The derived lists hide while the draft is unchanged; recording the
	// current draft is their dismissal.
	st.mentionDismissedDraft = a.draft
	st.slashDismissedDraft = a.draft
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

// slashMenuVisible reports whether the "/" palette is derived from the token
// under the caret and has not been dismissed with Escape for that draft.
func (a *App) slashMenuVisible() bool {
	return !a.attachOpen && a.slashVisible() && a.liveMenus().slashDismissedDraft != a.draft
}

// visibleMentionEntries returns the "@" menu rows for the current draft, or
// nil when Escape has dismissed the menu for that draft.
func (a *App) visibleMentionEntries() []string {
	if a.attachOpen || a.liveMenus().mentionDismissedDraft == a.draft {
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
	// Escape walks up one "@" directory before it closes the menu, so a wrong
	// drill is one key away from the folder list.
	if a.mentionDrillUp() {
		return true
	}
	if a.slashMenuVisible() && st.slashDismissedDraft != a.draft {
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
			if reason := strings.TrimSpace(request.ReviewReason); reason != "" {
				ui.Text(c, a.menuText("Auto review suggested denial: ", "自动审查建议拒绝：")+reason).
					FontSize(11).FixedLineHeight(16).TextColor(t.WarningText).
					MaxLines(3).Ellipsis("…")
			}
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

// commandPalette is the "/" menu above the composer. The grouped, filtered
// panel lives in composer_slash.go; this name is what the two composers call.
func (a *App) commandPalette(c *ui.Context, t *design.Tokens) {
	a.slashCommandPalette(c, t)
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
		a.stopTurn()
		a.draft = ""
	case "/file":
		a.draft = ""
		a.runFileCommand(commandArgs(line, name))
	case "/plan":
		a.runPlanCommand(line)
	case "/feedback":
		a.draft = ""
		a.runFeedbackCommand(commandArgs(line, name))
	case "/permission":
		a.draft = ""
		a.runPermissionCommand(commandArgs(line, name))
	case "/export":
		a.draft = ""
		a.runExportCommand()
	case "/help":
		a.draft = a.slashHelpText()
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
	case "/goal":
		a.draft = ""
		a.runGoalCommand(commandArgs(line, name))
	case "/compact":
		a.draft = ""
		a.runCompactCommand(commandArgs(line, name))
	default:
		a.draft = ""
	}
}

// planCommandArg returns the text after the "/plan" token on a slash line,
// trimmed. It is "" for a bare "/plan" (also what choosing the palette row
// passes) and "off" for "/plan off".
func planCommandArg(line string) string {
	text := strings.TrimSpace(line)
	name, ok := slashCommandName(text)
	if !ok || name != "/plan" {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(text, name))
}

// runPlanCommand implements /plan, the composer's plan-mode entry (ADR 0008):
// "/plan" turns plan mode on, "/plan off" turns it off, and "/plan <message>"
// turns it on and submits the message as the next prompt. The command runs
// locally, so the slash line itself never reaches the model; only the optional
// message is sent. Plan mode is soft guidance (the plan:policy prompt), not a
// tool gate, so this only persists the flag.
//
// On Home without a session the command creates the draft session the Home send
// path would create and turns plan mode on for it, instead of only asking for a
// session (ADR 0008 §4.4).
func (a *App) runPlanCommand(line string) {
	arg := planCommandArg(line)
	if a.Ctrl == nil {
		a.draft = ""
		a.toastText("Open a session before enabling plan mode.",
			"请先打开会话再启用计划模式。")
		return
	}
	if strings.TrimSpace(a.Ctrl.CurrentSessionID()) == "" {
		a.draft = ""
		a.startPlanFromHome(arg)
		return
	}
	id := a.Ctrl.CurrentSessionID()
	if strings.EqualFold(arg, "off") {
		a.draft = ""
		reviewID := a.currentPlanDeliveryID()
		if err := a.Ctrl.SetPlanMode(id, false); err != nil {
			a.toastText(err.Error(), err.Error())
			return
		}
		// Leaving through the command is a decision on the current review, like
		// the card's Exit button: re-entering plan mode starts a fresh review.
		a.decidePlanReview(reviewID)
		a.toastText("Plan mode off", "已退出计划模式")
		return
	}
	a.draft = ""
	if err := a.Ctrl.SetPlanMode(id, true); err != nil {
		a.toastText(err.Error(), err.Error())
		return
	}
	if arg == "" {
		a.toastText("Plan mode on", "已进入计划模式")
		return
	}
	a.submitPlanMessage(arg)
}

// startPlanFromHome implements /plan while the Home composer is showing and no
// session exists: it creates the same draft session the Home send path would
// create (current workspace, Home-staged preset/access/model, staged
// attachments), turns plan mode on for it, and submits the optional message as
// its first request. Creating the session here is one shot: the command has
// already switched to the Session screen, so it cannot re-enter this path.
func (a *App) startPlanFromHome(arg string) {
	if a.Ctrl == nil {
		return
	}
	c := a.startUI().lastComposerContext
	staged := append([]homeAttachment(nil), a.homeAttachments...)
	id, ok := a.newHomeSession(c)
	if !ok {
		return
	}
	if err := a.Ctrl.SetPlanMode(id, true); err != nil {
		// The session was created only to carry this command: roll it back so a
		// failed /plan does not leave an empty session behind (UX-01).
		a.rollbackHomeSession(id, staged)
		a.toastDeferred(c, err.Error())
		return
	}
	a.draft = ""
	if arg == "" {
		a.toastDeferred(c, a.menuText("Plan mode on", "已进入计划模式"))
		return
	}
	if err := a.Ctrl.Submit(context.Background(), id, arg); err != nil {
		a.rollbackHomeSession(id, staged)
		a.toastDeferred(c, fmt.Sprintf(a.menuText("Could not send: %v", "发送失败：%v"), err))
		return
	}
	a.homeDraft = ""
}

// submitPlanMessage submits the message of "/plan <message>" after plan mode
// was enabled. With the composer frame available it goes through the regular
// send path, so a running turn keeps the persisted Queue/Interrupt behavior
// (and a failed submit shows the same toast). Without a frame, as in a
// headless call, it submits through the controller directly.
func (a *App) submitPlanMessage(text string) {
	if a.Ctrl == nil {
		return
	}
	if c := a.startUI().lastComposerContext; c != nil {
		a.submitComposerText(c, text, false)
		return
	}
	if err := a.Ctrl.Submit(context.Background(), a.Ctrl.CurrentSessionID(), text); err != nil {
		a.toastText("Could not send: "+err.Error(), "发送失败："+err.Error())
	}
}

// toastText shows one of this file's messages through the composer frame's
// context. The command catalog lives in i18n.go, which this change does not
// own, so new messages carry both languages here.
func (a *App) toastText(en, zh string) {
	a.toastDeferred(a.startUI().lastComposerContext, a.tr(en, zh))
}

// toastDeferred shows message as a toast when the given frame context can take
// one, and otherwise keeps it for the next frame's View. mygo drops Context
// toasts raised outside a build pass, which is where key handlers run: without
// this fallback a failed Enter (send, /plan, model switch) failed silently
// (UX-01/UX-03). base may be nil, as when a command runs before the composer
// has built a frame.
func (a *App) toastDeferred(base *ui.Context, message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	if base != nil && base.Valid() {
		base.Toast(message)
		return
	}
	a.queueToast(message)
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
// that ends in "@"; a space after the "@" ends the plain mention.
// mentionEntries lists the workspace entries under the directory the "@" token
// is browsing, then the matching sessions from every workspace with the current
// one's sessions first, as DSH's reference menu orders them. The token's query
// is a path: "@docs/gu" lists "docs/" for names starting with "gu" and keeps
// the relative path in the value, so a nested pick inserts the full path.
// Directories carry a trailing slash so the inserted path reads as a directory
// and the row becomes a drill target; sessions are only offered at the root and
// are inserted as the canonical @[label](dsh-session:<base64url(id)>) mention
// (see insertSessionMention). DSH's quoted @"..." form searches files only: it
// never lists sessions.
func (a *App) mentionEntries() []string {
	if a.Ctrl == nil {
		return nil
	}
	trigger := a.composerTrigger()
	if trigger.kind != composerTriggerMention {
		return nil
	}
	dir, prefix := mentionQueryPath(trigger.query)
	dir = filepath.Clean(filepath.FromSlash(dir))
	if dir == "." || strings.HasPrefix(dir, "..") || filepath.IsAbs(dir) {
		if dir != "." {
			return nil
		}
		dir = ""
	}
	lowerPrefix := strings.ToLower(prefix)
	root := strings.TrimSpace(a.Ctrl.WorkspaceRoot())
	out := make([]string, 0, maxMentionFiles+maxMentionSessions)
	if root != "" {
		base := root
		if dir != "" {
			base = filepath.Join(root, dir)
		}
		if entries, err := os.ReadDir(base); err == nil {
			for _, entry := range entries {
				name := entry.Name()
				if !strings.HasPrefix(strings.ToLower(name), lowerPrefix) {
					continue
				}
				relative := name
				if dir != "" {
					relative = filepath.ToSlash(dir) + "/" + name
				}
				if entry.IsDir() {
					relative += "/"
				}
				out = append(out, relative)
				if len(out) == maxMentionFiles {
					break
				}
			}
		}
	}
	// Sessions are workspace-level references, not children of a folder, and
	// DSH's quoted @"..." form searches files only.
	if dir != "" || trigger.quoted {
		return out
	}
	files := len(out)
	// AR-08: render from the shell's session caches, refreshed in the
	// background, so opening @ never waits on the store.
	for _, candidate := range a.mentionSessionCandidates() {
		sess := candidate.session
		label := strings.TrimSpace(sess.Title)
		title := label
		if title == "" {
			title = a.T("common.untitled")
		}
		if !strings.HasPrefix(strings.ToLower(title), lowerPrefix) {
			continue
		}
		detail := a.sessionWhen(sess.UpdatedAt)
		if candidate.workspace != "" {
			// DSH names the workspace only when the session lives outside the
			// current one, in front of the same relative time.
			detail = candidate.workspace + " \u00b7 " + detail
		}
		out = append(out, encodeSessionMention(sessionMentionEntry{
			Title:  title,
			Label:  label,
			Detail: detail,
			ID:     sess.ID,
		}))
		if len(out)-files == maxMentionSessions {
			break
		}
	}
	return out
}

// mentionChoice is one selectable row of the "@" menu: a workspace path or a
// session title, with the text its selection inserts.
type mentionChoice struct {
	label   string
	value   string
	detail  string
	session bool
	dir     bool
	// sessionID and sessionLabel are set only for a session row:
	// sessionID feeds the canonical mention and sessionLabel is the label it
	// carries (the session's own title, "" for an untitled session so the
	// mention falls back to the session id).
	sessionID    string
	sessionLabel string
}

// mentionChoices splits the flat candidate list into rows. Files keep their
// relative path (and a trailing slash for a directory, which also marks the
// row as a drill target); sessions decode their JSON-encoded row data and
// insert the canonical @[label](dsh-session:<base64url(id)>) mention.
func mentionChoices(entries []string) []mentionChoice {
	out := make([]mentionChoice, 0, len(entries))
	for _, entry := range entries {
		if session, ok := decodeSessionMention(entry); ok {
			out = append(out, mentionChoice{
				label:        session.Title,
				value:        session.Title,
				detail:       session.Detail,
				session:      true,
				sessionID:    session.ID,
				sessionLabel: session.Label,
			})
			continue
		}
		out = append(out, mentionChoice{label: entry, value: entry,
			dir: strings.HasSuffix(entry, "/")})
	}
	return out
}

// mentionMenu lists the matching files and sessions for the trailing "@"
// token. It follows DSH's listbox capture (retired reference design): the
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
			if dir, ok := a.mentionDir(); ok && dir != "" {
				a.mentionBreadcrumb(c, t, dir)
			}
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
// focus group: the group arrows move the focus, and Enter inserts the choice.
// The body (icon, quoted name, hint) picks the entry; a directory also carries a
// separate chevron control at the end that drills into it, the pointer twin of
// the composer's Tab drill, so both verbs are reachable without duplicating the
// row's accessible name.
func (a *App) mentionRow(c *ui.Context, t *design.Tokens, choice mentionChoice, index int, st *liveMenuState) {
	label := mentionDisplayLabel(choice)
	if choice.session {
		// The row says "Session · title" in the UI language instead of the
		// English-only candidate prefix, and the relative time separates two
		// sessions that share a title (UX-10).
		label = a.menuText("Session \u00b7 ", "会话 \u00b7 ") + choice.label
	}
	item := ui.Row(c).Height(34).PaddingX(design.Space2).Gap(design.Space2).
		Radius(design.RadiusSM).AlignItems(ui.Center).Shrink(0).
		Label(label).Role(ui.RoleListItem).Focusable()
	if index == 0 {
		item = item.Bind(&a.mentionRef)
	}
	if index == st.mentionIndex || item.Focused() {
		item = item.Background(t.BgHover)
	}
	if item.Focused() {
		st.mentionIndex = index
	}
	choose := func() {
		if choice.session {
			a.insertSessionMention(choice.sessionLabel, choice.sessionID)
		} else {
			a.insertMention(choice.value)
		}
		a.composerRef.Focus(c)
	}
	drill := func() {
		a.drillMention(choice)
		a.composerRef.Focus(c)
	}
	item.Children(func() {
		body := ui.Row(c).Grow(1).Gap(design.Space2).AlignItems(ui.Center).
			Role(ui.RoleNone)
		body.Children(func() {
			switch {
			case choice.session:
				ui.Icon(c, icons.NewChat()).Size(14, 14).TextColor(t.LabelSecondary)
			case choice.dir:
				ui.Icon(c, icons.Folder()).Size(14, 14).TextColor(t.LabelSecondary)
			default:
				ui.Icon(c, mentionFileIcon).Size(14, 14).TextColor(t.LabelSecondary)
			}
			ui.Text(c, label).FontSize(13).FixedLineHeight(20).
				TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			switch {
			case choice.session:
				trailing := choice.detail
				if trailing == "" {
					trailing = a.tr("Session", "会话")
				}
				// The detail is the row's second column: the workspace name
				// (only for a session outside the current workspace) followed
				// by the relative time. It is capped and ellipsized so a long
				// workspace name cannot squeeze the session title to nothing.
				ui.Text(c, trailing).FontSize(11).FixedLineHeight(16).
					TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…").
					MaxWidthPercent(55).Shrink(0)
			case choice.dir:
				// The row is a real drill target: Tab or the chevron opens the
				// directory in place.
				ui.Text(c, a.tr("Open folder", "打开文件夹")).FontSize(11).FixedLineHeight(16).
					TextColor(t.LabelTertiary).NoWrap()
				ui.Text(c, "Tab").FontSize(10).FixedLineHeight(16).TextColor(t.LabelTertiary).
					Padding(0, 4).Background(t.BgHover).Radius(design.RadiusXS)
			default:
				ui.Text(c, a.tr("File", "文件")).FontSize(11).FixedLineHeight(16).
					TextColor(t.LabelTertiary).NoWrap()
			}
		})
		body.OnClick(choose)
		if choice.dir {
			// The chevron is the pointer twin of Tab's drill: a labelled
			// button nested at the row's end, so the row body (pick the folder
			// reference) and the drill verb keep distinct accessible names.
			chevron := ui.Row(c).AlignItems(ui.Center).
				Padding(0, design.Space1).Role(ui.RoleButton).
				Label(fmt.Sprintf(a.menuText("Open folder %s", "打开文件夹 %s"), label))
			chevron.Children(func() {
				ui.Icon(c, icons.ChevronRight()).Size(12, 12).TextColor(t.LabelTertiary)
			})
			chevron.OnClick(drill)
		}
	})
	if item.Focused() && item.Shortcut(0, ui.KeyEnter) {
		choose()
	}
}

// drillMentionAt opens the highlighted "@" row when it is a directory and
// reports whether it did. The composer's Tab uses it; choosing the row with
// Enter still inserts the folder path as a reference.
func (a *App) drillMentionAt(entries []string) bool {
	choices := mentionChoices(entries)
	st := a.liveMenus()
	if st.mentionIndex < 0 || st.mentionIndex >= len(choices) {
		return false
	}
	return a.drillMention(choices[st.mentionIndex])
}

// mentionBreadcrumb is the "@" menu's path bar while it browses a directory:
// one clickable crumb per level from the workspace root down to the current
// folder, then the current directory's canonical @ path and the Esc hint.
// Every ancestor crumb carries the same drill payload a folder row does, so
// "go back to that level" and "drill into it" write the same token (DSH's
// reference breadcrumb behaves the same way). Clicking the current path keeps
// the old one-level "go up" behavior, and Esc walks up one level too.
func (a *App) mentionBreadcrumb(c *ui.Context, t *design.Tokens, dir string) {
	trigger := a.composerTrigger()
	quoted := trigger.kind == composerTriggerMention && trigger.quoted
	closed := trigger.kind == composerTriggerMention && trigger.closed
	goTo := func(token string) {
		a.replaceComposerTrigger(token)
		a.composerRef.Focus(c)
	}
	// The root crumb returns to the workspace root. A quoted token keeps its
	// quote open (`@"`) so the user can keep typing a quoted path, exactly as
	// Esc from a quoted directory does.
	rootToken := "@"
	if quoted {
		rootToken = "@" + "\""
	}
	path := "@" + dir
	if quoted || needsMentionQuotes(dir) {
		path = "@\"" + dir + "\""
	}
	segments := mentionPathSegments(dir)
	row := ui.Row(c).Height(28).Padding(design.Space1, design.Space2).
		Radius(design.RadiusSM).Gap(design.Space2).AlignItems(ui.Center)
	row.Children(func() {
		a.mentionCrumb(c, t, a.menuText("Root", "根目录"),
			a.menuText("Back to the workspace root", "返回工作区根目录"), true,
			func() { goTo(rootToken) })
		prefix := ""
		for i, segment := range segments {
			prefix += segment + "/"
			ui.Icon(c, icons.ChevronRight()).Size(10, 10).TextColor(t.LabelTertiary)
			// The last segment is the menu's current directory: its full path
			// follows as the non-drill crumb, so it needs no level button.
			if i == len(segments)-1 {
				break
			}
			level := prefix
			// The accessible name is the canonical (closed) spelling so a
			// screen reader reads the same path the row would insert, while
			// the action keeps the live token's open/closed state like a drill.
			a11y := a.menuText("Go to ", "转到 ") + a.mentionToken(level, quoted, true)
			a.mentionCrumb(c, t, segment, a11y, false, func() {
				goTo(a.mentionToken(level, quoted, closed))
			})
		}
		back := ui.Row(c).Grow(1).Height(20).PaddingX(design.Space1).
			Radius(design.RadiusXS).Gap(design.Space2).AlignItems(ui.Center).
			Role(ui.RoleButton).Focusable().
			Label(a.menuText("Back to the parent folder", "返回上级文件夹"))
		if back.Hovered() {
			back = back.Background(t.BgHover)
		}
		back.Children(func() {
			ui.Text(c, path).FontSize(12).FixedLineHeight(18).
				TextColor(t.LabelPrimary).Grow(1).MaxLines(1).Ellipsis("…")
			ui.Text(c, a.menuText("Esc to go up", "Esc 返回上级")).
				FontSize(11).FixedLineHeight(16).TextColor(t.LabelTertiary).NoWrap()
		})
		back.OnClick(func() {
			a.mentionDrillUp()
			a.composerRef.Focus(c)
		})
	})
}

// mentionCrumb is one clickable level of the "@" menu's breadcrumb: a small
// labelled button that writes the level's token and returns the focus to the
// composer. The root level also draws the folder icon the old breadcrumb had.
func (a *App) mentionCrumb(c *ui.Context, t *design.Tokens, label, a11y string, folder bool, choose func()) {
	crumb := ui.Row(c).Height(20).PaddingX(design.Space1).Radius(design.RadiusXS).
		Gap(design.Space1).AlignItems(ui.Center).Role(ui.RoleButton).Label(a11y)
	if crumb.Hovered() {
		crumb = crumb.Background(t.BgHover)
	}
	crumb.Children(func() {
		if folder {
			ui.Icon(c, icons.Folder()).Size(14, 14).TextColor(t.LabelSecondary)
		}
		ui.Text(c, label).FontSize(12).FixedLineHeight(18).
			TextColor(t.LabelSecondary).NoWrap()
	})
	crumb.OnClick(choose)
}

// mentionPathSegments splits a browsed directory path into its levels:
// "docs/sub/" -> ["docs", "sub"]; the breadcrumb draws one crumb per level.
func mentionPathSegments(dir string) []string {
	trimmed := strings.Trim(strings.TrimSpace(dir), "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
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
		a.insertSessionMention(choice.sessionLabel, choice.sessionID)
	} else {
		a.insertMention(choice.value)
	}
	return true
}

func (a *App) insertMention(path string) {
	quoted := a.composerTrigger().kind == composerTriggerMention && a.composerTrigger().quoted
	a.replaceComposerTrigger(a.mentionToken(path, quoted, true) + " ")
}

// insertSessionMention inserts the canonical session reference DSH uses:
// @[label](dsh-session:<base64url(id)>). The controller parses it before the
// turn and appends the bounded referenced-session context (see
// internal/sessionreference). The mygo text area has no inline-chip primitive,
// so the draft shows the mention as text; the transcript renders it as the
// readable @label, which is the recorded UI deviation from DSH.
func (a *App) insertSessionMention(label, sessionID string) {
	a.replaceComposerTrigger(sessionreference.FormatMention(label, sessionID) + " ")
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
			a.popoverMenuActionRow(c, t, item.label, nil, func() {
				a.attachOpen = false
				item.run()
			})
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
	// The token was inserted outside the editor: put the caret after it so the
	// next frame derives the trigger from the end of the new token (the Home
	// composer is rebuilt when the attach menu closes, resetting its caret).
	a.setComposerCaretAfterInsert()
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
