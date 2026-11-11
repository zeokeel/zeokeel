package agent

// Regression tests for closing a running teammate: the durable record must end
// interrupted with a finish time so the interrupted completion receipt can fire.

import (
	"context"
	"strings"
	"testing"
	"time"

	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// blockingProvider streams forever until the run context is cancelled.
type blockingProvider struct {
	started chan struct{}
}

func (p *blockingProvider) Name() string { return "blocking" }

func (p *blockingProvider) Stream(ctx context.Context, _ rig.Request) (rig.Stream, error) {
	if p.started != nil {
		select {
		case p.started <- struct{}{}:
		default:
		}
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCloseRunningTeammatePersistsInterrupted(t *testing.T) {
	svc, team, _ := newTeamModuleService(t)
	provider := &blockingProvider{started: make(chan struct{}, 1)}
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	session, err := svc.NewSession("parent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.SetActiveSession(session); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	if _, err := team.Spawn(context.Background(), "阻塞队友", "一直等待", ""); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	select {
	case <-provider.started:
	case <-time.After(3 * time.Second):
		t.Fatal("teammate run never reached the provider")
	}
	findMember := func() (contracts.TeamMember, bool) {
		for _, member := range team.Members() {
			if member.Name == "阻塞队友" && !member.Historical {
				return member, true
			}
		}
		return contracts.TeamMember{}, false
	}
	member, ok := findMember()
	if !ok {
		t.Fatal("running teammate not listed")
	}
	if err := team.Close(member.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitFor(t, "interrupted child record", func() bool {
		for _, record := range svc.childSessions.records() {
			if record.Name == "阻塞队友" {
				return record.Status == contracts.ChildAgentInterrupted && !record.Finished.IsZero()
			}
		}
		return false
	})
	notices, _ := svc.prepareChildNotices(session)
	if len(notices) != 1 {
		t.Fatalf("interrupted teammate receipts = %d, want 1", len(notices))
	}
	if !strings.Contains(notices[0].Content[0].Text, "was interrupted") {
		t.Fatalf("receipt text = %q, want interrupted wording", notices[0].Content[0].Text)
	}
}
