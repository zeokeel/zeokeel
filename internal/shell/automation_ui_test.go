package shell

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/store"
)

// TestAutomationTasksCRUDAndRunNow drives the Automation screen end to end:
// create through the empty state, run now, disable/enable, edit and delete,
// with the store asserted after every step.
func TestAutomationTasksCRUDAndRunNow(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	var ran []string
	h.app.SetAutomationRunNow(func(id string) { ran = append(ran, id) })

	tt := ui.NewTester(h.app.View, 1600, 1000)
	if err := tt.Click("Automation tasks"); err != nil {
		t.Fatalf("click Automation tasks: %v", err)
	}
	if !tt.HasText("No tasks yet. Tasks created in your sessions appear here.") {
		t.Fatalf("empty state missing: %q", tt.Texts())
	}

	// Create through the empty state's outline action.
	if err := tt.Click("New automation task"); err != nil {
		t.Fatalf("click New automation task: %v", err)
	}
	tt.Type("Daily report")
	tt.Key(0, ui.KeyTab)
	tt.Type("Summarize yesterday's changes")
	tt.Key(0, ui.KeyTab)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("*/5 * * * *")
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("click Save: %v", err)
	}

	tasks, err := h.ctrl.Automations().List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v, want one", tasks)
	}
	task := tasks[0]
	if task.Title != "Daily report" || task.Prompt != "Summarize yesterday's changes" ||
		task.Cron != "*/5 * * * *" || !task.Enabled {
		t.Fatalf("stored task = %+v", task)
	}
	if task.NextRun == nil || !task.NextRun.After(time.Now().Add(-time.Minute)) {
		t.Fatalf("NextRun = %v, want a future schedule", task.NextRun)
	}
	if !tt.HasText("Daily report") || !tt.HasText("Every 5 minutes") {
		t.Fatalf("task row missing: %q", tt.Texts())
	}

	// Run now goes through the app's hook.
	if err := tt.Click("Run now"); err != nil {
		t.Fatalf("click Run now: %v", err)
	}
	if len(ran) != 1 || ran[0] != task.ID {
		t.Fatalf("RunNow calls = %v, want [%s]", ran, task.ID)
	}
	if !tt.HasText("Automation started") {
		t.Fatalf("run toast missing: %q", tt.Texts())
	}

	// Disable and enable through the row actions. A disabled task must not
	// keep (or render) a next run that can never fire.
	if err := tt.Click("Disable"); err != nil {
		t.Fatalf("click Disable: %v", err)
	}
	disabled, err := h.ctrl.Automations().Get(task.ID)
	if err != nil || disabled.Enabled {
		t.Fatalf("task after Disable = %+v, %v", disabled, err)
	}
	if disabled.NextRun != nil {
		t.Fatalf("disabled task NextRun = %v, want nil", disabled.NextRun)
	}
	if detail := h.app.automationTaskDetail(*disabled); strings.Contains(detail, "next ") {
		t.Fatalf("disabled task detail still shows a next run: %q", detail)
	}
	if !tt.HasText("Inactive") {
		t.Fatalf("inactive badge missing: %q", tt.Texts())
	}
	if err := tt.Click("Enable"); err != nil {
		t.Fatalf("click Enable: %v", err)
	}
	enabled, err := h.ctrl.Automations().Get(task.ID)
	if err != nil || !enabled.Enabled {
		t.Fatalf("task after Enable = %+v, %v", enabled, err)
	}
	if enabled.NextRun == nil {
		t.Fatal("enabled task has no NextRun")
	}

	// Edit keeps the row identity and updates the fields.
	if err := tt.Click("Edit"); err != nil {
		t.Fatalf("click Edit: %v", err)
	}
	if !tt.HasText("Edit automation task") {
		t.Fatalf("edit dialog missing: %q", tt.Texts())
	}
	h.app.shellUI().automationDraft.title = "Daily report v2"
	h.app.shellUI().automationDraft.prompt = "Summarize today"
	tt.Frame()
	if err := tt.Click("Save"); err != nil {
		t.Fatalf("click Save (edit): %v", err)
	}
	edited, err := h.ctrl.Automations().Get(task.ID)
	if err != nil {
		t.Fatalf("Get after edit: %v", err)
	}
	if edited.Title != "Daily report v2" || edited.Prompt != "Summarize today" {
		t.Fatalf("edited task = %+v", edited)
	}
	if len(edited.ID) != len(task.ID) {
		t.Fatalf("edit changed the id: %q -> %q", task.ID, edited.ID)
	}

	// Delete asks first: the task survives until the Danger button confirms.
	if err := tt.Click("Delete"); err != nil {
		t.Fatalf("click Delete: %v", err)
	}
	if !tt.HasText("Delete automation task?") {
		t.Fatalf("delete confirmation missing: %q", tt.Texts())
	}
	if tasks, err := h.ctrl.Automations().List(); err != nil || len(tasks) != 1 {
		t.Fatalf("task deleted before the confirmation = %+v, %v", tasks, err)
	}
	if err := tt.Click("Delete task"); err != nil {
		t.Fatalf("confirm Delete: %v", err)
	}
	if tasks, err := h.ctrl.Automations().List(); err != nil || len(tasks) != 0 {
		t.Fatalf("tasks after delete = %+v, %v", tasks, err)
	}
	if !tt.HasText("No tasks yet. Tasks created in your sessions appear here.") {
		t.Fatalf("empty state missing after delete: %q", tt.Texts())
	}
}

// TestAutomationFiltersAndSearch covers the All / Enabled / Inactive tabs and
// the search field over real tasks.
func TestAutomationFiltersAndSearch(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	if _, err := h.ctrl.Automations().Create(store.Automation{
		Title: "Enabled task", Cron: "@hourly", Enabled: true,
	}); err != nil {
		t.Fatalf("Create enabled: %v", err)
	}
	if _, err := h.ctrl.Automations().Create(store.Automation{
		Title: "Inactive task", Cron: "@daily", Enabled: false,
	}); err != nil {
		t.Fatalf("Create inactive: %v", err)
	}

	tt := ui.NewTester(h.app.View, 1600, 1000)
	if err := tt.Click("Automation tasks"); err != nil {
		t.Fatalf("click Automation tasks: %v", err)
	}
	if !tt.HasText("Enabled task") || !tt.HasText("Inactive task") {
		t.Fatalf("tasks missing on All: %q", tt.Texts())
	}
	if err := tt.Click("Enabled"); err != nil {
		t.Fatalf("click Enabled tab: %v", err)
	}
	if !tt.HasText("Enabled task") || tt.HasText("Inactive task") {
		t.Fatalf("Enabled filter = %q", tt.Texts())
	}
	if err := tt.Click("Inactive"); err != nil {
		t.Fatalf("click Inactive tab: %v", err)
	}
	if tt.HasText("Enabled task") || !tt.HasText("Inactive task") {
		t.Fatalf("Inactive filter = %q", tt.Texts())
	}

	// Search matches the title; typing reaches the live query state. Switch
	// back to All first so the filter does not hide the match.
	if err := tt.Click("All"); err != nil {
		t.Fatalf("click All tab: %v", err)
	}
	tt.Click("Search automation tasks")
	tt.Type("enabled")
	if got := h.app.shellUI().automationQuery; got != "enabled" {
		t.Fatalf("search query = %q, want enabled", got)
	}
	if !tt.HasText("Enabled task") || tt.HasText("Inactive task") {
		t.Fatalf("search result = %q", tt.Texts())
	}
}

// TestAutomationRunNowWithoutHook checks the UI reports the missing runner
// rather than silently doing nothing.
func TestAutomationRunNowWithoutHook(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	if _, err := h.ctrl.Automations().Create(store.Automation{
		Title: "No runner", Cron: "@daily", Enabled: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	tt.Click("Automation tasks")
	if err := tt.Click("Run now"); err != nil {
		t.Fatalf("click Run now: %v", err)
	}
	if !tt.HasText("No automation runner is configured") {
		t.Fatalf("missing-runner toast missing: %q", tt.Texts())
	}
}

// dangerPixels reports whether any pixel inside label's box paints the
// theme's danger color: the destructive action's text is the semantic red, not
// the neutral color the other row actions use.
func dangerPixels(tt *ui.Tester, label string) bool {
	r, ok := tt.Find(label)
	if !ok {
		return false
	}
	want := design.Light().Danger
	img := tt.Image()
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			c := img.RGBAAt(x, y)
			if c.R == want.R && c.G == want.G && c.B == want.B {
				return true
			}
		}
	}
	return false
}

// TestAutomationDeleteRequiresConfirmation covers the destructive-action rule:
// the row's Delete is painted in the danger color and only asks; cancel,
// Escape and the default-focused cancel keep the task; the modal's Danger
// button alone deletes and toasts.
func TestAutomationDeleteRequiresConfirmation(t *testing.T) {
	h := newLiveApp(t, &shellProvider{})
	h.app.Language = LangEN
	if _, err := h.ctrl.Automations().Create(store.Automation{
		Title: "Nightly backup", Cron: "@daily", Enabled: true,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	tt := ui.NewTester(h.app.View, 1600, 1000)
	if err := tt.Click("Automation tasks"); err != nil {
		t.Fatalf("open Automation: %v", err)
	}
	tt.Frame()
	if !dangerPixels(tt, "Delete") {
		t.Fatal("the row's Delete is not painted in the danger color")
	}
	tasks, err := h.ctrl.Automations().List()
	if err != nil || len(tasks) != 1 {
		t.Fatalf("List = %+v, %v", tasks, err)
	}
	id := tasks[0].ID
	taskExists := func() bool {
		_, err := h.ctrl.Automations().Get(id)
		return err == nil
	}
	openConfirm := func(t *testing.T) {
		t.Helper()
		if err := tt.Click("Delete"); err != nil {
			t.Fatalf("open delete confirmation: %v", err)
		}
		tt.Frame()
		if !tt.HasText("Delete automation task?") {
			t.Fatalf("delete confirmation missing: %q", tt.Texts())
		}
		if !tt.HasText("Nightly backup") {
			t.Fatalf("confirmation does not name the task: %q", tt.Texts())
		}
		if !dangerPixels(tt, "Delete task") {
			t.Fatal("the confirmation's Delete is not painted in the danger color")
		}
	}

	// Cancel keeps the task.
	openConfirm(t)
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	tt.Frame()
	if tt.HasText("Delete automation task?") {
		t.Fatalf("Cancel left the confirmation open: %q", tt.Texts())
	}
	if !taskExists() {
		t.Fatal("Cancel deleted the task")
	}

	// Escape dismisses without deleting.
	openConfirm(t)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.HasText("Delete automation task?") {
		t.Fatalf("Esc left the confirmation open: %q", tt.Texts())
	}
	if !taskExists() {
		t.Fatal("Esc deleted the task")
	}

	// Enter activates the default-focused Cancel: it must never delete.
	openConfirm(t)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if tt.HasText("Delete automation task?") {
		t.Fatalf("Enter left the confirmation open: %q", tt.Texts())
	}
	if !taskExists() {
		t.Fatal("Enter on the confirmation deleted the task; the danger button must not be the default")
	}

	// Clicking the backdrop dismisses too.
	openConfirm(t)
	tt.ClickAt(20, 20)
	tt.Frame()
	if tt.HasText("Delete automation task?") {
		t.Fatalf("a click outside left the confirmation open: %q", tt.Texts())
	}
	if !taskExists() {
		t.Fatal("a click outside deleted the task")
	}

	// Only the Danger button deletes, and it reports through the toast.
	openConfirm(t)
	if err := tt.Click("Delete task"); err != nil {
		t.Fatalf("confirm delete: %v", err)
	}
	tt.Frame()
	if taskExists() {
		t.Fatal("the confirmed delete did not remove the task")
	}
	if !tt.HasText("Automation task deleted") {
		t.Fatalf("delete toast missing: %q", tt.Texts())
	}
}

// TestAutomationScheduleLabelLocalized covers the schedule line the review
// flagged as mixed language: the Chinese UI shows 每天 00:00 / 每小时 /
// 每 15 分钟, the English UI keeps the runtime's English wording, and the
// persisted cron is untouched.
func TestAutomationScheduleLabelLocalized(t *testing.T) {
	zh := newLiveApp(t, &shellProvider{})
	zh.app.Language = LangZH
	for _, task := range []store.Automation{
		{Title: "日报", Cron: "0 9 * * *", Enabled: true},
		{Title: "巡检", Cron: "@hourly", Enabled: true},
		{Title: "间隔", Cron: "*/15 * * * *", Enabled: true},
	} {
		if _, err := zh.ctrl.Automations().Create(task); err != nil {
			t.Fatalf("create %q: %v", task.Title, err)
		}
	}
	ztt := ui.NewTester(zh.app.View, 1600, 1000)
	if err := ztt.Click("自动化任务"); err != nil {
		t.Fatalf("open Automation in zh: %v", err)
	}
	ztt.Frame()
	for _, want := range []string{"每天 09:00", "每小时", "每 15 分钟"} {
		if !ztt.HasText(want) {
			t.Fatalf("Chinese schedule label %q missing: %q", want, ztt.Texts())
		}
	}
	for _, gone := range []string{"Daily at", "Every hour", "Every 15 minutes"} {
		if ztt.HasText(gone) {
			t.Fatalf("Chinese UI still shows the English schedule %q: %q", gone, ztt.Texts())
		}
	}
	if got := zh.app.automationScheduleLabel("*/15 * * * *"); got != "每 15 分钟" {
		t.Fatalf("automationScheduleLabel(*/15) = %q, want 每 15 分钟", got)
	}

	en := newLiveApp(t, &shellProvider{})
	en.app.Language = LangEN
	if _, err := en.ctrl.Automations().Create(store.Automation{
		Title: "Daily", Cron: "0 9 * * *", Enabled: true,
	}); err != nil {
		t.Fatalf("create en: %v", err)
	}
	ett := ui.NewTester(en.app.View, 1600, 1000)
	if err := ett.Click("Automation tasks"); err != nil {
		t.Fatalf("open Automation in en: %v", err)
	}
	ett.Frame()
	if !ett.HasText("Daily at 09:00") {
		t.Fatalf("English schedule label missing: %q", ett.Texts())
	}
	if got := en.app.automationScheduleLabel("@hourly"); got != "Every hour" {
		t.Fatalf("automationScheduleLabel(@hourly) = %q, want Every hour", got)
	}
	// The persisted and scheduled data is language independent.
	tasks, err := en.ctrl.Automations().List()
	if err != nil || len(tasks) != 1 || tasks[0].Cron != "0 9 * * *" {
		t.Fatalf("cron changed by localization: %+v, %v", tasks, err)
	}
}

// TestRenderAutomationNativePreview renders the Automation page with a few
// persisted tasks for brand review: light and dark at the 1600x1000 design
// size, written to docs/design/preview.
func TestRenderAutomationNativePreview(t *testing.T) {
	dir := previewDir(t)
	tasks := []store.Automation{
		{Title: "每日构建报告", Prompt: "总结昨天的主干提交与失败构建，生成一份简短报告。", Cron: "@daily", Enabled: true},
		{Title: "依赖安全巡检", Prompt: "检查依赖更新与已知漏洞，列出需要升级的模块。", Cron: "@hourly", Enabled: false},
		{Title: "发布说明草稿", Prompt: "根据最近合并的 PR 起草发布说明。", Cron: "*/15 * * * *", Enabled: true},
	}
	for _, tc := range []struct {
		name  string
		theme string
	}{
		{"light", session.ThemeLight},
		{"dark", session.ThemeDark},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newLiveApp(t, &shellProvider{})
			h.app.Language = LangZH
			if err := h.store.SetSetting(session.SettingTheme, tc.theme); err != nil {
				t.Fatalf("theme setting: %v", err)
			}
			for _, task := range tasks {
				if _, err := h.ctrl.Automations().Create(task); err != nil {
					t.Fatalf("create task %q: %v", task.Title, err)
				}
			}
			h.app.Screen = Automation
			img := ui.Render(h.app.View, 1600, 1000, 1)
			path := filepath.Join(dir, "automation-native-"+tc.name+".png")
			f, err := os.Create(path)
			if err != nil {
				t.Fatalf("create %s: %v", path, err)
			}
			if err := png.Encode(f, img); err != nil {
				f.Close()
				t.Fatalf("encode %s: %v", path, err)
			}
			if err := f.Close(); err != nil {
				t.Fatalf("close %s: %v", path, err)
			}
			if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
				t.Fatalf("preview %s is empty: %v", path, err)
			}
			t.Logf("wrote %s", path)
		})
	}
}
