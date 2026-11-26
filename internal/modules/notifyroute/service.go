// Package notifyroute decides where a notification goes. It never sends: the
// channel layer executes the plan, so the routing rules stay testable and the
// channels stay dumb.
package notifyroute

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// defaultQuietHours is the product default for non-urgent categories (the
// interaction spec's "22:30–08:00"); approvals never use it.
const defaultQuietHours = "22:30-08:00"

var knownChannels = map[string]bool{
	contracts.NotifyChannelInApp:    true,
	contracts.NotifyChannelSystem:   true,
	contracts.NotifyChannelWeChat:   true,
	contracts.NotifyChannelDingTalk: true,
	contracts.NotifyChannelFeishu:   true,
	contracts.NotifyChannelEmail:    true,
}

// defaultRoutes is the policy a fresh install starts with: everything stays in
// the app until the user binds a channel. Quiet hours apply to the informative
// categories only.
func defaultRoutes() map[string]contracts.NotifyRoute {
	quiet := defaultQuietHours
	return map[string]contracts.NotifyRoute{
		contracts.NotifyCategoryApproval: {
			Category: contracts.NotifyCategoryApproval, Primary: contracts.NotifyChannelInApp, Enabled: true,
		},
		contracts.NotifyCategoryNeedsDecision: {
			Category: contracts.NotifyCategoryNeedsDecision, Primary: contracts.NotifyChannelInApp, Enabled: true,
		},
		contracts.NotifyCategoryTaskDone: {
			Category: contracts.NotifyCategoryTaskDone, Primary: contracts.NotifyChannelInApp,
			QuietHours: quiet, Enabled: true,
		},
		contracts.NotifyCategoryTaskFailed: {
			Category: contracts.NotifyCategoryTaskFailed, Primary: contracts.NotifyChannelInApp,
			QuietHours: quiet, Enabled: true,
		},
		contracts.NotifyCategoryInboxDigest: {
			Category: contracts.NotifyCategoryInboxDigest, Primary: contracts.NotifyChannelInApp,
			QuietHours: quiet, Enabled: true,
		},
		contracts.NotifyCategoryReport: {
			Category: contracts.NotifyCategoryReport, Primary: contracts.NotifyChannelInApp,
			QuietHours: quiet, Enabled: true,
		},
		contracts.NotifyCategoryDailySummary: {
			Category: contracts.NotifyCategoryDailySummary, Primary: contracts.NotifyChannelInApp,
			QuietHours: quiet, Enabled: true,
		},
	}
}

// Service is the router. Routes are persisted to the settings row
// (contracts.SettingNotifyRoutes) so a restart keeps the user's bindings: a
// route that silently resets to "in-app only" would make WeChat delivery look
// broken for no visible reason.
type Service struct {
	mu       sync.Mutex
	routes   map[string]contracts.NotifyRoute
	settings contracts.SettingsService
	logger   contracts.Logger
}

// NewService builds the router. initial (optional) replaces the defaults.
func NewService(initial []contracts.NotifyRoute) *Service {
	service := &Service{routes: defaultRoutes()}
	for _, route := range initial {
		if err := validateRoute(route); err != nil {
			continue
		}
		service.routes[route.Category] = route
	}
	return service
}

// Routes implements contracts.NotifyRouterService.
func (s *Service) Routes(ctx context.Context) ([]contracts.NotifyRoute, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contracts.NotifyRoute, 0, len(contracts.NotifyCategories()))
	for _, category := range contracts.NotifyCategories() {
		if route, ok := s.routes[category]; ok {
			out = append(out, cloneRoute(route))
		}
	}
	// Anything stored under a category this build does not know about is still
	// listed, after the known ones: hiding it would make a route the user edited
	// (on a newer build) look like it never existed.
	extra := make([]string, 0, len(s.routes))
	for category := range s.routes {
		if !slices.Contains(contracts.NotifyCategories(), category) {
			extra = append(extra, category)
		}
	}
	sort.Strings(extra)
	for _, category := range extra {
		out = append(out, cloneRoute(s.routes[category]))
	}
	return out, nil
}

// Upsert implements contracts.NotifyRouterService.
func (s *Service) Upsert(ctx context.Context, route contracts.NotifyRoute) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRoute(route); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[route.Category] = cloneRoute(route)
	return s.persistLocked()
}

// WithSettings attaches the settings store and loads any persisted routes.
func (s *Service) WithSettings(settings contracts.SettingsService, logger contracts.Logger) *Service {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = settings
	s.logger = logger
	if settings == nil {
		return s
	}
	routes, err := contracts.ParseNotifyRoutes(settings.Get(contracts.SettingNotifyRoutes, ""))
	if err != nil {
		if logger != nil {
			logger.Printf("[notifyroute] ignoring unreadable %s row: %v", contracts.SettingNotifyRoutes, err)
		}
		return s
	}
	for _, route := range routes {
		if err := validateRoute(route); err != nil {
			continue
		}
		s.routes[route.Category] = cloneRoute(route)
	}
	return s
}

// persistLocked writes the whole book (a route edit is rare; a partial write
// would be a worse contract than a slightly larger row).
func (s *Service) persistLocked() error {
	if s.settings == nil {
		return nil
	}
	rows := make([]contracts.NotifyRoute, 0, len(s.routes))
	for _, route := range s.routes {
		rows = append(rows, cloneRoute(route))
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Category < rows[j].Category })
	encoded, err := contracts.EncodeNotifyRoutes(rows)
	if err != nil {
		return fmt.Errorf("notifyroute: encode routes: %w", err)
	}
	if err := s.settings.Set(contracts.SettingNotifyRoutes, encoded); err != nil {
		if s.logger != nil {
			s.logger.Printf("[notifyroute] persist routes: %v", err)
		}
		return nil
	}
	return nil
}

// Plan implements contracts.NotifyRouterService.
func (s *Service) Plan(ctx context.Context, category string, now time.Time, healthy map[string]bool) (contracts.DeliveryPlan, error) {
	if err := ctx.Err(); err != nil {
		return contracts.DeliveryPlan{}, err
	}
	s.mu.Lock()
	route, ok := s.routes[category]
	s.mu.Unlock()
	if !ok {
		route = contracts.NotifyRoute{Category: category, Primary: contracts.NotifyChannelInApp, Enabled: true}
	}

	if !route.Enabled {
		return contracts.DeliveryPlan{
			Channels: []string{contracts.NotifyChannelInApp},
			Reason:   "这个类别已关闭，只在站内显示",
		}, nil
	}

	// Approval is the one category that must reach the human now; everything
	// else can wait for the morning.
	if category != contracts.NotifyCategoryApproval && inQuietHours(route.QuietHours, now) {
		return contracts.DeliveryPlan{
			Channels: []string{contracts.NotifyChannelInApp},
			Deferred: true,
			Reason:   "静音时段，早上一起汇总",
		}, nil
	}

	plan := contracts.DeliveryPlan{Channels: make([]string, 0, len(route.Fallback)+2)}
	seen := map[string]bool{}
	appendChannel := func(channel string) {
		if channel == "" || seen[channel] {
			return
		}
		seen[channel] = true
		plan.Channels = append(plan.Channels, channel)
	}

	candidates := append([]string{route.Primary}, route.Fallback...)
	for _, channel := range candidates {
		if !knownChannels[channel] {
			continue
		}
		if channel != contracts.NotifyChannelInApp && !channelHealthy(healthy, channel) {
			continue
		}
		appendChannel(channel)
	}
	// In-app is always available: it is the last resort that never fails.
	appendChannel(contracts.NotifyChannelInApp)
	return plan, nil
}

func channelHealthy(healthy map[string]bool, channel string) bool {
	if healthy == nil {
		return true
	}
	return healthy[channel]
}

func cloneRoute(route contracts.NotifyRoute) contracts.NotifyRoute {
	out := route
	out.Fallback = append([]string(nil), route.Fallback...)
	return out
}

func validateRoute(route contracts.NotifyRoute) error {
	if _, ok := defaultRoutes()[route.Category]; !ok {
		return fmt.Errorf("%w: unknown category %q", contracts.ErrNotifyRouteInvalid, route.Category)
	}
	if route.Primary == "" || !knownChannels[route.Primary] {
		return fmt.Errorf("%w: unknown primary %q", contracts.ErrNotifyRouteInvalid, route.Primary)
	}
	for _, channel := range route.Fallback {
		if !knownChannels[channel] {
			return fmt.Errorf("%w: unknown fallback %q", contracts.ErrNotifyRouteInvalid, channel)
		}
	}
	if route.QuietHours != "" {
		if _, ok := parseWindow(route.QuietHours); !ok {
			return fmt.Errorf("%w: quiet hours %q are not HH:MM-HH:MM", contracts.ErrNotifyRouteInvalid, route.QuietHours)
		}
	}
	return nil
}

// inQuietHours reports whether now falls inside the window (handles the
// midnight wrap).
func inQuietHours(window string, now time.Time) bool {
	parsed, ok := parseWindow(window)
	if !ok {
		return false
	}
	minutes := now.Hour()*60 + now.Minute()
	if parsed.start <= parsed.end {
		return minutes >= parsed.start && minutes < parsed.end
	}
	return minutes >= parsed.start || minutes < parsed.end
}

type window struct{ start, end int }

func parseWindow(value string) (window, bool) {
	parts := strings.Split(value, "-")
	if len(parts) != 2 {
		return window{}, false
	}
	start, ok := parseClock(parts[0])
	if !ok {
		return window{}, false
	}
	end, ok := parseClock(parts[1])
	if !ok {
		return window{}, false
	}
	return window{start: start, end: end}, true
}

func parseClock(value string) (int, bool) {
	fields := strings.Split(strings.TrimSpace(value), ":")
	if len(fields) != 2 {
		return 0, false
	}
	hour, err := strconv.Atoi(fields[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, false
	}
	minute, err := strconv.Atoi(fields[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

var _ contracts.NotifyRouterService = (*Service)(nil)
