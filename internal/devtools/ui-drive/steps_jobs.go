package main

// Background-jobs steps: the session header's 「N 个后台任务」chip appears only
// while the host reports jobs (R15: a zero count keeps the chip hidden instead
// of squeezing the title); clicking it opens the native overlay with each job's
// status, command, duration and exit code plus the stop action, which calls
// JobService.KillJob.

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// jobService requires the host's background-job read/control surface.
func (d *drive) jobService() contracts.JobService {
	jobs, err := d.root.Require(contracts.KeyJobs)
	if err != nil {
		panic(fmt.Sprintf("require job service: %v", err))
	}
	return jobs
}

// waitJobDone pumps frames until the named job reports a finished state (or
// times out): the manager keeps finished jobs, so the chip stays and the row's
// stopped state can be asserted.
func (d *drive) waitJobDone(jobID string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		d.tester.Frame()
		for _, job := range d.jobService().Jobs() {
			if job.ID == jobID && !job.Running {
				return true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func (d *drive) runJobsSteps() {
	d.step("jobs-chip", "session", "后台任务：页头 chip → 浮层 → 停止",
		"没有后台任务时页头不显示后台任务 chip（R15 零值隐藏）；job_start 启动一个后台命令后页头出现「1 个后台任务」；点开原生浮层显示 运行中/命令/用时/退出码 与「停止」；点停止后任务不再运行（JobService.KillJob）", func(c *check) {
			d.resetToSession()
			d.uiRoot.SetRightPanelOpen(false)
			d.frame()
			// The seeded run has no background jobs: the chip stays hidden so a
			// zero value never sits in the header (R15).
			if d.has("个后台任务") {
				c.failf("没有后台任务时页头仍显示后台任务 chip；texts=%v", paletteTailTexts(d))
			}
			if ok, detail := d.invokeToolResult("job_start", `{"command":"bash -c 'sleep 30'","timeout_ms":20000}`); !ok {
				c.failf("job_start 工具调用失败，无法验证后台任务 chip：%s", shortText(detail))
				return
			}
			jobs := d.jobService().Jobs()
			if len(jobs) != 1 {
				c.failf("job_start 后 JobService.Jobs()=%d 条，期望 1", len(jobs))
				return
			}
			related := jobs[0].ID
			defer func() {
				for _, job := range d.jobService().Jobs() {
					if job.Running {
						_ = d.jobService().KillJob(job.ID)
					}
				}
			}()
			d.frame()
			if !d.waitText("1 个后台任务", 3*time.Second) {
				c.failf("启动后台任务后页头没有「1 个后台任务」chip；texts=%v", paletteTailTexts(d))
				return
			}
			c.shot("jobs-chip")
			if err := d.click("1 个后台任务"); err != nil {
				c.failf("点击后台任务 chip 失败：%v", err)
				return
			}
			d.drain(2)
			c.mustText("后台任务", "运行中", "bash -c", "退出码 —", "停止")
			c.note("后台任务 %s 在浮层中显示运行中", related)
			c.shot("jobs-overlay")

			// Stop through the row action (the label carries the job id).
			stopLabel := ""
			for _, text := range d.texts() {
				if strings.HasPrefix(text, "停止后台任务 ") {
					stopLabel = text
					break
				}
			}
			if stopLabel == "" {
				c.failf("后台任务浮层没有「停止后台任务 …」动作；texts=%v", paletteTailTexts(d))
				return
			}
			if err := d.click(stopLabel); err != nil {
				c.failf("点击停止后台任务失败：%v", err)
				return
			}
			if !d.waitJobDone(related, 10*time.Second) {
				c.failf("停止后任务仍显示运行中（job %s）", related)
				return
			}
			d.drain(3)
			c.mustText("已取消", "退出码")
			c.note("停止后 JobService 报告任务已结束（killed）")
			c.shot("jobs-stopped")
			d.key(0, ui.KeyEscape)
			d.drain(2)
		})
}
