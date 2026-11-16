package settings_test

// Frame tests for the behavior page's teaching/clarity fixes (2026-11 wave D):
//
//   - US-06: the work-details level meanings (meaningZh/En, previously dead
//     code) render as the selected level's description, so the four abstract
//     levels are explained instead of guessed.
//   - US-13: the page's two submit models are partitioned explicitly — the
//     display preferences apply immediately, the run-limits form needs the
//     footer Save — so the user knows which controls need saving.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// TestBehaviorWorkStepsMeaningVisible covers US-06: the selected level's meaning
// is shown and follows the selection.
func TestBehaviorWorkStepsMeaningVisible(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openBehaviorPage(t, h)

	// The default (标准) level explains itself.
	h.assertTexts(tester, "一行状态 + 实时细节，回答结束后折叠")

	// Choosing another level swaps the description to that level's meaning.
	h.click(tester, "详细")
	h.assertTexts(tester, "运行中逐步显示工具调用")

	h.click(tester, "完全展开")
	h.assertTexts(tester, "不折叠不分组，全部展开")
}

// TestBehaviorLimitsRequireSaveNote covers US-13: the run-limits section says it
// needs the footer Save, distinguishing it from the immediate-apply preferences.
func TestBehaviorLimitsRequireSaveNote(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openBehaviorPage(t, h)

	h.assertTexts(tester, "运行限额", "需点页脚「保存」")
}
