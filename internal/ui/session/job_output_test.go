package session_test

// TDD frame tests for the background-jobs output panel and the two-stage stop:
// a live row expands a bounded mono output panel (read through JobService.
// JobOutput off the UI thread, refreshed while the job runs), the panel copies
// the command and the output, marks truncated reads and interrupted jobs, and
// stopping takes two presses (arm 确认停止, confirm within three seconds) both
// by pointer and by keyboard.

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// runningJobChat is the harness for the jobs overlay: one running job whose
// output the tests script.
func runningJobChat() *fakeChat { return headerChat("任务会话") }

// runningJob is one live job row.
func runningJob() contracts.BackgroundJob {
	return contracts.BackgroundJob{
		ID: "job-1", Kind: "bash", Command: "npm test --watch",
		State: "running", Running: true,
		Duration: 3 * time.Second,
	}
}

// openJobsOverlay opens the header chip's jobs overlay.
func openJobsOverlay(t *testing.T, h *harness, tester *ui.Tester) {
	t.Helper()
	h.click(tester, "1 个后台任务")
	h.assertTexts(tester, "后台任务")
}

// clickJobAction clicks one panel action, scrolling the overlay if the action
// sits below the visible area (font metrics differ across hosts; the panel is
// scrollable, so the test must scroll like the user would).
func clickJobAction(t *testing.T, h *harness, tester *ui.Tester, label string) {
	t.Helper()
	for attempt := 0; attempt < 8; attempt++ {
		if _, ok := tester.Find(label); ok {
			if err := tester.Click(label); err == nil {
				tester.Frame()
				return
			}
		}
		tester.Scroll(700, 500, 0, 120)
		tester.Frame()
	}
	h.click(tester, label)
}

// expandJobOutput opens one job row's output panel.
func expandJobOutput(t *testing.T, h *harness, tester *ui.Tester, jobID string) {
	t.Helper()
	h.click(tester, "查看任务输出 "+jobID)
}

// TestJobOutputPanelExpandsWithBoundedMonoOutput covers the expandable panel:
// the row offers the 输出 action, the bounded output renders as mono text, and
// the command/output copy actions are there.
func TestJobOutputPanelExpandsWithBoundedMonoOutput(t *testing.T) {
	jobs := &fakeJobs{
		jobs:    []contracts.BackgroundJob{runningJob()},
		outputs: map[string]string{"job-1": "line one\nline two\n"},
	}
	h := newAgentSurfaceHarness(t, runningJobChat(), agentServices{jobs: jobs})
	tester := h.tester()
	tester.Frame()
	openJobsOverlay(t, h, tester)

	if _, ok := tester.AccessByRole(ui.RoleButton, "查看任务输出 job-1"); !ok {
		t.Fatalf("the job row has no output action; texts = %v", tester.Texts())
	}
	expandJobOutput(t, h, tester, "job-1")

	waitFor(t, tester, func() bool { return tester.HasText("line one") }, "job output read")
	h.assertTexts(tester, "line two", "复制输出", "复制命令")
	if reads := jobs.outputReads(); len(reads) == 0 || reads[0] != "job-1" {
		t.Fatalf("JobService.JobOutput reads = %v, want [job-1]", reads)
	}
	// Opening collapses the panel again (the action is a toggle).
	h.click(tester, "收起任务输出 job-1")
	h.assertNoTexts(tester, "line one", "line two")
}

// TestJobOutputPanelCopiesCommandAndOutput covers the copy actions: the command
// and the loaded output land on the native clipboard.
func TestJobOutputPanelCopiesCommandAndOutput(t *testing.T) {
	jobs := &fakeJobs{
		jobs:    []contracts.BackgroundJob{runningJob()},
		outputs: map[string]string{"job-1": "npm test output\n"},
	}
	h := newAgentSurfaceHarness(t, runningJobChat(), agentServices{jobs: jobs})
	tester := h.tester()
	tester.Frame()
	openJobsOverlay(t, h, tester)
	expandJobOutput(t, h, tester, "job-1")
	waitFor(t, tester, func() bool { return tester.HasText("npm test output") }, "job output read")

	clickJobAction(t, h, tester, "复制任务命令 job-1")
	if got := tester.Clipboard(); got != "npm test --watch" {
		t.Fatalf("clipboard after copying the command = %q, want the command", got)
	}
	clickJobAction(t, h, tester, "复制任务输出 job-1")
	if got := tester.Clipboard(); got != "npm test output\n" {
		t.Fatalf("clipboard after copying the output = %q, want the output", got)
	}
}

// TestJobOutputLiveRefreshWhileRunning covers the live read: while the job runs
// and its panel is open, the panel asks the service again and shows the newer
// output.
func TestJobOutputLiveRefreshWhileRunning(t *testing.T) {
	var reads atomic.Int64
	jobs := &fakeJobs{jobs: []contracts.BackgroundJob{runningJob()}}
	jobs.outputFn = func(string) (string, error) {
		count := reads.Add(1)
		return fmt.Sprintf("tick %d\n", count), nil
	}
	h := newAgentSurfaceHarness(t, runningJobChat(), agentServices{jobs: jobs})
	tester := h.tester()
	tester.Frame()
	openJobsOverlay(t, h, tester)
	expandJobOutput(t, h, tester, "job-1")

	waitFor(t, tester, func() bool { return tester.HasText("tick 1") }, "first output read")
	// The jobs chip requests a frame while a job runs; the panel's throttle is
	// the poll interval.
	tester.Advance(jobsPollIntervalForTest() + 200*time.Millisecond)
	waitFor(t, tester, func() bool { return tester.HasText("tick 2") }, "refreshed output read")
	if reads.Load() < 2 {
		t.Fatalf("JobOutput reads = %d, want at least 2 while the job runs", reads.Load())
	}
}

// jobsPollIntervalForTest mirrors the module's live-refresh poll interval.
func jobsPollIntervalForTest() time.Duration { return 500 * time.Millisecond }

// TestJobOutputPanelTruncationAndInterruptHints covers the honest hints: a
// bounded read says its start is missing (the marker never shows as raw text),
// and a stopped job says the output is from before the stop.
func TestJobOutputPanelTruncationAndInterruptHints(t *testing.T) {
	killed := contracts.BackgroundJob{
		ID: "job-2", Kind: "bash", Command: "tail -f /dev/null",
		State: "killed", ExitCode: -1, Duration: 4 * time.Second,
	}
	jobs := &fakeJobs{
		jobs: []contracts.BackgroundJob{killed},
		outputs: map[string]string{
			"job-2": contracts.JobOutputTruncatedMarker + "last line before stop\n",
		},
	}
	h := newAgentSurfaceHarness(t, runningJobChat(), agentServices{jobs: jobs})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "1 个后台任务")
	h.click(tester, "查看任务输出 job-2")

	waitFor(t, tester, func() bool { return tester.HasText("last line before stop") }, "stopped job output")
	h.assertTexts(tester, "输出已截断", "任务已停止，以下为停止前的输出")
	// The raw machine marker is stripped from the panel copy.
	h.assertNoTexts(tester, contracts.JobOutputTruncatedMarker)
	if got := tester.Clipboard(); got != "" {
		// Copy still holds the marker-free text (checked after the click below).
		t.Fatalf("clipboard changed before any copy: %q", got)
	}
	clickJobAction(t, h, tester, "复制任务输出 job-2")
	if got := tester.Clipboard(); got != "last line before stop\n" {
		t.Fatalf("copied output = %q, want the marker-free output", got)
	}
}

// TestJobOutputPanelEmptyAndErrorStates covers the honest empty state and the
// localized read failure.
func TestJobOutputPanelEmptyAndErrorStates(t *testing.T) {
	t.Run("no output yet", func(t *testing.T) {
		jobs := &fakeJobs{jobs: []contracts.BackgroundJob{runningJob()}}
		h := newAgentSurfaceHarness(t, runningJobChat(), agentServices{jobs: jobs})
		tester := h.tester()
		tester.Frame()
		openJobsOverlay(t, h, tester)
		expandJobOutput(t, h, tester, "job-1")
		waitFor(t, tester, func() bool { return tester.HasText("暂无输出") }, "empty output state")
	})
	t.Run("unknown job", func(t *testing.T) {
		jobs := &fakeJobs{
			jobs:       []contracts.BackgroundJob{runningJob()},
			outputErrs: map[string]error{"job-1": contracts.ErrJobNotFound},
		}
		h := newAgentSurfaceHarness(t, runningJobChat(), agentServices{jobs: jobs})
		tester := h.tester()
		tester.Frame()
		openJobsOverlay(t, h, tester)
		expandJobOutput(t, h, tester, "job-1")
		waitFor(t, tester, func() bool { return tester.HasText("该后台任务已不存在") }, "localized read error")
	})
}

// TestJobOutputReadIsOffTheUiThread covers the non-blocking read: while the
// service is blocked the screen still renders, and the output lands afterwards.
func TestJobOutputReadIsOffTheUiThread(t *testing.T) {
	gate := make(chan struct{})
	jobs := &fakeJobs{jobs: []contracts.BackgroundJob{runningJob()}}
	var started atomic.Int64
	jobs.outputFn = func(string) (string, error) {
		started.Add(1)
		<-gate
		return "after the slow read\n", nil
	}
	h := newAgentSurfaceHarness(t, runningJobChat(), agentServices{jobs: jobs})
	tester := h.tester()
	tester.Frame()
	openJobsOverlay(t, h, tester)
	expandJobOutput(t, h, tester, "job-1")

	waitFor(t, tester, func() bool { return started.Load() == 1 }, "output read start")
	// The blocked read did not block the frame: the overlay still renders.
	h.assertTexts(tester, "后台任务", "npm test --watch", "读取输出…")
	close(gate)
	waitFor(t, tester, func() bool { return tester.HasText("after the slow read") }, "output after the read")
}

// TestJobStopIsTwoStage covers the two-stage stop: the first press only arms
// 确认停止, the second press within the window calls KillJob.
func TestJobStopIsTwoStage(t *testing.T) {
	jobs := &fakeJobs{jobs: []contracts.BackgroundJob{runningJob()}}
	h := newAgentSurfaceHarness(t, runningJobChat(), agentServices{jobs: jobs})
	tester := h.tester()
	tester.Frame()
	openJobsOverlay(t, h, tester)

	h.click(tester, "停止后台任务 job-1")
	h.assertTexts(tester, "确认停止")
	if killed := jobs.killed(); len(killed) != 0 {
		t.Fatalf("the first stop press already killed the job: %v", killed)
	}
	if _, ok := tester.AccessByRole(ui.RoleButton, "确认停止后台任务 job-1"); !ok {
		t.Fatalf("the armed row has no confirm action; texts = %v", tester.Texts())
	}

	h.click(tester, "确认停止后台任务 job-1")
	waitFor(t, tester, func() bool { return len(jobs.killed()) == 1 }, "KillJob call")
	if got := jobs.killed()[0]; got != "job-1" {
		t.Fatalf("KillJob(%q), want job-1", got)
	}
}

// TestJobStopArmExpiresAfterTheWindow covers the timeout: an arm older than the
// confirmation window resets to 停止 without killing anything.
func TestJobStopArmExpiresAfterTheWindow(t *testing.T) {
	jobs := &fakeJobs{jobs: []contracts.BackgroundJob{runningJob()}}
	h := newAgentSurfaceHarness(t, runningJobChat(), agentServices{jobs: jobs})
	tester := h.tester()
	tester.Frame()
	openJobsOverlay(t, h, tester)

	h.click(tester, "停止后台任务 job-1")
	h.assertTexts(tester, "确认停止")
	tester.Advance(3*time.Second + 100*time.Millisecond)
	tester.Frame()
	h.assertNoTexts(tester, "确认停止")
	if killed := jobs.killed(); len(killed) != 0 {
		t.Fatalf("the expired arm killed the job: %v", killed)
	}
	// The next press arms again instead of killing.
	h.click(tester, "停止后台任务 job-1")
	h.assertTexts(tester, "确认停止")
	if killed := jobs.killed(); len(killed) != 0 {
		t.Fatalf("re-arming killed the job: %v", killed)
	}
}

// TestJobStopKeyboardOperable covers the keyboard path: after arming a row, the
// focused native button's Enter confirms the stop.
func TestJobStopKeyboardOperable(t *testing.T) {
	jobs := &fakeJobs{jobs: []contracts.BackgroundJob{runningJob()}}
	h := newAgentSurfaceHarness(t, runningJobChat(), agentServices{jobs: jobs})
	tester := h.tester()
	tester.Frame()
	openJobsOverlay(t, h, tester)

	h.click(tester, "停止后台任务 job-1")
	h.assertTexts(tester, "确认停止")
	if !tester.Focused("确认停止后台任务 job-1") {
		t.Fatalf("the armed stop button does not keep the keyboard focus; texts = %v", tester.Texts())
	}
	tester.Key(0, ui.KeyEnter)
	waitFor(t, tester, func() bool { return len(jobs.killed()) == 1 }, "keyboard-confirmed KillJob call")
}

// TestJobOutputFinalReadAfterJobFinishes covers the live panel's last read: a
// job that finished since the previous read (even while the overlay was closed)
// gets one final read, so the panel never keeps a partial output as final.
func TestJobOutputFinalReadAfterJobFinishes(t *testing.T) {
	jobs := &fakeJobs{
		jobs:    []contracts.BackgroundJob{runningJob()},
		outputs: map[string]string{"job-1": "partial output\n"},
	}
	h := newAgentSurfaceHarness(t, runningJobChat(), agentServices{jobs: jobs})
	tester := h.tester()
	tester.Frame()
	openJobsOverlay(t, h, tester)
	expandJobOutput(t, h, tester, "job-1")
	waitFor(t, tester, func() bool { return tester.HasText("partial output") }, "first output read")

	// The job finishes while its panel is open.
	jobs.mu.Lock()
	jobs.jobs[0].State = "exited"
	jobs.jobs[0].Running = false
	jobs.mu.Unlock()
	jobs.setOutput("job-1", "final output\n")
	tester.Frame()

	waitFor(t, tester, func() bool { return tester.HasText("final output") }, "final output read")
	h.assertNoTexts(tester, "partial output")
}
