package heartbeat_test

// TDD for the autonomous loop: the product must keep triaging and keep reading
// its channels with no browser open, one broken channel must not stop the rest,
// and the loop must stop when its context does.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/heartbeat"
)

type fakeLead struct {
	mu     sync.Mutex
	calls  int
	report contracts.LeadReport
	err    error
}

func (f *fakeLead) RunOnce(context.Context) (contracts.LeadReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return contracts.LeadReport{}, f.err
	}
	return f.report, nil
}

func (f *fakeLead) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeChannel struct {
	mu    sync.Mutex
	name  string
	polls int
	new   int
	err   error
}

func (f *fakeChannel) PollOnce(context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	if f.err != nil {
		return 0, f.err
	}
	return f.new, nil
}

func (f *fakeChannel) polls_() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.polls
}

func TestTickRunsLeadAndChannels(t *testing.T) {
	leadService := &fakeLead{report: contracts.LeadReport{Dispatched: 2, Ignored: 1}}
	wechat := &fakeChannel{name: "wechat", new: 3}
	mail := &fakeChannel{name: "email", new: 1}

	service := heartbeat.New(heartbeat.Config{LeadInterval: time.Minute, ChannelInterval: time.Minute},
		leadService, []heartbeat.Channel{
			{Name: "wechat", Poll: wechat.PollOnce},
			{Name: "email", Poll: mail.PollOnce},
		}, nil)

	report, err := service.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if report.LeadRuns != 1 || report.Dispatched != 2 || report.Ignored != 1 {
		t.Fatalf("report = %+v, want one lead pass with its counts", report)
	}
	if report.Polls != 2 || report.NewMessages != 4 {
		t.Fatalf("report = %+v, want two polls and four messages", report)
	}
}

func TestOneFailingChannelDoesNotStopTheOthers(t *testing.T) {
	wechat := &fakeChannel{name: "wechat", err: contracts.ErrWeChatUnavailable}
	mail := &fakeChannel{name: "email", new: 2}
	service := heartbeat.New(heartbeat.Config{}, &fakeLead{}, []heartbeat.Channel{
		{Name: "wechat", Poll: wechat.PollOnce}, {Name: "email", Poll: mail.PollOnce},
	}, nil)

	report, err := service.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if mail.polls_() != 1 {
		t.Fatal("the healthy channel was not polled")
	}
	if len(report.Errors) != 1 {
		t.Fatalf("errors = %v, want exactly the failing channel", report.Errors)
	}
}

func TestLeadFailureIsRecordedNotFatal(t *testing.T) {
	leadService := &fakeLead{err: errors.New("board down")}
	mail := &fakeChannel{name: "email", new: 1}
	service := heartbeat.New(heartbeat.Config{}, leadService, []heartbeat.Channel{{Name: "email", Poll: mail.PollOnce}}, nil)

	report, err := service.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick returned an error, want it recorded: %v", err)
	}
	if len(report.Errors) == 0 || mail.polls_() != 1 {
		t.Fatalf("report = %+v, want a recorded error and the channel still polled", report)
	}
}

func TestRunTicksOnItsTimerAndStopsOnCancel(t *testing.T) {
	leadService := &fakeLead{}
	service := heartbeat.New(heartbeat.Config{
		LeadInterval: 10 * time.Millisecond, ChannelInterval: time.Hour, Enabled: boolPtr(true),
	}, leadService, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && leadService.count() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if leadService.count() < 2 {
		t.Fatalf("lead calls = %d, want at least 2 ticks", leadService.count())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v, want nil after cancel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}

	callsAfter := leadService.count()
	time.Sleep(50 * time.Millisecond)
	if leadService.count() != callsAfter {
		t.Fatal("Run kept ticking after cancellation")
	}
}

func TestDisabledLoopDoesNotStart(t *testing.T) {
	leadService := &fakeLead{}
	service := heartbeat.New(heartbeat.Config{LeadInterval: 10 * time.Millisecond, Enabled: boolPtr(false)}, leadService, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = service.Run(ctx) }()
	time.Sleep(60 * time.Millisecond)
	if leadService.count() != 0 {
		t.Fatalf("disabled loop ticked %d times", leadService.count())
	}
	status, _ := service.Status(context.Background())
	if status.Running {
		t.Fatal("status says running while disabled")
	}
}

func TestStatusReportsTheLastPass(t *testing.T) {
	service := heartbeat.New(heartbeat.Config{LeadInterval: time.Minute, ChannelInterval: 2 * time.Minute},
		&fakeLead{report: contracts.LeadReport{Dispatched: 1}}, nil, nil)
	if _, err := service.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.LastTick.IsZero() || status.LastReport.Dispatched != 1 {
		t.Fatalf("status = %+v", status)
	}
	if status.LeadInterval != time.Minute || status.ChannelInterval != 2*time.Minute {
		t.Fatalf("intervals = %v / %v", status.LeadInterval, status.ChannelInterval)
	}
}

func boolPtr(value bool) *bool { return &value }

func TestConfigureChangesTheCadenceAtRuntime(t *testing.T) {
	leadService := &fakeLead{}
	service := heartbeat.New(heartbeat.Config{LeadInterval: time.Hour, ChannelInterval: time.Hour, Enabled: boolPtr(true), MinInterval: time.Millisecond},
		leadService, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = service.Run(ctx) }()

	// Turn the cadence down: the running loop must pick it up without a restart.
	if err := service.Configure(ctx, contracts.HeartbeatConfig{LeadInterval: 10 * time.Millisecond}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && leadService.count() < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if leadService.count() < 2 {
		t.Fatalf("lead calls = %d after configuring a faster cadence, want at least 2", leadService.count())
	}
	status, _ := service.Status(context.Background())
	if status.LeadInterval != 10*time.Millisecond {
		t.Fatalf("status interval = %s, want the configured one", status.LeadInterval)
	}
}

func TestConfigureRefusesAnUnsafeCadence(t *testing.T) {
	service := heartbeat.New(heartbeat.Config{}, &fakeLead{}, nil, nil)
	if err := service.Configure(context.Background(), contracts.HeartbeatConfig{LeadInterval: time.Millisecond}); err == nil {
		t.Fatal("a 1ms cadence was accepted")
	}
	if err := service.Configure(context.Background(), contracts.HeartbeatConfig{ChannelInterval: 48 * time.Hour}); err == nil {
		t.Fatal("a 48h cadence was accepted")
	}
}

func TestConfigureCanDisableARunningLoop(t *testing.T) {
	leadService := &fakeLead{}
	service := heartbeat.New(heartbeat.Config{LeadInterval: 10 * time.Millisecond, Enabled: boolPtr(true), MinInterval: time.Millisecond}, leadService, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = service.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && leadService.count() < 1 {
		time.Sleep(5 * time.Millisecond)
	}
	if leadService.count() < 1 {
		t.Fatal("the loop never ticked")
	}

	disabled := false
	if err := service.Configure(ctx, contracts.HeartbeatConfig{Enabled: &disabled}); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	frozen := leadService.count()
	time.Sleep(80 * time.Millisecond)
	if leadService.count() != frozen {
		t.Fatalf("the disabled loop kept ticking: %d -> %d", frozen, leadService.count())
	}

	enabled := true
	if err := service.Configure(ctx, contracts.HeartbeatConfig{Enabled: &enabled}); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && leadService.count() <= frozen {
		time.Sleep(5 * time.Millisecond)
	}
	if leadService.count() <= frozen {
		t.Fatal("the loop did not resume after being re-enabled")
	}
}
