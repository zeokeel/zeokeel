package session

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zeokeel/zeokeel/old/internal/runtime"
)

// Automatic session titles name a conversation from its first completed turn.
// The controller asks a TitleGenerator for a title; when the generator is
// missing or fails, it falls back to the first user message. A manual rename
// always wins and the request is made at most once per session.
const (
	// AutoTitleSystemPrompt is the system instruction sent to the provider for
	// a title request. It is exported so providers and tests can recognize the
	// request without guessing from the message list.
	AutoTitleSystemPrompt = "You write short titles for chat sessions. " +
		"Given the user's first message and the assistant's reply, answer with a concise title of at most eight words. " +
		"Use the same language as the user's message. " +
		"Reply with the title text only, without quotes, trailing punctuation or explanation."

	// AutoTitleMaxChars caps a model-written title. A title longer than this is
	// truncated with an ellipsis before it is stored.
	AutoTitleMaxChars = 80

	// AutoTitleFallbackChars caps the fallback title derived from the first
	// user message, ellipsis included.
	AutoTitleFallbackChars = 60

	// DefaultAutoTitleTimeout bounds one title request. It is deliberately
	// short: a slow titler must never hold up the turn or the app's shutdown.
	DefaultAutoTitleTimeout = 15 * time.Second
)

// Title generator input caps keep the title request small even when the first
// turn produced a very long prompt or answer.
const (
	autoTitleUserMaxChars  = 2000
	autoTitleReplyMaxChars = 1500
)

// Setting keys written for one session. They make the "once per session" and
// "a manual rename wins" rules survive a restart.
func autoTitleKey(sessionID string) string   { return "session." + sessionID + ".title.auto" }
func manualTitleKey(sessionID string) string { return "session." + sessionID + ".title.manual" }

// autoTitleRetryKey marks a session whose first turn failed or was stopped
// before it could be titled. It lets the next successfully completed turn use
// the first user message even though that turn is no longer the session's only
// user row. Sessions without the marker keep the users == 1 guard, so a
// conversation that predates the feature is never renamed from its old first
// message.
func autoTitleRetryKey(sessionID string) string {
	return "session." + sessionID + ".title.retry"
}

// TitleGenerator writes a session title from the first completed turn.
// GenerateTitle is called at most once per session; a caller cancellation
// means the controller is closing and the result is discarded.
type TitleGenerator interface {
	GenerateTitle(ctx context.Context, sessionID, firstUserMessage, assistantReply string) (string, error)
}

// TitleFunc adapts an ordinary function to TitleGenerator.
type TitleFunc func(ctx context.Context, sessionID, firstUserMessage, assistantReply string) (string, error)

// GenerateTitle implements TitleGenerator.
func (f TitleFunc) GenerateTitle(ctx context.Context, sessionID, firstUserMessage, assistantReply string) (string, error) {
	return f(ctx, sessionID, firstUserMessage, assistantReply)
}

// LLMTitleGenerator is a TitleGenerator backed by a runtime.Provider. It sends
// one short completion request, reads the streamed title and normalizes the
// answer; an empty or unusable answer is reported as an error so the
// controller can use its deterministic fallback.
type LLMTitleGenerator struct {
	// Provider is the model backend. A nil Provider makes every title request
	// fail, which also selects the fallback.
	Provider runtime.Provider
	// Model is forwarded on the title request. Empty lets the provider use its
	// configured default model.
	Model string
	// Timeout bounds one title request. Zero means DefaultAutoTitleTimeout; a
	// negative value relies on the caller's context instead.
	Timeout time.Duration
	// MaxChars caps the normalized title. Zero or negative means
	// AutoTitleMaxChars.
	MaxChars int
}

var _ TitleGenerator = LLMTitleGenerator{}

// GenerateTitle implements TitleGenerator.
func (g LLMTitleGenerator) GenerateTitle(ctx context.Context, sessionID, firstUserMessage, assistantReply string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if g.Provider == nil {
		return "", errors.New("session: LLMTitleGenerator has no provider")
	}
	if strings.TrimSpace(firstUserMessage) == "" {
		return "", errors.New("session: LLMTitleGenerator has no user message")
	}

	reqCtx := ctx
	if timeout := g.timeout(); timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	ch, err := g.Provider.Stream(reqCtx, runtime.Request{
		SessionID: sessionID,
		Model:     g.Model,
		Messages: []runtime.Message{
			{Role: runtime.RoleSystem, Content: AutoTitleSystemPrompt},
			{Role: runtime.RoleUser, Content: autoTitlePrompt(firstUserMessage, assistantReply)},
		},
	})
	if err != nil {
		return "", err
	}
	if ch == nil {
		return "", errors.New("session: LLMTitleGenerator provider returned no stream")
	}

	var b strings.Builder
	for {
		var (
			chunk runtime.Chunk
			ok    bool
		)
		select {
		case chunk, ok = <-ch:
		case <-reqCtx.Done():
			return "", reqCtx.Err()
		}
		if !ok {
			break
		}
		if chunk.Err != nil {
			return "", chunk.Err
		}
		if chunk.Text != "" {
			b.WriteString(chunk.Text)
		}
		if chunk.Done {
			break
		}
	}

	title := normalizeAutoTitle(b.String(), g.maxChars())
	if title == "" {
		return "", errors.New("session: LLMTitleGenerator returned no title")
	}
	return title, nil
}

func (g LLMTitleGenerator) timeout() time.Duration {
	if g.Timeout == 0 {
		return DefaultAutoTitleTimeout
	}
	return g.Timeout
}

func (g LLMTitleGenerator) maxChars() int {
	if g.MaxChars <= 0 {
		return AutoTitleMaxChars
	}
	return g.MaxChars
}

// autoTitlePrompt renders the material the model titles. The user message is
// the first one, not the latest: the title describes the session's topic.
func autoTitlePrompt(firstUserMessage, assistantReply string) string {
	var b strings.Builder
	b.WriteString("First user message:\n")
	b.WriteString(truncateTitle(strings.TrimSpace(firstUserMessage), autoTitleUserMaxChars))
	b.WriteString("\n\nAssistant reply:\n")
	reply := strings.TrimSpace(assistantReply)
	if reply == "" {
		reply = "(no reply)"
	}
	b.WriteString(truncateTitle(reply, autoTitleReplyMaxChars))
	b.WriteString("\n\nWrite the session title now.")
	return b.String()
}

// fallbackAutoTitle is the deterministic title used when the provider is
// unavailable or returns no usable title: the first user message collapsed to
// one line and truncated. The message is untrusted too, so it goes through the
// same control-character sanitisation as a model-written title.
func fallbackAutoTitle(firstUserMessage string) string {
	text := strings.Join(strings.Fields(sanitizeAutoTitle(firstUserMessage)), " ")
	if text == "" {
		return ""
	}
	return truncateTitle(text, AutoTitleFallbackChars)
}

// autoTitleMarkdownTrim is the markdown/list punctuation normalizeAutoTitle
// removes from both ends of a title. A model often wraps a title in emphasis
// (including across a whole line); matched and unmatched wrappers alike must
// not leak into the stored title. '#' is deliberately absent: it is decoration
// only as a leading heading marker, while C#, F# and "#1" carry it as content,
// so leading heading hashes are stripped separately by
// trimAutoTitleLeadingHeading.
const autoTitleMarkdownTrim = ">*-_~•` \t"

// trimAutoTitleLeadingHeading removes a leading ATX heading marker: a run of
// '#' followed by whitespace or the end of the line. A '#' that starts a token
// such as "#1" is content and is preserved, as is any trailing '#' (C#, F#).
func trimAutoTitleLeadingHeading(line string) string {
	hashes := 0
	for hashes < len(line) && line[hashes] == '#' {
		hashes++
	}
	if hashes == 0 {
		return line
	}
	if hashes < len(line) && line[hashes] != ' ' && line[hashes] != '\t' {
		return line
	}
	return strings.TrimLeft(line[hashes:], " \t")
}

// autoTitleWrapperPairs are matched wrappers stripped before and after the
// label and leading-markdown cleanup. Multi-character pairs are listed first so
// "**bold**" is not mistaken for two independent "*bold*" strips.
var autoTitleWrapperPairs = [][2]string{
	{"**", "**"},
	{"__", "__"},
	{"~~", "~~"},
	{"“", "”"},
	{"‘", "’"},
	{"\"", "\""},
	{"'", "'"},
	{"`", "`"},
	{"*", "*"},
	{"_", "_"},
}

// stripAutoTitleWrappers removes one matched wrapper layer from both ends and
// reports whether the line changed.
func stripAutoTitleWrappers(line string) (string, bool) {
	for _, pair := range autoTitleWrapperPairs {
		open, close := pair[0], pair[1]
		if len(line) <= len(open)+len(close) {
			continue
		}
		if strings.HasPrefix(line, open) && strings.HasSuffix(line, close) {
			return strings.TrimSpace(line[len(open) : len(line)-len(close)]), true
		}
	}
	return line, false
}

// sanitizeAutoTitle removes terminal escape sequences and invisible controls
// from untrusted title text. The set mirrors DSH's session-title sanitizer:
// OSC/CSI/ESC sequences and non-whitespace C0/C1 controls are dropped so a
// title cannot drive the terminal, and directional/invisible controls are
// dropped so a title cannot visually misrepresent itself in the session
// header, sidebar or accessibility labels. Whitespace controls survive for the
// later whitespace collapse.
func sanitizeAutoTitle(raw string) string {
	if raw == "" {
		return ""
	}
	runes := []rune(raw)
	var b strings.Builder
	b.Grow(len(raw))
	for i := 0; i < len(runes); {
		r := runes[i]
		switch {
		case r == '\u001B' && i+1 < len(runes) && runes[i+1] == ']':
			i = skipAutoTitleOSC(runes, i)
			continue
		case r == '\u009D':
			i = skipAutoTitleOSC(runes, i)
			continue
		case r == '\u001B' && i+1 < len(runes) && runes[i+1] == '[':
			i = skipAutoTitleCSI(runes, i)
			continue
		case r == '\u009B':
			i = skipAutoTitleCSI(runes, i)
			continue
		case r == '\u001B':
			// A remaining two-byte ESC sequence: ESC followed by 0x40-0x5F.
			// A lone ESC falls through to the control-character filter below.
			if i+1 < len(runes) && runes[i+1] >= '@' && runes[i+1] <= '_' {
				i += 2
				continue
			}
		}
		if !autoTitleControl(r) && !autoTitleInvisible(r) {
			b.WriteRune(r)
		}
		i++
	}
	return b.String()
}

// skipAutoTitleOSC drops one OSC sequence, from its introducer through BEL,
// ST (ESC \) or the end of the string. An unterminated sequence therefore
// swallows the rest of the title rather than leaking it to the renderer.
func skipAutoTitleOSC(runes []rune, i int) int {
	if runes[i] == '\u001B' {
		i += 2 // ESC ]
	} else {
		i++ // C1 OSC
	}
	for i < len(runes) {
		switch runes[i] {
		case '\u0007': // BEL
			return i + 1
		case '\u001B':
			if i+1 < len(runes) && runes[i+1] == '\\' {
				return i + 2
			}
		}
		i++
	}
	return i
}

// skipAutoTitleCSI drops one complete CSI sequence. An unterminated sequence
// strips only its introducer, matching DSH's ESC[@-_] fallback.
func skipAutoTitleCSI(runes []rune, i int) int {
	introducer := i
	if runes[i] == '\u001B' {
		i += 2 // ESC [
	} else {
		i++ // C1 CSI
	}
	for i < len(runes) && runes[i] >= 0x30 && runes[i] <= 0x3F {
		i++
	}
	for i < len(runes) && runes[i] >= 0x20 && runes[i] <= 0x2F {
		i++
	}
	if i < len(runes) && runes[i] >= 0x40 && runes[i] <= 0x7E {
		return i + 1
	}
	if runes[introducer] == '\u001B' {
		return introducer + 2
	}
	return introducer + 1
}

// autoTitleControl reports whether r is a non-whitespace C0/C1 control
// character. Tab, newline and carriage return are excluded: they are plain
// whitespace and are collapsed later.
func autoTitleControl(r rune) bool {
	switch {
	case r <= 0x08, r == 0x0B, r == 0x0C, r >= 0x0E && r <= 0x1F:
		return true
	case r >= 0x7F && r <= 0x9F:
		return true
	}
	return false
}

// autoTitleInvisible reports whether r is a directional or invisible control
// that could make a displayed title deceptive.
func autoTitleInvisible(r rune) bool {
	switch {
	case r == '\u200B', r == '\u200E', r == '\u200F':
		return true
	case r >= '\u202A' && r <= '\u202E':
		return true
	case r >= '\u2060' && r <= '\u2064':
		return true
	case r >= '\u2066' && r <= '\u206F':
		return true
	case r == '\uFEFF':
		return true
	}
	return false
}

// normalizeAutoTitle cleans one model-written title: it first strips terminal
// control and invisible characters, then keeps the first non-empty line, drops
// matched markdown/quote wrappers and list/heading decoration on both ends,
// strips a "Title:" label and trailing punctuation, collapses whitespace and
// applies the character cap.
func normalizeAutoTitle(raw string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = AutoTitleMaxChars
	}
	raw = sanitizeAutoTitle(raw)
	line := ""
	for _, candidate := range strings.Split(raw, "\n") {
		if strings.TrimSpace(candidate) != "" {
			line = strings.TrimSpace(candidate)
			break
		}
	}

	// Cleanup can expose another layer (a quoted heading, a bold label), so
	// repeat until the line stops changing.
	for {
		before := line
		if stripped, ok := stripAutoTitleWrappers(line); ok {
			line = stripped
		}
		line = strings.TrimSpace(line)
		line = strings.Trim(line, autoTitleMarkdownTrim)
		line = trimAutoTitleLeadingHeading(line)
		line = strings.TrimSpace(line)
		// Trim terminal punctuation inside the loop too: in "**Fix**.", the
		// period hides the closing emphasis from the markdown trim.
		line = strings.TrimRight(line, ".。!！?？;；,，:：")
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"session title:", "title:", "会话标题：", "会话标题:", "标题：", "标题:"} {
			if len(line) >= len(prefix) && strings.EqualFold(line[:len(prefix)], prefix) {
				line = strings.TrimSpace(line[len(prefix):])
				break
			}
		}
		if line == before {
			break
		}
	}
	if stripped, ok := stripAutoTitleWrappers(line); ok {
		line = stripped
	}
	line = strings.TrimSpace(line)
	line = strings.Trim(line, autoTitleMarkdownTrim)
	line = trimAutoTitleLeadingHeading(line)
	line = strings.TrimSpace(line)
	line = strings.TrimRight(line, ".。!！?？;；,，:：")
	line = strings.TrimSpace(line)
	line = strings.Join(strings.Fields(line), " ")
	if line == "" {
		return ""
	}
	return truncateTitle(line, maxChars)
}

// truncateTitle caps s at limit runes, appending an ellipsis when it drops
// text. It is rune-based so CJK titles are not cut mid-character.
func truncateTitle(s string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	if limit == 1 {
		return "…"
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}

// autoTitleEligible reports whether the controller may auto-title sessionID.
// A manual title or an earlier automatic title permanently disables it.
//
// The controller also protects titles that predate the manual-title marker.
// For a session this Controller created, initialTitles remembers the title it
// was created with: a different stored title means somebody changed it outside
// RenameSession (for example the old rename path, or an edit by another
// process), so it is treated as manual and never overwritten. An unchanged
// creation title is only auto-titleable when it is still a placeholder: a
// session created with a meaningful name (an automation run named after its
// task, for example) was named on purpose and that name is preserved. A
// session loaded from the store without such a baseline is likewise only
// auto-titled while it still carries a placeholder title such as "New Session"
// or "Untitled"; anything else is a title chosen before this feature existed
// and is preserved.
func (c *Controller) autoTitleEligible(sessionID string) bool {
	if c.settingFlag(manualTitleKey(sessionID)) || c.settingFlag(autoTitleKey(sessionID)) {
		return false
	}
	sess, err := c.store.GetSession(sessionID)
	if err != nil {
		return false
	}

	c.mu.Lock()
	initialTitle, createdHere := c.initialTitles[sessionID]
	c.mu.Unlock()
	if createdHere {
		if sess.Title != initialTitle {
			_ = c.store.SetSetting(manualTitleKey(sessionID), "1")
			return false
		}
		if !isPlaceholderSessionTitle(initialTitle) {
			// The creation title was chosen deliberately, so pin it. This is
			// what keeps an automation run's task title, the only stored link
			// from the run to its task.
			_ = c.store.SetSetting(manualTitleKey(sessionID), "1")
			return false
		}
		return true
	}
	if isPlaceholderSessionTitle(sess.Title) {
		return true
	}
	// The session and its title both predate this Controller; there is no
	// rename marker to consult, so keep the existing title.
	_ = c.store.SetSetting(manualTitleKey(sessionID), "1")
	return false
}

// isPlaceholderSessionTitle reports whether title is an initial, replaceable
// name rather than a title somebody chose on purpose.
func isPlaceholderSessionTitle(title string) bool {
	switch strings.ToLower(strings.TrimSpace(title)) {
	case "", "new session", "new chat", "untitled", "untitled session",
		"new conversation", "新建会话", "新会话", "未命名", "会话":
		return true
	}
	return false
}

// settingFlag reads a boolean-ish setting. A missing value or a store error
// means false: the guard falls back to the in-memory attempt marker.
func (c *Controller) settingFlag(key string) bool {
	raw, err := c.store.GetSetting(key)
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// firstCompletedTurnText returns the first user message and the assistant
// reply of the successfully completed turn. ok is false when the prompt could
// not be recovered or when this is not the session's first successful turn.
//
// Normally the session must have exactly one user message. The exception is a
// session whose first turn failed or was stopped: autoTitleRetryKey records
// that attempt, and the next successfully completed turn is allowed to title
// the session from the original user message. turnID identifies that successful
// turn, so its reply is preferred over a partial answer persisted by the failed
// attempt.
func (c *Controller) firstCompletedTurnText(sessionID, turnID string) (user, assistant string, ok bool) {
	msgs, err := c.store.ListMessages(sessionID)
	if err != nil {
		return "", "", false
	}
	retry := c.settingFlag(autoTitleRetryKey(sessionID))
	users := 0
	firstReply := ""
	turnReply := ""
	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			users++
			if users == 1 {
				user = msg.Content
			}
		case "assistant":
			if users == 0 {
				continue
			}
			if firstReply == "" {
				firstReply = msg.Content
			}
			if turnReply == "" && turnID != "" && msg.TurnID == turnID {
				turnReply = msg.Content
			}
		}
	}
	if strings.TrimSpace(user) == "" {
		return "", "", false
	}
	if users != 1 && !retry {
		return "", "", false
	}
	assistant = turnReply
	if assistant == "" {
		assistant = firstReply
	}
	return user, assistant, true
}

// rememberAutoTitleRetry records that a session's first turn ended without a
// title (failed or stopped). The next successful turn is then allowed to use
// the first user message even though it is not the only user row. A session
// that already has a conversation is left alone: only a first attempt earns
// the retry marker.
func (c *Controller) rememberAutoTitleRetry(sessionID string) {
	c.mu.Lock()
	closed := c.closed
	considered := c.autoTitles[sessionID]
	c.mu.Unlock()
	if closed || considered {
		return
	}
	if !c.autoTitleEligible(sessionID) {
		return
	}
	msgs, err := c.store.ListMessages(sessionID)
	if err != nil {
		return
	}
	users := 0
	for _, msg := range msgs {
		if msg.Role == "user" {
			users++
		}
	}
	if users != 1 {
		return
	}
	_ = c.store.SetSetting(autoTitleRetryKey(sessionID), "1")
}

// SetTitleGenerator installs or clears the automatic title generator. A nil
// generator keeps the feature on with the truncated-first-message fallback;
// sessions this Controller already titled are not retried.
func (c *Controller) SetTitleGenerator(gen TitleGenerator) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.titleGen = gen
}

// maybeAutoTitle decides whether the completed turn t earns the session's
// automatic title, then writes it off the turn goroutine. Eligibility (manual
// rename, an earlier automatic title, a later turn) is checked synchronously
// so a manual rename never leaves a request in flight; only the model call and
// the store write are asynchronous. A nil generator still yields the
// truncated-first-message fallback.
func (c *Controller) maybeAutoTitle(t *turn) {
	sessionID := t.sessionID

	c.mu.Lock()
	if c.closed || c.autoTitles[sessionID] {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	// A failed or stopped turn does not consume the session's one title
	// attempt. Remember the first attempt so the next successful turn may
	// still title the session from the original prompt.
	if t.stopped.Load() || t.failed.Load() {
		c.rememberAutoTitleRetry(sessionID)
		return
	}

	c.mu.Lock()
	if c.closed || c.autoTitles[sessionID] {
		c.mu.Unlock()
		return
	}
	c.autoTitles[sessionID] = true
	gen := c.titleGen
	c.mu.Unlock()

	if !c.autoTitleEligible(sessionID) {
		return
	}
	user, assistant, ok := c.firstCompletedTurnText(sessionID, t.id)
	if !ok {
		return
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	// Add under mu: Close sets closed and then waits on wg, so it either sees
	// this job or this branch never runs.
	c.wg.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.wg.Done()
		c.writeAutoTitle(gen, sessionID, user, assistant)
	}()
}

// writeAutoTitle asks the generator for a title and persists it. Every exit
// path is best effort: a title failure must never fail the turn or keep the
// store open at shutdown.
func (c *Controller) writeAutoTitle(gen TitleGenerator, sessionID, user, assistant string) {
	ctx := c.titleCtx
	if ctx == nil || ctx.Err() != nil {
		return
	}
	original, err := c.store.GetSession(sessionID)
	if err != nil {
		return
	}

	title := ""
	if gen != nil {
		generated, genErr := gen.GenerateTitle(ctx, sessionID, user, assistant)
		if ctx.Err() != nil {
			// The controller is closing; do not write the fallback either.
			return
		}
		if genErr == nil {
			title = normalizeAutoTitle(generated, AutoTitleMaxChars)
		}
	}
	if title == "" {
		title = fallbackAutoTitle(user)
	}
	if title == "" {
		return
	}

	// Re-check after the model call: the user may have renamed the session
	// while the title was being written. The check and the write are one
	// critical section with RenameSession, otherwise a rename that commits
	// between them is overwritten by this stale automatic title.
	c.titleWriteMu.Lock()
	stored := func() bool {
		defer c.titleWriteMu.Unlock()
		if !c.autoTitleEligible(sessionID) {
			return false
		}
		current, err := c.store.GetSession(sessionID)
		if err != nil || current.Title != original.Title {
			return false
		}
		if ctx.Err() != nil {
			return false
		}
		if _, err := c.store.UpdateSessionTitle(sessionID, title); err != nil {
			return false
		}
		_ = c.store.SetSetting(autoTitleKey(sessionID), "1")
		_ = c.store.SetSetting(autoTitleRetryKey(sessionID), "")
		return true
	}()
	if !stored {
		return
	}
	c.notifyTitleChange(sessionID)
}

// notifyTitleChange republishes the current snapshot so the UI repaints the
// header and sidebar after a title-only store change. Snapshots do not carry a
// title, so listeners refresh it from the session row. The notification is
// unconditional: a background session's title still changes the global
// sidebar, so the listener must run even when another session is current.
func (c *Controller) notifyTitleChange(sessionID string) {
	c.mu.Lock()
	snap := c.views[sessionID]
	if c.currentID != sessionID {
		snap = c.views[c.currentID]
	}
	c.mu.Unlock()
	if snap == nil {
		// No session is current (for example the user deleted the one they
		// were looking at). There is no per-session view to republish, but the
		// global sidebar still needs a repaint; Snapshot never returns nil.
		snap = c.Snapshot()
	}
	c.notifyAny(snap)
}
