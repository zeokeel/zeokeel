// Package replyrelay makes "reply 同意 in the chat" work. Channels file their
// messages in the inbox; this pass asks the approval action service to read each
// channel message as a reply, and archives the ones that answered a request. It
// creates no work and sends nothing: triage and delivery stay where they are.
package replyrelay

import (
	"context"
	"fmt"
	"sync"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Config is the module's typed assembly config.
type Config struct {
	// Enabled starts the pass loop (default true).
	Enabled *bool `json:"enabled,omitempty"`
}

func (c Config) enabled() bool { return c.Enabled == nil || *c.Enabled }

// channelSources are the inbox sources whose text may be a human reply. Webhook
// and cron text is machine input and must never answer an approval.
//
// DingTalk and Feishu are absent on purpose: they are outbound only and can no
// longer file an inbox row, so their text could only ever be a leftover from an
// older ingest path — never a fresh human reply
// (docs/research/channel-capabilities.md §6).
//
// Email is absent for a stronger reason: **the sender is not authenticated**.
// The adapter files whatever the mailbox holds and takes the `From` header as an
// unverified Sender string (internal/modules/email/imap.go); there is no
// allowlist, no signature and no header check. ApplyReply then answers the
// *global* pending queue by position (internal/modules/approvals/action.go), so
// a mail whose body is "1 同意" from anyone at all would resolve whatever
// approval happens to be first. The instruction entries are Web and WeChat only;
// making email a human source here would turn a mailbox into an approval bypass.
var channelSources = map[string]bool{
	"wechat": true,
}

// Service implements contracts.ReplyRelayService.
type Service struct {
	inbox   contracts.InboxService
	actions contracts.ApprovalActionService
	cfg     Config

	// mu guards seen: item id -> the last Count we already parsed. A message that
	// arrived again (count grew) is a new utterance and deserves a fresh look.
	mu   sync.Mutex
	seen map[string]int
}

// New wires the relay.
func New(inbox contracts.InboxService, actions contracts.ApprovalActionService, cfg Config) *Service {
	return &Service{inbox: inbox, actions: actions, cfg: cfg, seen: make(map[string]int)}
}

// Relay implements contracts.ReplyRelayService.
func (s *Service) Relay(ctx context.Context) (contracts.ReplyRelayReport, error) {
	if err := ctx.Err(); err != nil {
		return contracts.ReplyRelayReport{}, err
	}
	items, err := s.inbox.List(ctx, contracts.InboxFilter{
		Statuses: []string{contracts.InboxStatusNew}, Limit: 50,
	})
	if err != nil {
		return contracts.ReplyRelayReport{}, fmt.Errorf("replyrelay: list inbox: %w", err)
	}

	report := contracts.ReplyRelayReport{}
	for _, item := range items {
		if !channelSources[item.Source] {
			continue
		}
		text := item.Body
		if text == "" {
			text = item.Title
		}
		if text == "" {
			continue
		}
		if !s.claim(item) {
			continue
		}
		report.Scanned++
		outcome, replyErr := s.actions.ApplyReply(ctx, text)
		if replyErr != nil {
			// Nothing pending is the ordinary case: people talk to the bot
			// without any approval open. Anything else is worth surfacing.
			if !isQuiet(replyErr) {
				report.Errors = append(report.Errors, fmt.Sprintf("%s: %v", item.ID, replyErr))
			}
			continue
		}
		if !outcome.Applied {
			continue
		}
		// It answered a request: take it out of the triage queue so the Lead does
		// not turn "同意" into a task.
		if _, triageErr := s.inbox.Triage(ctx, []string{item.ID}, contracts.InboxActionArchived, nil); triageErr != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("%s: archive: %v", item.ID, triageErr))
			continue
		}
		report.Applied++
		report.ApprovalIDs = append(report.ApprovalIDs, outcome.ApprovalID)
	}
	return report, nil
}

// claim marks an item as parsed for its current arrival count.
func (s *Service) claim(item contracts.InboxItem) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seen, ok := s.seen[item.ID]; ok && seen == item.Count {
		return false
	}
	s.seen[item.ID] = item.Count
	return true
}

func isQuiet(err error) bool {
	return err == contracts.ErrApprovalNoPending || err == contracts.ErrApprovalNotPending
}

var _ contracts.ReplyRelayService = (*Service)(nil)
