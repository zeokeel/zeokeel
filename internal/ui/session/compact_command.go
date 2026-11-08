package session

// /compact: the manual context-compaction command. It runs the optional
// contracts.SessionCompactor asynchronously (the loading badge stays visible
// until the result arrives) and reports the before/after message counts, with
// localized messages for the stable compaction errors.

import (
	"context"
	"errors"
	"fmt"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// compactCommandEnabled reports whether the active session can be compacted:
// the backend implements SessionCompactor, a session is active, it has
// messages and it is not running, and no compaction is already in flight.
func compactCommandEnabled(chat contracts.ChatService, st *state) bool {
	if chat == nil || st == nil || st.compacting {
		return false
	}
	if _, ok := chat.(contracts.SessionCompactor); !ok {
		return false
	}
	sessionID := chat.ActiveSession()
	if sessionID == "" {
		return false
	}
	session, ok := chat.Session(sessionID)
	if !ok || session.Running || len(session.Messages) == 0 {
		return false
	}
	return true
}

// runCompactCommand starts one asynchronous compaction. The st.compacting guard
// (and the command's Enabled callback) prevents duplicate runs.
func runCompactCommand(chat contracts.ChatService, st *state, runtime contracts.UIRuntime) {
	if !compactCommandEnabled(chat, st) {
		return
	}
	compactor, ok := chat.(contracts.SessionCompactor)
	if !ok {
		return
	}
	sessionID := chat.ActiveSession()
	st.compacting = true
	st.commandErr = ""
	runtime.Invalidate()
	go func() {
		result, err := compactor.CompactNow(context.Background(), sessionID)
		runtime.Post(func() {
			st.compacting = false
			if err != nil {
				st.commandErr = compactErrorText(st, err)
				runtime.Invalidate()
				return
			}
			st.commandErr = ""
			notice := ""
			if st.english() {
				notice = fmt.Sprintf("Compacted: %d → %d messages", result.Replaced, result.Saved)
			} else {
				notice = fmt.Sprintf("已压缩：%d 条 → %d 条", result.Replaced, result.Saved)
			}
			if result.BeforeTokens > 0 || result.AfterTokens > 0 {
				if st.english() {
					notice += fmt.Sprintf(" (%d → %d tokens)", result.BeforeTokens, result.AfterTokens)
				} else {
					notice += fmt.Sprintf("（%d → %d tokens）", result.BeforeTokens, result.AfterTokens)
				}
			}
			// The completed compaction is a transient toast, never a resident
			// status badge in the composer.
			st.toast(contracts.NotifySuccess, notice)
			runtime.Invalidate()
		})
	}()
}

// compactErrorText maps the stable compaction errors to localized copy.
func compactErrorText(st *state, err error) string {
	switch {
	case errors.Is(err, contracts.ErrCompactRunning):
		return st.tr("会话正在运行，无法压缩", "Session is running; cannot compact")
	case errors.Is(err, contracts.ErrCompactUnavailable):
		return st.tr("压缩不可用（未配置上下文预算或压缩器）",
			"Compaction is unavailable (no context budget or compactor configured)")
	case errors.Is(err, contracts.ErrCompactNothing):
		return st.tr("没有可压缩的内容", "Nothing to compact")
	case errors.Is(err, contracts.ErrCompactSave):
		return st.tr("保存压缩结果失败：", "Failed to save compacted history: ") + err.Error()
	default:
		return st.tr("压缩失败：", "Compaction failed: ") + err.Error()
	}
}
