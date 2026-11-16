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
// against the workspace. workspaceRoot is the run's captured tool root (the
// SESSION binding, not whichever workspace is current — BE-11, 2026-11 audit:
// spilling outside the run's sandbox roots made the spilled file unreadable by
// the model's read_file, WithRoots contract).
func (s *service) spillPolicy(ctx context.Context, workspaceRoot string) *rig.SpillPolicy {
	return &rig.SpillPolicy{
		Dir:            s.spillDir(ctx, workspaceRoot),
		ThresholdBytes: spillThresholdBytes,
		MaxFiles:       spillKeepFiles,
	}
}

// spillDir resolves the run root's spill directory. An empty root (headless /
// ACP sessions without a registered workspace) falls back to the product data
// directory; with neither, Dir stays empty and Rig uses its own temp directory.
func (s *service) spillDir(ctx context.Context, workspaceRoot string) string {
	if root := strings.TrimSpace(workspaceRoot); root != "" {
		return filepath.Join(root, filepath.FromSlash(spillDirName))
	}
	// Headless/ACP sessions have no registered workspace; spill under the
	// product data directory instead of resending oversized tool results in
	// every following model round (verification finding ZK-13).
	if dataDir := strings.TrimSpace(s.dataDir); dataDir != "" {
		return filepath.Join(dataDir, filepath.FromSlash(spillDirName))
	}
	return ""
}
