package toolcards_test

// Tests for the transcript plan review card (plan_review tool).

import (
	"context"
	"testing"

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
	h.assertTexts(tester, "计划审阅", "摘要")
	h.assertNoTexts(tester, "not-json")
	h.click(tester, "摘要")
	h.assertTexts(tester, "not-json")
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
