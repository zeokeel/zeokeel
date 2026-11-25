package tasklease

// The per-task tool binding (M5 execution policy). It lives beside the leases
// but is not part of them: a lease says who is working, the binding says what
// that work may touch. Keeping the two apart is what lets a task be released
// and re-claimed without losing its policy, and it is why the binding is
// written in the same document rather than a second file — one atomic write
// per mutation, one file to quarantine if it is ever corrupted.
//
// The rules are deliberately narrow. A binding is an execution *policy*, so a
// malformed request must be rejected loudly instead of being sanitized into
// something the caller did not ask for: an empty name, a name outside the tool
// alphabet, a duplicate or a 200-entry list is ErrTaskToolsInvalid, and a task
// that is not on the board is ErrTaskToolsUnknownTask. The read path is cheap
// and does not consult the board: it answers "what was bound", not "is it
// still legal".

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// taskToolName is the accepted tool-name alphabet: the one the tool surface
// already uses (`browser_open`, `mcp__server__tool`, `fs.read`). Lower case
// only — names are compared by exact match downstream, and accepting mixed
// case would make "the same tool" ambiguous.
var taskToolName = regexp.MustCompile(`^[a-z0-9_.:-]{1,120}$`)

// SetTools implements contracts.TaskLeaseService.
func (s *Service) SetTools(ctx context.Context, taskID string, tools []string) (contracts.TaskTools, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TaskTools{}, err
	}
	if strings.TrimSpace(taskID) == "" {
		return contracts.TaskTools{}, contracts.ErrTaskToolsInvalid
	}
	normalized, err := normalizeTaskTools(tools)
	if err != nil {
		return contracts.TaskTools{}, err
	}

	// Clearing is a delete: it must work even for a task the board no longer
	// has (a stale binding must be removable), so only a real binding pays the
	// board read.
	if len(normalized) > 0 {
		exists, err := s.taskExists(ctx, taskID)
		if err != nil {
			return contracts.TaskTools{}, err
		}
		if !exists {
			return contracts.TaskTools{}, contracts.ErrTaskToolsUnknownTask
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.cfg.Now()
	if len(normalized) == 0 {
		delete(s.tools, taskID)
		if err := s.persistLocked(); err != nil {
			return contracts.TaskTools{}, err
		}
		return contracts.TaskTools{TaskID: taskID, UpdatedAt: now}, nil
	}
	// Rebinding replaces: the previous set is not merged, because a policy that
	// only ever grows is not a policy.
	stored := &contracts.TaskTools{TaskID: taskID, Tools: normalized, UpdatedAt: now}
	s.tools[taskID] = stored
	if err := s.persistLocked(); err != nil {
		return contracts.TaskTools{}, err
	}
	return cloneTaskTools(stored), nil
}

// Tools implements contracts.TaskLeaseService.
func (s *Service) Tools(ctx context.Context, taskID string) (contracts.TaskTools, bool, error) {
	if err := ctx.Err(); err != nil {
		return contracts.TaskTools{}, false, err
	}
	if strings.TrimSpace(taskID) == "" {
		return contracts.TaskTools{}, false, contracts.ErrTaskToolsInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.tools[taskID]
	if !ok || len(binding.Tools) == 0 {
		return contracts.TaskTools{}, false, nil
	}
	return cloneTaskTools(binding), true, nil
}

// taskExists reads the board to answer "is this a real task". A service opened
// without a board cannot vouch for any task, so every binding looks unknown —
// honest, and unreachable through the module, which requires the board.
func (s *Service) taskExists(ctx context.Context, taskID string) (bool, error) {
	if s.board == nil {
		return false, nil
	}
	tasks, err := s.board.List(ctx)
	if err != nil {
		return false, fmt.Errorf("tasklease: list board: %w", err)
	}
	for _, task := range tasks {
		if task.ID == taskID {
			return true, nil
		}
	}
	return false, nil
}

// normalizeTaskTools validates and copies a requested binding. A nil or empty
// slice is "clear" and normalizes to nil.
func normalizeTaskTools(tools []string) ([]string, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	if len(tools) > contracts.MaxTaskTools {
		return nil, fmt.Errorf("%w: %d tools (max %d)", contracts.ErrTaskToolsInvalid, len(tools), contracts.MaxTaskTools)
	}
	out := make([]string, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for _, name := range tools {
		if !taskToolName.MatchString(name) {
			return nil, fmt.Errorf("%w: %q", contracts.ErrTaskToolsInvalid, name)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("%w: %q repeated", contracts.ErrTaskToolsInvalid, name)
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

func cloneTaskTools(binding *contracts.TaskTools) contracts.TaskTools {
	out := *binding
	out.Tools = append([]string(nil), binding.Tools...)
	return out
}
