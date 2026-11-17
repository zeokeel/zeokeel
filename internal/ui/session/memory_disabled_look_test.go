package session_test

// 2026-11 UI review P2: the Memory tab's "添加记忆" action, disabled on an empty
// draft, must use the same neutral disabled-primary look as the composer's
// disabled send — not mygo's 50% accent wash (light-blue fill, white text) that
// read as a clickable brand button next to the gray disabled send. Both go
// through t.DisabledPrimary (BgStrong fill + LabelTertiary text) + the native
// Disabled state, so their painted fills must match.

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestMemoryAddDisabledMatchesComposerSend(t *testing.T) {
	store := newFakeMemoryStore()
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "记忆禁用态"}, true)
	h := newHarnessWithMemory(t, chat, store)
	tester := h.tester()
	tester.Frame()

	// The composer send is disabled on the empty draft: capture its neutral fill
	// before leaving the conversation pane.
	sendInfo, ok := tester.AccessByRole(ui.RoleButton, "发送")
	if !ok || !sendInfo.Disabled {
		t.Fatalf("the composer send is not disabled on an empty draft; %+v (texts=%v)", sendInfo, tester.Texts())
	}
	sendFill := sendButtonFill(t, tester, "发送")
	if spread := channelSpread(sendFill); spread > 12 {
		t.Fatalf("the disabled composer send is not neutral (channel spread %d)", spread)
	}

	// Open the Memory tab: the add action is disabled on the empty draft.
	h.click(tester, "记忆")
	tester.Frame()
	addInfo, ok := tester.AccessByRole(ui.RoleButton, "添加记忆")
	if !ok || !addInfo.Disabled {
		t.Fatalf("the memory add action is not disabled on an empty draft; %+v (texts=%v)", addInfo, tester.Texts())
	}
	addFill := sendButtonFill(t, tester, "添加记忆")

	// One neutral disabled look: the two disabled primaries paint the same fill
	// (BgStrong), not a light-blue wash on one and gray on the other.
	if addFill != sendFill {
		t.Fatalf("disabled fills differ: memory add=%+v composer send=%+v; want one neutral disabled-primary look", addFill, sendFill)
	}
	if spread := channelSpread(addFill); spread > 12 {
		t.Fatalf("the disabled memory add still paints an accent wash (channel spread %d); want the neutral fill", spread)
	}

	// With a draft the add action returns to the accent primary (enabled), so the
	// neutral look is the disabled state only.
	inputInfo, ok := tester.AccessByRole(ui.RoleTextField, "新建记忆")
	if !ok {
		t.Fatalf("memory input missing; texts = %v", tester.Texts())
	}
	tester.ClickAt(inputInfo.Bounds.X+inputInfo.Bounds.W/2, inputInfo.Bounds.Y+inputInfo.Bounds.H/2)
	tester.Frame()
	tester.Type("记住这条内容")
	tester.Frame()
	if info, ok := tester.AccessByRole(ui.RoleButton, "添加记忆"); !ok || info.Disabled {
		t.Fatalf("the memory add action stayed disabled with a draft; %+v", info)
	}
	if spread := channelSpread(sendButtonFill(t, tester, "添加记忆")); spread <= 40 {
		t.Fatalf("the enabled memory add lost its accent fill (channel spread %d); want the brand primary", spread)
	}
}
