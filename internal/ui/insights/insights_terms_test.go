package insights_test

// Frame tests for the Insights screen's terminology fixes (2026-11 wave D):
//
//   - A5 §3.1: the usage unit is "tokens" in both languages — 词元 is gone — and
//     the screen carries a visible plain-language intro line.
//   - US-09: the sessions KPI's 轮/步 detail is plain language — a step is a
//     tool call, said explicitly (the status-bar labels stay, rule 17, but
//     they teach instead of puzzling).

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestInsightsTokensUnitNamed covers A5 §3.1: the unit is spelled "tokens" in
// the zh interface too (词元 is retired), so the KPI label and the table headers
// teach one name for the unit.
func TestInsightsTokensUnitNamed(t *testing.T) {
	service := &fakeInsights{result: sampleInsights()}
	h := newHarness(t, service, &fakeChat{})
	tester := h.tester(1280, 800)
	h.waitForText(tester, "发布检查")

	h.assertTexts(tester, "tokens")
	h.assertNoTexts(tester, "词元")
}

// TestInsightsIntroGuidance covers US-08: the empty state carries a
// plain-language intro that explains what the screen aggregates and how to
// populate it (the screen's 空态引导; the title also describes it in a tooltip).
func TestInsightsIntroGuidance(t *testing.T) {
	service := &fakeInsights{result: contracts.Insights{Days: 7}}
	h := newHarness(t, service, &fakeChat{})
	tester := h.tester(1280, 800)
	h.waitForText(tester, "这个时间范围内还没有用量")

	h.assertTexts(tester, "先运行一轮对话")
}

// TestInsightsTurnsStepsPlainLanguage covers US-09: the 轮/步 detail says a step
// is a tool call.
func TestInsightsTurnsStepsPlainLanguage(t *testing.T) {
	service := &fakeInsights{result: sampleInsights()}
	h := newHarness(t, service, &fakeChat{})
	tester := h.tester(1280, 800)
	h.waitForText(tester, "发布检查")

	// sampleInsights totals: Turns 6, Steps 10.
	h.assertTexts(tester, "6 轮", "10 次工具调用")
}
