package insights_test

// Frame test for the 2026-11-17 UI review proposal: the three usage tables'
// token column header names the metric without the unit suffix (the old
// "词元 tokens" truncated to "词元 tok…" at the column width while the row had
// room to spare); the unit stays on the KPI label above.

import "testing"

func TestInsightsTokenHeadersNotTruncated(t *testing.T) {
	service := &fakeInsights{result: sampleInsights()}
	h := newHarness(t, service, &fakeChat{})
	tester := h.tester(1280, 800)
	h.waitForText(tester, "发布检查")

	// The day/model/workspace tables each name the metric once, untruncated.
	count := 0
	for _, text := range tester.Texts() {
		if text == "tokens" {
			count++
		}
	}
	// Three table headers plus the KPI label above them.
	if count != 4 {
		t.Fatalf("exact tokens labels = %d, want 4 (3 headers + the KPI label); texts = %v",
			count, tester.Texts())
	}
	h.assertNoTexts(tester, "tok…")
	// The unit is the header itself now (A5 §3.1: one name for the unit).
	h.assertNoTexts(tester, "词元")
}
