package session

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// CompactCurrent compacts the current session's conversation on demand: the
// backend of the shell's /compact command. The Controller owns the durable
// conversation, so it implements runtime.CompactionPersister: CompactNow hands
// the compacted surface back here, this method replaces the stored messages
// (delete the replaced rows, insert the summary), logs the compaction event and
// folds it into the live snapshot. The next turn's History reads the compacted
// conversation and a reopened session replays it, so /compact is durable.
//
// It returns the number of stored history items the compaction replaced and the
// estimated tokens it saved. Failures are classified with errors.Is:
// ErrInvalidArgument when no session is current, ErrTurnRunning while a turn
// is in flight, ErrNoAgent when no agent is configured, and the runtime
// compaction sentinels (runtime.ErrNoCompactor, runtime.ErrNoCompactableHistory,
// ...) from the agent.
func (c *Controller) CompactCurrent(ctx context.Context) (replacedItems, estimatedTokensSaved int, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, 0, ErrClosed
	}
	sessionID := strings.TrimSpace(c.currentID)
	agent := c.agent
	factory := c.agentFactory
	busy := sessionID != "" && c.turns[sessionID] != nil
	c.mu.Unlock()

	if sessionID == "" {
		return 0, 0, fmt.Errorf("%w: compact: no current session", ErrInvalidArgument)
	}
	if busy {
		// The controller knows the turn before the runtime's runMu does (a turn
		// still preparing its prompt has not taken the topology lock yet), so
		// report the busy state here.
		return 0, 0, fmt.Errorf("%w: compact: session %q", ErrTurnRunning, sessionID)
	}

	// Per-session agents (RT-04): compact the session's own agent, never the
	// shared legacy one.
	if factory != nil {
		built, buildErr := c.agentFor(sessionID)
		if buildErr != nil {
			return 0, 0, fmt.Errorf("session: compact: build agent for session %q: %w", sessionID, buildErr)
		}
		agent = built
	}
	if agent == nil {
		return 0, 0, ErrNoAgent
	}
	return agent.CompactNow(ctx, sessionID, c)
}

// PersistCompaction implements runtime.CompactionPersister over the messages
// table: it deletes the stored messages the compacted conversation dropped,
// keeps the recent tail rows (ids, attachments and feedback intact), inserts the
// summary message in their place, prunes the replaced turns' tool calls, then
// records and folds the state.Compaction event. The messages table is the
// single source of truth, so the live snapshot and a reopened session agree.
func (c *Controller) PersistCompaction(ctx context.Context, sessionID string, prior, compacted []runtime.Message) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return 0, fmt.Errorf("%w: compact: empty session id", ErrInvalidArgument)
	}
	if len(compacted) == 0 {
		return 0, runtime.ErrNoCompactableHistory
	}

	messages, err := c.store.ListMessages(sessionID)
	if err != nil {
		return 0, fmt.Errorf("session: compact: load messages for %q: %w", sessionID, err)
	}
	if len(messages) == 0 {
		return 0, runtime.ErrNoCompactableHistory
	}
	keepStart, err := compactedStoreBoundary(compacted, messages)
	if err != nil {
		return 0, fmt.Errorf("session: compact: %w", err)
	}
	if keepStart <= 0 {
		// The compactor kept every stored message: nothing to replace.
		return 0, runtime.ErrNoCompactableHistory
	}

	replacedIDs := make([]string, 0, keepStart)
	for i := 0; i < keepStart; i++ {
		replacedIDs = append(replacedIDs, messages[i].ID)
	}

	// Tool calls are interleaved into the model history by start time. A call
	// that started at or after the first kept message belongs to a kept turn;
	// everything before it belonged to the replaced turns and is deleted with
	// them (the store API refuses to leave orphans that would replay above the
	// summary).
	var keepToolCallIDs []string
	if keepStart < len(messages) {
		calls, callErr := c.store.ToolCallsForSession(sessionID)
		if callErr != nil {
			return 0, fmt.Errorf("session: compact: load tool calls for %q: %w", sessionID, callErr)
		}
		boundary := messages[keepStart].CreatedAt
		for _, call := range calls {
			if !call.StartedAt.Before(boundary) {
				keepToolCallIDs = append(keepToolCallIDs, call.ID)
			}
		}
	}

	// The summary row is persisted as an assistant message because the app's
	// History adapter (storeHistory) only replays user/assistant rows; the
	// content is framed with the runtime's stable compaction marker so a later
	// auto-compaction still recognizes it as a summary after a reopen instead
	// of treating it as an ordinary answer.
	inserted, err := c.store.ReplaceCompactedMessages(sessionID, replacedIDs, store.Message{
		Role:    string(state.RoleAssistant),
		Content: runtime.CompactionSummaryContent(len(replacedIDs), compactedSummaryText(compacted)),
	}, keepToolCallIDs)
	if err != nil {
		return 0, fmt.Errorf("session: compact: replace messages for %q: %w", sessionID, err)
	}

	c.recordCompaction(sessionID, replacedIDs, inserted)
	return len(replacedIDs), nil
}

// recordCompaction persists the compaction event and folds it into the live
// snapshot so the open session immediately shows the compacted conversation.
// The messages table is authoritative for a reopen; a failed event append is
// logged, and folding still happens because the snapshot is the live view.
func (c *Controller) recordCompaction(sessionID string, replacedIDs []string, inserted *store.Message) {
	if inserted == nil {
		return
	}
	event := state.Compaction{
		ReplacedMsgIDs: append([]string(nil), replacedIDs...),
		Summary: state.Message{
			MsgID:     inserted.ID,
			Role:      state.Role(inserted.Role),
			Text:      inserted.Content,
			Reasoning: inserted.Reasoning,
		},
	}
	if err := c.appendEvent(sessionID, event); err != nil {
		log.Printf("zeokeel: session: append compaction event for %q: %v", sessionID, err)
	}

	c.mu.Lock()
	snap := c.views[sessionID]
	if snap == nil {
		snap = state.NewSnapshot()
		snap.SessionID = sessionID
	}
	next := state.Apply(snap, event)
	c.views[sessionID] = next
	current := c.currentID == sessionID && !c.closed
	c.mu.Unlock()

	if c.broker != nil {
		c.broker.Publish(event)
	}
	if current {
		c.notify(sessionID, next)
	}
}

// compactedStoreBoundary matches the compacted conversation's kept tail against
// the stored messages from the end and returns the index of the first kept
// stored message (every earlier row was replaced by the summary). It skips the
// runtime-only entries the History adapter synthesizes (system summary, tool
// call/result expansion) and fails when a user/assistant text message of the
// compacted tail has no matching stored row: that means the agent's History is
// not this session's store, and deleting unrepresented rows would corrupt it.
func compactedStoreBoundary(compacted []runtime.Message, messages []store.Message) (int, error) {
	si := len(messages) - 1
	ci := len(compacted) - 1
	for si >= 0 && ci >= 0 {
		rm := compacted[ci]
		switch {
		case rm.Role == runtime.RoleSystem:
			ci--
			continue
		case rm.Role == runtime.RoleTool:
			ci--
			continue
		case rm.Role == runtime.RoleAssistant && len(rm.ToolCalls) > 0:
			ci--
			continue
		}
		if !storeMessageMatchesRuntime(messages[si], rm) {
			break
		}
		si--
		ci--
	}
	for _, rm := range compacted[:ci+1] {
		if rm.Role == runtime.RoleUser || (rm.Role == runtime.RoleAssistant && len(rm.ToolCalls) == 0) {
			return 0, fmt.Errorf(
				"%w: compacted conversation does not match the stored history: unexpected %q message %q",
				runtime.ErrCompactionFailed, rm.Role, string(rm.Content))
		}
	}
	return si + 1, nil
}

// storeMessageMatchesRuntime reports whether one stored row and one runtime
// message describe the same conversation entry. The production History adapter
// prefixes attachment prompts onto the stored user text, so the runtime content
// may extend the stored content (never rewrite it).
func storeMessageMatchesRuntime(stored store.Message, message runtime.Message) bool {
	if strings.TrimSpace(stored.Role) != string(message.Role) {
		return false
	}
	if stored.Content == message.Content {
		return true
	}
	return message.Role == runtime.RoleUser && strings.HasPrefix(message.Content, stored.Content)
}

// compactedSummaryText returns the compactor's own summary text: WindowCompactor
// and LLMCompactor append a synthetic system summary after any preserved system
// messages. It may be empty (a custom compactor without a summary); the caller
// frames it with runtime.CompactionSummaryContent so the persisted row is always
// recognizable.
func compactedSummaryText(compacted []runtime.Message) string {
	summary := ""
	for _, message := range compacted {
		if message.Role == runtime.RoleSystem && strings.TrimSpace(message.Content) != "" {
			summary = strings.TrimSpace(message.Content)
		}
	}
	return summary
}
