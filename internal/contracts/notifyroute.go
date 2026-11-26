package contracts

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
)

// Notification channels and categories. The router decides *where* a message
// goes; the channel adapters decide how to send it.
const (
	NotifyChannelInApp    = "inapp"
	NotifyChannelSystem   = "system"
	NotifyChannelWeChat   = "wechat"
	NotifyChannelDingTalk = "dingtalk"
	NotifyChannelFeishu   = "feishu"
	NotifyChannelEmail    = "email"
)

// Notification categories: what kind of thing wants the human's attention.
const (
	NotifyCategoryApproval      = "approval"
	NotifyCategoryNeedsDecision = "needs_decision"
	NotifyCategoryTaskDone      = "task_done"
	NotifyCategoryTaskFailed    = "task_failed"
	NotifyCategoryInboxDigest   = "inbox_digest"
	NotifyCategoryDailySummary  = "daily_summary"
	// SettingNotifyRoutes persists the router's book (the user's bindings).
	SettingNotifyRoutes = "notify.routes"
	// NotifyCategoryReport is a finished task/workflow report (M4). Informative,
	// so quiet hours apply; never a reason to block the work.
	NotifyCategoryReport = "report"
)

// NotifyRoute is one category's delivery policy: a primary channel, ordered
// fallbacks, quiet hours and an escalation delay.
type NotifyRoute struct {
	Category string
	// Primary is where the message goes first.
	Primary string
	// Fallback is tried in order when the primary is unhealthy or unanswered.
	Fallback []string
	// QuietHours is "HH:MM-HH:MM" (empty = none). Approval ignores it.
	QuietHours string
	// Escalation is how long to wait before moving to the next channel
	// (0 = no escalation).
	Escalation time.Duration
	// Enabled false parks the category (messages still land in the inbox).
	Enabled bool
}

// DeliveryPlan is the router's answer for one message: the ordered channels to
// try, plus whether the message was deferred by quiet hours.
type DeliveryPlan struct {
	Channels []string
	// Deferred is true when quiet hours held a non-urgent message.
	Deferred bool
	// Reason is one line the UI can show ("静音时段，早上一起汇总").
	Reason string
}

// NotifyCategories is the canonical order of the routing vocabulary: one row per
// category, in the order the UI lists them. A new category must be added here as
// well as to the router's defaults — `report` was routed correctly for a while
// but invisible in Routes() because only the module knew the order.
func NotifyCategories() []string {
	return []string{
		NotifyCategoryApproval,
		NotifyCategoryNeedsDecision,
		NotifyCategoryTaskDone,
		NotifyCategoryTaskFailed,
		NotifyCategoryInboxDigest,
		NotifyCategoryReport,
		NotifyCategoryDailySummary,
	}
}

// NotifyRouterService decides where notifications go. It never sends anything:
// the channel layer executes the plan.
type NotifyRouterService interface {
	// Routes lists the current policy (one row per category, defaults included).
	Routes(ctx context.Context) ([]NotifyRoute, error)
	// Upsert replaces one category's policy.
	Upsert(ctx context.Context, route NotifyRoute) error
	// Plan returns the ordered channels for one category. healthy reports which
	// channels can currently deliver; the in-app channel is always available.
	Plan(ctx context.Context, category string, now time.Time, healthy map[string]bool) (DeliveryPlan, error)
}

// ErrNotifyRouteInvalid is returned for an unknown category, an unknown channel
// or a missing primary.
var ErrNotifyRouteInvalid = errors.New("notifyroute: invalid route")

// KeyNotifyRoute is provided by the notify route module.
var KeyNotifyRoute = cordis.NewServiceKey[NotifyRouterService]("zeokeel/notifyroute")

// ParseNotifyRoutes decodes the notify.routes settings row. Empty returns nil;
// a malformed row is an error the router degrades from (defaults) rather than
// failing activation on.
func ParseNotifyRoutes(raw string) ([]NotifyRoute, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var routes []NotifyRoute
	if err := json.Unmarshal([]byte(raw), &routes); err != nil {
		return nil, err
	}
	return routes, nil
}

// EncodeNotifyRoutes renders the notify.routes settings row.
func EncodeNotifyRoutes(routes []NotifyRoute) (string, error) {
	if routes == nil {
		routes = []NotifyRoute{}
	}
	encoded, err := json.Marshal(routes)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
