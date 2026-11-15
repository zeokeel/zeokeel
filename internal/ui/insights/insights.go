// Package insights contributes the Insights screen: the cross-session usage
// surface (DSH dsh-context P1). It reads contracts.InsightsService once per
// visit (and on demand) on a background goroutine and renders the aggregate with
// mygo's native controls only: a KPI row, a 7/30-day switch, bounded
// ui.Table sections for the daily/model/workspace aggregates and a ui.List of
// the busiest conversations whose rows open the conversation.
//
// It is a UI module like ui/automation: a kernel Descriptor that requires the
// host UI runtime and navigator, registers the "insights" screen and reads the
// optional contracts.KeyInsights service. The aggregation runs off the UI
// thread and the result hops back through UIRuntime.Post; the screen never
// touches the session store or Rig directly. The screen registration and its
// top-band contributions are kernel-effect owned, so disposal unwinds them.
package insights

import (
	"context"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

const (
	// ModuleID is the compile-time module identity.
	ModuleID = "zeokeel-ui/insights"
	// ScreenID is the screen the module contributes.
	ScreenID = "insights"
	// ScreenOrder places the destination between Automation (20) and Plugins
	// (30) in the host's screen order and the sidebar's navigation.
	ScreenOrder = 25
	// refreshAfter is the automatic refresh age: while the screen stays open,
	// an aggregate older than this is re-read in the background. The backend
	// also caches, so a refresh is a bounded read, never a per-frame scan.
	refreshAfter = 60 * time.Second
)

// Config is the module's typed assembly config (all fields have defaults).
type Config struct {
	// Title is the screen title and page heading (default "洞察").
	Title string `json:"title"`
}

// resolved fills the configured strings with their defaults.
func (c Config) resolved() Config {
	return Config{Title: firstNonEmpty(c.Title, "洞察")}
}

// state is the module's UI state. It is only touched on the UI thread; the
// aggregation goroutine hands its result back through UIRuntime.Post.
type state struct {
	ctx       cordis.Context
	locale    contracts.LocaleService
	runtime   contracts.UIRuntime
	navigator contracts.Navigator
	service   contracts.InsightsService
	chat      contracts.ChatService

	// days is the active window (contracts.InsightsDaysWeek/Month); rangeIndex
	// is the segmented control's index (0 = week, 1 = month).
	days       int
	rangeIndex int

	data     contracts.Insights
	hasData  bool
	loading  bool
	loadedAt time.Time
	errText  string
	// requestID identifies the in-flight aggregation so a stale result after a
	// range switch is dropped instead of overwriting the newer window.
	requestID uint64

	// Per-collection native state: the tables are read-only (they only need
	// stable keys and accessible row names) and the session list keeps the
	// keyboard choice that Enter opens.
	dailyTable     ui.ListState
	modelTable     ui.ListState
	workspaceTable ui.ListState
	sessionList    ui.ListState
	sessionChosen  int
	// sessionQuery is the leaderboard's local filter draft (title, model or
	// workspace). It filters the rows on screen only: the aggregate and its
	// per-range read are untouched.
	sessionQuery string
}

// Module registers the Insights screen.
func Module() *module.Descriptor[Config] {
	return module.Define(ModuleID, func(ctx cordis.Context, cfg Config) error {
		runtime, err := ctx.Require(contracts.KeyUIRuntime)
		if err != nil {
			return err
		}
		navigator, err := ctx.Require(contracts.KeyNavigator)
		if err != nil {
			return err
		}
		config := cfg.resolved()
		locale, _ := ctx.Get(contracts.KeyLocale)
		moduleState := &state{
			ctx:           ctx,
			locale:        locale,
			runtime:       runtime,
			navigator:     navigator,
			days:          contracts.InsightsDaysWeek,
			sessionChosen: -1,
		}
		// Both services are optional: the screen renders an explanatory empty
		// state without the aggregation backend, and the session link is simply
		// unavailable without the chat service.
		if service, ok := ctx.Get(contracts.KeyInsights); ok {
			moduleState.service = service
		}
		if chat, ok := ctx.Get(contracts.KeyChat); ok {
			moduleState.chat = chat
		}

		if err := own(ctx, func() (func(), error) {
			return runtime.RegisterScreen(contracts.ScreenSpec{
				ID:    ScreenID,
				Title: config.Title,
				Order: ScreenOrder,
				Build: func(c *ui.Context) {
					buildScreen(c, config, moduleState)
				},
			})
		}); err != nil {
			return err
		}
		// The screen's chrome lives in the host's B3 top band: the title
		// (SlotScreenHeader) and the page's one immediate action, Refresh
		// (SlotScreenActions). Item IDs carry the screen namespace so the band
		// stays on this screen only.
		screenSlots := []contracts.SlotItem{
			{
				Slot: contracts.SlotScreenHeader, ID: "insights.title", Order: 10,
				Build: func(c *ui.Context) { buildScreenHeaderSlot(c, config, moduleState) },
			},
			{
				Slot: contracts.SlotScreenActions, ID: "insights.actions", Order: 10,
				Build: func(c *ui.Context) { buildScreenActionsSlot(c, moduleState) },
			},
		}
		for _, item := range screenSlots {
			item := item
			if err := own(ctx, func() (func(), error) {
				return runtime.RegisterSlotItem(item)
			}); err != nil {
				return err
			}
		}
		return nil
	}).
		AddRequires(module.Requires(contracts.KeyUIRuntime)).
		AddRequires(module.Requires(contracts.KeyNavigator)).
		WithName("ZeoKeel insights screen")
}

// own registers one contribution and ties its unregistration to ctx's effect
// lifetime.
func own(ctx cordis.Context, register func() (func(), error)) error {
	unregister, err := register()
	if err != nil {
		return err
	}
	if _, err := ctx.Effect(func() cordis.Effect {
		return cordis.Effect{cordis.CleanupOf(unregister)}
	}); err != nil {
		unregister()
		return err
	}
	return nil
}

// tr returns the localized copy for the current frame's language.
func (st *state) tr(zh, en string) string { return uikit.T(st.locale, zh, en) }

// english reports whether the current frame renders English copy.
func (st *state) english() bool { return uikit.Language(st.locale) == "en" }

// localized translates a value that still carries its built-in Chinese default
// (an assembly-configured custom label is kept verbatim).
func (st *state) localized(value, zhDefault, enDefault string) string {
	if strings.TrimSpace(value) == "" || value == zhDefault {
		return st.tr(zhDefault, enDefault)
	}
	return value
}

// ensureLoaded starts the first aggregation, and a background refresh once the
// visible data is older than refreshAfter. It is called once per frame and
// never blocks: the aggregation runs on its own goroutine.
func (st *state) ensureLoaded() {
	if st.service == nil || st.loading {
		return
	}
	if st.errText != "" {
		// A failed load stays failed until the user retries: re-requesting on
		// every frame would spin on a broken backend and never show the error.
		return
	}
	if !st.hasData {
		st.requestAggregate(false)
		return
	}
	if time.Since(st.loadedAt) >= refreshAfter {
		st.requestAggregate(false)
	}
}

// requestAggregate loads one aggregation window on a background goroutine and
// posts the result to the UI thread. Stale results (a newer request started) are
// dropped.
func (st *state) requestAggregate(force bool) {
	if st.service == nil || st.loading {
		return
	}
	st.loading = true
	st.errText = ""
	st.requestID++
	requestID := st.requestID
	days := st.days
	go func() {
		data, err := st.service.Insights(context.Background(), contracts.InsightsQuery{
			Days:         days,
			SessionLimit: contracts.InsightsDefaultSessionLimit,
			ForceRefresh: force,
		})
		st.runtime.Post(func() {
			if requestID != st.requestID {
				return
			}
			st.loading = false
			if err != nil {
				st.errText = insightsErrorText(st, "读取洞察数据失败：", "Failed to read insights: ", err)
			} else {
				st.data = data
				st.hasData = true
				st.loadedAt = time.Now()
				st.errText = ""
			}
			st.runtime.Invalidate()
		})
	}()
}

// daysForRange maps the segmented control's index to the window length.
func daysForRange(index int) int {
	if index == 1 {
		return contracts.InsightsDaysMonth
	}
	return contracts.InsightsDaysWeek
}

// setRange switches the aggregation window and reloads it (the previous window's
// data is not shown under the new range: an honest loading state is better).
func (st *state) setRange(index int) {
	if index != 0 {
		index = 1
	}
	st.rangeIndex = index
	st.days = daysForRange(index)
	if st.loading {
		// A request for the old window is in flight: invalidate it so its result
		// cannot land under the new range.
		st.requestID++
		st.loading = false
	}
	st.hasData = false
	st.data = contracts.Insights{}
	st.sessionChosen = -1
	st.dailyTable = ui.ListState{}
	st.modelTable = ui.ListState{}
	st.workspaceTable = ui.ListState{}
	st.sessionList = ui.ListState{}
	st.requestAggregate(false)
}

// openSession activates the conversation and pushes the session screen. A
// failure keeps the user on the Insights screen with an inline error instead of
// navigating to a conversation the backend refused.
func (st *state) openSession(sessionID string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	if st.chat != nil {
		if err := st.chat.SetActiveSession(sessionID); err != nil {
			st.errText = st.tr("打开会话失败：", "Failed to open the conversation: ") + err.Error()
			st.runtime.Invalidate()
			return
		}
	}
	if st.navigator != nil {
		if err := st.navigator.Push("session"); err != nil {
			st.errText = st.tr("打开会话失败：", "Failed to open the conversation: ") + err.Error()
			st.runtime.Invalidate()
			return
		}
	}
	st.runtime.Invalidate()
}

// firstNonEmpty returns the first non-blank value.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// insightsErrorText localizes a backend failure: the module's own "insights:"
// prefix is implementation noise and never reaches the UI.
func insightsErrorText(st *state, zhPrefix, enPrefix string, err error) string {
	if err == nil {
		return uikit.T(st.locale, zhPrefix, enPrefix)
	}
	message := strings.TrimSpace(strings.TrimPrefix(err.Error(), "insights: "))
	return uikit.T(st.locale, zhPrefix, enPrefix) + message
}
