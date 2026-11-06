package agentteam

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSpawnBatchIsAtomicAndHonorsLimit(t *testing.T) {
	runner := &scriptedRunner{}
	mgr := New(Config{Runner: runner, MaxAgents: 2})
	defer mgr.Close()

	if _, err := mgr.SpawnBatch(context.Background(), nil); !errors.Is(err, ErrEmptyBatch) {
		t.Fatalf("empty batch error = %v, want ErrEmptyBatch", err)
	}
	if _, err := mgr.SpawnBatch(context.Background(), []SpawnRequest{{Task: "ok"}, {Task: " "}}); !errors.Is(err, ErrEmptyTask) {
		t.Fatalf("blank task batch error = %v, want ErrEmptyTask", err)
	}
	if got := len(mgr.Members()); got != 0 {
		t.Fatalf("members after invalid batch = %d, want 0", got)
	}
	if _, err := mgr.SpawnBatch(context.Background(), []SpawnRequest{
		{Task: "a"}, {Task: "b"}, {Task: "c"},
	}); !errors.Is(err, ErrTooManyAgents) {
		t.Fatalf("over-limit batch error = %v, want ErrTooManyAgents", err)
	}
	if got := len(mgr.Members()); got != 0 {
		t.Fatalf("members after over-limit batch = %d, want 0", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mgr.SpawnBatch(ctx, []SpawnRequest{{Task: "a"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled batch error = %v, want context.Canceled", err)
	}

	members, err := mgr.SpawnBatch(context.Background(), []SpawnRequest{
		{Task: "first", Agent: "worker"},
		{Task: "second", Agent: "worker"},
	})
	if err != nil {
		t.Fatalf("SpawnBatch: %v", err)
	}
	if len(members) != 2 || members[0].ID == members[1].ID {
		t.Fatalf("members = %+v, want two distinct ids", members)
	}
	for i, want := range []string{"first", "second"} {
		settled, err := mgr.Wait(waitCtx(t), members[i].ID)
		if err != nil {
			t.Fatalf("Wait(%s): %v", members[i].ID, err)
		}
		if settled.Status != StatusCompleted || settled.Task != want || settled.Turns != 1 {
			t.Fatalf("settled[%d] = %+v", i, settled)
		}
	}
}

func TestCloseMemberStopsOneAndKeepsManagerOpen(t *testing.T) {
	runner := &blockingRunner{started: make(chan struct{}, 2)}
	mgr := New(Config{Runner: runner})
	defer mgr.Close()

	first, err := mgr.Spawn(context.Background(), SpawnRequest{Task: "first"})
	if err != nil {
		t.Fatalf("Spawn first: %v", err)
	}
	second, err := mgr.Spawn(context.Background(), SpawnRequest{Task: "second"})
	if err != nil {
		t.Fatalf("Spawn second: %v", err)
	}
	<-runner.started
	<-runner.started

	if _, err := mgr.Send(first.ID, "parked"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := mgr.CloseMember(first.ID); err != nil {
		t.Fatalf("CloseMember: %v", err)
	}
	settled, err := mgr.Wait(waitCtx(t), first.ID)
	if err != nil {
		t.Fatalf("Wait first: %v", err)
	}
	if settled.Status != StatusInterrupted || settled.QueueLen != 0 {
		t.Fatalf("first after close = %+v, want interrupted with an empty queue", settled)
	}

	// Idempotent and safe on an idle member.
	if err := mgr.CloseMember(first.ID); err != nil {
		t.Fatalf("second CloseMember: %v", err)
	}
	if err := mgr.CloseMember("missing"); !errors.Is(err, ErrUnknownTarget) {
		t.Fatalf("CloseMember unknown target error = %v, want ErrUnknownTarget", err)
	}

	// The manager stays open for later members.
	mgr.SetRunner(&scriptedRunner{})
	third, err := mgr.Spawn(context.Background(), SpawnRequest{Task: "third"})
	if err != nil {
		t.Fatalf("Spawn after CloseMember: %v", err)
	}
	thirdSettled, err := mgr.Wait(waitCtx(t), third.ID)
	if err != nil {
		t.Fatalf("Wait third: %v", err)
	}
	if thirdSettled.Status != StatusCompleted || thirdSettled.Output != "done:third" {
		t.Fatalf("third = %+v", thirdSettled)
	}
	if second.Status != StatusRunning {
		t.Fatalf("second spawn snapshot = %+v, want running", second)
	}
}

func TestCloseMembersStopsAllAndIsIdempotent(t *testing.T) {
	runner := &blockingRunner{started: make(chan struct{}, 2)}
	mgr := New(Config{Runner: runner})
	defer mgr.Close()

	spawned, err := mgr.SpawnBatch(context.Background(), []SpawnRequest{
		{Task: "one", Agent: "worker"},
		{Task: "two", Agent: "worker"},
	})
	if err != nil {
		t.Fatalf("SpawnBatch: %v", err)
	}
	<-runner.started
	<-runner.started

	if stopped := mgr.CloseMembers(); stopped != 2 {
		t.Fatalf("CloseMembers stopped = %d, want 2", stopped)
	}
	for _, member := range spawned {
		settled, err := mgr.Member(member.ID)
		if err != nil {
			t.Fatalf("Member(%s): %v", member.ID, err)
		}
		if settled.Status != StatusInterrupted || settled.QueueLen != 0 {
			t.Fatalf("member after close = %+v, want interrupted", settled)
		}
	}
	if stopped := mgr.CloseMembers(); stopped != 0 {
		t.Fatalf("second CloseMembers stopped = %d, want 0", stopped)
	}
	if got := len(mgr.Snapshot()); got != 2 {
		t.Fatalf("Snapshot after close = %d members, want 2", got)
	}

	mgr.SetRunner(&scriptedRunner{})
	next, err := mgr.Spawn(context.Background(), SpawnRequest{Task: "after close"})
	if err != nil {
		t.Fatalf("Spawn after CloseMembers: %v", err)
	}
	settled, err := mgr.Wait(waitCtx(t), next.ID)
	if err != nil {
		t.Fatalf("Wait after CloseMembers: %v", err)
	}
	if settled.Status != StatusCompleted {
		t.Fatalf("member after CloseMembers spawn = %+v", settled)
	}
}

func TestSnapshotAndMemberLookup(t *testing.T) {
	runner := &scriptedRunner{}
	mgr := New(Config{Runner: runner})
	defer mgr.Close()

	first, err := mgr.Spawn(context.Background(), SpawnRequest{Task: "one", Agent: "dup"})
	if err != nil {
		t.Fatalf("Spawn first: %v", err)
	}
	second, err := mgr.Spawn(context.Background(), SpawnRequest{Task: "two", Agent: "dup"})
	if err != nil {
		t.Fatalf("Spawn second: %v", err)
	}
	if _, err := mgr.Wait(waitCtx(t), first.ID); err != nil {
		t.Fatalf("Wait first: %v", err)
	}
	if _, err := mgr.Wait(waitCtx(t), second.ID); err != nil {
		t.Fatalf("Wait second: %v", err)
	}

	snapshot := mgr.Snapshot()
	if len(snapshot) != 2 || snapshot[0].ID != first.ID || snapshot[1].ID != second.ID {
		t.Fatalf("Snapshot = %+v", snapshot)
	}
	got, err := mgr.Member(first.ID)
	if err != nil {
		t.Fatalf("Member by id: %v", err)
	}
	if got.ID != first.ID || got.Status != StatusCompleted {
		t.Fatalf("Member by id = %+v", got)
	}
	if _, err := mgr.Member("dup"); !errors.Is(err, ErrAmbiguousTarget) {
		t.Fatalf("Member duplicate name error = %v, want ErrAmbiguousTarget", err)
	}
	if _, err := mgr.Member("missing"); !errors.Is(err, ErrUnknownTarget) {
		t.Fatalf("Member missing error = %v, want ErrUnknownTarget", err)
	}
}

func TestMemberCarriesSessionID(t *testing.T) {
	runner := &scriptedRunner{}
	mgr := New(Config{Runner: runner})
	defer mgr.Close()

	spawned, err := mgr.Spawn(context.Background(), SpawnRequest{Task: "scoped task", SessionID: "session-a"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if spawned.SessionID != "session-a" {
		t.Fatalf("spawned.SessionID = %q, want session-a", spawned.SessionID)
	}
	if _, err := mgr.Wait(waitCtx(t), spawned.ID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	members := mgr.Members()
	if len(members) != 1 || members[0].SessionID != "session-a" {
		t.Fatalf("Members = %+v, want one member scoped to session-a", members)
	}
}

func TestStopSessionCancelsOnlyThatSessionsMembers(t *testing.T) {
	runner := &blockingRunner{started: make(chan struct{}, 4)}
	mgr := New(Config{Runner: runner})
	defer mgr.Close()
	ctx := context.Background()

	first, err := mgr.Spawn(ctx, SpawnRequest{Task: "first", SessionID: "session-a"})
	if err != nil {
		t.Fatalf("Spawn first: %v", err)
	}
	second, err := mgr.Spawn(ctx, SpawnRequest{Task: "second", SessionID: "session-b"})
	if err != nil {
		t.Fatalf("Spawn second: %v", err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-runner.started:
		case <-time.After(5 * time.Second):
			t.Fatalf("member %d did not start", i)
		}
	}

	if stopped := mgr.StopSession("session-a"); stopped != 1 {
		t.Fatalf("StopSession(session-a) = %d, want 1", stopped)
	}
	gotFirst, err := mgr.Member(first.ID)
	if err != nil {
		t.Fatalf("Member first: %v", err)
	}
	if gotFirst.Status != StatusInterrupted {
		t.Fatalf("first status = %q, want interrupted", gotFirst.Status)
	}
	gotSecond, err := mgr.Member(second.ID)
	if err != nil {
		t.Fatalf("Member second: %v", err)
	}
	if gotSecond.Status != StatusRunning {
		t.Fatalf("second status = %q, want running (another session must not be stopped)", gotSecond.Status)
	}

	if stopped := mgr.StopSession("session-b"); stopped != 1 {
		t.Fatalf("StopSession(session-b) = %d, want 1", stopped)
	}
	gotSecond, err = mgr.Member(second.ID)
	if err != nil {
		t.Fatalf("Member second after stop: %v", err)
	}
	if gotSecond.Status != StatusInterrupted {
		t.Fatalf("second status = %q, want interrupted", gotSecond.Status)
	}
}

func TestStopSessionDropsQueuedMessagesAndIsIdempotent(t *testing.T) {
	runner := &blockingRunner{started: make(chan struct{}, 2)}
	mgr := New(Config{Runner: runner})
	defer mgr.Close()
	ctx := context.Background()

	spawned, err := mgr.Spawn(ctx, SpawnRequest{Task: "first", SessionID: "session-a"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("member did not start")
	}
	if _, err := mgr.Send(spawned.ID, "queued follow-up"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if stopped := mgr.StopSession("session-a"); stopped != 1 {
		t.Fatalf("first StopSession = %d, want 1", stopped)
	}
	got, err := mgr.Member(spawned.ID)
	if err != nil {
		t.Fatalf("Member: %v", err)
	}
	if got.Status != StatusInterrupted || got.QueueLen != 0 {
		t.Fatalf("member after StopSession = %+v, want interrupted with an empty queue", got)
	}
	if stopped := mgr.StopSession("session-a"); stopped != 0 {
		t.Fatalf("second StopSession = %d, want 0 (idempotent)", stopped)
	}
	if stopped := mgr.StopSession("missing-session"); stopped != 0 {
		t.Fatalf("StopSession(unknown) = %d, want 0", stopped)
	}
	if stopped := mgr.StopSession("  "); stopped != 0 {
		t.Fatalf("StopSession(blank) = %d, want 0", stopped)
	}
}

func TestStopSessionAfterCloseIsNoop(t *testing.T) {
	mgr := New(Config{Runner: &blockingRunner{}})
	mgr.Close()
	if stopped := mgr.StopSession("session-a"); stopped != 0 {
		t.Fatalf("StopSession after Close = %d, want 0", stopped)
	}
}
