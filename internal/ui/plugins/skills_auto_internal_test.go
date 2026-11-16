package plugins

import (
	"testing"
	"time"
)

func TestSkillsAutoRefreshDue(t *testing.T) {
	state := &skillsState{}
	if state.autoRefreshDue(time.Now()) {
		t.Fatal("fresh state must not auto-refresh")
	}
	now := time.Now()
	state.lastScan = now.Add(-skillAutoRefreshInterval - time.Second)
	if !state.autoRefreshDue(now) {
		t.Fatal("stale state should auto-refresh")
	}
	state.lastScan = now
	if state.autoRefreshDue(now) {
		t.Fatal("recent scan must not auto-refresh")
	}
}
