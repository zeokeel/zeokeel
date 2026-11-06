package app

import (
	"fmt"
	"strings"

	"github.com/zeokeel/zeokeel/old/internal/session"
)

// stepLimitNoticeFor returns the assistant notice the runtime emits when one
// turn reaches the configured per-turn step ceiling. The runtime completes such
// a turn like any other (history, tool results and the checkpoint are kept), so
// the copy says what happened and how to continue. The language follows
// ui.language with the product's Chinese default, like the notification copy
// (see app.uiLanguage).
func stepLimitNoticeFor(language string) func(limit int) string {
	if strings.EqualFold(strings.TrimSpace(language), session.LanguageEnglish) {
		return func(limit int) string {
			return fmt.Sprintf(
				"Reached this turn's step limit (%d steps). The work and results so far are saved; send another message and I'll continue from here.",
				limit)
		}
	}
	return func(limit int) string {
		return fmt.Sprintf(
			"已达到单轮步骤上限（%d 步）。已完成的操作与结果都已保留，继续发消息我会接着完成。",
			limit)
	}
}
