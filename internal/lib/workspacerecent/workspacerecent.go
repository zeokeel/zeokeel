// Package workspacerecent orders the workspaces a human last used, for the two
// places that ask "which folder were we in?":
//
//   - the desktop startup gate (uiroot) preselects the most recent workspace;
//   - the sidebar's switch dialog lists the recent workspaces.
//
// Both read the same registry and the same chat read model, so the ordering
// lives here instead of being derived twice (and drifting).
//
// Recency is the workspace's newest visible conversation (ChatSessionInfo.
// UpdatedAt) with the workspace registry's CreatedAt as the fallback: a freshly
// added folder with no conversations still counts as recent, and a folder whose
// conversations were all archived falls back to its own creation time (archived
// conversations never lift a folder).
package workspacerecent

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Recent is one workspace and the time it was last used.
type Recent struct {
	// Workspace is the registry entry.
	Workspace contracts.Workspace
	// Activity is the workspace's newest conversation time, or the workspace's
	// CreatedAt when it has none.
	Activity time.Time
	// Current marks the workspace the product has open right now.
	Current bool
}

// Build returns the workspaces ordered by recency (newest first), marking the
// current one. limit caps the result (<= 0 keeps every workspace). Ties keep the
// registry order, so the list is stable across frames.
func Build(workspaces []contracts.Workspace, sessions []contracts.ChatSessionInfo, currentID string, limit int) []Recent {
	currentID = strings.TrimSpace(currentID)
	known := make(map[string]bool, len(workspaces))
	newest := make(map[string]time.Time, len(workspaces))
	for _, workspace := range workspaces {
		id := strings.TrimSpace(workspace.ID)
		if id == "" {
			continue
		}
		known[id] = true
	}
	for _, info := range sessions {
		if info.Archived {
			continue
		}
		id := strings.TrimSpace(info.WorkspaceID)
		if !known[id] {
			continue
		}
		if info.UpdatedAt.After(newest[id]) {
			newest[id] = info.UpdatedAt
		}
	}
	recent := make([]Recent, 0, len(workspaces))
	for _, workspace := range workspaces {
		if strings.TrimSpace(workspace.ID) == "" {
			continue
		}
		activity := newest[workspace.ID]
		if activity.IsZero() {
			activity = workspace.CreatedAt
		}
		recent = append(recent, Recent{
			Workspace: workspace,
			Activity:  activity,
			Current:   workspace.ID == currentID,
		})
	}
	sort.SliceStable(recent, func(i, j int) bool {
		return recent[i].Activity.After(recent[j].Activity)
	})
	if limit > 0 && len(recent) > limit {
		recent = recent[:limit]
	}
	if len(recent) == 0 {
		return nil
	}
	return recent
}

// AgeLabel renders how long ago a workspace was used, in the caller's language:
// "今天" / "昨天" / "3 天前" ("Today" / "Yesterday" / "3 days ago"). A zero or
// future time renders nothing, so a registry entry without times stays quiet.
// The caller substitutes its own word for the current workspace ("上次使用",
// "当前") instead of calling this.
func AgeLabel(tr func(zh, en string) string, activity, now time.Time) string {
	if activity.IsZero() || now.IsZero() {
		return ""
	}
	days := calendarDaysBetween(activity, now)
	switch {
	case days <= 0:
		return tr("今天", "Today")
	case days == 1:
		return tr("昨天", "Yesterday")
	default:
		return fmt.Sprintf(tr("%d 天前", "%d days ago"), days)
	}
}

// calendarDaysBetween counts the calendar days from earlier to later in the
// local zone, so 23:59 → 00:01 counts as one day rather than zero.
func calendarDaysBetween(earlier, later time.Time) int {
	start := time.Date(earlier.Year(), earlier.Month(), earlier.Day(), 0, 0, 0, 0, earlier.Location())
	end := time.Date(later.Year(), later.Month(), later.Day(), 0, 0, 0, 0, later.Location())
	return int(end.Sub(start).Hours() / 24)
}
