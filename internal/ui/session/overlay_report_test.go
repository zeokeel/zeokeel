package session_test

// 2026-11 UI review #13: a module-owned popover reports its open/close transition
// to the optional host toast anchor (contracts.ToastAnchorService.ReportOverlay)
// so the toast stack yields while the overlay is open. Reports pair one open with
// one close and fire once per transition; a host without the service is a no-op.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeToastAnchor records the overlay open/close reports a module sends.
type fakeToastAnchor struct {
	reports []bool
	dockTop float32
}

func (f *fakeToastAnchor) Anchor() contracts.ToastAnchor { return contracts.ToastAnchor{} }
func (f *fakeToastAnchor) ReportOverlay(open bool)       { f.reports = append(f.reports, open) }
func (f *fakeToastAnchor) ReportDockTop(top float32)     { f.dockTop = top }

func TestPopoverReportsOverlayOpenToToastAnchor(t *testing.T) {
	anchor := &fakeToastAnchor{}
	team := &fakeTeam{members: []contracts.TeamMember{
		{ID: "m1", Name: "审查员", RunState: contracts.TeamRunRunning, SessionID: "sess-1"},
	}}
	h := newAgentSurfaceHarness(t, headerChat("上报"), agentServices{
		team: team, board: &fakeAgentBoard{},
	})
	provide[contracts.ToastAnchorService](t, h.ctx, contracts.KeyToastAnchor, anchor)
	tester := h.tester()

	// Idle: every overlay is closed, and a closed→closed frame reports nothing.
	tester.Frame()
	if len(anchor.reports) != 0 {
		t.Fatalf("an idle frame reported overlay transitions: %v", anchor.reports)
	}

	// Opening the team overlay reports true.
	h.click(tester, "智能体团队")
	if len(anchor.reports) == 0 || !anchor.reports[len(anchor.reports)-1] {
		t.Fatalf("opening the team overlay did not report true: %v", anchor.reports)
	}
	openCount := len(anchor.reports)

	// An idle open frame does not re-report (idempotent, the host count stays paired).
	tester.Frame()
	if len(anchor.reports) != openCount {
		t.Fatalf("an idle open frame re-reported: %v", anchor.reports)
	}

	// Closing reports false, pairing the open.
	tester.Key(0, ui.KeyEscape)
	tester.Frame()
	if len(anchor.reports) != openCount+1 || anchor.reports[len(anchor.reports)-1] {
		t.Fatalf("closing the team overlay did not report a paired false: %v", anchor.reports)
	}
}

// TestPopoverReportNoopWithoutService covers the minimal host: no anchor service
// means opening a popover builds normally and reports nothing (no panic).
func TestPopoverReportNoopWithoutService(t *testing.T) {
	team := &fakeTeam{members: []contracts.TeamMember{
		{ID: "m1", Name: "审查员", RunState: contracts.TeamRunRunning, SessionID: "sess-1"},
	}}
	h := newAgentSurfaceHarness(t, headerChat("无锚"), agentServices{
		team: team, board: &fakeAgentBoard{},
	})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "智能体团队")
	h.assertTexts(tester, "智能体团队", "审查员")
}
