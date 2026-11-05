package shell

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/icons"
	"github.com/zeokeel/zeokeel/internal/state"
)

// toolcards.go renders one card per tool call. The card's leading icon, title
// and summary are chosen by the tool's wire name: a shell call
// shows its command and output, a file call its path and change, a search its
// matches, a web call its URL, an image its dimensions, and a question its
// prompt. Unregistered tools keep the generic arguments card.

// toolImageIcon is Lucide's image glyph, drawn inline for the image variant
// without changing the shared icon library.
var toolImageIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect width="18" height="18" x="3" y="3" rx="2" ry="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21"/></svg>`))

// toolFileIcon is Lucide's file-text glyph for read/write/edit cards: the
// shared icon library has no file icon and is not part of this change.
var toolFileIcon = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M10 9H8"/><path d="M16 13H8"/><path d="M16 17H8"/></svg>`))

// toolVariantKind is how a tool call presents itself.
type toolVariantKind uint8

const (
	toolGeneric toolVariantKind = iota
	toolCommand
	toolFile
	toolSearch
	toolWeb
	toolImage
	toolQuestion
)

// toolVariant is the resolved presentation of one call.
type toolVariant struct {
	kind    toolVariantKind
	summary string // the second half of the collapsed row
	detail  string // the variant's own first detail line
}

// toolKindFor maps a wire tool name onto its card variant. The name sets are
// the tools this build registers plus their common aliases.
func toolKindFor(name string) toolVariantKind {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "bash", "pwsh", "sh", "shell", "terminal", "exec", "run", "run_code",
		"job_start", "job_list", "job_output", "job_kill", "jobs":
		return toolCommand
	case "read_file", "write_file", "edit_file", "apply_patch", "create_file",
		"delete_file", "str_replace_editor", "read", "write", "edit", "file_read",
		"file_write", "notebook_edit":
		return toolFile
	case "glob", "grep", "search", "code_search", "find", "rg", "list_files":
		return toolSearch
	case "web_search", "web_fetch", "webfetch", "fetch", "browse", "browser", "http":
		return toolWeb
	case "read_image", "view_image", "image", "screenshot":
		return toolImage
	case "ask_user", "askuser", "question", "elicit", "request_user_input":
		return toolQuestion
	default:
		return toolGeneric
	}
}

// liveToolCards draws the session's tool calls, ordered by call ID so a map's
// iteration order never moves them from frame to frame.
func (a *App) liveToolCards(c *ui.Context, t *design.Tokens, snap *state.Snapshot) {
	if len(snap.Tools) == 0 {
		return
	}
	ids := make([]string, 0, len(snap.Tools))
	for id := range snap.Tools {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a.liveToolCard(c, t, snap.Tools[id])
	}
}

// liveToolCard is one tool call: a variant-aware row the user clicks to open,
// and a detail that grows in and collapses away.
func (a *App) liveToolCard(c *ui.Context, t *design.Tokens, tool state.ToolState) {
	open := a.live().expanded[tool.CallID]
	ui.Column(c).Key("tool:" + tool.CallID).Gap(design.Space1).
		Transition(design.LayoutTransition()).Children(func() {
		a.liveToolRow(c, t, tool, open)
		if open {
			a.liveToolDetail(c, t, tool)
		}
	})
}

// liveToolRow is the clickable tool card row: a status dot, the variant's
// leading icon, the "name · status" title and the variant summary, on a
// borderless button with the brand hover tint. The 4 DIP grid sets the
// height, padding and gaps.
func (a *App) liveToolRow(c *ui.Context, t *design.Tokens, tool state.ToolState, open bool) {
	th := t.Theme()
	v := a.toolVariant(tool)
	row := ui.ButtonBase(c).Height(th.Space(6)).PaddingX(th.Space(1)).
		Gap(th.Space(1.5)).Radius(design.RadiusSM).
		Transition(design.ColorTransition()).Label(a.T("session.tool_label", tool.Name))
	if row.Hovered() {
		row = row.Background(t.BgHover)
	}
	row.Children(func() {
		a.liveStatusDot(c, t, tool)
		a.toolLeadingIcon(c, t, v.kind)
		ui.Text(c, tool.Name+" · "+string(tool.Status)).FontSize(13).
			FixedLineHeight(20).TextColor(t.LabelSecondary).NoWrap()
		if v.summary != "" {
			ui.Box(c).Size(th.Space(0.5), th.Space(0.5)).Radius(th.Space(0.25)).
				Background(t.LabelTertiary)
			ui.Text(c, v.summary).FontSize(13).FixedLineHeight(20).
				TextColor(t.LabelTertiary).MaxLines(1).Ellipsis("…").Shrink(1)
		}
		ui.Spacer(c).Grow(1)
		caret := "›"
		if open {
			caret = "⌄"
		}
		ui.Text(c, caret).FontSize(11).TextColor(t.LabelTertiary).NoWrap().
			ContentTransition(ui.ContentTransition{Animation: &design.AnimFast})
	})
	if row.Clicked() {
		a.live().toggleTool(tool.CallID)
	}
}

// toolLeadingIcon draws the variant's 16px leading icon. Available Lucide
// icons are used where one is semantic; the globe and image marks are drawn
// from mygo boxes so this file needs no icon-library change.
func (a *App) toolLeadingIcon(c *ui.Context, t *design.Tokens, kind toolVariantKind) {
	th := t.Theme()
	box := ui.Row(c).Size(th.Space(4), th.Space(4)).Center()
	switch kind {
	case toolCommand:
		box.Children(func() { ui.Icon(c, icons.Terminal()).Size(th.Space(4), th.Space(4)).TextColor(t.LabelTertiary) })
	case toolFile:
		box.Children(func() { ui.Icon(c, toolFileIcon).Size(th.Space(3.5), th.Space(3.5)).TextColor(t.LabelTertiary) })
	case toolSearch:
		box.Children(func() { ui.Icon(c, icons.Search()).Size(th.Space(4), th.Space(4)).TextColor(t.LabelTertiary) })
	case toolQuestion:
		box.Children(func() { ui.Icon(c, icons.Message()).Size(th.Space(4), th.Space(4)).TextColor(t.LabelTertiary) })
	case toolImage:
		box.Children(func() { ui.Icon(c, toolImageIcon).Size(th.Space(3.5), th.Space(3.5)).TextColor(t.LabelTertiary) })
	case toolWeb:
		box.Children(func() {
			ui.Row(c).Size(th.Space(3), th.Space(3)).Center().
				Border(1, t.LabelTertiary).Radius(th.Space(1.5)).
				Children(func() {
					ui.Box(c).Width(th.Space(2.5)).Height(1).Background(t.LabelTertiary)
				})
		})
	default:
		box.Children(func() { ui.Icon(c, icons.Sparkles()).Size(th.Space(4), th.Space(4)).TextColor(t.LabelTertiary) })
	}
}

// liveToolDetail is the expanded body. It always reports the variant's own
// facts (command and output, path and change, pattern and matches, URL, size,
// or question) and keeps the duration line the card already had.
func (a *App) liveToolDetail(c *ui.Context, t *design.Tokens, tool state.ToolState) {
	th := t.Theme()
	v := a.toolVariant(tool)
	ui.Column(c).Key("tool-detail:" + tool.CallID).
		Transition(design.CollapseTransition()).
		Background(t.BgHover).Radius(design.RadiusSM).
		Padding(th.Space(2)).Gap(th.Space(1)).Children(func() {
		if v.detail != "" {
			text := ui.Text(c, v.detail).FontSize(12).FixedLineHeight(18).
				TextColor(t.LabelSecondary).MaxLines(2).Ellipsis("…")
			if v.kind == toolCommand || v.kind == toolFile || v.kind == toolSearch {
				text.Font(monoFont)
			}
		}
		if tool.Result != "" {
			result := ui.Text(c, tool.Result).FontSize(12).FixedLineHeight(18).
				TextColor(t.LabelPrimary).MaxLines(10).Ellipsis("…")
			if v.kind == toolCommand || v.kind == toolFile || v.kind == toolSearch {
				result.Font(monoFont)
			}
			if tool.IsError {
				result.TextColor(t.Danger)
			}
		} else if tool.Status == state.ToolRunning || tool.Status == state.ToolPending {
			ui.Text(c, a.menuText("Waiting for output…", "等待输出…")).
				FontSize(12).FixedLineHeight(18).TextColor(t.LabelTertiary)
		}
		if tool.Duration > 0 {
			ui.Text(c, a.T("session.took", tool.Duration.Round(time.Millisecond).String())).
				FontSize(11).FixedLineHeight(16).TextColor(t.LabelTertiary)
		}
	})
}

// liveStatusDot is a tool call's state dot. While the call runs it pulses its
// opacity on the motion scale's status-dot cycle; every color change fades on
// the activity-row transition.
func (a *App) liveStatusDot(c *ui.Context, t *design.Tokens, tool state.ToolState) {
	th := t.Theme()
	dot := ui.Box(c).Size(th.Space(2), th.Space(2)).Radius(th.Space(1)).
		Background(liveToolColor(t, tool.Status)).
		Transition(design.ColorTransition())
	if tool.Status == state.ToolRunning || tool.Status == state.ToolPending {
		p := dot.Loop("pulse", design.DurationPulse, design.Pulse)
		dot.Opacity(design.PulseOpacity(p))
	}
}

// liveToolColor maps a tool status onto the theme's state colors.
func liveToolColor(t *design.Tokens, s state.ToolStatus) ui.Color {
	switch s {
	case state.ToolDone:
		return t.Success
	case state.ToolError, state.ToolDenied:
		return t.Danger
	case state.ToolAwaitingApproval:
		return t.Warning
	case state.ToolRunning:
		return t.Accent
	default:
		return t.LabelTertiary
	}
}

// toolVariant resolves what the card shows for tool.
func (a *App) toolVariant(tool state.ToolState) toolVariant {
	v := toolVariant{kind: toolKindFor(tool.Name)}
	args := parseToolArgs(tool.Args)
	switch v.kind {
	case toolCommand:
		cmd := firstArg(args, "cmd", "command", "script", "code")
		if cmd == "" {
			cmd = oneLine(tool.Result)
		}
		v.summary = cmd
		if cmd != "" {
			v.detail = "$ " + cmd
		}
	case toolFile:
		path := firstArg(args, "path", "file", "file_path", "filename", "target")
		_, change := fileChange(tool, args)
		v.summary = joinDot(path, change)
		v.detail = v.summary
	case toolSearch:
		pattern := firstArg(args, "pattern", "query", "glob", "regex", "term")
		matches := ""
		if tool.Status == state.ToolDone || tool.Status == state.ToolError {
			matches = a.matchCountText(searchCount(tool.Result))
		}
		v.summary = joinDot(pattern, matches)
		v.detail = joinDot(pattern, matches)
	case toolWeb:
		url := firstArg(args, "url", "href", "link")
		query := firstArg(args, "query", "q", "prompt")
		if url == "" {
			url = query
		} else if query != "" && query != url {
			url = query + " · " + url
		}
		v.summary = url
		v.detail = url
	case toolImage:
		path := firstArg(args, "path", "file", "file_path", "url")
		v.summary = joinDot(path, imageDimensions(tool, args))
		v.detail = v.summary
	case toolQuestion:
		question := firstArg(args, "question", "prompt", "text", "message")
		if question == "" && len(tool.Args) > 0 {
			question = oneLine(compactArgs(tool.Args))
		}
		v.summary = question
		v.detail = question
	default:
		// A tool with no dedicated card keeps the old collapsed behavior:
		// its arguments stay in the detail until the user opens it.
		if len(tool.Args) > 0 {
			v.detail = oneLine(compactArgs(tool.Args))
		}
	}
	return v
}

// fileChange names what a file tool did, plus a short change tally. Reads
// count result lines; writes count content lines; edits report added and
// removed lines when the arguments carry both strings.
func fileChange(tool state.ToolState, args map[string]any) (action, change string) {
	name := strings.ToLower(tool.Name)
	switch {
	case strings.Contains(name, "write") || strings.Contains(name, "create") || strings.Contains(name, "delete"):
		action = "written"
	case strings.Contains(name, "edit") || strings.Contains(name, "replace") || strings.Contains(name, "patch"):
		action = "edited"
	default:
		action = "read"
	}
	switch action {
	case "read":
		change = action
		if n := nonEmptyLines(tool.Result); n > 0 {
			change = fmt.Sprintf("%s · %d lines", action, n)
		}
	case "written":
		change = action
		if content := firstArg(args, "content", "text", "new_string"); content != "" {
			if n := nonEmptyLines(content); n > 0 {
				change = fmt.Sprintf("%s · %d lines", action, n)
			}
		}
	case "edited":
		change = action
		oldText := firstArg(args, "old_string", "old_str", "old_text")
		newText := firstArg(args, "new_string", "new_str", "new_text")
		if oldText != "" || newText != "" {
			added, removed := nonEmptyLines(newText), nonEmptyLines(oldText)
			change = fmt.Sprintf("%s · +%d −%d", action, added, removed)
		}
	}
	return action, change
}

// matchCountText is the summary's match count, localized.
func (a *App) matchCountText(n int) string {
	if a.lang() == LangZH {
		return fmt.Sprintf("%d 个匹配", n)
	}
	if n == 1 {
		return "1 match"
	}
	return fmt.Sprintf("%d matches", n)
}

// searchCount reads a search result's match count. JSON arrays count their
// entries; a result that names a count uses it; otherwise the non-empty
// result lines are the matches, as grep -n prints them.
func searchCount(result string) int {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return 0
	}
	var rows []any
	if err := json.Unmarshal([]byte(trimmed), &rows); err == nil {
		return len(rows)
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "no matches") || strings.Contains(lower, "no results") ||
		strings.Contains(lower, "no files found") {
		return 0
	}
	if m := searchCountRE.FindStringSubmatch(trimmed); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}
	return nonEmptyLines(trimmed)
}

// searchCountRE finds "3 matches", "12 results" and similar counts.
var searchCountRE = regexp.MustCompile(`(?i)\b(\d+)\s+(?:matches|results|files|entries|hits)\b`)

// imageDimensions returns an image result's "W×H" size: the arguments when
// they carry both numbers, or the first WxH pair the result reports.
func imageDimensions(tool state.ToolState, args map[string]any) string {
	if w, ok := argInt(args, "width"); ok {
		if h, ok := argInt(args, "height"); ok && w > 0 && h > 0 {
			return fmt.Sprintf("%d×%d", w, h)
		}
	}
	if m := imageSizeRE.FindStringSubmatch(tool.Result); m != nil {
		return m[1] + "×" + m[2]
	}
	return ""
}

// imageSizeRE finds a "640x480" style size in an image tool's result.
var imageSizeRE = regexp.MustCompile(`(\d{1,5})\s*[x×]\s*(\d{1,5})`)

// parseToolArgs decodes a call's JSON arguments, returning nil for a
// malformed or empty payload instead of failing the card.
func parseToolArgs(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil
	}
	return args
}

// firstArg returns the first non-empty string-fiable argument among keys.
func firstArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := args[key]; ok {
			if s := argString(v); strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

// argString formats an argument value as text, keeping arrays and objects
// readable rather than dropping them.
func argString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(raw)
	}
}

// argInt returns an integer argument, accepting JSON numbers and numeric
// strings.
func argInt(args map[string]any, key string) (int, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return int(x), true
	case string:
		n, err := strconv.Atoi(x)
		return n, err == nil
	}
	return 0, false
}

// nonEmptyLines counts the non-blank lines of text.
func nonEmptyLines(text string) int {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// oneLine collapses whitespace and newlines into one summary line.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// joinDot joins non-empty parts with the middle dot separator.
func joinDot(parts ...string) string {
	out := ""
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if out != "" {
			out += " · "
		}
		out += part
	}
	return out
}
