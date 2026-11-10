package settings_test

// Frame tests for the behavior page's 工作步骤 (work details) section: the
// DSH-aligned four-level display control (compact / standard / detailed /
// verbose, default standard) and the two collapse timings (completion, default /
// next-input), which persist immediately through ui.workSteps.*.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// openBehaviorPage opens the behavior page and returns its tester.
func openBehaviorPage(t *testing.T, h *harness) *ui.Tester {
	t.Helper()
	tester := h.tester()
	tester.Frame()
	h.click(tester, "行为")
	tester.Frame()
	return tester
}

// requireSegmentChecked asserts one segmented-control segment is selected.
func requireSegmentChecked(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	info, ok := tester.Access(label)
	if !ok {
		t.Fatalf("segment %q is not accessible; texts = %v", label, tester.Texts())
	}
	if !info.Checked {
		t.Fatalf("segment %q is not selected; texts = %v", label, tester.Texts())
	}
}

// requireSegmentUnchecked asserts one segmented-control segment is not selected.
func requireSegmentUnchecked(t *testing.T, tester *ui.Tester, label string) {
	t.Helper()
	info, ok := tester.Access(label)
	if !ok {
		t.Fatalf("segment %q is not accessible; texts = %v", label, tester.Texts())
	}
	if info.Checked {
		t.Fatalf("segment %q is unexpectedly selected; texts = %v", label, tester.Texts())
	}
}

// TestBehaviorPageWorkStepsDefaults covers the visible section and its defaults:
// the four display levels and two collapse timings are listed, the defaults
// (标准 / 回答结束后) are selected, rendering writes nothing, and the section
// sits between the permission section and the collapsed conversation options.
func TestBehaviorPageWorkStepsDefaults(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openBehaviorPage(t, h)

	h.assertTexts(tester,
		"工作步骤", "工作步骤展示", "工作步骤收起时机",
		"选择希望看到多少工具调用细节。默认：标准。",
		"简洁", "标准", "详细", "完全展开",
		"回答结束后", "下次有新消息时",
		"标准：一行状态 + 实时细节，回答结束后折叠",
		"完全展开：不折叠不分组，全部展开",
	)
	requireSegmentChecked(t, tester, "标准")
	requireSegmentChecked(t, tester, "回答结束后")
	requireSegmentUnchecked(t, tester, "简洁")
	requireSegmentUnchecked(t, tester, "详细")
	requireSegmentUnchecked(t, tester, "完全展开")
	requireSegmentUnchecked(t, tester, "下次有新消息时")

	// The section sits between the daily permission choice and the collapsed
	// low-frequency settings.
	permission, ok := tester.Find("默认代理权限")
	if !ok {
		t.Fatalf("permission section missing; texts = %v", tester.Texts())
	}
	workSteps, ok := tester.Find("工作步骤")
	if !ok {
		t.Fatalf("work-details section missing; texts = %v", tester.Texts())
	}
	more, ok := tester.Find("更多对话设置")
	if !ok {
		t.Fatalf("more-settings disclosure missing; texts = %v", tester.Texts())
	}
	if !(permission.Y < workSteps.Y && workSteps.Y < more.Y) {
		t.Fatalf("section order y = permission %.1f, work steps %.1f, more %.1f; want permission < work steps < more",
			permission.Y, workSteps.Y, more.Y)
	}

	// Rendering never writes the defaults: the rows stay absent.
	if raw := store.get(contracts.SettingWorkStepsMode); raw != "" {
		t.Fatalf("rendering wrote %s = %q", contracts.SettingWorkStepsMode, raw)
	}
	if raw := store.get(contracts.SettingWorkStepsCollapse); raw != "" {
		t.Fatalf("rendering wrote %s = %q", contracts.SettingWorkStepsCollapse, raw)
	}
}

// TestBehaviorPageWorkStepsPersist covers the immediate writes: choosing a
// display level or a collapse timing persists the documented value and reports
// "已保存", and switching back writes the defaults again.
func TestBehaviorPageWorkStepsPersist(t *testing.T) {
	store := newFakeSettings(nil)
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openBehaviorPage(t, h)

	h.click(tester, "完全展开")
	if raw := store.get(contracts.SettingWorkStepsMode); raw != contracts.WorkStepsVerbose {
		t.Fatalf("%s = %q, want %q", contracts.SettingWorkStepsMode, raw, contracts.WorkStepsVerbose)
	}
	h.assertTexts(tester, "已保存")
	requireSegmentChecked(t, tester, "完全展开")

	h.click(tester, "详细")
	if raw := store.get(contracts.SettingWorkStepsMode); raw != contracts.WorkStepsDetailed {
		t.Fatalf("%s = %q, want %q", contracts.SettingWorkStepsMode, raw, contracts.WorkStepsDetailed)
	}
	h.click(tester, "简洁")
	if raw := store.get(contracts.SettingWorkStepsMode); raw != contracts.WorkStepsCompact {
		t.Fatalf("%s = %q, want %q", contracts.SettingWorkStepsMode, raw, contracts.WorkStepsCompact)
	}

	h.click(tester, "下次有新消息时")
	if raw := store.get(contracts.SettingWorkStepsCollapse); raw != contracts.WorkStepsCollapseNextInput {
		t.Fatalf("%s = %q, want %q", contracts.SettingWorkStepsCollapse, raw,
			contracts.WorkStepsCollapseNextInput)
	}
	h.assertTexts(tester, "已保存")
	requireSegmentChecked(t, tester, "下次有新消息时")

	// Choosing the defaults back persists the documented default values.
	h.click(tester, "标准")
	if raw := store.get(contracts.SettingWorkStepsMode); raw != contracts.WorkStepsStandard {
		t.Fatalf("%s = %q, want %q", contracts.SettingWorkStepsMode, raw, contracts.WorkStepsStandard)
	}
	h.click(tester, "回答结束后")
	if raw := store.get(contracts.SettingWorkStepsCollapse); raw != contracts.WorkStepsCollapseCompletion {
		t.Fatalf("%s = %q, want %q", contracts.SettingWorkStepsCollapse, raw,
			contracts.WorkStepsCollapseCompletion)
	}
	requireSegmentChecked(t, tester, "标准")
	requireSegmentChecked(t, tester, "回答结束后")
}

// TestBehaviorPageWorkStepsBadValuesFallBack covers malformed stored rows: they
// display (and are treated as) the defaults without being rewritten by a frame,
// and a subsequent explicit choice recovers the row.
func TestBehaviorPageWorkStepsBadValuesFallBack(t *testing.T) {
	store := newFakeSettings(map[string]string{
		contracts.SettingWorkStepsMode:     "bogus",
		contracts.SettingWorkStepsCollapse: "whenever",
	})
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openBehaviorPage(t, h)

	requireSegmentChecked(t, tester, "标准")
	requireSegmentChecked(t, tester, "回答结束后")
	requireSegmentUnchecked(t, tester, "完全展开")
	requireSegmentUnchecked(t, tester, "下次有新消息时")
	// A frame keeps the malformed rows untouched (they normalize on read).
	if raw := store.get(contracts.SettingWorkStepsMode); raw != "bogus" {
		t.Fatalf("a frame rewrote the malformed mode row to %q", raw)
	}
	if raw := store.get(contracts.SettingWorkStepsCollapse); raw != "whenever" {
		t.Fatalf("a frame rewrote the malformed collapse row to %q", raw)
	}

	// An explicit choice overwrites the malformed row.
	h.click(tester, "完全展开")
	if raw := store.get(contracts.SettingWorkStepsMode); raw != contracts.WorkStepsVerbose {
		t.Fatalf("explicit mode choice wrote %q, want %q", raw, contracts.WorkStepsVerbose)
	}
}

// TestBehaviorPageWorkStepsWriteFailure covers the shared page status line: a
// failed write reports the store error instead of claiming success.
func TestBehaviorPageWorkStepsWriteFailure(t *testing.T) {
	store := newFakeSettings(nil)
	store.setErr = errBehaviorStoreWrite
	h := newHarness(t, &fakeChat{}, store, settings.Config{})
	tester := openBehaviorPage(t, h)

	h.click(tester, "详细")
	h.assertTexts(tester, "保存失败：")
}

// TestBehaviorPageWorkStepsEnglish covers the English copy of the section, its
// defaults and its writes.
func TestBehaviorPageWorkStepsEnglish(t *testing.T) {
	store := newFakeSettings(nil)
	h := newLocaleHarness(t, &fakeChat{}, store, settings.Config{}, "en")
	tester := h.tester()
	tester.Frame()
	h.click(tester, "Behavior")
	tester.Frame()

	h.assertTexts(tester,
		"Work details", "When to collapse work details",
		"Choose how much tool-call detail to see. Default: standard.",
		"Compact", "Standard", "Detailed", "Verbose",
		"After the answer", "On the next message",
		"One status line + live details, folded after the answer",
		"Everything expanded, never folded or grouped",
	)
	h.assertNoTexts(tester, "工作步骤", "标准：")
	requireSegmentChecked(t, tester, "Standard")
	requireSegmentChecked(t, tester, "After the answer")

	h.click(tester, "Verbose")
	if raw := store.get(contracts.SettingWorkStepsMode); raw != contracts.WorkStepsVerbose {
		t.Fatalf("English mode choice wrote %q, want %q", raw, contracts.WorkStepsVerbose)
	}
	h.click(tester, "On the next message")
	if raw := store.get(contracts.SettingWorkStepsCollapse); raw != contracts.WorkStepsCollapseNextInput {
		t.Fatalf("English collapse choice wrote %q, want %q", raw,
			contracts.WorkStepsCollapseNextInput)
	}
	h.assertTexts(tester, "Saved")
}
