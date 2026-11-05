package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/session"
	"github.com/zeokeel/zeokeel/internal/state"
	"github.com/zeokeel/zeokeel/internal/store"
)

// liveTab is the selected tab of the live session header.
type liveTab int

const (
	liveChat liveTab = iota
	liveTrajectory
)

// liveUIState is the live-only interaction state of one App: the selected tab,
// which raw payloads are expanded, and the drafts of the question cards. Like
// liveState in motion.go it lives here, keyed by the App, because App is
// defined in app.go, which this change does not own. The static shell
// (App.Ctrl == nil) never reads it, so the pixel baseline is untouched.
type liveUIState struct {
	tab         int
	expandedRaw map[int64]bool
	artifactRaw map[string]bool

	// questionDraft is the text input of the pending ask_user card. The
	// questionErr is shown below it when the controller rejects the answer.
	questionDraft string
	questionErr   string

	// Trajectory event cache. It is refreshed when the snapshot's sequence
	// changes, not on every frame, so the working indicator's animation
	// frames do not turn into store queries.
	trajSessionID string
	trajSeq       uint64
	trajLoaded    bool
	trajEvents    []store.Event
	trajErr       error
}

// liveUIStates maps each App to its live UI state. Entries live as long as
// their App; the real app has one.
var liveUIStates sync.Map // map[*App]*liveUIState

// uiState returns the live UI state of a, making it on first use.
func (a *App) uiState() *liveUIState {
	if s, ok := liveUIStates.Load(a); ok {
		return s.(*liveUIState)
	}
	s := &liveUIState{
		expandedRaw: make(map[int64]bool),
		artifactRaw: make(map[string]bool),
	}
	actual, _ := liveUIStates.LoadOrStore(a, s)
	return actual.(*liveUIState)
}

// liveTabs is the clickable Chat/Trajectory row. The active tab carries the 2px
// accent underline; switching tabs only changes liveUIState, so the transcript
// and composer keep their state. The Trajectory tab is the "coding view" that
// Settings > General > Show coding view controls, so it is absent while that
// setting is off.
func (a *App) liveTabs(c *ui.Context, t *design.Tokens) {
	st := a.uiState()
	n := 1
	if a.currentSettings().ShowCodingView {
		n = 2
	}
	if st.tab < 0 || st.tab >= n {
		st.tab = int(liveChat)
	}
	// TabsBase provides RoleTabList, a horizontal focus group, per-tab
	// Checked state and the Left/Right/Home/End keys; the artwork below is
	// the brand's quiet tab: label plus a small accent underline.
	th := t.Theme()
	tabs := ui.TabsBase(c, &st.tab, n)
	tabs.List.Height(sessTabsH).Margin(th.Space(2), 0, 0, 0).
		Padding(0, 0, 0, th.Space(2)).
		Gap(th.Space(9)).AlignItems(ui.Stretch).Children(func() {
		a.liveTabButton(c, t, tabs, 0, liveChat, a.T("session.chat"))
		if n > 1 {
			a.liveTabButton(c, t, tabs, 1, liveTrajectory, a.T("session.trajectory"))
		}
	})
}

// liveTabButton draws one header tab through the TabsBase part, which owns
// selection, checked state and arrow-key navigation.
func (a *App) liveTabButton(c *ui.Context, t *design.Tokens, tabs ui.TabsParts, index int, target liveTab, label string) {
	th := t.Theme()
	active := a.uiState().tab == int(target)
	row := tabs.Tab(index).Height(sessTabsH).PaddingX(0).Gap(0).
		Label(label).Transition(design.ColorTransition())
	row.Children(func() {
		ui.Column(c).Height(sessTabsH).Justify(ui.SpaceBetween).Children(func() {
			color := t.LabelSecondary
			weight := 500
			if active {
				color, weight = t.LabelPrimary, 600
			}
			ui.Text(c, label).FontSize(13).FontWeight(weight).NoWrap().TextColor(color)
			bar := ui.Box(c).Height(th.Space(0.5)).Radius(th.Space(0.25))
			if active {
				bar.Background(t.Accent)
			}
		})
	})
}

// trajectoryPane is the Trajectory tab: the session's persisted event trace as
// a scrollable timeline, oldest first (newest last). Each row is clickable; the
// raw payload opens below it in a collapsed detail.
func (a *App) trajectoryPane(c *ui.Context, t *design.Tokens, snap *state.Snapshot) {
	events, err := a.trajectoryEvents(snap)
	entries := buildTrajectory(events)
	ui.Column(c).Grow(1).Padding(design.Space6).Gap(design.Space3).Children(func() {
		ui.Row(c).Gap(design.Space2).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.T("session.trajectory")).FontSize(13).FontWeight(600).TextColor(t.LabelPrimary)
			ui.Text(c, a.T("session.trajectory_count", len(entries), len(events))).
				FontSize(11).TextColor(t.LabelTertiary).Grow(1)
		})
		if err != nil {
			ui.Text(c, a.T("session.trajectory_load_error", err.Error())).FontSize(12).TextColor(t.Danger)
			return
		}
		if len(entries) == 0 {
			ui.Text(c, a.T("session.trajectory_empty")).
				FontSize(12).TextColor(t.LabelTertiary)
			return
		}
		ui.Scroll(c).Grow(1).Children(func() {
			for _, entry := range entries {
				a.trajectoryRow(c, t, entry)
			}
		})
	})
}

// trajectoryEvents returns the stored event trace for the current session,
// cached per snapshot sequence. A store read failure is cached until the next
// snapshot so the view does not hammer a broken store.
func (a *App) trajectoryEvents(snap *state.Snapshot) ([]store.Event, error) {
	if a.Ctrl == nil {
		return nil, nil
	}
	sessionID := snap.SessionID
	if sessionID == "" {
		sessionID = a.Ctrl.CurrentSessionID()
	}
	if sessionID == "" {
		return nil, nil
	}
	uiState := a.uiState()
	if uiState.trajLoaded && uiState.trajSessionID == sessionID && uiState.trajSeq == snap.Seq {
		return uiState.trajEvents, uiState.trajErr
	}
	events, err := a.Ctrl.Events(context.Background(), sessionID)
	uiState.trajSessionID, uiState.trajSeq = sessionID, snap.Seq
	uiState.trajEvents, uiState.trajErr, uiState.trajLoaded = events, err, true
	return events, err
}

// trajectoryRow draws one timeline entry. Clicking the row toggles its raw
// payload; the color is the event class, so message, tool, approval, usage,
// status and error rows are distinguishable at a glance.
func (a *App) trajectoryRow(c *ui.Context, t *design.Tokens, entry trajectoryEntry) {
	open := a.uiState().expandedRaw[entry.Seq]
	color := trajectoryColor(t, entry)
	button := ui.Button(c, "").Padding(design.Space1).Radius(design.RadiusSM).
		Transition(design.ColorTransition())
	button.Children(func() {
		ui.Row(c).Gap(design.Space2).AlignItems(ui.Start).Children(func() {
			ui.Box(c).Size(6, 6).Radius(3).Background(color).Margin(0, 5, 0, 0)
			ui.Text(c, entry.At.Local().Format("15:04:05")).FontSize(11).
				FixedLineHeight(18).TextColor(t.LabelTertiary).Width(56).NoWrap()
			ui.Text(c, a.trajectoryLabelText(entry.Label)).FontSize(12).FixedLineHeight(18).FontWeight(500).
				TextColor(color).NoWrap()
			if entry.Count > 1 {
				ui.Text(c, fmt.Sprintf("×%d", entry.Count)).FontSize(11).
					FixedLineHeight(18).TextColor(t.LabelTertiary).NoWrap()
			}
			if entry.Detail != "" {
				ui.Text(c, entry.Detail).FontSize(12).FixedLineHeight(18).
					TextColor(t.LabelSecondary).Grow(1).MaxLines(2).Ellipsis("…")
			}
		})
		if open {
			ui.Text(c, entry.Raw).FontSize(11).FixedLineHeight(16).
				TextColor(t.LabelTertiary).MaxLines(8).Ellipsis("…").
				Margin(design.Space4, 0, 0, 0)
		}
	})
	if button.Clicked() {
		uiState := a.uiState()
		uiState.expandedRaw[entry.Seq] = !open
	}
}

// trajectoryEntry is one rendered timeline row: an event, or a run of token
// events for one message aggregated into a single row.
type trajectoryEntry struct {
	Seq     int64
	At      time.Time
	Type    string
	Label   string
	Detail  string
	Raw     string
	Count   int
	IsError bool

	// msgID is set on aggregated token/reasoning rows.
	msgID string
}

// buildTrajectory folds stored events into timeline rows. Consecutive token (or
// reasoning) events for the same message collapse into one row that keeps the
// full text and the count; every other event gets its own row. The result is
// ordered oldest first, matching the store's sequence order.
func buildTrajectory(events []store.Event) []trajectoryEntry {
	entries := make([]trajectoryEntry, 0, len(events))
	for _, event := range events {
		switch event.Type {
		case session.EventToken, session.EventReasoning:
			payload := decodePayload(event.Payload)
			msgID := payloadString(payload, "msg_id")
			text := payloadString(payload, "text")
			if n := len(entries); n > 0 {
				last := &entries[n-1]
				if last.Type == event.Type && (msgID == "" || last.msgID == msgID) {
					last.Detail += text
					last.Count++
					last.Seq = event.Seq
					last.At = event.TS
					continue
				}
			}
			label := "assistant"
			if event.Type == session.EventReasoning {
				label = "reasoning"
			}
			entries = append(entries, trajectoryEntry{
				Seq:    event.Seq,
				At:     event.TS,
				Type:   event.Type,
				Label:  label,
				Detail: text,
				Count:  1,
				msgID:  msgID,
			})
		default:
			label, detail, isError := trajectoryLabel(event)
			entries = append(entries, trajectoryEntry{
				Seq:     event.Seq,
				At:      event.TS,
				Type:    event.Type,
				Label:   label,
				Detail:  detail,
				Raw:     compactJSON(event.Payload),
				Count:   1,
				IsError: isError,
			})
		}
	}
	// Aggregated rows did not keep a single event payload; synthesize one that
	// describes the whole run for the raw detail.
	for i := range entries {
		entry := &entries[i]
		if entry.Type != session.EventToken && entry.Type != session.EventReasoning {
			continue
		}
		raw, err := json.Marshal(map[string]any{
			"msg_id": entry.msgID,
			"text":   entry.Detail,
			"events": entry.Count,
		})
		if err == nil {
			entry.Raw = string(raw)
		}
	}
	return entries
}

// trajectoryLabel summarizes one non-token event's payload. It returns the row
// label, a compact detail, and whether the row should use the error color.
func trajectoryLabel(event store.Event) (label, detail string, isError bool) {
	payload := decodePayload(event.Payload)
	switch event.Type {
	case session.EventMessage:
		role := payloadString(payload, "role")
		label = "message"
		if role != "" {
			label += " · " + role
		}
		detail = strings.TrimSpace(payloadString(payload, "content"))
	case session.EventToolStart:
		name := payloadString(payload, "name")
		label = "tool_start · " + name
		if args := payloadJSON(payload, "args"); args != "" {
			detail = compactJSON(json.RawMessage(args))
		}
	case session.EventToolEnd:
		callID := payloadString(payload, "call_id")
		label = "tool_end · " + callID
		isError = payloadBool(payload, "is_error")
		detail = strings.TrimSpace(payloadString(payload, "result"))
		if isError && detail != "" {
			detail = "error: " + detail
		}
	case session.EventApprovalRequest:
		name := payloadString(payload, "name")
		label = "approval · " + name
		if args := payloadJSON(payload, "args"); args != "" {
			detail = compactJSON(json.RawMessage(args))
		}
	case session.EventUsage:
		label = "usage"
		detail = fmt.Sprintf("turns=%d steps=%d in=%d out=%d cache_hit=%d ctx=%.1f%%",
			payloadInt(payload, "turns"), payloadInt(payload, "steps"),
			payloadInt(payload, "in"), payloadInt(payload, "out"),
			payloadInt(payload, "cache_hit"), payloadFloat(payload, "context_pct"))
	case session.EventStatus:
		phase := payloadString(payload, "phase")
		label = "status · " + phase
		isError = phase == string(state.PhaseFailed)
	case session.EventError:
		label = "error"
		isError = true
		detail = strings.TrimSpace(payloadString(payload, "text"))
	default:
		label = event.Type
		detail = compactJSON(event.Payload)
	}
	return label, truncateText(detail, 400), isError
}

// trajectoryColor maps an entry class onto the theme: accent for conversation,
// success/warning for tool lifecycle, muted for counters, danger for failures.
func trajectoryColor(t *design.Tokens, entry trajectoryEntry) ui.Color {
	if entry.IsError || entry.Type == session.EventError {
		return t.Danger
	}
	switch entry.Type {
	case session.EventMessage:
		return t.LabelPrimary
	case session.EventToken, session.EventReasoning:
		return t.Accent
	case session.EventToolStart:
		return t.WarningText
	case session.EventToolEnd:
		return t.Success
	case session.EventApprovalRequest:
		return t.WarningText
	case session.EventStatus:
		return t.LabelSecondary
	default:
		return t.LabelTertiary
	}
}

// decodePayload unmarshals an event payload into a generic object. Malformed
// payloads yield nil, which every accessor treats as absent.
func decodePayload(raw json.RawMessage) map[string]any {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	return payload
}

func payloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, _ := payload[key].(string)
	return value
}

// payloadJSON re-encodes a nested payload value (for example tool args) as
// compact JSON. It returns "" when the key is absent or null.
func payloadJSON(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}

func payloadBool(payload map[string]any, key string) bool {
	if payload == nil {
		return false
	}
	value, _ := payload[key].(bool)
	return value
}

func payloadInt(payload map[string]any, key string) int {
	if payload == nil {
		return 0
	}
	value, _ := payload[key].(float64)
	return int(value)
}

func payloadFloat(payload map[string]any, key string) float64 {
	if payload == nil {
		return 0
	}
	value, _ := payload[key].(float64)
	return value
}

// compactJSON compacts a payload for the one-line detail and keeps the same
// text when the payload is not JSON.
func compactJSON(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

// truncateText caps long detail text so a huge tool result cannot build an
// enormous view row. The raw payload is still complete.
func truncateText(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
