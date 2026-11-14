package contracts

// Tests for the notification contract helpers: the system-notification setting
// normalization and the two background-run helpers (level choice, nil notifier
// safety, and the fallback from the OS half to a plain toast).

import (
	"testing"
)

// recordingNotifier is a contracts.Notifier that records what it delivers; with
// osSystem set it also implements contracts.SystemNotifier.
type recordingNotifier struct {
	system bool
	levels []string
	texts  []string
}

func (n *recordingNotifier) SetHandler(func(Notification)) {}
func (n *recordingNotifier) Notify(level, text string) Notification {
	n.levels = append(n.levels, level)
	n.texts = append(n.texts, text)
	return Notification{ID: "toast", Level: level, Text: text}
}
func (n *recordingNotifier) Dismiss(string) {}

// systemRecordingNotifier is recordingNotifier plus the OS half.
type systemRecordingNotifier struct {
	recordingNotifier
	supported bool
}

func (n *systemRecordingNotifier) NotifySystem(level, text string) Notification {
	n.levels = append(n.levels, level)
	n.texts = append(n.texts, text)
	return Notification{ID: "system", Level: level, Text: text}
}
func (n *systemRecordingNotifier) SystemNotificationsSupported() bool { return n.supported }

func TestSystemNotificationsEnabled(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"", true},
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"yes", true},
		{" banana ", true},
		{"false", false},
		{" FALSE ", false},
		{"0", false},
		{"no", false},
	}
	for _, test := range tests {
		if got := SystemNotificationsEnabled(test.raw); got != test.want {
			t.Fatalf("SystemNotificationsEnabled(%q) = %v, want %v", test.raw, got, test.want)
		}
	}
}

func TestNotifyRunFinishedChoosesLevel(t *testing.T) {
	notifier := &recordingNotifier{}
	done := NotifyRunFinished(notifier, "会话已完成", false)
	if done.Level != NotifySuccess {
		t.Fatalf("completed run level = %q, want success", done.Level)
	}
	failed := NotifyRunFinished(notifier, "会话失败", true)
	if failed.Level != NotifyError {
		t.Fatalf("failed run level = %q, want error", failed.Level)
	}
	if len(notifier.texts) != 2 || notifier.texts[0] != "会话已完成" {
		t.Fatalf("delivered texts = %v, want both copies", notifier.texts)
	}
}

func TestNotifyRunFinishedDegradesWithoutOSHalf(t *testing.T) {
	notifier := &recordingNotifier{}
	notification := NotifyRunFinished(notifier, "完成", false)
	if notification.ID != "toast" {
		t.Fatalf("plain notifier id = %q, want the toast fallback", notification.ID)
	}
	system := &systemRecordingNotifier{}
	system.supported = true
	notification = NotifyRunFinished(system, "完成", false)
	if notification.ID != "system" {
		t.Fatalf("system notifier id = %q, want the OS half", notification.ID)
	}
}

func TestNotifyApprovalWaitingUsesWarning(t *testing.T) {
	notifier := &recordingNotifier{}
	if got := NotifyApprovalWaiting(notifier, "等待审批"); got.Level != NotifyWarning {
		t.Fatalf("approval waiting level = %q, want warning", got.Level)
	}
}

func TestNotifyHelpersNilNotifierIsNoop(t *testing.T) {
	if notification := NotifyRunFinished(nil, "完成", false); notification.ID != "" || notification.Text != "" {
		t.Fatalf("NotifyRunFinished(nil) = %+v, want the zero notification", notification)
	}
	if notification := NotifyApprovalWaiting(nil, "等待审批"); notification.ID != "" || notification.Text != "" {
		t.Fatalf("NotifyApprovalWaiting(nil) = %+v, want the zero notification", notification)
	}
}
