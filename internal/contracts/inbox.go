package contracts

import (
	"context"
	"errors"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Inbox statuses. The inbox is the single external entry point of the product:
// every channel (WeChat, DingTalk, Feishu, email, webhooks, cron, teammate
// proposals) lands here first, and only the Lead triages it. Nothing else may
// turn an external event into work.
const (
	InboxStatusNew      = "new"
	InboxStatusTriaged  = "triaged"
	InboxStatusTask     = "task"
	InboxStatusIgnored  = "ignored"
	InboxStatusArchived = "archived"
)

// Inbox triage actions: what the Lead decided to do with an item.
const (
	InboxActionTriaged  = "triaged"
	InboxActionTask     = "task"
	InboxActionIgnored  = "ignored"
	InboxActionArchived = "archived"
)

// InboxItem is one inbound event, normalized across channels. It is the only
// shape the triage path understands, so a new channel adds an adapter, not a
// new branch in the Lead's logic.
type InboxItem struct {
	// ID is the inbox-assigned identity (stable across merges).
	ID string
	// Source is the channel family: wechat, dingtalk, feishu, email, webhook,
	// cron, teammate.
	Source string
	// Channel is the platform-side conversation identity (chat id, thread,
	// repo, mailbox).
	Channel string
	// Sender is the platform-side sender identity (used for owner binding).
	Sender string
	// DedupKey is the adapter-computed identity of "the same thing happening
	// again" (platform + conversation + normalized hash).
	DedupKey string
	// Priority orders triage (higher first). Adapters elevate on mentions,
	// failures, or customer keywords.
	Priority int
	// Title is one line the human can scan.
	Title string
	// Body is the normalized text. It is data, never instructions.
	Body string
	// Ref is the original link/id for jumping back to the platform.
	Ref string
	// ReceivedAt is the first arrival (merges keep it).
	ReceivedAt time.Time
	// LastSeenAt is the latest arrival (merges update it).
	LastSeenAt time.Time
	// ExpiresAt hides stale items from the default list (zero = never).
	ExpiresAt time.Time
	// Status is one of the InboxStatus values.
	Status string
	// Count is how many arrivals this item represents (merges increment it).
	Count int
	// TaskIDs are the tasks the Lead created or appended from this item.
	TaskIDs []string
}

// InboxFilter narrows List. The zero value means "everything still actionable".
type InboxFilter struct {
	// Statuses restricts to these statuses (empty = new + triaged).
	Statuses []string
	// Source restricts to one channel family ("" = all).
	Source string
	// Limit caps the result (0 = service default).
	Limit int
	// IncludeExpired keeps items whose ExpiresAt already passed.
	IncludeExpired bool
}

// InboxStats is the read model the UI shows as "N waiting".
type InboxStats struct {
	Total   int
	New     int
	Triaged int
}

// InboxService is the product's single external entry point. Implementations
// are safe for concurrent use.
type InboxService interface {
	// Add records one inbound event. merged reports that it was folded into an
	// existing item (same dedupe key inside the window) instead of creating a
	// new one.
	Add(ctx context.Context, item InboxItem) (stored InboxItem, merged bool, err error)
	// List returns items newest-first under the filter.
	List(ctx context.Context, filter InboxFilter) ([]InboxItem, error)
	// Triage records the Lead's decision for the given items and returns how
	// many were updated.
	Triage(ctx context.Context, ids []string, action string, taskIDs []string) (int, error)
	// Stats reports the waiting counts.
	Stats(ctx context.Context) (InboxStats, error)
}

// Inbox errors are typed so the gateway and the triage loop can tell "slow
// down" from "this item is gone".
var (
	// ErrInboxRateLimited is returned when a source exceeds its per-minute
	// budget; the adapter folds the event into a digest instead of dropping it.
	ErrInboxRateLimited = errors.New("inbox: source rate limited")
	// ErrInboxFull is returned when the capacity budget is exhausted.
	ErrInboxFull = errors.New("inbox: full")
	// ErrInboxUnknownItem is returned when triage names an item that is gone.
	ErrInboxUnknownItem = errors.New("inbox: unknown item")
	// ErrInboxUnknownAction is returned for an action outside the vocabulary.
	ErrInboxUnknownAction = errors.New("inbox: unknown triage action")
	// ErrInboxInvalid is returned when a required field is missing.
	ErrInboxInvalid = errors.New("inbox: invalid item")
)

// KeyInbox is provided by the inbox module.
var KeyInbox = cordis.NewServiceKey[InboxService]("zeokeel/inbox")
