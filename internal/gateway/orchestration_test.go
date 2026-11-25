package gateway

// Unit tests for the orchestration payload mapping and the dispatch presence.
// The end-to-end behaviour lives in the module tests (`inbox`, `triage`,
// `tasklease`, `approvals`); what this file protects is the seam: the methods
// are really dispatched, and the wire shape does not silently drop fields.

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestOrchestrationMethodsAreDispatched(t *testing.T) {
	want := []string{
		"inbox.snapshot", "inbox.dispatch", "inbox.triage",
		"tasks.lease", "tasks.claim", "tasks.heartbeat", "tasks.submit", "tasks.review",
		"approval.token", "approval.action",
		"notify.route.snapshot", "notify.route.upsert",
		"webhook.sources", "webhook.source.set", "webhook.source.remove",
		"lead.run",
		"credentials.list", "credentials.set", "credentials.delete",
		"heartbeat.status", "heartbeat.tick", "heartbeat.configure", "browser.status",
		"team.notes", "team.note.record", "team.note.review",
		"wechat.login.start", "wechat.login.state",
	}
	methods := SupportedMethods()
	for _, name := range want {
		if !slices.Contains(methods, name) {
			t.Errorf("dispatch table is missing %q", name)
		}
	}
}

func TestInboxItemDTOMapping(t *testing.T) {
	received := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	item := contracts.InboxItem{
		ID: "inbox-1", Source: "wechat", Channel: "dm-1", Sender: "owner",
		DedupKey: "k", Priority: 3, Title: "查退款率", Body: "上周",
		Ref: "https://x", ReceivedAt: received, LastSeenAt: received.Add(time.Minute),
		Status: contracts.InboxStatusNew, Count: 2, TaskIDs: []string{"task-1"},
	}
	dto := inboxItemFromContract(item)
	if dto.ID != item.ID || dto.Source != item.Source || dto.Count != 2 || dto.Priority != 3 {
		t.Fatalf("dto = %+v, want the item's fields", dto)
	}
	if dto.ReceivedAt != "2026-11-25T09:00:00Z" {
		t.Fatalf("receivedAt = %q, want RFC3339 UTC", dto.ReceivedAt)
	}
	if len(dto.TaskIDs) != 1 || dto.TaskIDs[0] != "task-1" {
		t.Fatalf("taskIds = %v, want [task-1]", dto.TaskIDs)
	}
	// Defensive copy: mutating the item must not reach the DTO.
	item.TaskIDs[0] = "mutated"
	if dto.TaskIDs[0] != "task-1" {
		t.Fatal("dto shares the item's TaskIDs slice")
	}
}

func TestInboxItemDTOZeroTimes(t *testing.T) {
	dto := inboxItemFromContract(contracts.InboxItem{ID: "x"})
	if dto.ReceivedAt != "" || dto.LastSeenAt != "" || dto.ExpiresAt != "" {
		t.Fatalf("zero times = %q/%q/%q, want empty strings", dto.ReceivedAt, dto.LastSeenAt, dto.ExpiresAt)
	}
}

func TestTaskLeaseDTOMapping(t *testing.T) {
	until := time.Date(2026, 11, 25, 9, 10, 0, 0, time.UTC)
	lease := contracts.TaskLease{
		TaskID: "task-1", Owner: "bot-1", Until: until,
		Heartbeat: until.Add(-time.Minute), State: contracts.LeaseStateReview,
		Evidence:   []contracts.Evidence{{Kind: "link", Ref: "https://x", Note: "src"}},
		ReviewNote: "",
	}
	dto := taskLeaseFromContract(lease)
	if dto.TaskID != "task-1" || dto.Owner != "bot-1" || dto.State != "review" {
		t.Fatalf("dto = %+v", dto)
	}
	if dto.Until != "2026-11-25T09:10:00Z" {
		t.Fatalf("until = %q", dto.Until)
	}
	if len(dto.Evidence) != 1 || dto.Evidence[0].Kind != "link" {
		t.Fatalf("evidence = %+v", dto.Evidence)
	}
}

func TestEvidenceRoundTrip(t *testing.T) {
	in := []evidenceDTO{{Kind: "file", Ref: "a.xlsx", Note: "结果"}}
	out := evidenceFromDTO(in)
	if len(out) != 1 || out[0].Kind != "file" || out[0].Ref != "a.xlsx" {
		t.Fatalf("round trip = %+v", out)
	}
	if evidenceFromDTO(nil) != nil {
		t.Fatal("nil evidence should stay nil (Submit rejects it with a typed error)")
	}
}

func TestOrchestrationErrorMapsTypedErrorsToClientErrors(t *testing.T) {
	for _, err := range []error{
		contracts.ErrLeaseHeld, contracts.ErrLeaseNotHeld, contracts.ErrLeaseEvidenceRequired,
		contracts.ErrInboxUnknownItem, contracts.ErrApprovalTokenExpired,
	} {
		mapped := orchestrationError(err)
		if mapped == nil || mapped.Code != codeInvalidRequest {
			t.Errorf("orchestrationError(%v) = %+v, want codeInvalidRequest", err, mapped)
		}
		if !strings.Contains(mapped.Message, "lease") && !strings.Contains(mapped.Message, "inbox") &&
			!strings.Contains(mapped.Message, "approval") {
			t.Errorf("mapped message = %q, want the original typed text", mapped.Message)
		}
	}
}

func TestNotifyRouteDTOMapping(t *testing.T) {
	route := contracts.NotifyRoute{
		Category: contracts.NotifyCategoryApproval, Primary: contracts.NotifyChannelWeChat,
		Fallback: []string{contracts.NotifyChannelDingTalk}, QuietHours: "22:30-08:00",
		Escalation: 15 * time.Minute, Enabled: true,
	}
	dto := notifyRouteFromContract(route)
	if dto.Category != route.Category || dto.Primary != route.Primary || !dto.Enabled {
		t.Fatalf("dto = %+v", dto)
	}
	if dto.EscalationMS != (15 * time.Minute).Milliseconds() {
		t.Fatalf("escalationMs = %d, want %d", dto.EscalationMS, (15 * time.Minute).Milliseconds())
	}
	if len(dto.Fallback) != 1 || dto.Fallback[0] != contracts.NotifyChannelDingTalk {
		t.Fatalf("fallback = %v", dto.Fallback)
	}
	route.Fallback[0] = "mutated"
	if dto.Fallback[0] != contracts.NotifyChannelDingTalk {
		t.Fatal("dto shares the route's fallback slice")
	}
}
