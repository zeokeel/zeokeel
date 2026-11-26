package notifyroute_test

// TDD for the delivery planner. It is the only place that decides "which channel
// gets this", so the tests pin the four rules the product promised: approval is
// never deferred, quiet hours hold everything else, unhealthy channels are
// skipped in order, and the in-app channel always exists.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/notifyroute"
)

func newRouter(t *testing.T) *notifyroute.Service {
	t.Helper()
	return notifyroute.NewService(nil)
}

func at(hour, minute int) time.Time {
	return time.Date(2026, 11, 25, hour, minute, 0, 0, time.Local)
}

func TestDefaultsCoverEveryCategory(t *testing.T) {
	router := newRouter(t)
	routes, err := router.Routes(context.Background())
	if err != nil {
		t.Fatalf("Routes: %v", err)
	}
	if len(routes) != len(contracts.NotifyCategories()) {
		t.Fatalf("routes = %d, want %d default categories (one per known category)", len(routes), len(contracts.NotifyCategories()))
	}
	seen := map[string]bool{}
	for _, route := range routes {
		if seen[route.Category] {
			t.Fatalf("category %q listed twice", route.Category)
		}
		seen[route.Category] = true
	}
	for _, category := range contracts.NotifyCategories() {
		if !seen[category] {
			t.Fatalf("category %q has no default route", category)
		}
	}
	for _, route := range routes {
		if route.Primary != contracts.NotifyChannelInApp {
			t.Fatalf("default primary for %q = %q, want inapp", route.Category, route.Primary)
		}
	}
}

func TestUpsertReplacesAndValidates(t *testing.T) {
	router := newRouter(t)
	ctx := context.Background()

	if err := router.Upsert(ctx, contracts.NotifyRoute{
		Category: contracts.NotifyCategoryApproval,
		Primary:  contracts.NotifyChannelWeChat,
		Fallback: []string{contracts.NotifyChannelDingTalk},
		Enabled:  true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	routes, _ := router.Routes(ctx)
	for _, route := range routes {
		if route.Category == contracts.NotifyCategoryApproval {
			if route.Primary != contracts.NotifyChannelWeChat || len(route.Fallback) != 1 {
				t.Fatalf("approval route = %+v, want wechat + dingtalk fallback", route)
			}
		}
	}

	if err := router.Upsert(ctx, contracts.NotifyRoute{Category: "nope", Primary: contracts.NotifyChannelInApp}); !errors.Is(err, contracts.ErrNotifyRouteInvalid) {
		t.Fatalf("unknown category err = %v, want ErrNotifyRouteInvalid", err)
	}
	if err := router.Upsert(ctx, contracts.NotifyRoute{Category: contracts.NotifyCategoryApproval, Primary: "pigeon"}); !errors.Is(err, contracts.ErrNotifyRouteInvalid) {
		t.Fatalf("unknown channel err = %v, want ErrNotifyRouteInvalid", err)
	}
	if err := router.Upsert(ctx, contracts.NotifyRoute{Category: contracts.NotifyCategoryApproval, Primary: ""}); !errors.Is(err, contracts.ErrNotifyRouteInvalid) {
		t.Fatalf("missing primary err = %v, want ErrNotifyRouteInvalid", err)
	}
}

func TestPlanPrimaryThenFallbacksInOrder(t *testing.T) {
	router := newRouter(t)
	ctx := context.Background()
	_ = router.Upsert(ctx, contracts.NotifyRoute{
		Category: contracts.NotifyCategoryApproval,
		Primary:  contracts.NotifyChannelWeChat,
		Fallback: []string{contracts.NotifyChannelDingTalk, contracts.NotifyChannelEmail},
		Enabled:  true,
	})
	plan, err := router.Plan(ctx, contracts.NotifyCategoryApproval, at(10, 0), map[string]bool{
		contracts.NotifyChannelWeChat: true, contracts.NotifyChannelDingTalk: true, contracts.NotifyChannelEmail: true,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	// In-app is the terminal catch-all: it is always the last channel, exactly
	// once, so a delivery loop can stop there and the UI is never empty.
	want := []string{contracts.NotifyChannelWeChat, contracts.NotifyChannelDingTalk, contracts.NotifyChannelEmail, contracts.NotifyChannelInApp}
	if len(plan.Channels) != len(want) {
		t.Fatalf("channels = %v, want %v", plan.Channels, want)
	}
	for i := range want {
		if plan.Channels[i] != want[i] {
			t.Fatalf("channels = %v, want %v", plan.Channels, want)
		}
	}
}

func TestPlanSkipsUnhealthyChannels(t *testing.T) {
	router := newRouter(t)
	ctx := context.Background()
	_ = router.Upsert(ctx, contracts.NotifyRoute{
		Category: contracts.NotifyCategoryApproval,
		Primary:  contracts.NotifyChannelWeChat,
		Fallback: []string{contracts.NotifyChannelDingTalk},
		Enabled:  true,
	})
	plan, err := router.Plan(ctx, contracts.NotifyCategoryApproval, at(10, 0), map[string]bool{
		contracts.NotifyChannelWeChat: true, contracts.NotifyChannelDingTalk: false,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Channels) != 2 || plan.Channels[0] != contracts.NotifyChannelWeChat ||
		plan.Channels[1] != contracts.NotifyChannelInApp {
		t.Fatalf("channels = %v, want wechat then inapp (in-app always available)", plan.Channels)
	}
}

func TestPlanAlwaysFallsBackToInApp(t *testing.T) {
	router := newRouter(t)
	ctx := context.Background()
	plan, err := router.Plan(ctx, contracts.NotifyCategoryTaskDone, at(10, 0), nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Channels) == 0 || plan.Channels[len(plan.Channels)-1] != contracts.NotifyChannelInApp {
		t.Fatalf("channels = %v, want inapp at the end", plan.Channels)
	}
}

func TestQuietHoursDeferEverythingButApproval(t *testing.T) {
	router := newRouter(t)
	ctx := context.Background()
	_ = router.Upsert(ctx, contracts.NotifyRoute{
		Category: contracts.NotifyCategoryTaskDone, Primary: contracts.NotifyChannelWeChat,
		QuietHours: "22:30-08:00", Enabled: true,
	})
	plan, err := router.Plan(ctx, contracts.NotifyCategoryTaskDone, at(23, 0), map[string]bool{
		contracts.NotifyChannelWeChat: true,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !plan.Deferred {
		t.Fatal("quiet hours did not defer a task-done message")
	}
	if len(plan.Channels) != 1 || plan.Channels[0] != contracts.NotifyChannelInApp {
		t.Fatalf("channels = %v, want inapp only while deferred", plan.Channels)
	}

	// Quote the approval route and check it ignores the same quiet window.
	_ = router.Upsert(ctx, contracts.NotifyRoute{
		Category: contracts.NotifyCategoryApproval, Primary: contracts.NotifyChannelWeChat,
		QuietHours: "22:30-08:00", Enabled: true,
	})
	approval, err := router.Plan(ctx, contracts.NotifyCategoryApproval, at(23, 0), map[string]bool{
		contracts.NotifyChannelWeChat: true,
	})
	if err != nil {
		t.Fatalf("Plan approval: %v", err)
	}
	if approval.Deferred || approval.Channels[0] != contracts.NotifyChannelWeChat {
		t.Fatalf("approval plan = %+v, want not deferred on wechat", approval)
	}
}

func TestQuietHoursWrapAcrossMidnight(t *testing.T) {
	router := newRouter(t)
	ctx := context.Background()
	_ = router.Upsert(ctx, contracts.NotifyRoute{
		Category: contracts.NotifyCategoryInboxDigest, Primary: contracts.NotifyChannelEmail,
		QuietHours: "22:30-08:00", Enabled: true,
	})
	if plan, _ := router.Plan(ctx, contracts.NotifyCategoryInboxDigest, at(2, 0), nil); !plan.Deferred {
		t.Fatal("02:00 inside a 22:30-08:00 window was not deferred")
	}
	if plan, _ := router.Plan(ctx, contracts.NotifyCategoryInboxDigest, at(10, 0), nil); plan.Deferred {
		t.Fatal("10:00 outside the window was deferred")
	}
}

func TestDisabledCategoryPlansInAppOnly(t *testing.T) {
	router := newRouter(t)
	ctx := context.Background()
	_ = router.Upsert(ctx, contracts.NotifyRoute{
		Category: contracts.NotifyCategoryTaskDone, Primary: contracts.NotifyChannelWeChat, Enabled: false,
	})
	plan, err := router.Plan(ctx, contracts.NotifyCategoryTaskDone, at(10, 0), map[string]bool{
		contracts.NotifyChannelWeChat: true,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Channels) != 1 || plan.Channels[0] != contracts.NotifyChannelInApp {
		t.Fatalf("channels = %v, want inapp only for a disabled category", plan.Channels)
	}
}
