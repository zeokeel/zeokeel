package uikit_test

// Headless tests for the shared UI kit. They render the helpers through mygo's
// in-memory Tester (no display, no window) and assert the text the user would
// see, which is the same technique the module tests use.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/uikit"
)

// view draws every uikit helper in one frame so the frame's text list can be
// asserted.
func view(c *ui.Context) {
	uikit.PageHeader(c, "页面标题", "页面副标题")
	uikit.Section(c, "小节标题", func(c *ui.Context) {
		uikit.Bullet(c, "第一条要点")
		uikit.Bullet(c, "第二条要点")
		uikit.KeyValueRows(c, []uikit.KeyValue{
			{Key: "版本", Value: "v0.1.0"},
			{Key: "平台", Value: "linux"},
		})
	})
	uikit.EmptyState(c, "暂无内容")
}

func TestUikitHelpersRenderExpectedText(t *testing.T) {
	tester := ui.NewTester(view, 800, 600)
	for _, want := range []string{
		"页面标题", "页面副标题",
		"小节标题",
		"第一条要点", "第二条要点",
		"版本", "v0.1.0",
		"平台", "linux",
		"暂无内容",
	} {
		if !tester.HasText(want) {
			t.Errorf("frame does not show %q; texts = %v", want, tester.Texts())
		}
	}
}

func TestPageHeaderOmitsEmptySubtitle(t *testing.T) {
	tester := ui.NewTester(func(c *ui.Context) {
		uikit.PageHeader(c, "只有标题", "")
	}, 400, 200)
	if !tester.HasText("只有标题") {
		t.Fatalf("frame does not show the title; texts = %v", tester.Texts())
	}
	for _, text := range tester.Texts() {
		if text == "" {
			t.Fatalf("empty text element in frame: %v", tester.Texts())
		}
	}
}

func TestSectionWithNilBodyStillRendersTitle(t *testing.T) {
	tester := ui.NewTester(func(c *ui.Context) {
		uikit.Section(c, "空小节", nil)
	}, 400, 200)
	if !tester.HasText("空小节") {
		t.Fatalf("frame does not show the section title; texts = %v", tester.Texts())
	}
}
