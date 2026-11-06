package shell

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
)

// This file holds the composer's "/" command palette: its catalog, the trigger
// token detection both derived composer popups share, the grouped/filtered
// panel and its keyboard state. The palette rows replace the old flat button
// list with DSH's layout: an "add" section (actions that bring something into
// the next message) over a "commands" section (session controls), each row
// carrying an icon, a localized title, the command name and a right-aligned
// hint.

// slashGroup is which section of the palette a command belongs to.
type slashGroup int

const (
	slashGroupAdd slashGroup = iota
	slashGroupCommand
)

// slashCommand is one row of the composer's "/" palette. name is the typed
// command and the only spelling the dispatcher accepts; title and hint are the
// localized row face.
type slashCommand struct {
	name    string
	titleEN string
	titleZH string
	hintEN  string
	hintZH  string
	group   slashGroup
	icon    func() *ui.SVG
}

// Inline 16px line icons for the command faces that have no stock glyph in
// internal/icons; they follow the shell's icon style (16 viewBox, currentColor
// stroke, round caps). The rest reuse the shell's existing line icons.
var (
	slashStopIcon     = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round"><rect x="4" y="4" width="8" height="8" rx="1.6"/></svg>`))
	slashFileIcon     = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d="M12.8 6.9 7.5 12.2a2.6 2.6 0 0 1-3.7-3.7l5.6-5.6a1.8 1.8 0 0 1 2.5 2.5l-5.5 5.5a.9.9 0 0 1-1.3-1.3l5-5"/></svg>`))
	slashFeedbackIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d="M14.2 2.2 2.4 6.5l4.3 2.2 2.2 4.3z"/><path d="M14.2 2.2 6.7 8.7"/></svg>`))
	slashExportIcon   = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d="M8 2.6v6.8M5 6.6 8 9.6l3-3"/><path d="M2.8 11.2v1.4a1.4 1.4 0 0 0 1.4 1.4h7.6a1.4 1.4 0 0 0 1.4-1.4v-1.4"/></svg>`))
	slashGoalIcon     = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><circle cx="8" cy="8" r="5.4"/><circle cx="8" cy="8" r="1.7"/></svg>`))
	slashCompactIcon  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.3" stroke-linecap="round" stroke-linejoin="round"><path d="M8 1.9v4.2M6.2 4.3 8 6.1l1.8-1.8M8 14.1V9.9M6.2 11.7 8 9.9l1.8 1.8"/></svg>`))
	// mentionFileIcon is the plain-document row glyph of the "@" menu; its
	// folder and session faces reuse internal/icons.
	mentionFileIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 1.6h5l3 3v9.8H4z"/><path d="M9 1.8v3h3"/></svg>`))
)

// slashCommands is the composer command catalog: every entry runs locally (its
// slash line never reaches the model) and every entry has a real handler. The
// order is the menu order: the add section first (file, plan, feedback), then
// the session commands, with the new DSH-aligned ones before the older shell
// commands.
var slashCommands = []slashCommand{
	{"/file", "Attach files", "添加文件",
		"Pick files to attach to the next message (/file <path> for a workspace file)",
		"选择要附加到下一条消息的文件（/file <路径> 可直接附加工作区文件）",
		slashGroupAdd, func() *ui.SVG { return slashFileIcon }},
	{"/plan", "Plan mode", "计划模式",
		"Enter or leave plan mode; /plan <message> starts a turn with it",
		"进入或退出计划模式；/plan <消息> 以计划模式发送",
		slashGroupAdd, func() *ui.SVG { return icons.Sparkles() }},
	{"/feedback", "Feedback", "反馈",
		"Record good/bad feedback for the last answer, or session feedback",
		"记录最后一条回答的好/差反馈，或本次会话的反馈",
		slashGroupAdd, func() *ui.SVG { return slashFeedbackIcon }},
	{"/permission", "Permission", "权限",
		"Change the access mode: read-only, workspace, full or auto",
		"切换访问模式：只读、工作区、完全访问或自动审查",
		slashGroupCommand, func() *ui.SVG { return homeIconShield }},
	{"/model", "Model", "模型",
		"Choose the model this session uses",
		"选择本会话使用的模型",
		slashGroupCommand, func() *ui.SVG { return icons.Data() }},
	{"/memory", "Memory", "记忆",
		"Open Settings › Memory",
		"打开 设置 › 记忆",
		slashGroupCommand, func() *ui.SVG { return memoryIcon }},
	{"/goal", "Session goal", "会话目标",
		"Create, pause, resume or clear the session goal",
		"创建、暂停、恢复或清除会话目标",
		slashGroupCommand, func() *ui.SVG { return slashGoalIcon }},
	{"/compact", "Compact conversation", "压缩对话",
		"Replace older history with a summary now",
		"立即用摘要替换较早的历史",
		slashGroupCommand, func() *ui.SVG { return slashCompactIcon }},
	{"/export", "Export log", "下载日志",
		"Export this session's log to a file",
		"将会话日志导出为文件",
		slashGroupCommand, func() *ui.SVG { return slashExportIcon }},
	{"/clear", "Clear draft", "清空草稿",
		"Clear the composer draft",
		"清空输入框草稿",
		slashGroupCommand, func() *ui.SVG { return icons.Reload() }},
	{"/stop", "Stop turn", "停止回合",
		"Stop the running turn",
		"停止当前回合",
		slashGroupCommand, func() *ui.SVG { return slashStopIcon }},
	{"/help", "Help", "帮助",
		"List the composer commands",
		"列出输入框命令",
		slashGroupCommand, func() *ui.SVG { return pluginIconInfo }},
	{"/new", "New session", "新建会话",
		"Start a new session",
		"新建会话",
		slashGroupCommand, func() *ui.SVG { return icons.NewChat() }},
	{"/settings", "Settings", "设置",
		"Open Settings",
		"打开设置",
		slashGroupCommand, func() *ui.SVG { return settingsIconGear }},
	{"/automate", "Automation tasks", "自动化任务",
		"Open Automation tasks",
		"打开自动化任务",
		slashGroupCommand, func() *ui.SVG { return icons.Clock() }},
	{"/plugins", "Plugins", "插件",
		"Open Plugins",
		"打开插件",
		slashGroupCommand, func() *ui.SVG { return icons.Plugin() }},
}

// slashMenuState is the palette's interaction state: the highlighted row and
// the query it belongs to, so typing a new query starts the highlight at the
// first match. It is keyed by the App like the shell's other view state, so
// this file adds no fields to App or liveMenuState.
type slashMenuState struct {
	index int
	key   string
	set   bool
	// deferredToast is a message a key-triggered command raised while no frame
	// was building (mygo only accepts toasts during a build); View shows it on
	// the next frame so command feedback from Enter is never silent.
	deferredToast string

	// caret/caretDraft are the composer caret recorded while the input built,
	// together with the draft it belonged to. The popups are derived from the
	// token at the caret (not the whole draft), so the "@" list and the "/"
	// palette are mutually exclusive and a pick replaces exactly that token.
	// The composers build the input after the popup column, so the pair also
	// guards against one-frame-stale carets: a mismatch falls back to the end
	// of the draft.
	caret      int
	caretDraft string
	caretSet   bool
	// caretPending asks the next composer build to push the caret into the
	// editor: a programmatic replacement (mention pick or folder drill) changes
	// the draft outside the editor, whose stale selection would otherwise keep
	// deriving the next trigger from the old position.
	caretPending bool
}

// slashMenuStates maps each App to its palette highlight state; the entry lives
// as long as the App (the real app has one).
var slashMenuStates sync.Map // map[*App]*slashMenuState

// slashMenu returns the palette state of a, making it on first use.
func (a *App) slashMenu() *slashMenuState {
	if s, ok := slashMenuStates.Load(a); ok {
		return s.(*slashMenuState)
	}
	s := &slashMenuState{}
	actual, _ := slashMenuStates.LoadOrStore(a, s)
	return actual.(*slashMenuState)
}

// filterSlashCommands returns the commands matching query in menu order. An
// empty query keeps the whole catalog; a prefix match outranks a substring
// match; the command name, its localized title and its hint are searched; rows
// keep the catalog order for equal ranks, as DSH's substring mode does.
func filterSlashCommands(query string) []slashCommand {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return slashCommands
	}
	prefix := make([]slashCommand, 0, len(slashCommands))
	matches := make([]slashCommand, 0, len(slashCommands))
	for _, command := range slashCommands {
		name := strings.TrimPrefix(command.name, "/")
		title := strings.ToLower(command.titleEN + " " + command.titleZH)
		hint := strings.ToLower(command.hintEN + " " + command.hintZH)
		rank := 0 // 0 = no match, 1 = substring, 2 = prefix
		for _, haystack := range []string{name, command.titleEN, command.titleZH, title, hint} {
			index := strings.Index(haystack, query)
			if index < 0 {
				continue
			}
			if index == 0 {
				rank = 2
				break
			}
			if rank < 1 {
				rank = 1
			}
		}
		switch rank {
		case 2:
			prefix = append(prefix, command)
		case 1:
			matches = append(matches, command)
		}
	}
	return append(prefix, matches...)
}

// slashSection is one titled group of the palette.
type slashSection struct {
	group    slashGroup
	commands []slashCommand
}

// slashPaletteSections splits matching commands into the add and command
// sections in catalog order; empty sections are dropped.
func slashPaletteSections(commands []slashCommand) []slashSection {
	sections := make([]slashSection, 0, 2)
	for _, group := range []slashGroup{slashGroupAdd, slashGroupCommand} {
		var rows []slashCommand
		for _, command := range commands {
			if command.group == group {
				rows = append(rows, command)
			}
		}
		if len(rows) > 0 {
			sections = append(sections, slashSection{group: group, commands: rows})
		}
	}
	return sections
}

// composerTriggerKind is the kind of derived composer popup the token under
// the caret opens.
type composerTriggerKind int

const (
	composerTriggerNone composerTriggerKind = iota
	composerTriggerSlash
	composerTriggerMention
)

// composerTrigger is the trigger token under the composer caret: its kind, the
// query after the trigger character, and its span in runes (start inclusive,
// end exclusive). The span is the range a pick will replace once the mention
// menu starts using it; the command palette only needs kind and query.
type composerTrigger struct {
	kind  composerTriggerKind
	query string
	start int
	end   int
	// quoted marks a quoted "@\"...\"" / @'...' path token that may
	// contain spaces; insertion and drilling keep the quoted form. closed says
	// whether that token already has its closing quote: an open token is still
	// being typed, so a drill keeps it open for the next keystroke.
	quoted bool
	closed bool
}

// draftRuneLen returns the draft length in runes.
func draftRuneLen(draft string) int { return utf8.RuneCountInString(draft) }

// isTriggerBoundary reports whether the rune before a trigger character keeps
// the character a completion trigger: the start of the draft, whitespace, or
// punctuation — never a letter, digit or underscore (an email's "@").
func isTriggerBoundary(r rune) bool {
	return unicode.IsSpace(r) || !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
}

// activeComposerTrigger finds the trigger token under caret, following DSH's
// pipeline rules: a token must start the draft or follow whitespace or
// punctuation; "/" inside a URL ("https:/…", "//…") is ordinary text; "@" is
// checked first, so a caret inside an "@" token opens the mention menu instead
// of the command palette (the two popups are mutually exclusive).
func activeComposerTrigger(draft string, caret int) composerTrigger {
	runes := []rune(draft)
	if caret < 0 {
		caret = 0
	}
	if caret > len(runes) {
		caret = len(runes)
	}
	if len(runes) == 0 || caret == 0 {
		return composerTrigger{}
	}
	// "@": the nearest plain or quoted token to the left of the caret.
	if mention, ok := mentionTriggerAt(runes, caret); ok {
		return mention
	}
	// "/": scan back to the token's slash, rejecting URL slashes.
	for i := caret - 1; i >= 0; i-- {
		if unicode.IsSpace(runes[i]) {
			return composerTrigger{}
		}
		if runes[i] != '/' {
			continue
		}
		if i > 0 && !isTriggerBoundary(runes[i-1]) {
			continue
		}
		// "//": neither slash of a double slash is a command trigger.
		if (i > 0 && runes[i-1] == '/') || (i+1 < len(runes) && runes[i+1] == '/') {
			continue
		}
		// "https:/": a slash after a URL scheme's colon is URL text.
		if i >= 2 && runes[i-1] == ':' && !unicode.IsSpace(runes[i-2]) {
			continue
		}
		return composerTrigger{kind: composerTriggerSlash,
			query: string(runes[i+1 : caret]), start: i, end: caret}
	}
	return composerTrigger{}
}

// unquotedMentionTrigger finds the nearest plain "@" token ending at the
// caret: the token runs back to its "@" and may not contain whitespace.
func unquotedMentionTrigger(runes []rune, caret int) (composerTrigger, bool) {
	for i := caret - 1; i >= 0; i-- {
		if unicode.IsSpace(runes[i]) {
			return composerTrigger{}, false
		}
		if runes[i] == '@' && (i == 0 || isTriggerBoundary(runes[i-1])) {
			return composerTrigger{kind: composerTriggerMention,
				query: string(runes[i+1 : caret]), start: i, end: caret}, true
		}
	}
	return composerTrigger{}, false
}

// quotedMentionTrigger finds the nearest @"..." or @'...' token that contains
// the caret. The closing quote is optional while the path is being typed; when
// it exists the token (and the replacement span) includes it, so a pick
// replaces the whole quoted reference even when the caret is in the middle.
func quotedMentionTrigger(runes []rune, caret int) (composerTrigger, bool) {
	for i := caret - 1; i >= 0; i-- {
		if runes[i] != '@' || i+1 >= len(runes) {
			continue
		}
		quote := runes[i+1]
		if quote != '"' && quote != '\'' {
			continue
		}
		if i > 0 && !isTriggerBoundary(runes[i-1]) {
			continue
		}
		contentStart := i + 2
		if caret < contentStart {
			continue
		}
		closeAt := -1
		for j := contentStart; j < len(runes); j++ {
			// A backslash escapes the next rune, so an escaped quote inside the
			// path is content, not the closing delimiter.
			if runes[j] == '\\' && j+1 < len(runes) {
				j++
				continue
			}
			if runes[j] == quote {
				closeAt = j
				break
			}
		}
		tokenEnd := caret
		contentEnd := caret
		if closeAt >= 0 {
			tokenEnd = closeAt + 1
			if caret > tokenEnd {
				// The caret is past this quoted token; an earlier "@" may
				// still be live, so keep scanning right-to-left.
				continue
			}
			if caret == tokenEnd {
				contentEnd = closeAt
			}
		}
		if contentEnd < contentStart {
			contentEnd = contentStart
		}
		return composerTrigger{kind: composerTriggerMention, quoted: true,
			closed: closeAt >= 0, query: unescapeMentionPath(string(runes[contentStart:contentEnd])),
			start: i, end: tokenEnd}, true
	}
	return composerTrigger{}, false
}

// mentionTriggerAt picks the mention token closest to the caret: the quoted
// and unquoted spellings can overlap (a quoted token starts with "@" too), so
// the candidate whose "@" is nearest to the caret wins.
func mentionTriggerAt(runes []rune, caret int) (composerTrigger, bool) {
	unquoted, hasUnquoted := unquotedMentionTrigger(runes, caret)
	quoted, hasQuoted := quotedMentionTrigger(runes, caret)
	switch {
	case hasUnquoted && hasQuoted:
		if quoted.start >= unquoted.start {
			return quoted, true
		}
		return unquoted, true
	case hasQuoted:
		return quoted, true
	case hasUnquoted:
		return unquoted, true
	}
	return composerTrigger{}, false
}

// needsMentionQuotes reports whether a workspace path must be written as a
// quoted "@\"...\"" token: paths with spaces (or quote characters) cannot be
// expressed by the plain spelling.
func needsMentionQuotes(path string) bool {
	return strings.ContainsAny(path, " \t\r\n\"'")
}

// escapeMentionPath escapes a path for the quoted @"..." token grammar. The
// double quote and the backslash are the only characters that grammar treats
// specially, so a path like a"b.txt is inserted as @"a\"b.txt" and parses back
// to the exact workspace path instead of ending the token at the inner quote.
func escapeMentionPath(path string) string {
	if !strings.ContainsAny(path, "\\\"") {
		return path
	}
	var b strings.Builder
	for _, r := range path {
		if r == '\\' || r == '"' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// unescapeMentionPath reverses escapeMentionPath: a backslash makes the next
// rune literal, so the parsed query is the real workspace path. A trailing
// backslash (an escape still being typed) stays literal.
func unescapeMentionPath(content string) string {
	if !strings.Contains(content, "\\") {
		return content
	}
	var b strings.Builder
	escaped := false
	for _, r := range content {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		b.WriteRune(r)
	}
	if escaped {
		b.WriteByte('\\')
	}
	return b.String()
}

// mentionToken spells a workspace path as an @ token, quoting it when needed or
// when the live token already uses the quoted form. Inside the quotes the path
// is escaped, so a path containing a double quote round-trips through the
// token grammar. A drill that keeps the token open (no closing quote yet) leaves
// the quote open too, so the next keystroke continues the path inside the
// quotes; a completed pick uses the canonical closed form.
func (a *App) mentionToken(path string, quoted, closed bool) string {
	if quoted || needsMentionQuotes(path) {
		escaped := escapeMentionPath(path)
		if !closed {
			return "@\"" + escaped
		}
		return "@\"" + escaped + "\""
	}
	return "@" + path
}

// mentionDisplayLabel is the row label for a workspace path: quoted paths show
// their escaped spelling so the row and the composer token match.
func mentionDisplayLabel(choice mentionChoice) string {
	if choice.session || !needsMentionQuotes(choice.value) {
		return choice.label
	}
	return "@\"" + escapeMentionPath(choice.value) + "\""
}

// recordComposerCaret remembers the composer caret for trigger detection. The
// composer builds its input after the popup column, so the pair (caret, draft)
// is state from the previous frame; composerTrigger only trusts it while the
// draft still matches, and falls back to the end of the draft otherwise.
func (a *App) recordComposerCaret(input ui.Element) {
	st := a.slashMenu()
	if st.caretPending {
		// The composer's text changed under the editor; align its selection with
		// the replacement before reading it back, or the next trigger would be
		// derived from the old caret.
		caret := st.caret
		if limit := draftRuneLen(a.draft); caret > limit {
			caret = limit
		}
		if caret < 0 {
			caret = 0
		}
		input.SetTextSelection(caret, caret)
		st.caretPending = false
	}
	_, caret := input.TextAnchorCaret()
	limit := draftRuneLen(a.draft)
	// Inserting the popup column above the composer shifts the composer's
	// element ids; mygo then recreates the editor and its selection falls back
	// to the end of the text. Keep the caret the popup is derived from instead
	// of adopting that reset: a real edit changes the draft and a real arrow
	// move lands inside the text, so only an end-of-text caret with an
	// unchanged draft counts as the reset.
	if st.caretSet && st.caretDraft == a.draft && caret == limit && st.caret < limit {
		input.SetTextSelection(st.caret, st.caret)
		caret = st.caret
	}
	st.caret = caret
	st.caretDraft = a.draft
	st.caretSet = true
}

// setComposerCaretAfterInsert places the caret after an inserted token so the
// next frame derives the trigger from the same position the user sees.
func (a *App) setComposerCaretAfterInsert() {
	st := a.slashMenu()
	st.caret = draftRuneLen(a.draft)
	st.caretDraft = a.draft
	st.caretSet = true
	st.caretPending = true
}

// composerTrigger returns the live trigger of this App at the caret the
// composer recorded last frame (falling back to the end of the draft).
func (a *App) composerTrigger() composerTrigger {
	caret := draftRuneLen(a.draft)
	if st := a.slashMenu(); st.caretSet && st.caretDraft == a.draft && st.caret >= 0 {
		if st.caret < caret {
			caret = st.caret
		}
	}
	return activeComposerTrigger(a.draft, caret)
}

// replaceComposerTrigger replaces the trigger token under the caret and leaves
// the text around it untouched, so a mid-message "/plan" or "@file" can be
// completed in place. A replacement ending in a space swallows one following
// space, so inserting a file mention next to " this" does not double the gap.
func (a *App) replaceComposerTrigger(replacement string) {
	trigger := a.composerTrigger()
	runes := []rune(a.draft)
	start, end := trigger.start, trigger.end
	if trigger.kind == composerTriggerNone || start < 0 || end < start || end > len(runes) {
		return
	}
	if strings.HasSuffix(replacement, " ") && end < len(runes) && runes[end] == ' ' {
		end++
	}
	a.draft = string(runes[:start]) + replacement + string(runes[end:])
	a.setComposerCaretAfterInsert()
}

// mentionQueryPath splits the "@" token query into the directory being browsed
// and the name prefix: "@docs/gu" browses "docs/" for names starting "gu";
// "@do" is the workspace root. Nested drilling is just a path query, so the
// composer draft itself is the path state.
func mentionQueryPath(query string) (dir, prefix string) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", ""
	}
	if index := strings.LastIndex(query, "/"); index >= 0 {
		return query[:index+1], query[index+1:]
	}
	return "", query
}

// mentionDir returns the directory the "@" menu is browsing, "" at the
// workspace root, and ok=false when no mention token is live.
func (a *App) mentionDir() (dir string, ok bool) {
	trigger := a.composerTrigger()
	if trigger.kind != composerTriggerMention {
		return "", false
	}
	dir, _ = mentionQueryPath(trigger.query)
	return dir, true
}

// drillMention opens the highlighted directory row in place: the "@" token
// becomes the folder path (with a trailing slash and without trailing space)
// and the menu stays open on that directory's contents.
func (a *App) drillMention(choice mentionChoice) bool {
	if !choice.dir {
		return false
	}
	content := strings.TrimSuffix(choice.value, "/") + "/"
	trigger := a.composerTrigger()
	quoted := trigger.kind == composerTriggerMention && trigger.quoted
	closed := trigger.kind == composerTriggerMention && trigger.closed
	a.replaceComposerTrigger(a.mentionToken(content, quoted, closed))
	return true
}

// mentionDrillUp leaves the current directory for its parent, and the root for
// no trigger: "@docs/sub/" -> "@docs/" -> "@" -> "". Esc uses it before
// dismissing the menu, so Esc walks up one level first.
func (a *App) mentionDrillUp() bool {
	dir, ok := a.mentionDir()
	if !ok {
		return false
	}
	trigger := a.composerTrigger()
	quoted := trigger.kind == composerTriggerMention && trigger.quoted
	closed := trigger.kind == composerTriggerMention && trigger.closed
	// A quoted root stays open (`@"`) so the user can keep typing a quoted
	// path; the plain root token is just "@".
	token := func(path string) string {
		if path == "" && quoted {
			return "@" + "\""
		}
		return a.mentionToken(path, quoted, closed)
	}
	if dir != "" {
		// Inside a browsed folder, a typed filter is cleared before the menu
		// walks up: "@docs/gu" -> "@docs/", then "@docs/" -> "@".
		if _, prefix := mentionQueryPath(trigger.query); prefix != "" {
			a.replaceComposerTrigger(token(dir))
			return true
		}
	}
	if dir == "" {
		return false
	}
	trimmed := strings.TrimSuffix(dir, "/")
	parent := ""
	if index := strings.LastIndex(trimmed, "/"); index >= 0 {
		parent = trimmed[:index+1]
	}
	a.replaceComposerTrigger(token(parent))
	return true
}

// slashPaletteViewport bounds the scrollable row area so a long command list
// never pushes the composer off a short window.
func (a *App) slashPaletteViewport(c *ui.Context, commands []slashCommand, sections int) float32 {
	height := float32(len(commands))*32 + float32(sections)*22 + 2*design.Space1
	if len(commands) == 0 {
		height = 44
	}
	_, windowH := c.Size()
	limit := windowH * 0.55
	if limit < 220 {
		limit = 220
	}
	if limit > 520 {
		limit = 520
	}
	if height > limit {
		height = limit
	}
	return height
}

// slashCommandPalette is the "/" menu above the composer: the add section over
// the commands section, filtered by the token under the caret. Rows carry an
// icon, the localized title, the command name and a right-aligned hint; the
// highlighted row follows the palette state and is activated by a click (the
// keyboard wiring lives with the composer's key handling).
func (a *App) slashCommandPalette(c *ui.Context, t *design.Tokens) {
	// runSlashCommand has no context parameter (the compositors call it with
	// the bare line), so keep the frame's context for the toast a command may
	// show. The palette is rebuilt on every frame whose token is live, which is
	// exactly the frame whose click runs a command.
	a.startUI().lastComposerContext = c
	trigger := a.composerTrigger()
	commands := filterSlashCommands(trigger.query)
	sections := slashPaletteSections(commands)
	st := a.slashMenu()
	if !st.set || st.key != trigger.query {
		st.key = trigger.query
		st.set = true
		st.index = 0
	}
	if len(commands) == 0 {
		st.index = -1
	} else if st.index < 0 {
		st.index = 0
	} else if st.index >= len(commands) {
		st.index = len(commands) - 1
	}
	// The palette spans the composer's own column, as DSH's command panel
	// does, so the right-aligned hints have room and the rows line up with the
	// input below.
	width := a.composerPopupWidthFor(c)
	if width <= 0 {
		width = 440
	}
	viewport := a.slashPaletteViewport(c, commands, len(sections))
	ui.Row(c).Justify(ui.Center).Children(func() {
		design.Apply(ui.Column(c), t.Panel()).Width(width).
			Padding(design.Space1).Gap(1).Children(func() {
			ui.Scroll(c).Height(viewport).Children(func() {
				list := ui.Column(c).Role(ui.RoleList).
					Label(a.menuText("Command suggestions", "命令建议"))
				var active ui.Element
				hasActive := false
				list.Children(func() {
					index := 0
					for _, section := range sections {
						title := a.menuText("Add", "添加")
						if section.group == slashGroupCommand {
							title = a.menuText("Commands", "指令")
						}
						a.slashSectionTitle(c, t, title)
						for _, command := range section.commands {
							row := a.slashCommandRow(c, t, command, index == st.index)
							if index == st.index {
								active = row
								hasActive = true
							}
							index++
						}
					}
					if len(commands) == 0 {
						ui.Text(c, a.menuText("No matching commands", "没有匹配的命令")).
							FontSize(12).FixedLineHeight(18).TextColor(t.LabelTertiary).
							Padding(design.Space1, design.Space2)
					}
				})
				if hasActive {
					list.ActiveDescendant(active)
				}
			})
			ui.Text(c, a.menuText(
				"Type to filter · ↑↓ select · Tab or Enter run · Esc close. Unknown commands are sent to the model.",
				"输入即过滤 · ↑↓ 选择 · Tab 或 Enter 执行 · Esc 关闭。未知命令会发送给模型。")).
				FontSize(11).FixedLineHeight(16).TextColor(t.LabelTertiary).
				MaxLines(2).Padding(design.Space1, design.Space2)
		})
	})
}

// slashSectionTitle is one group heading of the palette.
func (a *App) slashSectionTitle(c *ui.Context, t *design.Tokens, label string) {
	ui.Row(c).Height(22).PaddingX(design.Space2).AlignItems(ui.Center).
		Label(label).Role(ui.RoleHeading).Children(func() {
		ui.Text(c, label).FontSize(11).FixedLineHeight(16).TextColor(t.LabelTertiary)
	})
}

// slashCommandRow is one command row: icon, localized title, command name and a
// right-aligned hint. The row's accessible name stays the typed command so the
// tester and assistive technology address it by "/name".
func (a *App) slashCommandRow(c *ui.Context, t *design.Tokens, command slashCommand, selected bool) ui.Element {
	row := ui.Row(c).FillWidth().MinHeight(design.Space8).
		Padding(design.Space1, design.Space2).Gap(design.Space2).
		Radius(design.RadiusSM).AlignItems(ui.Center).
		Label(command.name).Role(ui.RoleMenuItem).Focusable()
	if selected || row.Hovered() {
		row = row.Background(t.BgHover)
	}
	row.Children(func() {
		ui.Icon(c, command.icon()).Size(16, 16).TextColor(t.LabelSecondary)
		ui.Text(c, a.tr(command.titleEN, command.titleZH)).FontSize(13).
			FixedLineHeight(18).TextColor(t.LabelPrimary).NoWrap()
		ui.Text(c, command.name).FontSize(11).FixedLineHeight(16).
			TextColor(t.LabelTertiary).NoWrap()
		ui.Spacer(c).Grow(1)
		ui.Text(c, a.tr(command.hintEN, command.hintZH)).FontSize(11).
			FixedLineHeight(16).TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…")
	})
	row.OnClick(func() { a.runSlashCommand(command.name) })
	return row
}

// moveSlash moves the palette highlight by delta; the next frame clamps it to
// the filtered list, so this cannot highlight a row that is not there.
func (a *App) moveSlash(delta int) {
	st := a.slashMenu()
	if !st.set {
		st.set = true
	}
	st.index += delta
	if st.index < 0 {
		st.index = 0
	}
}

// chooseSlash runs the highlighted palette row. It reports false when the
// current query matches nothing, so Enter can fall back to the typed-line
// dispatch (an unknown command is sent to the model, as documented).
func (a *App) chooseSlash(k ui.TextKey) bool {
	st := a.slashMenu()
	commands := filterSlashCommands(a.composerTrigger().query)
	if st.index < 0 || st.index >= len(commands) {
		return false
	}
	command := commands[st.index]
	a.draft = ""
	k.SetText("")
	a.runSlashCommand(command.name)
	return true
}

// commandArgs returns the text after the command token on a slash line, with
// surrounding whitespace trimmed. It is "" for a bare command.
func commandArgs(line, name string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), name))
}

// permissionsByAlias maps the /permission argument spellings to the session's
// canonical permission values.
var permissionsByAlias = map[string]string{
	"read-only":   session.PermissionAsk,
	"readonly":    session.PermissionAsk,
	"read":        session.PermissionAsk,
	"workspace":   session.PermissionWorkspace,
	"write":       session.PermissionWorkspace,
	"full":        session.PermissionFull,
	"full-access": session.PermissionFull,
	"auto":        session.PermissionAutoReview,
	"review":      session.PermissionAutoReview,
}

// runPermissionCommand implements /permission [read-only|workspace|full|auto]:
// the bare command reports the current access mode and the accepted values;
// the argument form applies the mode the way the composer's access menu does —
// Home stages it for the next session, a Session writes its own override.
func (a *App) runPermissionCommand(args string) {
	current := a.composerPermissionLabel(a.composerPermissionMode())
	args = strings.ToLower(strings.TrimSpace(args))
	if args == "" {
		a.toastText(
			"Access mode: "+current+". Use /permission read-only|workspace|full|auto.",
			"当前访问模式："+current+"。用法：/permission read-only|workspace|full|auto。")
		return
	}
	value, ok := permissionsByAlias[args]
	if !ok {
		a.toastText(
			"Unknown access mode \""+args+"\". Use read-only, workspace, full or auto.",
			"未知的访问模式“"+args+"”。可用：read-only、workspace、full、auto。")
		return
	}
	if a.Ctrl == nil {
		a.toastText("Access mode needs a live session controller.",
			"访问模式需要实时会话控制器。")
		return
	}
	label := a.composerPermissionLabel(value)
	if a.backgroundScreen() == Home {
		// Home stages the access mode for the next session it creates, exactly
		// like the composer's access menu.
		a.homePermission = value
		a.toastText("Home access mode: "+label, "首页访问模式："+label)
		return
	}
	id := strings.TrimSpace(a.Ctrl.CurrentSessionID())
	if id == "" {
		a.toastText("Open a session before changing the access mode.",
			"请先打开会话再切换访问模式。")
		return
	}
	a.Ctrl.SetSessionPermission(id, value)
	a.toastText("Access mode: "+label, "访问模式："+label)
}

// runFileCommand implements /file [path]. The bare command opens the real file
// picker (or the deterministic fallback when no desktop backend exists); a
// workspace-relative path is attached directly after checking it resolves under
// the session workspace.
func (a *App) runFileCommand(args string) {
	c := a.startUI().lastComposerContext
	if c == nil {
		return
	}
	args = strings.TrimSpace(args)
	if args == "" {
		a.chooseAttachmentFiles(c)
		return
	}
	path := strings.Trim(args, `"'`)
	root := strings.TrimSpace(a.workspaceRoot())
	if root == "" {
		a.toastText("Open a workspace before attaching a file by path.",
			"请先打开工作区再按路径添加附件。")
		return
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		a.toastText("Could not resolve the workspace root.", "无法解析工作区路径。")
		return
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(rootAbs, abs)
	}
	abs, err = filepath.Abs(abs)
	if err != nil {
		a.toastText("Could not resolve the file path.", "无法解析文件路径。")
		return
	}
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		a.toastText("Attachments must be inside the session workspace.",
			"附件必须位于会话工作区内。")
		return
	}
	if a.backgroundScreen() == Home {
		// The Home composer owns its files with the Home draft until its Send
		// creates the session.
		a.stageHomeAttachments(c, []string{abs})
		return
	}
	id := strings.TrimSpace(a.Ctrl.CurrentSessionID())
	if id == "" {
		a.toastText("Open a session before attaching files.",
			"请先打开会话再添加附件。")
		return
	}
	if _, err := a.Ctrl.AttachFiles(context.Background(), id, []string{abs}); err != nil {
		a.toastText("Could not attach the file: "+err.Error(),
			"无法添加附件："+err.Error())
		return
	}
	a.toastText("Attached "+filepath.Base(abs), "已添加附件："+filepath.Base(abs))
}

// runExportCommand implements /export: it exports the current session's log
// through the session-header download path (the save-dialog seam when the host
// provides one, a deterministic workspace path otherwise).
func (a *App) runExportCommand() {
	c := a.startUI().lastComposerContext
	if c == nil {
		return
	}
	if a.Ctrl == nil {
		a.toastText("Session log export is not available.", "会话日志导出不可用。")
		return
	}
	sess, err := a.Ctrl.CurrentSession()
	if err != nil || sess == nil {
		a.toastText("Open a session before exporting its log.",
			"请先打开会话再导出日志。")
		return
	}
	a.downloadSessionLog(c, *sess)
}

// lastAssistantMessage returns the newest assistant answer with text, the
// message /feedback applies to.
func (a *App) lastAssistantMessage() (state.Message, bool) {
	snap := a.currentSnapshot()
	if snap == nil {
		return state.Message{}, false
	}
	for i := len(snap.Messages) - 1; i >= 0; i-- {
		message := snap.Messages[i]
		if message.Role == state.RoleAssistant && strings.TrimSpace(message.Text) != "" {
			return message, true
		}
	}
	return state.Message{}, false
}

// runFeedbackCommand implements /feedback [good|bad]: with a rating it records
// the rating on the newest assistant answer; without one it records a
// session-level feedback row. Both are the same persisted rows the transcript's
// thumbs-up/down actions write.
func (a *App) runFeedbackCommand(args string) {
	if a.Ctrl == nil || a.Ctrl.Store() == nil {
		a.toastText("Feedback is not available.", "反馈不可用。")
		return
	}
	id := strings.TrimSpace(a.Ctrl.CurrentSessionID())
	if id == "" {
		a.toastText("Open a session before recording feedback.",
			"请先打开会话再记录反馈。")
		return
	}
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "good", "bad":
		message, ok := a.lastAssistantMessage()
		if !ok {
			a.toastText("There is no assistant answer to rate yet.",
				"还没有可评价的回答。")
			return
		}
		messageID := message.MsgID
		if messageID == "" {
			messageID = "last"
		}
		if err := a.Ctrl.RecordFeedback(context.Background(), id, messageID,
			strings.ToLower(strings.TrimSpace(args)), ""); err != nil {
			a.toastText("Could not record feedback: "+err.Error(),
				"反馈记录失败："+err.Error())
			return
		}
		a.toastText("Feedback recorded", "反馈已记录")
	case "":
		if err := a.Ctrl.RecordFeedback(context.Background(), id, "session", "recorded", ""); err != nil {
			a.toastText("Could not record feedback: "+err.Error(),
				"反馈记录失败："+err.Error())
			return
		}
		a.toastText("Session feedback recorded", "会话反馈已记录")
	default:
		a.toastText("Usage: /feedback good|bad (no argument records session feedback).",
			"用法：/feedback good|bad（不带参数记录会话反馈）。")
	}
}

// slashHelpText is /help's draft text: the full command list, the compaction
// note (automatic and manual), and the documented fate of unknown slash lines.
func (a *App) slashHelpText() string {
	return a.menuText(
		"Commands: /file /plan /feedback · /permission /model /memory /goal /compact /export · /clear /stop /help /new /settings /automate /plugins. "+
			"/file opens the attachment picker (/file <path> attaches a workspace file); /plan enters plan mode (/plan off leaves it, /plan <message> starts with it); "+
			"/permission read-only|workspace|full|auto changes the access mode; /memory opens Settings › Memory; /feedback good|bad rates the last answer; /export saves the session log. "+
			"/goal shows or controls the session goal (/goal <objective> creates one, /goal edit <objective> replaces it, /goal pause|resume|complete|abandon|clear control it). "+
			"@ mentions complete workspace files and folders (Tab drills into a folder, Esc walks back); a path with spaces can be quoted as @\"my file.txt\". "+
			"Session mentions insert the atomic @[label](dsh-session:<id>) reference and add the referenced session's bounded read-only context to the next request. "+
			"Compaction is automatic (the runtime compacts long conversations before a turn); /compact compacts the current conversation now. "+
			"Unknown commands are sent to the model as a normal message.",
		"命令：/file /plan /feedback · /permission /model /memory /goal /compact /export · /clear /stop /help /new /settings /automate /plugins。"+
			"/file 打开附件选择器（/file <路径> 直接附加工作区文件）；/plan 进入计划模式（/plan off 退出，/plan <消息> 以计划模式发送）；"+
			"/permission read-only|workspace|full|auto 切换访问模式；/memory 打开 设置 › 记忆；/feedback good|bad 评价最后一条回答；/export 保存会话日志。"+
			"/goal 查看或控制会话目标（/goal <目标> 创建，/goal edit <目标> 替换目标，/goal pause|resume|complete|abandon|clear 控制生命周期）。"+
			"@ 提及会补全工作区文件与文件夹（Tab 进入文件夹、Esc 逐级返回）；含空格的路径可写成 @\"my file.txt\"。"+
			"会话提及插入原子 @[label](dsh-session:<id>) 引用，并在下一请求中附带被引用会话的有界只读上下文。"+
			"压缩是自动的（运行时会在每轮开始前压缩过长的对话）；/compact 可立即压缩当前对话。"+
			"未知命令会作为普通消息发送给模型。")
}

// queueToast stores a message for the next frame's View, which is where mygo
// accepts Context toasts.
func (a *App) queueToast(message string) {
	if message == "" {
		return
	}
	a.slashMenu().deferredToast = message
}

// flushDeferredToast shows a toast a key handler queued outside a build pass.
func (a *App) flushDeferredToast(c *ui.Context) {
	st := a.slashMenu()
	if st.deferredToast == "" {
		return
	}
	message := st.deferredToast
	st.deferredToast = ""
	c.Toast(message)
}
