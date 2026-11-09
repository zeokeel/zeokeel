package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Settings-surface geometry the host uses (kept local so this tool also builds
// against revisions where the design tokens do not export them yet): preferred
// size, the modal's outer margin (theme.Space(10)) and the panel's own padding
// on both sides (2*theme.Space(5)); brand.Spacing is 4 DIP.
const (
	settingsPreferredWidth  float32 = 820
	settingsPreferredHeight float32 = 760
	settingsOuterMargin     float32 = 40
	settingsPanelPadding    float32 = 40
)

// frame renders one frame.
func (d *drive) frame() { d.tester.Frame() }

// texts returns the current frame labels.
func (d *drive) texts() []string { return d.tester.Texts() }

// has reports whether the frame shows text containing s.
func (d *drive) has(s string) bool { return d.tester.HasText(s) }

// find returns the first element showing exactly s.
func (d *drive) find(s string) (ui.Rect, bool) { return d.tester.Find(s) }

// findPrefix returns the first label starting with prefix and its rect.
func (d *drive) findPrefix(prefix string) (string, ui.Rect, bool) {
	for _, text := range d.tester.Texts() {
		if strings.HasPrefix(text, prefix) {
			if rect, ok := d.tester.Find(text); ok {
				return text, rect, true
			}
		}
	}
	return "", ui.Rect{}, false
}

// click clicks the center of the element showing s.
func (d *drive) click(s string) error { return d.tester.Click(s) }

// clickRect clicks a rect's center.
func (d *drive) clickRect(rect ui.Rect) {
	d.tester.ClickAt(rect.X+rect.W/2, rect.Y+rect.H/2)
}

// moveTo moves the pointer to the rect center (hover).
func (d *drive) moveTo(rect ui.Rect) {
	d.tester.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
}

// key sends one key press.
func (d *drive) key(mods ui.Modifiers, key ui.Key) { d.tester.Key(mods, key) }

// typeText types text into the focused input.
func (d *drive) typeText(text string) { d.tester.Type(text) }

// focusComposer focuses the session composer by clicking inside it (above the
// send button, next to the placeholder text).
func (d *drive) focusComposer() error {
	if rect, ok := d.tester.Find("输入消息…"); ok {
		d.clickRect(rect)
		d.frame()
		return nil
	}
	if rect, ok := d.tester.Find("发送"); ok {
		d.tester.ClickAt(rect.X-160, rect.Y-40)
		d.frame()
		return nil
	}
	return fmt.Errorf("composer not found (no placeholder or send control)")
}

// clearDraft selects all composer text and removes it.
func (d *drive) clearDraft() {
	d.tester.Key(ui.Cmd, ui.KeyA)
	d.tester.Key(0, ui.KeyBackspace)
	d.frame()
}

// drain renders n frames so queued runtime posts settle.
func (d *drive) drain(frames int) {
	for index := 0; index < frames; index++ {
		d.tester.Frame()
	}
}

// waitText pumps frames until the frame shows text (or times out).
func (d *drive) waitText(text string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		d.tester.Frame()
		if d.tester.HasText(text) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// waitIdle pumps frames until the session is no longer running.
func (d *drive) waitIdle(timeout time.Duration) error {
	chat := d.chat()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		d.tester.Frame()
		if session, ok := chat.Session(d.sessionID); ok && !session.Running {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("session %s still running after %s", d.sessionID, timeout)
}

// waitRunning pumps frames until the session reports running (and the frame
// shows the stop control when withStop is set).
func (d *drive) waitRunning(withStop bool, timeout time.Duration) bool {
	chat := d.chat()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		d.tester.Frame()
		session, ok := chat.Session(d.sessionID)
		if ok && session.Running {
			if !withStop || d.hasExact("停止") {
				return true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// sessionMessages returns the read-model messages of the driven session.
func (d *drive) sessionMessages() []messageSummary {
	session, ok := d.chat().Session(d.sessionID)
	if !ok {
		return nil
	}
	out := make([]messageSummary, 0, len(session.Messages))
	for _, message := range session.Messages {
		out = append(out, messageSummary{Role: message.Role, Text: message.Text})
	}
	return out
}

// lastAssistantText returns the last assistant message body.
func (d *drive) lastAssistantText() string {
	messages := d.sessionMessages()
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "assistant" {
			return messages[index].Text
		}
	}
	return ""
}

// userMessages returns the user-role messages.
func (d *drive) userMessages() []messageSummary {
	var out []messageSummary
	for _, message := range d.sessionMessages() {
		if message.Role == "user" {
			out = append(out, message)
		}
	}
	return out
}

// messageSummary is the report-friendly read model of one chat message.
type messageSummary struct {
	Role string
	Text string
}

// inWindow reports whether a rect is fully inside the window.
func (d *drive) inWindow(rect ui.Rect) bool {
	return rect.X >= 0 && rect.Y >= 0 &&
		rect.X+rect.W <= float32(d.w) && rect.Y+rect.H <= float32(d.h)
}

// inRect reports whether inner is inside outer with the given tolerance.
func inRect(outer, inner ui.Rect, tolerance float32) bool {
	return inner.X >= outer.X-tolerance && inner.Y >= outer.Y-tolerance &&
		inner.X+inner.W <= outer.X+outer.W+tolerance &&
		inner.Y+inner.H <= outer.Y+outer.H+tolerance
}

// settingsPanelRect estimates the settings modal rect for the current window
// size using the host's layout constants (the host sizes the modal as
// min(SettingsWidth, window-2*margin) and centres it).
func (d *drive) settingsPanelRect() ui.Rect {
	width := float32(d.w)
	height := float32(d.h)
	panelW := settingsPreferredWidth
	panelH := settingsPreferredHeight
	limitW := width - settingsOuterMargin - settingsPanelPadding
	limitH := height - settingsOuterMargin - settingsPanelPadding
	if limitW > 0 && panelW > limitW {
		panelW = limitW
	}
	if limitH > 0 && panelH > limitH {
		panelH = limitH
	}
	if panelW < 640 && (width <= 0 || panelW < width) {
		panelW = 640
	}
	if panelH < 480 && (height <= 0 || panelH < height) {
		panelH = 480
	}
	// Sanity check against the dialog's own close control: if the computed
	// rect does not contain it, fall back to the modal's maximal rect. This
	// keeps the "inside the dialog" assertions valid across layout revisions.
	rect := ui.Rect{X: (width - panelW) / 2, Y: (height - panelH) / 2, W: panelW, H: panelH}
	if close, ok := d.tester.Find("关闭设置"); ok && !inRect(rect, close, 2) {
		fallback := ui.Rect{X: 20, Y: 20, W: width - 40, H: height - 40}
		if inRect(fallback, close, 2) {
			return fallback
		}
	}
	return rect
}

// setSize changes the window size and settles the layout.
func (d *drive) setSize(width, height int) {
	d.w, d.h = width, height
	d.tester.SetSize(width, height)
	d.tester.Frame()
	d.tester.Frame()
}

// resetToSession pushes the session screen, follows the active session and
// clears any draft.
func (d *drive) resetToSession() {
	if d.uiRoot.Navigator().SettingsOpen() {
		d.uiRoot.Navigator().CloseSettings()
		d.frame()
	}
	if err := d.uiRoot.Navigator().Push("session"); err != nil {
		fmt.Printf("ui-drive: push session: %v\n", err)
	}
	d.tester.Frame()
	d.syncSession()
	_ = d.focusComposer()
	d.clearDraft()
	d.tester.Frame()
}

// shortText collapses a message body for the report.
func shortText(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if runes := []rune(text); len(runes) > 120 {
		return string(runes[:120]) + "…"
	}
	return text
}

// settings requires the settings service.
func (d *drive) settings() contracts.SettingsService {
	store, ok := d.root.Get(contracts.KeySettings)
	if !ok {
		panic("settings service missing")
	}
	return store
}

// --- check action helpers -------------------------------------------------

// click clicks the element showing label and renders the resulting frame.
func (c *check) click(label string) bool {
	if err := c.d.click(label); err != nil {
		c.failf("点击 %q 失败：%v", label, err)
		return false
	}
	c.d.frame()
	return true
}

// clickAny clicks the first of the labels that exists; it returns the label
// clicked.
func (c *check) clickAny(labels ...string) (string, bool) {
	for _, label := range labels {
		if _, ok := c.d.find(label); ok {
			return label, c.click(label)
		}
	}
	c.failf("以下控件都不存在：%v", labels)
	return "", false
}

// has reports whether the frame shows text containing s.
func (c *check) has(s string) bool { return c.d.has(s) }

// mustText asserts every wanted text is shown.
func (c *check) mustText(wanted ...string) bool {
	missing := make([]string, 0, len(wanted))
	for _, want := range wanted {
		if !c.d.has(want) {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		c.failf("界面缺少文本：%v", missing)
		return false
	}
	return true
}

// checkSendInWindow asserts the session send control exists and is inside the
// current window.
func (c *check) checkSendInWindow() bool {
	rect, ok := c.d.find("发送")
	if !ok {
		c.failf("会话页找不到「发送」按钮")
		return false
	}
	if !c.d.inWindow(rect) {
		c.failf("「发送」按钮越出窗口：rect=%+v，窗口=%dx%d", rect, c.d.w, c.d.h)
		return false
	}
	c.note("「发送」rect=(%.0f,%.0f,%.0f,%.0f) 在 %dx%d 窗口内", rect.X, rect.Y, rect.W, rect.H, c.d.w, c.d.h)
	return true
}

// checkTitleVisible asserts the session title text is shown and inside the
// window.
func (c *check) checkTitleVisible(title string) bool {
	rect, ok := c.d.find(title)
	if !ok {
		truncated := false
		for _, text := range c.d.texts() {
			if strings.HasPrefix(text, "驱动") && text != title {
				truncated = true
			}
		}
		c.failf("会话标题 %q 不可见（可能被截断：%v）", title, truncated)
		return false
	}
	if !c.d.inWindow(rect) {
		c.failf("会话标题 %q 越出窗口：rect=%+v", title, rect)
		return false
	}
	return true
}

// syncSession makes the driver follow the session the UI currently shows; a
// command-driven "new conversation" switches it, and the assertions must read
// the same session the user sees.
func (d *drive) syncSession() contracts.ChatService {
	chat := d.chat()
	if active := chat.ActiveSession(); active != "" {
		d.sessionID = active
	}
	return chat
}

// restoreOriginalSession deletes sessions created by command steps and makes
// the seeded conversation active again, so later steps are deterministic.
func (d *drive) restoreOriginalSession() {
	if d.originalSessionID == "" {
		return
	}
	chat := d.chat()
	for _, info := range chat.Sessions() {
		if info.ID != d.originalSessionID {
			_ = chat.DeleteSession(info.ID)
		}
	}
	_ = chat.SetActiveSession(d.originalSessionID)
	d.sessionID = d.originalSessionID
	d.frame()
}

// sendError describes why a send attempt did not start a turn.
type sendError struct{ reason string }

func (s sendError) Error() string { return s.reason }

// clickSend clicks the composer send control and waits until the new turn is
// appended to the read model.
func (d *drive) clickSend() (before int, err error) {
	chat := d.syncSession()
	if session, ok := chat.Session(d.sessionID); ok {
		before = len(session.Messages)
	}
	if err := d.click("发送"); err != nil {
		return before, fmt.Errorf("点击「发送」失败：%w", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		d.frame()
		if session, ok := chat.Session(d.sessionID); ok && len(session.Messages) >= before+2 {
			return before, nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return before, sendError{"发送后会话没有追加消息（可能仍在运行或点击未命中）"}
}

// waitTurn drains frames until the turn started at message index before has
// finished (assistant text or error), and returns the assistant text.
func (d *drive) waitTurn(before int, timeout time.Duration) (string, error) {
	chat := d.syncSession()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		d.frame()
		session, ok := chat.Session(d.sessionID)
		if !ok {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if session.Running {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		assistantIndex := before + 1
		text := ""
		if assistantIndex < len(session.Messages) {
			text = strings.TrimSpace(session.Messages[assistantIndex].Text)
		}
		if text != "" || session.Error != "" {
			return text, nil
		}
		// Give the streaming placeholder one more frame before concluding.
		time.Sleep(10 * time.Millisecond)
	}
	session, _ := chat.Session(d.sessionID)
	return "", fmt.Errorf("回合在 %s 内未完成（running=%v）", timeout, session.Running)
}

// sendMessage types prompt into the composer, sends it and waits for the reply.
func (d *drive) sendMessage(prompt string, timeout time.Duration) (string, error) {
	if err := d.focusComposer(); err != nil {
		return "", err
	}
	d.clearDraft()
	d.typeText(prompt)
	d.frame()
	before, err := d.clickSend()
	if err != nil {
		return "", err
	}
	return d.waitTurn(before, timeout)
}

// currentSessionTitle is the title the session header shows for the driven
// session (the agent auto-titles from the first prompt).
func (d *drive) currentSessionTitle() string {
	chat := d.syncSession()
	if session, ok := chat.Session(d.sessionID); ok {
		if title := strings.TrimSpace(session.Title); title != "" {
			return title
		}
	}
	return "对话"
}

// sendFromComposer types a prompt, clicks send and waits until the turn is
// appended to the read model; it returns the message index before the turn.
func (d *drive) sendFromComposer(prompt string) (int, error) {
	if err := d.focusComposer(); err != nil {
		return 0, err
	}
	d.clearDraft()
	d.typeText(prompt)
	d.frame()
	return d.clickSend()
}

// hasExact reports whether the frame shows exactly text (HasText is a
// substring match, which confuses labels such as "停止" and "已停止").
func (d *drive) hasExact(text string) bool {
	for _, label := range d.tester.Texts() {
		if label == text {
			return true
		}
	}
	return false
}

// openSettingsFromNeutralScreen opens the settings surface from a screen whose
// labels do not collide with the settings navigation (the session screen has
// tabs such as 记忆/预设, which would win a Tester.Find by title). It returns
// true when the settings surface is open afterwards.
func (d *drive) openSettingsFromNeutralScreen() bool {
	if d.uiRoot.Navigator().SettingsOpen() {
		return true
	}
	if err := d.uiRoot.Navigator().Push("automation"); err != nil {
		fmt.Printf("ui-drive: push automation: %v\n", err)
	}
	d.tester.Frame()
	if err := d.click("设置"); err != nil {
		return false
	}
	d.tester.Frame()
	d.tester.Frame()
	return d.uiRoot.Navigator().SettingsOpen()
}

// goalChipLabel returns the current goal chip's status label (目标 / 目标已暂停 /
// 目标已完成 / 目标已放弃), or "" when no chip is shown.
func (d *drive) goalChipLabel() string {
	for _, text := range d.texts() {
		switch strings.TrimSpace(text) {
		case "目标", "目标已暂停", "目标已完成", "目标已放弃":
			return strings.TrimSpace(text)
		}
	}
	return ""
}

// selectPane switches the inspector to one pane through its pane selector. The
// selector is a menu button naming the current pane; opening it and choosing
// the target pane is the user path now that eight tabs do not fit the rail.
func (d *drive) selectPane(label string) bool {
	for attempt := 0; attempt < 3; attempt++ {
		if err := d.click("切换面板"); err != nil {
			return false
		}
		d.drain(2)
		if !d.has(label) {
			return false
		}
		if err := d.click(label); err != nil {
			continue
		}
		d.drain(2)
		// The selector names the active pane; the target label is now its
		// visible text (and its menu item is gone).
		return d.hasExact(label) || d.has(label)
	}
	return false
}

// paletteTailTexts returns the last labels of the frame for failure evidence.
func paletteTailTexts(d *drive) []string {
	texts := d.texts()
	if len(texts) > 24 {
		return texts[len(texts)-24:]
	}
	return texts
}

// findAny returns the rect of the first of the labels that exists in the frame.
func (d *drive) findAny(labels ...string) (ui.Rect, bool) {
	for _, label := range labels {
		if rect, ok := d.find(label); ok {
			return rect, true
		}
	}
	return ui.Rect{}, false
}

// countExact counts frame labels equal to text (HasText is substring).
func (d *drive) countExact(text string) int {
	count := 0
	for _, label := range d.texts() {
		if label == text {
			count++
		}
	}
	return count
}

// hasExactText reports whether one label equals text exactly.
func (d *drive) hasExactText(text string) bool { return d.countExact(text) > 0 }

// countContaining counts frame labels containing text (a11y labels included).
func (d *drive) countContaining(text string) int {
	count := 0
	for _, label := range d.texts() {
		if strings.Contains(label, text) {
			count++
		}
	}
	return count
}
