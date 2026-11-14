package agent

// Tool-result spilling for the persisted conversation history.
//
// Results over 32 KiB are written to <workspace>/.zeokeel/tool-results/ (or
// <dataDir>/.zeokeel/tool-results for headless/ACP sessions without a registered
// workspace) and the model only sees Rig's stable summary + path, so an
// oversized tool output is not resent in every following model round.
//
// The product keeps exactly one thing here: resolving the directory. Writing,
// naming, summarizing and prune-to-MaxFiles are Rig's rig.SpillPolicy
// (rig/context.go, applied in rig/agent/agent.go before the tool message is
// recorded); the policy is installed on the run's AgentSpec in agent.go. The
// tombstone guard stays outermost on the History wrapper (see guardedHistory).

import (
	"context"
	"path/filepath"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
)

const (
	// spillThresholdBytes is the tool-result size above which the model sees
	// only Rig's summary + path (32 KiB; Rig's own default is 16 KiB).
	spillThresholdBytes = 32 << 10
	// spillKeepFiles is the number of spill files kept per directory (Rig keeps
	// the newest by mtime).
	spillKeepFiles = 100
	// spillDirName is the workspace-relative spill directory (slash separated).
	spillDirName = ".zeokeel/tool-results"
)

// spillPolicy builds the run's tool-result spill policy. Dir is absolute:
// Rig resolves a relative Dir against the host process working directory, not
// against the workspace.
func (s *service) spillPolicy(ctx context.Context) *rig.SpillPolicy {
	return &rig.SpillPolicy{
		Dir:            s.spillDir(ctx),
		ThresholdBytes: spillThresholdBytes,
		MaxFiles:       spillKeepFiles,
	}
}

// spillDir resolves the current workspace's spill directory ("" when no
// workspace is open or the workspace backend is unavailable).
func (s *service) spillDir(ctx context.Context) string {
	if s.workspace != nil {
		workspace, ok, err := s.workspace.Current(ctx)
		if err != nil {
			s.logger.Printf("[agent] spill dir: current workspace: %v", err)
		} else if ok && strings.TrimSpace(workspace.Root) != "" {
			return filepath.Join(workspace.Root, filepath.FromSlash(spillDirName))
		}
	}
	// Headless/ACP sessions have no registered workspace; spill under the
	// product data directory instead of resending oversized tool results in
	// every following model round (verification finding ZK-13).
	if dataDir := strings.TrimSpace(s.dataDir); dataDir != "" {
		return filepath.Join(dataDir, filepath.FromSlash(spillDirName))
	}
	return ""
}
