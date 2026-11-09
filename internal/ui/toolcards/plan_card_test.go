package toolcards_test

// Tests for the transcript plan review card (plan_review tool).

import (
	"time"

	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/toolcards"
)

// fakePlanReviewer is a scripted contracts.PlanReviewer.
type fakePlanReviewer struct{ state string }

func (f *fakePlanReviewer) SetPlanHandler(func(contracts.PlanReview)) {}
func (f *fakePlanReviewer) Review(context.Context, string) (string, error) {
	return f.state, nil
}
func (f *fakePlanReviewer) Exit(string) (string, error) { return f.state, nil }
func (f *fakePlanReviewer) State() string               { return f.state }

func newPlanHarness(t *testing.T, reviewer *fakePlanReviewer) *harness {
	t.Helper()
	h := newHarness(t)
	if reviewer != nil {
		provide[contracts.PlanReviewer](t, h.ctx, contracts.KeyPlanReviewer, reviewer)
	}
	activate(t, h.ctx, toolcards.Module(), toolcards.Config{})
	return h
}

func TestPlanReviewCardStatusLabels(t *testing.T) {
	reviewer := &fakePlanReviewer{state: "reviewing"}
	h := newPlanHarness(t, reviewer)
	h.showCalls([]contracts.ToolCall{{
		ID: "plan-1", Name: "plan_review", Status: "ok",
		Summary: `{"plan":"# Release plan\n\n1. test"}`,
	}}, nil, nil)
	tester := h.tester()

	for state, label := range map[string]string{
		"reviewing": "审阅中",
		"approved":  "已批准",
		"cancelled": "已取消",
		"idle":      "待审阅",
	} {
		reviewer.state = state
		tester.Frame()
		h.assertTexts(tester, "计划审阅", label)
	}
}

func TestPlanReviewCardRendersMarkdown(t *testing.T) {
	reviewer := &fakePlanReviewer{state: "reviewing"}
	h := newPlanHarness(t, reviewer)
	h.showCalls([]contracts.ToolCall{{
		ID: "plan-1", Name: "plan_review", Status: "ok",
		Summary: `{"plan":"# Release plan\n\n1. test\n2. ship"}`,
	}}, nil, nil)
	tester := h.tester()
	h.assertTexts(tester, "计划审阅", "Release plan", "test", "ship")
	// The raw JSON envelope is not shown as the primary content.
	h.assertNoTexts(tester, `{"plan":`)
}

func TestPlanReviewCardBadArgumentsFallBack(t *testing.T) {
	reviewer := &fakePlanReviewer{state: "reviewing"}
	h := newPlanHarness(t, reviewer)
	h.showCalls([]contracts.ToolCall{{
		ID: "plan-2", Name: "plan_review", Status: "ok", Summary: "not-json",
	}}, nil, nil)
	tester := h.tester()
	h.assertTexts(tester, "计划审阅", "详情")
	h.assertTexts(tester, "not-json") // short/failed payload starts expanded (H15)
	h.click(tester, "详情")
	tester.Advance(300 * time.Millisecond) // the 200 ms close animation
	h.assertNoTexts(tester, "not-json")
}

func TestPlanReviewCardWithoutServiceHasNoStatus(t *testing.T) {
	h := newPlanHarness(t, nil)
	h.showCalls([]contracts.ToolCall{{
		ID: "plan-3", Name: "plan_review", Status: "ok",
		Summary: `{"plan":"# Plan"}`,
	}}, nil, nil)
	tester := h.tester()
	h.assertTexts(tester, "计划审阅", "Plan")
	h.assertNoTexts(tester, "审阅中", "已批准", "已取消", "待审阅")
}

func TestPlanReviewCardEnglish(t *testing.T) {
	reviewer := &fakePlanReviewer{state: "approved"}
	h := newPlanHarness(t, reviewer)
	h.showCalls([]contracts.ToolCall{{
		ID: "plan-1", Name: "plan_review", Status: "ok",
		Summary: `{"plan":"# Ship it"}`,
	}}, nil, nil)
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "Plan review", "Approved", "Ship it")
}

func TestPlanReviewIsCoveredByName(t *testing.T) {
	names := toolcards.RealToolNames()
	found := false
	for _, name := range names {
		if name == "plan_review" {
			found = true
		}
	}
	if !found {
		t.Fatalf("plan_review missing from real tool names: %v", names)
	}
}

// dangerPixels counts the frame's brand danger-red pixels: a neutral status
// badge must not paint the failure color.
func dangerPixels(t *testing.T, tester *ui.Tester) int {
	t.Helper()
	img := tester.Image()
	count := 0
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			pixel := img.RGBAAt(x, y)
			r, g, b := int(pixel.R), int(pixel.G), int(pixel.B)
			if r > 120 && r > g+40 && r > b+40 {
				count++
			}
		}
	}
	return count
}

// TestPlanReviewCardCancelledIsNeutral covers DESIGN.md §5.2 / F-2: a cancelled
// plan review is a user stop, so its badge is neutral — it must not paint the
// danger red of a failure nor read as 失败. Approved and pending keep their
// existing success/waiting semantics.
func TestPlanReviewCardCancelledIsNeutral(t *testing.T) {
	h := newPlanHarness(t, &fakePlanReviewer{state: "cancelled"})
	h.showCalls([]contracts.ToolCall{{
		ID: "plan-4", Name: "plan_review", Status: "ok",
		Summary: `{"plan":"# Plan"}`,
	}}, nil, nil)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "计划审阅", "已取消")
	h.assertNoTexts(tester, "失败")
	if got := dangerPixels(t, tester); got != 0 {
		t.Fatalf("cancelled plan badge paints %d danger-red pixels, want a neutral tint", got)
	}

	// Approved and pending keep their labels (and the success tint is not the
	// failure red either).
	for _, want := range []struct{ state, label string }{
		{"approved", "已批准"},
		{"idle", "待审阅"},
	} {
		reviewer := h.ctx
		_ = reviewer
		other := newPlanHarness(t, &fakePlanReviewer{state: want.state})
		other.showCalls([]contracts.ToolCall{{
			ID: "plan-5", Name: "plan_review", Status: "ok",
			Summary: `{"plan":"# Plan"}`,
		}}, nil, nil)
		otherTester := other.tester()
		otherTester.Frame()
		other.assertTexts(otherTester, want.label)
	}
}
