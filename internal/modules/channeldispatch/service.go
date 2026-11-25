// Package channeldispatch is the send half of notification routing. The router
// decides where a message should go; this module tries, in order, retrying once
// per channel, and always ends at the in-app terminal — a send that did not
// happen is reported, never papered over.
package channeldispatch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Config is the module's typed assembly config.
type Config struct {
	// Targets maps channel -> the conversation to send to (a DM id, a chat id,
	// a mailbox). A channel without a target is configured in the plan but not
	// deliverable, and is skipped with a reason.
	Targets map[string]string `json:"targets,omitempty"`
	// RetryDelay is the pause before the single retry (default 2s).
	RetryDelay time.Duration `json:"-"`
}

func (c Config) retryDelay() time.Duration {
	if c.RetryDelay > 0 {
		return c.RetryDelay
	}
	return 2 * time.Second
}

// Service implements contracts.ChannelDispatchService.
type Service struct {
	router  contracts.NotifyRouterService
	senders map[string]contracts.ChannelSender
	targets map[string]string
	cfg     Config
	// RetryDelay overrides the pause before the single retry (tests set 0).
	RetryDelay time.Duration
	now        func() time.Time
	sleep      func(context.Context, time.Duration) bool
}

// New wires the dispatcher (now/sleep nil = real clock and timers).
func New(router contracts.NotifyRouterService, senders []contracts.ChannelSender, targets map[string]string, now func() time.Time) *Service {
	registry := make(map[string]contracts.ChannelSender, len(senders))
	for _, sender := range senders {
		if sender != nil {
			registry[sender.Name()] = sender
		}
	}
	if now == nil {
		now = time.Now
	}
	return &Service{
		router: router, senders: registry, targets: targets, cfg: Config{},
		now: now, sleep: sleepContext,
	}
}

// Deliver implements contracts.ChannelDispatchService.
func (s *Service) Deliver(ctx context.Context, category, text string) (contracts.DispatchReceipt, error) {
	if err := ctx.Err(); err != nil {
		return contracts.DispatchReceipt{}, err
	}
	plan, err := s.router.Plan(ctx, category, s.now(), s.healthy(ctx))
	if err != nil {
		return contracts.DispatchReceipt{}, fmt.Errorf("channeldispatch: plan: %w", err)
	}
	receipt := contracts.DispatchReceipt{Deferred: plan.Deferred, Reason: plan.Reason}

	for _, channel := range plan.Channels {
		// in-app is the terminal channel: the UI already has it, so there is
		// nothing to send and nothing to retry.
		if channel == contracts.NotifyChannelInApp {
			receipt.Channel = contracts.NotifyChannelInApp
			receipt.Attempts = append(receipt.Attempts, channel)
			return receipt, nil
		}
		receipt.Attempts = append(receipt.Attempts, channel)
		sender, ok := s.senders[channel]
		if !ok {
			receipt.Errors = append(receipt.Errors, fmt.Sprintf("%s: not configured", channel))
			continue
		}
		conversation := s.targets[channel]
		if conversation == "" {
			receipt.Errors = append(receipt.Errors, fmt.Sprintf("%s: no target conversation", channel))
			continue
		}
		if sendErr := s.sendWithRetry(ctx, sender, conversation, text); sendErr != nil {
			receipt.Errors = append(receipt.Errors, fmt.Sprintf("%s: %v", channel, sendErr))
			continue
		}
		receipt.Channel = channel
		return receipt, nil
	}

	// Every named channel failed or was unusable: the message stays in-app.
	receipt.Channel = contracts.NotifyChannelInApp
	return receipt, nil
}

// sendWithRetry tries once more before the plan moves on: a closed window or a
// hiccup is worth one retry, more than that is just noise on the platform.
func (s *Service) sendWithRetry(ctx context.Context, sender contracts.ChannelSender, conversation, text string) error {
	err := sender.Send(ctx, conversation, text)
	if err == nil {
		return nil
	}
	// A refusal that needs a human (window closed, unauthorized) will not fix
	// itself in two seconds.
	if errors.Is(err, contracts.ErrWeChatWindowClosed) || errors.Is(err, contracts.ErrWeChatUnauthorized) {
		return err
	}
	delay := s.RetryDelay
	if delay <= 0 {
		delay = s.cfg.retryDelay()
	}
	if !s.sleep(ctx, delay) {
		return ctx.Err()
	}
	return sender.Send(ctx, conversation, text)
}

// healthy reports which channels the router should consider. A configured
// sender counts as healthy: whether the platform is reachable right now is the
// send's problem, and the router's own fallback already handles a refusal.
func (s *Service) healthy(context.Context) map[string]bool {
	out := make(map[string]bool, len(s.senders))
	for name := range s.senders {
		out[name] = true
	}
	return out
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

var _ contracts.ChannelDispatchService = (*Service)(nil)
