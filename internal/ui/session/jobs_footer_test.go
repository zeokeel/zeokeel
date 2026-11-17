package session_test

// 2026-11 UI review P3: the jobs overlay's footer stop warning is conditional on
// a running job. While something runs it reads "停止会终止任务的进程 · Esc 关闭";
// an idle overlay (exited jobs / history only) keeps just "Esc 关闭" — no stop
// promise about processes that no longer exist.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestJobsOverlayFooterHintConditional(t *testing.T) {
	t.Run("running keeps the stop warning", func(t *testing.T) {
		jobs := &fakeJobs{jobs: []contracts.BackgroundJob{
			{ID: "job-1", Kind: "bash", Command: "npm test --watch",
				State: "running", Status: "running", Running: true},
		}}
		h := newAgentSurfaceHarness(t, headerChat("任务会话"), agentServices{jobs: jobs})
		tester := h.tester()
		tester.Frame()
		h.click(tester, "1 个后台任务")
		h.assertTexts(tester, "后台任务", "停止会终止任务的进程 · Esc 关闭")
	})

	t.Run("idle keeps only the close hint", func(t *testing.T) {
		jobs := &fakeJobs{jobs: []contracts.BackgroundJob{
			{ID: "job-2", Kind: "bash", Command: "go build ./...",
				State: "exited", Status: "completed", ExitCode: 0},
		}}
		h := newAgentSurfaceHarness(t, headerChat("任务会话"), agentServices{jobs: jobs})
		tester := h.tester()
		tester.Frame()
		h.click(tester, "1 个后台任务")
		h.assertTexts(tester, "后台任务", "Esc 关闭")
		h.assertNoTexts(tester, "停止会终止任务的进程")
	})
}
