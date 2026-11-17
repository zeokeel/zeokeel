package settings

// v0.2.50 removal pin: the settings search vocabulary must not advertise the
// removed tool-loop configuration — no max-iterations search item/anchor on the
// model page, no iteration keywords, and no loop-budget keywords on the
// capabilities page.

import (
	"strings"
	"testing"
)

func TestModelSearchVocabularyHasNoIterations(t *testing.T) {
	for _, item := range modelPageSearchItems(nil) {
		if item.ID == "max-iterations" || item.AnchorKey == "model.max-iterations" {
			t.Errorf("model page still advertises the removed iteration knob: %+v", item)
		}
	}
	for _, keyword := range modelPageKeywords() {
		switch strings.ToLower(keyword) {
		case "最大迭代", "iterations", "迭代", "轮数", "rounds":
			t.Errorf("model page keywords still advertise the removed iteration knob: %q", keyword)
		}
	}
}

func TestCapabilitiesKeywordsHaveNoLoopBudgets(t *testing.T) {
	for _, keyword := range capabilitiesPageKeywords() {
		switch strings.ToLower(keyword) {
		case "循环", "loop", "预算", "budget", "跑飞", "runaway",
			"重复", "repeat", "无变更", "no-diff", "墙钟", "wall", "时长", "token":
			t.Errorf("capabilities keywords still advertise the removed loop budgets: %q", keyword)
		}
	}
}
