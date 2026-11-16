package session_test

// RG-09 (2026-11 audit): when the permission/sandbox identity is downgraded,
// running background jobs keep the OLD OS confinement (applied at spawn time,
// not retroactive). The jobs overlay consumes the optional
// contracts.JobPermissionReporter half of KeyJobs and shows a one-line awareness
// hint plus a bulk stop entry (the KillJob route is fixed, RG-01/(c)), so the
// user can stop the stale jobs and restart under the new permission. The bulk
// stop keeps the product's two-stage danger confirmation.

import (
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// staleJobs wraps the shared fake jobs service with the optional
// JobPermissionReporter half, returning a scripted stale list.
type staleJobs struct {
	*fakeJobs
	stale []contracts.BackgroundJob
}

func (s *staleJobs) StalePermissionJobs() []contracts.BackgroundJob {
	return append([]contracts.BackgroundJob(nil), s.stale...)
}

func twoRunningJobs() []contracts.BackgroundJob {
	return []contracts.BackgroundJob{
		{ID: "job-1", Kind: "bash", Command: "npm test --watch", State: "running", Status: "running", Running: true, Duration: 3 * time.Second},
		{ID: "job-2", Kind: "bash", Command: "go build ./...", State: "running", Status: "running", Running: true, Duration: 2 * time.Second},
	}
}

// TestJobsPanelStalePermissionHintAndKill covers the hint and the two-stage bulk
// stop: the stale count shows, the first press arms 确认停止, the second press
// kills every stale job through KillJob.
func TestJobsPanelStalePermissionHintAndKill(t *testing.T) {
	list := twoRunningJobs()
	jobs := &staleJobs{fakeJobs: &fakeJobs{jobs: list}, stale: list}
	h := newAgentSurfaceHarness(t, runningJobChat(), agentServices{jobs: jobs})
	tester := h.tester()
	tester.Frame()

	h.click(tester, "2 个后台任务")
	h.assertTexts(tester, "2 个运行中任务仍按旧权限运行", "停止这些任务")

	// First press arms the confirmation (no kill yet).
	h.click(tester, "停止这些任务")
	tester.Frame()
	h.assertTexts(tester, "确认停止")
	if got := jobs.killed(); len(got) != 0 {
		t.Fatalf("the first press killed jobs before confirmation: %v", got)
	}

	// Second press within the window stops every stale job.
	h.click(tester, "确认停止")
	waitFor(t, tester, func() bool { return len(jobs.killed()) == 2 }, "both stale jobs killed")
	killed := jobs.killed()
	if killed[0] != "job-1" || killed[1] != "job-2" {
		t.Fatalf("bulk stop killed %v, want [job-1 job-2]", killed)
	}
}

// TestJobsPanelNoStaleHintWithoutReporter covers a backend that does not expose
// the JobPermissionReporter half: the overlay keeps its ordinary rows, no hint.
func TestJobsPanelNoStaleHintWithoutReporter(t *testing.T) {
	jobs := &fakeJobs{jobs: []contracts.BackgroundJob{runningJob()}}
	h := newAgentSurfaceHarness(t, runningJobChat(), agentServices{jobs: jobs})
	tester := h.tester()
	tester.Frame()
	openJobsOverlay(t, h, tester)
	h.assertNoTexts(tester, "仍按旧权限运行", "停止这些任务")
}

// TestJobsPanelStaleHintAbsentWhenNoneStale covers a reporter that returns an
// empty stale list: no hint row is drawn.
func TestJobsPanelStaleHintAbsentWhenNoneStale(t *testing.T) {
	jobs := &staleJobs{fakeJobs: &fakeJobs{jobs: []contracts.BackgroundJob{runningJob()}}, stale: nil}
	h := newAgentSurfaceHarness(t, runningJobChat(), agentServices{jobs: jobs})
	tester := h.tester()
	tester.Frame()
	openJobsOverlay(t, h, tester)
	h.assertNoTexts(tester, "仍按旧权限运行", "停止这些任务")
}
