package session

// Unit tests for the receipt merge: live transcripts interleave receipts by time
// (above the streaming answer), while restored messages without timestamps keep
// the receipts appended.

import (
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func noticeAt(id string, at time.Time) contracts.ChatMessage {
	return contracts.ChatMessage{ID: "child-notice-" + id, Role: contracts.ChatRoleNotice, CreatedAt: at}
}

func messageAt(id string, role string, at time.Time) contracts.ChatMessage {
	return contracts.ChatMessage{ID: id, Role: role, CreatedAt: at}
}

func TestMergeChildNoticesChronological(t *testing.T) {
	base := time.Now()
	messages := []contracts.ChatMessage{
		messageAt("u1", contracts.ChatRoleUser, base),
		messageAt("a1", contracts.ChatRoleAssistant, base.Add(2*time.Second)),
		messageAt("u2", contracts.ChatRoleUser, base.Add(10*time.Second)),
	}
	notices := []contracts.ChatMessage{
		noticeAt("n1", base.Add(5*time.Second)),
		noticeAt("n2", base.Add(12*time.Second)),
	}
	merged := mergeChildNotices(messages, notices, false)
	if len(merged) != 5 {
		t.Fatalf("merged length = %d, want 5", len(merged))
	}
	if merged[2].ID != "child-notice-n1" {
		t.Fatalf("notice n1 not placed between turns: %v", merged)
	}
	if merged[4].ID != "child-notice-n2" {
		t.Fatalf("notice n2 not placed at the end: %v", merged)
	}
}

func TestMergeChildNoticesAboveStreamingAnswer(t *testing.T) {
	base := time.Now()
	messages := []contracts.ChatMessage{
		messageAt("u1", contracts.ChatRoleUser, base),
		messageAt("a1", contracts.ChatRoleAssistant, base.Add(time.Second)),
		messageAt("a-live", contracts.ChatRoleAssistant, base.Add(2*time.Second)),
	}
	messages[2].Streaming = true
	notices := []contracts.ChatMessage{noticeAt("n1", base.Add(3*time.Second))}
	merged := mergeChildNotices(messages, notices, true)
	if len(merged) != 4 {
		t.Fatalf("merged length = %d, want 4", len(merged))
	}
	if merged[2].ID != "child-notice-n1" {
		t.Fatalf("receipt not placed above the streaming answer: %v", merged)
	}
	if !merged[3].Streaming || merged[3].ID != "a-live" {
		t.Fatal("streaming answer is no longer the trailing row")
	}
}

func TestMergeChildNoticesWithoutTimestamps(t *testing.T) {
	// Restored durable messages carry no CreatedAt: receipts are appended after
	// them (the documented reload fallback).
	messages := []contracts.ChatMessage{
		{ID: "u1", Role: contracts.ChatRoleUser},
		{ID: "a1", Role: contracts.ChatRoleAssistant},
	}
	notices := []contracts.ChatMessage{{ID: "child-notice-n1", Role: contracts.ChatRoleNotice}}
	merged := mergeChildNotices(messages, notices, false)
	if len(merged) != 3 || merged[2].ID != "child-notice-n1" {
		t.Fatalf("zero-timestamp merge = %v, want receipt appended last", merged)
	}
	if mergeChildNotices(nil, notices, false) == nil {
		t.Fatal("empty transcript merge returned nil")
	}
}

func TestCapFailedNoticeMarkers(t *testing.T) {
	notices := []contracts.ChatMessage{
		{ID: "a", Role: contracts.ChatRoleNotice, Error: "failed"},
		{ID: "b", Role: contracts.ChatRoleNotice, Error: "failed"},
		{ID: "c", Role: contracts.ChatRoleNotice, Error: "failed"},
		{ID: "d", Role: contracts.ChatRoleNotice, Error: "failed"},
		{ID: "e", Role: contracts.ChatRoleNotice},
	}
	capFailedNoticeMarkers(notices)
	danger := 0
	for _, notice := range notices {
		if notice.Error == "failed" {
			danger++
		}
	}
	if danger != 2 {
		t.Fatalf("danger receipts = %d, want 2 (red budget)", danger)
	}
	if notices[0].Error != "" || notices[1].Error != "" {
		t.Fatal("the oldest failed receipts must be muted")
	}
	if notices[2].Error != "failed" || notices[3].Error != "failed" {
		t.Fatal("the two newest failed receipts must keep danger styling")
	}
}
