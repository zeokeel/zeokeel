package automation

// BE-07 (2026-11 audit, P3→P2): RunNow's startRun did runs.Add(1) OUTSIDE the
// service lock with no closed guard, while Close sets closed under the lock
// and then runs.Wait(). A RunNow racing Close could Add after Wait started
// with a zero counter — the sync.WaitGroup misuse panic ("reused before
// previous Wait has returned") — and the panic lands AFTER the atomic
// increment, so a recovered Close leaked a permanent +1 and hung forever on
// runs.Wait. claimRun also had no closed check, so executions delivered
// prompts after Close returned.
//
// This is the permanent regression form of the audit probe: 150 rounds of
// "fresh service + task → 4 goroutines hammer RunNow → micro-delayed Close",
// asserting zero WaitGroup panics, zero teardown hangs and zero post-close
// dispatches. Close is guarded by a 500ms timeout so a hang is recorded as a
// failure instead of wedging the suite.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestRunNowCloseRaceStress(t *testing.T) {
	const iterations = 150
	var wgPanics, closePanics, closeHangs, postCloseSends atomic.Int64

	for i := 0; i < iterations; i++ {
		chat := newFakeChat()
		svc, err := newService(t.TempDir(), Config{TickMs: 3_600_000}, &fakeLogger{}, chat, nil)
		if err != nil {
			t.Fatalf("newService: %v", err)
		}
		task := mustCreate(t, svc, contracts.AutomationTask{
			Name: "race", Prompt: "run", Schedule: "every 3600s", Enabled: true,
		})
		var stop atomic.Bool
		var callers sync.WaitGroup
		for g := 0; g < 4; g++ {
			callers.Add(1)
			go func() {
				defer callers.Done()
				for !stop.Load() {
					func() {
						defer func() {
							if r := recover(); r != nil {
								msg := fmt.Sprint(r)
								if strings.Contains(msg, "WaitGroup") {
									if wgPanics.Add(1) <= 3 {
										t.Logf("iteration %d: RunNow panic: %s", i, msg)
									}
								} else {
									panic(r)
								}
							}
						}()
						_ = svc.RunNow(context.Background(), task.ID)
					}()
				}
			}()
		}
		time.Sleep(time.Duration(i%7) * 100 * time.Microsecond)
		closed := make(chan struct{})
		go func() {
			defer func() {
				if r := recover(); r != nil {
					msg := fmt.Sprint(r)
					if strings.Contains(msg, "WaitGroup") {
						if closePanics.Add(1) <= 3 {
							t.Logf("iteration %d: Close panic at runs.Wait: %s", i, msg)
						}
					} else {
						panic(r)
					}
				}
				close(closed)
			}()
			svc.Close()
		}()
		hung := false
		select {
		case <-closed:
		case <-time.After(500 * time.Millisecond):
			hung = true
			closeHangs.Add(1)
		}
		stop.Store(true)
		callers.Wait()
		if !hung {
			before := chat.sendCount()
			time.Sleep(20 * time.Millisecond)
			if after := chat.sendCount(); after > before {
				postCloseSends.Add(int64(after - before))
			}
		}
	}

	t.Logf("iterations=%d runnow-panics=%d close-panics=%d close-hangs=%d post-close-sends=%d",
		iterations, wgPanics.Load(), closePanics.Load(), closeHangs.Load(), postCloseSends.Load())
	if wgPanics.Load() > 0 {
		t.Errorf("BE-07: RunNow Add raced Close Wait → %d WaitGroup panics", wgPanics.Load())
	}
	if closePanics.Load() > 0 {
		t.Errorf("BE-07: %d Close calls panicked at runs.Wait (WaitGroup reused before previous Wait returned)", closePanics.Load())
	}
	if closeHangs.Load() > 0 {
		t.Errorf("BE-07: %d Close calls hung >500ms on runs.Wait after a leaked Add counter", closeHangs.Load())
	}
	if postCloseSends.Load() > 0 {
		t.Errorf("BE-07: %d task sends started after Close returned", postCloseSends.Load())
	}
}

// RunNow after Close is a clean error, never a panic or a silent dispatch.
func TestRunNowAfterCloseReturnsError(t *testing.T) {
	chat := newFakeChat()
	svc, err := newService(t.TempDir(), Config{TickMs: 3_600_000}, &fakeLogger{}, chat, nil)
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	task := mustCreate(t, svc, contracts.AutomationTask{
		Name: "closed", Prompt: "run", Schedule: "every 3600s", Enabled: true,
	})
	svc.Close()
	before := chat.sendCount()
	if err := svc.RunNow(context.Background(), task.ID); err == nil {
		t.Fatal("RunNow after Close = nil, want an error")
	}
	time.Sleep(20 * time.Millisecond)
	if after := chat.sendCount(); after != before {
		t.Fatalf("RunNow after Close dispatched %d sends", after-before)
	}
}
