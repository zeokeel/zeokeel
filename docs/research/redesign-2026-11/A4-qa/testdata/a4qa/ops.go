package main

// The scenario operations. Every operation either does something to the booted
// product or asserts a user-visible fact; a returned string is the failure
// message for the case.

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type caseState struct {
	harness   *harness
	outDir    string
	booted    bool
	sessions0 int
	notes     []string
	shots     []string
}

func (s *caseState) note(format string, args ...any) {
	s.notes = append(s.notes, fmt.Sprintf(format, args...))
}

func (s *caseState) exec(o op) string {
	h := s.harness
	switch o.name {
	case "boot":
		return s.boot(o.args)
	case "expect-left-of":
		return s.expectLeftOf(o.args)
	case "workspace":
		if err := h.seedWorkspace(); err != nil {
			return err.Error()
		}
		h.frame()
		return ""
	case "session":
		if _, err := h.newSession(strings.TrimSpace(o.args)); err != nil {
			return err.Error()
		}
		h.drain(2)
		return ""
	case "run-slow":
		if err := h.startSlowTurn("QA 慢速检查"); err != nil {
			return err.Error()
		}
		h.frame()
		return ""
	case "approval":
		parts := strings.SplitN(o.args, "|", 2)
		tool := strings.TrimSpace(parts[0])
		summary := tool + " 调用"
		if len(parts) == 2 {
			summary = strings.TrimSpace(parts[1])
		}
		if err := h.requestApproval(tool, summary); err != nil {
			return err.Error()
		}
		h.drain(2)
		return ""
	case "key":
		return s.key(o.args)
	case "tab-to-any":
		for _, label := range strings.Split(o.args, "|") {
			label = strings.TrimSpace(label)
			if label == "" {
				continue
			}
			if h.tabTo(label) {
				return ""
			}
		}
		return fmt.Sprintf("Tab never reached any of %q (focus=%q)", o.args, h.focusedName())
	case "tab-to":
		if !h.tabTo(strings.TrimSpace(o.args)) {
			return fmt.Sprintf("Tab never reached %q (focus=%q)", o.args, h.focusedName())
		}
		return ""
	case "type":
		h.tester.Type(o.args)
		h.frame()
		return ""
	case "click-any":
		return s.clickAny(o.args)
	case "expect-focus-any":
		return s.expectFocusAny(o.args)
	case "click":
		if err := h.tester.Click(strings.TrimSpace(o.args)); err != nil {
			return err.Error()
		}
		h.frame()
		return ""
	case "hover":
		label := strings.TrimSpace(o.args)
		rect, ok := h.findRect(label)
		if !ok {
			return fmt.Sprintf("%q is not on screen (texts=%v)", label, tail(h.tester.Texts(), 6))
		}
		h.tester.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
		h.frame()
		return ""
	case "advance":
		ms, err := strconv.Atoi(strings.TrimSpace(o.args))
		if err != nil {
			return fmt.Sprintf("advance needs milliseconds: %v", err)
		}
		h.tester.Advance(time.Duration(ms) * time.Millisecond)
		h.frame()
		return ""
	case "snapshot":
		path, err := h.snapshot(strings.TrimSpace(o.args))
		if err != nil {
			return err.Error()
		}
		s.shots = append(s.shots, path)
		return ""
	case "wait-text":
		if !h.waitText(strings.TrimSpace(o.args), 3*time.Second) {
			return fmt.Sprintf("%q never appeared", o.args)
		}
		return ""
	case "expect-text":
		if !h.tester.HasText(o.args) {
			return fmt.Sprintf("%q is not on screen (texts=%v)", o.args, tail(h.tester.Texts(), 6))
		}
		return ""
	case "expect-no-text":
		if h.tester.HasText(o.args) {
			return fmt.Sprintf("%q is still on screen", o.args)
		}
		return ""
	case "expect-tooltip":
		return s.expectTooltip(o.args)
	case "expect-focus":
		want := strings.TrimSpace(o.args)
		if got := h.focusedName(); got != want {
			return fmt.Sprintf("focus is %q, want %q", got, want)
		}
		return ""
	case "expect-running":
		want := strings.TrimSpace(o.args) == "true"
		if want {
			if !h.waitRunning(true, 5*time.Second) {
				return "conversation never started running"
			}
			return ""
		}
		// Leaving the running state settles on the backend's own schedule
		// (Cancel signals, the run finishes a little later): poll instead of
		// reading one frame, so a slow settle is not a false failure.
		if !h.waitRunning(false, 3*time.Second) {
			return "conversation still running after 3s"
		}
		return ""
	case "expect-sessions":
		want, err := strconv.Atoi(strings.TrimSpace(o.args))
		if err != nil {
			return err.Error()
		}
		if got := h.sessionCount(); got != want {
			return fmt.Sprintf("%d conversations, want %d", got, want)
		}
		return ""
	case "expect-sessions-delta":
		want, err := strconv.Atoi(strings.TrimSpace(o.args))
		if err != nil {
			return err.Error()
		}
		if got := h.sessionCount() - s.sessions0; got != want {
			return fmt.Sprintf("%d new conversations, want %d", got, want)
		}
		return ""
	case "expect-settings":
		want := strings.TrimSpace(o.args) == "open"
		if got := h.uiRoot.Navigator().SettingsOpen(); got != want {
			return fmt.Sprintf("settings open = %v, want %v", got, want)
		}
		return ""
	case "expect-palette":
		want := strings.TrimSpace(o.args) == "open"
		if got := h.tester.HasText("命令面板"); got != want {
			return fmt.Sprintf("palette open = %v, want %v", got, want)
		}
		return ""
	case "expect-pending":
		want, err := strconv.Atoi(strings.TrimSpace(o.args))
		if err != nil {
			return err.Error()
		}
		if got := h.pendingApprovals(); got != want {
			return fmt.Sprintf("%d pending approvals, want %d", got, want)
		}
		return ""
	case "expect-tab-order":
		return s.expectTabOrder(o.args)
	case "expect-hover-selected-distinct":
		return s.expectHoverSelectedDistinct(o.args)
	case "expect-palette-all-commands":
		return s.expectPaletteAllCommands()
	case "expect-drop-taken":
		return s.expectDropTaken()
	case "expect-hover-reveals":
		return s.expectHoverReveals(o.args)
	case "expect-toast-above":
		// A notice must never sit on the action a run waits for (toastanchor.go,
		// the pending-approval branch): the toast stack has to end above the
		// inline card's buttons.
		label := strings.TrimSpace(o.args)
		toast, okToast := h.findRect("警告")
		if !okToast {
			return "no toast is on screen"
		}
		button, okButton := h.findRect(label)
		if !okButton {
			return fmt.Sprintf("%q is not on screen", label)
		}
		s.note("toast y=%0.f..%0.f, %q y=%0.f..%0.f", toast.Y, toast.Y+toast.H, label, button.Y, button.Y+button.H)
		if toast.Y+toast.H > button.Y {
			return fmt.Sprintf("the toast (bottom %.0f) overlaps %q (top %.0f)", toast.Y+toast.H, label, button.Y)
		}
		return ""
	case "dump-node":
		name := strings.TrimSpace(o.args)
		found := false
		for _, info := range h.tester.AccessAll() {
			if info.Name == name {
				found = true
				s.note("node %q: role=%v focusable=%v disabled=%v bounds=%v", name, info.Role, info.Focusable, info.Disabled, info.Bounds)
			}
		}
		if !found {
			s.note("node %q: absent from the accessibility tree", name)
		}
		return ""
	case "dump-tree":
		var rows []string
		for _, info := range h.tester.AccessAll() {
			if info.Role == ui.RoleTreeItem {
				rows = append(rows, fmt.Sprintf("%s(sel=%v)", info.Name, info.Selected))
			}
		}
		s.note("tree rows: %v", rows)
		return ""
	case "expect-tree-preview":
		// The row's selection must drive the preview: a file shows its content,
		// a directory says 目录. Row order is the workspace's, so the case must
		// not assume which row the cursor lands on.
		markers := map[string]string{
			"README.md": "QA 预览标记 alpha",
			"notes.txt": "QA 预览标记 beta",
			"main.go":   "QA 预览标记 gamma",
		}
		selected := ""
		for _, info := range h.tester.AccessAll() {
			if info.Role == ui.RoleTreeItem && info.Selected {
				selected = info.Name
				break
			}
		}
		if selected == "" {
			return "no tree row is selected in the files page"
		}
		s.note("selected row = %q", selected)
		if want, ok := markers[selected]; ok {
			if !h.tester.HasText(want) {
				return fmt.Sprintf("the preview does not show %q's content (%q)", selected, want)
			}
			return ""
		}
		if !h.tester.HasText("目录") {
			return fmt.Sprintf("%q is a directory but the preview does not say 目录", selected)
		}
		return ""
	case "expect-tree-selected":
		return s.expectTreeSelected(o.args)
	case "expect-disabled":
		label := strings.TrimSpace(o.args)
		for _, info := range h.tester.AccessAll() {
			if info.Name == label {
				if !info.Disabled {
					return fmt.Sprintf("%q is enabled, want disabled", label)
				}
				return ""
			}
		}
		return fmt.Sprintf("no control named %q", label)
	case "expect-tab":
		name := strings.TrimSpace(o.args)
		for _, info := range h.tester.AccessAll() {
			if info.Role == ui.RoleTab && info.Name == name {
				return ""
			}
		}
		return fmt.Sprintf("no file tab named %q", name)
	case "expect-control-name":
		return s.expectControlName(o.args)
	case "expect-reason":
		return s.expectReason(o.args)
	default:
		return fmt.Sprintf("unknown operation %q", o.name)
	}
}

func (s *caseState) boot(mode string) string {
	h := s.harness
	switch strings.TrimSpace(mode) {
	case "idle":
		h.frame()
	case "session", "session-nomodel":
		// Both seed a workspace and one conversation; session-nomodel's harness
		// was booted without a chat model (the no-model copy case).
		if err := h.seedWorkspace(); err != nil {
			return err.Error()
		}
		if _, err := h.newSession("QA 会话"); err != nil {
			return err.Error()
		}
		h.drain(2)
	default:
		return fmt.Sprintf("boot needs idle|session|session-nomodel, got %q", mode)
	}
	s.booted = true
	s.sessions0 = h.sessionCount()
	return ""
}

var chordKeys = map[string]ui.Key{
	"esc":       ui.KeyEscape,
	"escape":    ui.KeyEscape,
	"tab":       ui.KeyTab,
	"enter":     ui.KeyEnter,
	"down":      ui.KeyDown,
	"up":        ui.KeyUp,
	"home":      ui.KeyHome,
	"end":       ui.KeyEnd,
	"pagedown":  ui.KeyPageDown,
	"pageup":    ui.KeyPageUp,
	"backspace": ui.KeyBackspace,
}

func (s *caseState) key(raw string) string {
	chord := strings.TrimSpace(raw)
	var mods ui.Modifiers
	parts := strings.Split(chord, "+")
	name := strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
	for _, mod := range parts[:len(parts)-1] {
		switch strings.ToLower(strings.TrimSpace(mod)) {
		case "cmd", "ctrl":
			mods |= ui.Cmd
		case "shift":
			mods |= ui.Shift
		case "alt":
			mods |= ui.Alt
		default:
			return fmt.Sprintf("unknown modifier %q", mod)
		}
	}
	if key, ok := chordKeys[name]; ok {
		s.harness.tester.Key(mods, key)
		s.harness.frame()
		return ""
	}
	letters := map[string]ui.Key{
		"a": ui.KeyA, "b": ui.KeyB, "c": ui.KeyC, "f": ui.KeyF, "i": ui.KeyI,
		"k": ui.KeyK, "n": ui.KeyN, "p": ui.KeyP, "comma": ui.KeyComma, "period": ui.KeyPeriod,
	}
	key, ok := letters[name]
	if !ok {
		return fmt.Sprintf("unknown key %q", name)
	}
	s.harness.tester.Key(mods, key)
	s.harness.frame()
	return ""
}

// clickAny clicks the first of the labels that is on screen (a label renamed
// mid-redesign is tried as an alternative).
func (s *caseState) clickAny(raw string) string {
	var tried []string
	for _, label := range strings.Split(raw, "|") {
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		if err := s.harness.tester.Click(label); err == nil {
			s.harness.frame()
			return ""
		}
		tried = append(tried, label)
	}
	return fmt.Sprintf("none of %v is on screen", tried)
}

// expectFocusAny requires the focus to be on one of the labels.
func (s *caseState) expectFocusAny(raw string) string {
	got := s.harness.focusedName()
	for _, label := range strings.Split(raw, "|") {
		if label = strings.TrimSpace(label); label != "" && got == label {
			return ""
		}
	}
	return fmt.Sprintf("focus is %q, want one of %q", got, raw)
}

func (s *caseState) expectTooltip(text string) string {
	if !s.harness.tester.HasText(text) {
		return fmt.Sprintf("no tooltip %q (hover a labelled control first; texts=%v)", text, tail(s.harness.tester.Texts(), 6))
	}
	return ""
}

// expectTabOrder checks that the labels appear in this order in one Tab pass.
func (s *caseState) expectTabOrder(raw string) string {
	var want []string
	for _, part := range strings.Split(raw, "|") {
		if part = strings.TrimSpace(part); part != "" {
			want = append(want, part)
		}
	}
	sequence := s.harness.tabSequence(40)
	position := 0
	for _, name := range sequence {
		if position < len(want) && name == want[position] {
			position++
		}
	}
	if position != len(want) {
		return fmt.Sprintf("Tab order lacks %q in order; got %v", want[position], sequence)
	}
	s.note("tab order: %v", sequence[:minInt(len(sequence), 20)])
	return ""
}

// expectHoverSelectedDistinct samples the painted fill of the current row and of
// a hovered row: selection carries the brand hue, hover is a neutral tint. The
// rows are located by role and name (a conversation title also shows in the
// header, so Find's first text is not reliable).
func (s *caseState) expectHoverSelectedDistinct(raw string) string {
	parts := strings.SplitN(raw, "|", 2)
	if len(parts) != 2 {
		return "needs selectedLabel|otherLabel"
	}
	selectedLabel, otherLabel := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	h := s.harness
	selectedRect, ok := s.rowBounds(selectedLabel)
	if !ok {
		return fmt.Sprintf("no row named %q", selectedLabel)
	}
	img := h.tester.Image()
	if img == nil {
		return "no frame image"
	}
	selected := sampleRowFill(img, selectedRect)
	otherRect, ok := s.rowBounds(otherLabel)
	if !ok {
		return fmt.Sprintf("no row named %q", otherLabel)
	}
	h.tester.Move(otherRect.X+otherRect.W/2, otherRect.Y+otherRect.H/2)
	h.frame()
	img = h.tester.Image()
	hovered := sampleRowFill(img, otherRect)
	s.note("selected fill %v (blue bias %d), hovered fill %v (blue bias %d)", selected, blueBias(selected), hovered, blueBias(hovered))
	if selected == hovered {
		return "the hovered row and the selected row paint the same fill"
	}
	if blueBias(selected) < blueBias(hovered)+5 {
		return fmt.Sprintf("the selected row's brand tint (%d) is not clearly stronger than the hovered row's (%d)",
			blueBias(selected), blueBias(hovered))
	}
	// Restore the pointer off the rows.
	h.tester.Move(2, 2)
	h.frame()
	return ""
}

// expectPaletteAllCommands opens the palette once per registered command, types
// that command's title and requires a row for it.
func (s *caseState) expectPaletteAllCommands() string {
	h := s.harness
	runtimeService, ok := h.root.Get(contracts.KeyUIRuntime)
	if !ok || runtimeService == nil {
		return "no UI runtime"
	}
	commands := runtimeService.Commands()
	checked, missing := 0, 0
	var missed []string
	for _, command := range commands {
		title := strings.TrimSpace(command.Title)
		if title == "" {
			continue
		}
		if command.Enabled != nil && !command.Enabled() {
			continue
		}
		h.tester.Key(ui.Cmd, ui.KeyK)
		h.frame()
		if !h.tester.HasText("命令面板") {
			return "the palette did not open"
		}
		h.tester.Type(title)
		h.frame()
		found := false
		for _, info := range h.tester.AccessAll() {
			if info.Name == title && (info.Role == ui.RoleButton || info.Role == ui.RoleListItem || info.Role == ui.RoleRow) {
				found = true
				break
			}
		}
		if !found && h.tester.HasText(title) {
			found = true
		}
		h.tester.Key(0, ui.KeyEscape)
		h.frame()
		if h.tester.HasText("命令面板") {
			return "the palette did not close on Escape"
		}
		checked++
		if !found {
			missing++
			missed = append(missed, title)
		}
	}
	s.note("palette reachability: %d commands checked, %d missing", checked, missing)
	if missing > 0 {
		return fmt.Sprintf("%d commands are not reachable by their own title: %v", missing, tail(missed, 6))
	}
	return ""
}

// expectDropTaken drops one file on the composer and requires an element-level
// drop zone to take it (the mechanism a drop highlight is drawn from).
func (s *caseState) expectDropTaken() string {
	h := s.harness
	rect, ok := h.findRect("输入消息…")
	if !ok {
		return "the composer input is not on screen"
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("qa-drop-%d.txt", time.Now().UnixNano()))
	if err := os.WriteFile(path, []byte("qa drop\n"), 0o600); err != nil {
		return err.Error()
	}
	defer os.Remove(path)
	taken := h.tester.DropFiles(rect.X+rect.W/2, rect.Y+rect.H/2, path)
	h.frame()
	s.note("DropFiles taken by an element = %v", taken)
	if !taken {
		return "no element took the drop (only the window-level handler did), so no drop zone can highlight"
	}
	return ""
}

// expectHoverReveals hovers a control and requires new explanatory text to show
// (a disabled action's reason, a tooltip's copy, ...).
func (s *caseState) expectHoverReveals(label string) string {
	h := s.harness
	before := map[string]bool{}
	for _, text := range h.tester.Texts() {
		before[text] = true
	}
	rect, ok := h.findRect(label)
	if !ok {
		return fmt.Sprintf("%q is not on screen (texts=%v)", label, tail(h.tester.Texts(), 6))
	}
	h.tester.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	h.tester.Advance(900 * time.Millisecond)
	h.frame()
	var added []string
	for _, text := range h.tester.Texts() {
		if !before[text] {
			added = append(added, text)
		}
	}
	s.note("hovering %q added texts: %v", label, added)
	if len(added) == 0 {
		return fmt.Sprintf("hovering %q revealed nothing", label)
	}
	return ""
}

// expectTreeSelected requires the named tree item to be the chosen one.
func (s *caseState) expectTreeSelected(name string) string {
	for _, info := range s.harness.tester.AccessAll() {
		if info.Role == ui.RoleTreeItem && info.Name == name {
			if !info.Selected {
				return fmt.Sprintf("the tree's %q row is not selected", name)
			}
			return ""
		}
	}
	return fmt.Sprintf("the tree has no %q row", name)
}

// rowBounds finds a list/tree row by its accessible name.
func (s *caseState) rowBounds(name string) (ui.Rect, bool) {
	for _, info := range s.harness.tester.AccessAll() {
		switch info.Role {
		case ui.RoleTreeItem, ui.RoleListItem, ui.RoleRow:
			if info.Name == name {
				return info.Bounds, true
			}
		}
	}
	return ui.Rect{}, false
}

// expectLeftOf requires the first labelled text to sit left of the second on
// the same row (the composer's note sits left of the send control).
func (s *caseState) expectLeftOf(raw string) string {
	parts := strings.SplitN(raw, "|", 2)
	if len(parts) != 2 {
		return "needs leftLabel|rightLabel"
	}
	left, ok := s.harness.findRect(strings.TrimSpace(parts[0]))
	if !ok {
		return fmt.Sprintf("%q is not on screen", parts[0])
	}
	right, ok := s.harness.findRect(strings.TrimSpace(parts[1]))
	if !ok {
		return fmt.Sprintf("%q is not on screen", parts[1])
	}
	s.note("%q at x=%.0f, %q at x=%.0f", parts[0], left.X, parts[1], right.X)
	if left.X >= right.X {
		return fmt.Sprintf("%q (x=%.0f) is not left of %q (x=%.0f)", parts[0], left.X, parts[1], right.X)
	}
	if left.Y+left.H <= right.Y || right.Y+right.H <= left.Y {
		return fmt.Sprintf("%q and %q are not on the same row", parts[0], parts[1])
	}
	return ""
}

// expectControlName requires a visible control whose accessible name contains
// the substring (a new affordance the audit asks for).
func (s *caseState) expectControlName(substr string) string {
	substr = strings.TrimSpace(substr)
	for _, info := range s.harness.tester.AccessAll() {
		if strings.Contains(info.Name, substr) {
			s.note("control %q found (role %v)", info.Name, info.Role)
			return ""
		}
	}
	return fmt.Sprintf("no control named like %q on screen", substr)
}

// expectReason requires a readable explanation for a control: either a visible
// text already contains the substring, or hovering the control reveals one.
func (s *caseState) expectReason(raw string) string {
	parts := strings.SplitN(raw, "|", 2)
	if len(parts) != 2 {
		return "needs label|substring"
	}
	label, substr := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	for _, text := range s.harness.tester.Texts() {
		if strings.Contains(text, substr) {
			return ""
		}
	}
	rect, ok := s.harness.findRect(label)
	if !ok {
		return fmt.Sprintf("%q is not on screen", label)
	}
	s.harness.tester.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
	s.harness.tester.Advance(900 * time.Millisecond)
	s.harness.frame()
	for _, text := range s.harness.tester.Texts() {
		if strings.Contains(text, substr) {
			return ""
		}
	}
	return fmt.Sprintf("nothing explains %q (looked for %q; texts=%v)", label, substr, tail(s.harness.tester.Texts(), 6))
}

// sampleRowFill returns the row's own fill: the pixel a few DIPs left of the
// row's text, inside the row.
func sampleRowFill(img *image.RGBA, rect ui.Rect) color.RGBA {
	// The row paints its fill from its own left edge; the text starts some DIPs
	// in. Sample a few DIPs inside the edge, on a line above the text's middle
	// so no glyph lands on the sample.
	cy := int(rect.Y + 4)
	if cy >= img.Bounds().Dy() {
		cy = img.Bounds().Dy() - 1
	}
	for _, dx := range []int{6, 8, 10, 4, 12} {
		x := int(rect.X) + dx
		if x < 1 || x >= img.Bounds().Dx() {
			continue
		}
		return img.RGBAAt(x, cy)
	}
	return img.RGBAAt(int(rect.X), cy)
}

func blueBias(c color.RGBA) int { return int(c.B) - int(c.R) }

func writePNG(path string, img *image.RGBA) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func tail(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return values[len(values)-n:]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
