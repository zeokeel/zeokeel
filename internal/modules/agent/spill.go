package agent

// Tool-result spilling for the persisted conversation history.
//
// Results over 32 KiB are written to <workspace>/.zeokeel/tool-results/ (or
// <dataDir>/.zeokeel/tool-results for headless/ACP sessions without a registered
// workspace) and the model only sees Rig's stable summary + path, so an
// oversized tool output is not resent in every following model round.
//
// The product keeps two things here: resolving the directory (file fallback) and
// handing over the shared store. Writing, naming, summarizing and prune-to-MaxFiles
// are Rig's rig.SpillPolicy (rig/context.go, applied in rig/agent/agent.go
// before the tool message is recorded); the policy is installed on the run's
// AgentSpec in agent.go. With a store the model gets a handle plus the toolkit's
// spill_read entry instead of a path. The tombstone guard stays outermost on the
// History wrapper (see guardedHistory).

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	// A shared store (toolkit's data-dir store) turns oversized results into
	// handles the model can read back with spill_read; the file backend below is
	// the fallback for assemblies without one.
	if s != nil && s.spillStore != nil {
		return &rig.SpillPolicy{
			Store:          s.spillStore,
			ThresholdBytes: spillThresholdBytes,
			MaxFiles:       spillKeepFiles,
		}
	}
	dir := s.spillDir(ctx, workspaceRoot)
	// rig v0.24.0 partitions its prune by session ("spill.<scope>.…"), so files
	// written by earlier versions — and any other file this directory already
	// holds — are no longer pruned by anything. Keep the directory bounded across
	// that naming change; rig still owns its own per-session files.
	pruneLegacySpillFiles(dir, spillKeepFiles)
	return &rig.SpillPolicy{
		Dir:            dir,
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

// spillScopedPrefix is the file-name prefix rig v0.24.0 writes for a session
// scope ("spill.<scope>.<tool>-<id>-<time>-<rand>.txt"). Only files carrying it
// belong to rig's per-session prune; everything else in the directory is legacy
// (or foreign) and is bounded here.
const spillScopedPrefix = "spill."

// pruneLegacySpillFiles keeps the newest keep files among the *.txt files rig's
// scoped prune does not own, mirroring the whole-directory prune the previous
// rig version performed. It is best-effort: an unreadable entry is skipped, and
// a directory that does not exist yet is a no-op. keep <= 0 disables it.
func pruneLegacySpillFiles(dir string, keep int) {
	dir = strings.TrimSpace(dir)
	if dir == "" || keep <= 0 {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type legacyFile struct {
		path string
		mod  time.Time
	}
	files := make([]legacyFile, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".txt") || strings.HasPrefix(name, spillScopedPrefix) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		files = append(files, legacyFile{path: filepath.Join(dir, name), mod: info.ModTime()})
	}
	if len(files) <= keep {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for _, file := range files[keep:] {
		_ = os.Remove(file.path)
	}
}
