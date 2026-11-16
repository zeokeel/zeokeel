package toolkit

// Permission-downgrade disclosure read model (2026-11 audit RG-09): a
// permission change rebuilds the tools for NEW work, but running jobs keep the
// confinement they were spawned with — an OS fact this product does not change
// (confinement is applied at spawn). The rebind group therefore exposes WHICH
// running jobs still follow an older permission identity, so the UI can show
// "N running jobs still use the old permission mode" with a kill entry. These
// tests pin the count: only running jobs of the current workspace root whose
// build identity differs from the live one are reported.

import (
	"testing"
	"time"

	"github.com/zeokeel/zeokeel-rig/sandbox"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// staleJobIDs collects the ids of one stale-job read.
func staleJobIDs(jobs []contracts.BackgroundJob) []string {
	ids := make([]string, 0, len(jobs))
	for _, job := range jobs {
		ids = append(ids, job.ID)
	}
	return ids
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestStalePermissionJobsCountsRunningUnderOldIdentity(t *testing.T) {
	requireUnixShell(t)
	cfg := Config{
		Root:                t.TempDir(),
		CommandAllowlist:    []string{"bash", "sh", "sleep", "echo", "true"},
		AllowShellOperators: true,
	}
	group, mode := routingRebuildGroup(t, cfg, 30*time.Second)
	initial := group.initial()
	byName := proxiesByName(t, group, initial)

	first := executionCall(t, byName["bash"], "", `{"command":"sleep 5","run_in_background":true}`)
	firstID := executionArgumentID(t, first, "started background job")
	second := executionCall(t, byName["bash"], "", `{"command":"sleep 5","run_in_background":true}`)
	secondID := executionArgumentID(t, second, "started background job")

	// Same identity: nothing is stale yet.
	if got := group.stalePermissionJobs(); len(got) != 0 {
		t.Fatalf("stale jobs under the current identity = %v, want none", staleJobIDs(got))
	}

	// Permission change → new build identity; both running jobs keep the old.
	*mode = sandbox.ModeDangerFullAccess
	stale := group.stalePermissionJobs()
	if len(stale) != 2 {
		t.Fatalf("stale count after permission change = %d (%v), want 2", len(stale), staleJobIDs(stale))
	}
	ids := staleJobIDs(stale)
	if !containsID(ids, firstID) || !containsID(ids, secondID) {
		t.Fatalf("stale ids = %v, want both %s and %s", ids, firstID, secondID)
	}
	for _, job := range stale {
		if !job.Running {
			t.Fatalf("stale row %+v is not running", job)
		}
	}

	// The host-facing service exposes the same read (UI hint + kill entry).
	service := newJobService(group)
	if got := service.StalePermissionJobs(); len(got) != 2 {
		t.Fatalf("jobService.StalePermissionJobs() = %d rows, want 2", len(got))
	}

	// Killing one stale job through the service drops it from the count.
	if err := service.KillJob(firstID); err != nil {
		t.Fatalf("KillJob(%s): %v", firstID, err)
	}
	waitStaleCount(t, group, 1)

	// NEW work under the new identity is not stale.
	fresh := executionCall(t, byName["bash"], "", `{"command":"sleep 5","run_in_background":true}`)
	freshID := executionArgumentID(t, fresh, "started background job")
	stale = group.stalePermissionJobs()
	if len(stale) != 1 || stale[0].ID != secondID {
		t.Fatalf("stale after new-identity job = %v, want only %s", staleJobIDs(stale), secondID)
	}
	if containsID(staleJobIDs(stale), freshID) {
		t.Fatal("a job started under the NEW identity was reported as stale")
	}
}

func TestStalePermissionJobsFinishedAndOtherRootNotCounted(t *testing.T) {
	requireUnixShell(t)
	rootA, rootB := t.TempDir(), t.TempDir()
	current := rootA
	cfg := Config{
		CommandAllowlist:    []string{"bash", "sh", "sleep", "echo", "true"},
		AllowShellOperators: true,
	}
	mode := sandbox.ModeWorkspaceWrite
	sandboxOf := func() (sandbox.Mode, sandbox.Runner) { return mode, nil }
	group := newRebindGroup(cfg, 30*time.Second, func() string { return current }, sandboxOf, t.Logf)
	t.Cleanup(group.close)
	byName := proxiesByName(t, group, group.initial())

	// A job that finishes before the permission change must not be counted.
	quick := executionCall(t, byName["bash"], "", `{"command":"true","run_in_background":true}`)
	quickID := executionArgumentID(t, quick, "started background job")
	waitJobOutput(t, byName["job_output"], quickID, "state: exited")

	// A long job in root A, then switch the workspace to root B (same mode):
	// root A's job belongs to another workspace and is out of the chip's scope.
	long := executionCall(t, byName["bash"], "", `{"command":"sleep 5","run_in_background":true}`)
	longID := executionArgumentID(t, long, "started background job")
	current = rootB
	group.forRoot()
	if got := group.stalePermissionJobs(); len(got) != 0 {
		t.Fatalf("stale jobs after a same-mode workspace switch = %v, want none (other root / same identity)", staleJobIDs(got))
	}

	// Back in root A with a changed permission: only the still-running job is
	// stale; the finished one stays out.
	current = rootA
	mode = sandbox.ModeDangerFullAccess
	group.forRoot()
	stale := group.stalePermissionJobs()
	if len(stale) != 1 || stale[0].ID != longID {
		t.Fatalf("stale = %v, want only the running %s (finished %s excluded)", staleJobIDs(stale), longID, quickID)
	}
}

// waitStaleCount polls the stale read until the kill settles (the manager marks
// the job killed asynchronously).
func waitStaleCount(t *testing.T, group *rebindGroup, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := group.stalePermissionJobs(); len(got) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("stale count never settled to %d (got %v)", want, staleJobIDs(group.stalePermissionJobs()))
}
