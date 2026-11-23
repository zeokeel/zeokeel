package workspacerecent

import (
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func tr(zh, en string) string { return zh }

func TestBuildOrdersByNewestConversationAndFallsBackToCreatedAt(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	workspaces := []contracts.Workspace{
		{ID: "old", Name: "旧项目", CreatedAt: base.Add(-10 * time.Hour)},
		{ID: "active", Name: "活跃项目", CreatedAt: base.Add(-9 * time.Hour)},
		{ID: "fresh", Name: "新建项目", CreatedAt: base},
	}
	sessions := []contracts.ChatSessionInfo{
		{ID: "s1", WorkspaceID: "old", UpdatedAt: base.Add(-time.Hour)},
		{ID: "s2", WorkspaceID: "active", UpdatedAt: base.Add(-2 * time.Hour)},
		// An archived conversation must not lift its workspace.
		{ID: "s3", WorkspaceID: "active", Archived: true, UpdatedAt: base.Add(time.Hour)},
		{ID: "s4", WorkspaceID: "unknown", UpdatedAt: base.Add(2 * time.Hour)},
	}
	got := Build(workspaces, sessions, "active", 0)
	if len(got) != 3 {
		t.Fatalf("Build returned %d entries, want 3", len(got))
	}
	order := []string{got[0].Workspace.ID, got[1].Workspace.ID, got[2].Workspace.ID}
	if order[0] != "fresh" || order[1] != "old" || order[2] != "active" {
		t.Fatalf("order = %v, want [fresh old active] (CreatedAt of the empty workspace, then the newest conversation, then the rest)", order)
	}
	if !got[0].Activity.Equal(base) {
		t.Fatalf("fresh activity = %v, want its CreatedAt", got[0].Activity)
	}
	if !got[1].Activity.Equal(base.Add(-time.Hour)) {
		t.Fatalf("old activity = %v, want its newest conversation", got[1].Activity)
	}
	if got[0].Current {
		t.Fatal("old workspace marked current")
	}
	if !got[2].Current {
		t.Fatal("the open workspace is not marked current")
	}
}

func TestBuildAppliesTheLimitAfterOrdering(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	workspaces := []contracts.Workspace{
		{ID: "a", CreatedAt: base.Add(-3 * time.Hour)},
		{ID: "b", CreatedAt: base.Add(-2 * time.Hour)},
		{ID: "c", CreatedAt: base.Add(-time.Hour)},
	}
	got := Build(workspaces, nil, "", 2)
	if len(got) != 2 || got[0].Workspace.ID != "c" || got[1].Workspace.ID != "b" {
		t.Fatalf("Build(limit 2) = %+v, want the two newest", got)
	}
}

func TestBuildSkipsEmptyIDsAndToleratesNoData(t *testing.T) {
	if got := Build(nil, nil, "", 3); got != nil {
		t.Fatalf("Build(nil) = %v, want nil", got)
	}
	got := Build([]contracts.Workspace{{ID: "", Name: "no id"}}, nil, "", 3)
	if len(got) != 0 {
		t.Fatalf("Build kept a workspace without an id: %+v", got)
	}
}

func TestAgeLabel(t *testing.T) {
	now := time.Date(2026, time.November, 23, 10, 0, 0, 0, time.Local)
	cases := []struct {
		name     string
		activity time.Time
		want     string
	}{
		{"same day", time.Date(2026, time.November, 23, 1, 0, 0, 0, time.Local), "今天"},
		{"yesterday late", time.Date(2026, time.November, 22, 23, 50, 0, 0, time.Local), "昨天"},
		{"three days", time.Date(2026, time.November, 20, 9, 0, 0, 0, time.Local), "3 天前"},
		{"future", time.Date(2026, time.November, 24, 9, 0, 0, 0, time.Local), "今天"},
		{"zero", time.Time{}, ""},
	}
	for _, test := range cases {
		if got := AgeLabel(tr, test.activity, now); got != test.want {
			t.Errorf("AgeLabel(%s) = %q, want %q", test.name, got, test.want)
		}
	}
	english := func(zh, en string) string { return en }
	if got := AgeLabel(english, time.Date(2026, time.November, 20, 9, 0, 0, 0, time.Local), now); got != "3 days ago" {
		t.Errorf("English AgeLabel = %q", got)
	}
}
