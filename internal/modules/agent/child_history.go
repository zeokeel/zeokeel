package agent

// Durable child-agent history: the finished teammates and synchronous
// subagents the session shell kept in memory are written to one small JSON
// file in the product data directory, so reopening the app still shows the
// team and the subagent chips with their prompts, answers and activity. The
// DSH-aligned shell promised exactly this ("a future durable child-session
// backend only has to change this mapping"): the UI keeps reading the same
// contracts.ChildAgentService/TeamService and the restored records arrive as
// historical, read-only rows.
//
// The file is bounded (newest records per kind, TTL) and every mutation is
// written atomically (temporary file + fsync + rename + directory fsync). A
// corrupt document is quarantined next to the original and the store starts
// empty instead of failing the agent module.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// childHistoryFileName is the history document inside the data directory.
	childHistoryFileName = "child-agents.json"
	// childHistoryVersion is the document schema version.
	childHistoryVersion = 1
	// childHistoryMemberKeep/childHistorySubagentKeep bound the retained
	// records per kind (the oldest are pruned).
	childHistoryMemberKeep   = 24
	childHistorySubagentKeep = 48
	// childHistoryTTL drops records older than this (the app keeps roughly two
	// weeks of child-agent runs).
	childHistoryTTL = 14 * 24 * time.Hour
	// childHistoryNameRunes bounds one stored display name.
	childHistoryNameRunes = 120
	// childHistoryPromptRunes bounds one stored task prompt.
	childHistoryPromptRunes = 240
	// childHistoryOutputRunes bounds one stored answer, so the document stays
	// small even when teammates answer in long Markdown.
	childHistoryOutputRunes = 1600
	// childHistoryErrorRunes bounds one stored error.
	childHistoryErrorRunes = 400
	// childHistoryKeyPrefix names a restored record's row identity: a manager
	// reference ("member-1") is only unique within one process, so the history
	// gives every record a key that can never collide with a live row.
	childHistoryKeyPrefix = "history-"
)

// childHistoryFile is the persisted document.
type childHistoryFile struct {
	Version   int                    `json:"version"`
	Members   []childHistoryMember   `json:"members,omitempty"`
	Subagents []childHistorySubagent `json:"subagents,omitempty"`
}

// childHistoryActivity is one persisted activity row of a teammate.
type childHistoryActivity struct {
	ID    string    `json:"id,omitempty"`
	Kind  string    `json:"kind,omitempty"`
	Text  string    `json:"text,omitempty"`
	Time  time.Time `json:"time,omitempty"`
	Error bool      `json:"error,omitempty"`
}

// childHistoryMember is one persisted teammate record.
type childHistoryMember struct {
	// Key is the stable row identity of the restored record.
	Key string `json:"key"`
	// OriginalID is the manager reference the member had while it was live;
	// it dedupes the restored row against a live member of the same process.
	OriginalID  string                 `json:"originalId,omitempty"`
	SessionID   string                 `json:"sessionId,omitempty"`
	Name        string                 `json:"name,omitempty"`
	Model       string                 `json:"model,omitempty"`
	Prompt      string                 `json:"prompt,omitempty"`
	RunState    string                 `json:"runState,omitempty"`
	Error       string                 `json:"error,omitempty"`
	Last        string                 `json:"last,omitempty"`
	Started     time.Time              `json:"started,omitempty"`
	Finished    time.Time              `json:"finished,omitempty"`
	UsageTokens int                    `json:"usageTokens,omitempty"`
	Iterations  int                    `json:"iterations,omitempty"`
	Activity    []childHistoryActivity `json:"activity,omitempty"`
	Updated     time.Time              `json:"updated,omitempty"`
}

// childHistorySubagent is one persisted synchronous delegation.
type childHistorySubagent struct {
	Key        string    `json:"key"`
	OriginalID string    `json:"originalId,omitempty"`
	SessionID  string    `json:"sessionId,omitempty"`
	Name       string    `json:"name,omitempty"`
	Model      string    `json:"model,omitempty"`
	Prompt     string    `json:"prompt,omitempty"`
	Status     string    `json:"status,omitempty"`
	Detail     string    `json:"detail,omitempty"`
	Fork       bool      `json:"fork,omitempty"`
	Started    time.Time `json:"started,omitempty"`
	Finished   time.Time `json:"finished,omitempty"`
	Updated    time.Time `json:"updated,omitempty"`
}

// childHistoryStore owns the in-memory copy of the durable child-agent history
// and its file. It is safe for concurrent use: archiving happens on background
// run watchers while the UI thread reads snapshots.
type childHistoryStore struct {
	mu      sync.Mutex
	path    string
	logger  contracts.Logger
	members []childHistoryMember
	subs    []childHistorySubagent
	// nextKey disambiguates two records archived in the same nanosecond.
	nextKey uint64
}

// newChildHistoryStore returns the store for dataDir ("" disables persistence
// and keeps everything in memory, which embedders and focused tests use).
func newChildHistoryStore(dataDir string, logger contracts.Logger) *childHistoryStore {
	if logger == nil {
		logger = discardLogger{}
	}
	store := &childHistoryStore{logger: logger}
	if dir := strings.TrimSpace(dataDir); dir != "" {
		store.path = filepath.Join(dir, childHistoryFileName)
	}
	store.load()
	return store
}

// load reads the persisted document, tolerating every failure: an unreadable
// or invalid file is quarantined and the store starts empty (the agent module
// must still activate).
func (h *childHistoryStore) load() {
	if h == nil || h.path == "" {
		return
	}
	data, err := os.ReadFile(h.path)
	if err != nil {
		if !os.IsNotExist(err) {
			h.logf("read child-agent history: %v", err)
		}
		return
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return
	}
	var document childHistoryFile
	if err := json.Unmarshal(data, &document); err != nil {
		h.quarantine("child-agent history is not valid JSON: " + err.Error())
		return
	}
	if document.Version != childHistoryVersion {
		h.quarantine(fmt.Sprintf("child-agent history has unsupported version %d", document.Version))
		return
	}
	h.mu.Lock()
	h.members = document.Members
	h.subs = document.Subagents
	h.pruneLocked(time.Now())
	h.mu.Unlock()
}

// quarantine moves the unusable document out of the way and logs why; the
// store keeps running empty.
func (h *childHistoryStore) quarantine(reason string) {
	backup := fmt.Sprintf("%s.corrupt-%d", h.path, time.Now().UTC().UnixNano())
	message := reason
	if err := os.Rename(h.path, backup); err != nil {
		message += fmt.Sprintf(" (could not quarantine the file: %v)", err)
	} else {
		message += " (quarantined as " + backup + ")"
	}
	h.logf("%s; starting with an empty child-agent history", message)
}

// logf reports one store warning through the module logger.
func (h *childHistoryStore) logf(format string, args ...any) {
	if h == nil || h.logger == nil {
		return
	}
	h.logger.Printf("[agent] "+format, args...)
}

// clear drops every retained record and removes the document (best effort).
func (h *childHistoryStore) clear() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.members = nil
	h.subs = nil
	h.mu.Unlock()
	if h.path != "" {
		if err := os.Remove(h.path); err != nil && !os.IsNotExist(err) {
			h.logf("remove child-agent history: %v", err)
		}
	}
}

// memberRecords returns a copy of the retained teammate records, newest first.
func (h *childHistoryStore) memberRecords() []childHistoryMember {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]childHistoryMember, len(h.members))
	copy(out, h.members)
	return out
}

// subagentRecords returns a copy of the retained subagent records, newest
// first.
func (h *childHistoryStore) subagentRecords() []childHistorySubagent {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]childHistorySubagent, len(h.subs))
	copy(out, h.subs)
	return out
}

// upsertMember archives one teammate snapshot. An existing record for the same
// original member is updated in place (the stable key keeps the UI row), and the
// document is only rewritten when something actually changed.
func (h *childHistoryStore) upsertMember(record childHistoryMember) {
	if h == nil {
		return
	}
	if strings.TrimSpace(record.Name) == "" && strings.TrimSpace(record.OriginalID) == "" {
		return
	}
	record.Name = truncateTeamText(strings.TrimSpace(record.Name), childHistoryNameRunes)
	record.Prompt = collapseTeamText(record.Prompt, childHistoryPromptRunes)
	record.Last = truncateTeamText(strings.TrimSpace(record.Last), childHistoryOutputRunes)
	record.Error = truncateTeamText(strings.TrimSpace(record.Error), childHistoryErrorRunes)
	record.RunState = normalizeHistoryRunState(record.RunState)

	h.mu.Lock()
	defer h.mu.Unlock()
	index := h.memberIndexLocked(record)
	if index >= 0 {
		if childHistoryMemberEqual(h.members[index], record) {
			return
		}
		record.Key = h.members[index].Key
		record.Updated = time.Now()
		h.members[index] = record
	} else {
		h.nextKey++
		record.Key = historyKey("m", h.nextKey)
		record.Updated = time.Now()
		h.members = append(h.members, record)
	}
	h.pruneLocked(time.Now())
	h.persistLocked()
}

// upsertSubagent archives one finished synchronous delegation.
func (h *childHistoryStore) upsertSubagent(record childHistorySubagent) {
	if h == nil {
		return
	}
	record.Name = truncateTeamText(strings.TrimSpace(record.Name), childHistoryNameRunes)
	record.Prompt = collapseTeamText(record.Prompt, childHistoryPromptRunes)
	record.Detail = truncateTeamText(strings.TrimSpace(record.Detail), childHistoryOutputRunes)

	h.mu.Lock()
	defer h.mu.Unlock()
	index := h.subagentIndexLocked(record)
	if index >= 0 {
		if childHistorySubagentEqual(h.subs[index], record) {
			return
		}
		record.Key = h.subs[index].Key
		record.Updated = time.Now()
		h.subs[index] = record
	} else {
		h.nextKey++
		record.Key = historyKey("s", h.nextKey)
		record.Updated = time.Now()
		h.subs = append(h.subs, record)
	}
	h.pruneLocked(time.Now())
	h.persistLocked()
}

// memberIndexLocked finds the record of the same original member (the caller
// holds h.mu).
func (h *childHistoryStore) memberIndexLocked(record childHistoryMember) int {
	for index := range h.members {
		if sameHistoryOwner(h.members[index].OriginalID, h.members[index].SessionID,
			record.OriginalID, record.SessionID) {
			return index
		}
	}
	return -1
}

// subagentIndexLocked finds the record of the same original delegation (the
// caller holds h.mu).
func (h *childHistoryStore) subagentIndexLocked(record childHistorySubagent) int {
	for index := range h.subs {
		if sameHistoryOwner(h.subs[index].OriginalID, h.subs[index].SessionID,
			record.OriginalID, record.SessionID) {
			return index
		}
	}
	return -1
}

// pruneLocked enforces the per-kind count bounds and the TTL, then orders the
// records newest first (the caller holds h.mu).
func (h *childHistoryStore) pruneLocked(now time.Time) {
	cutoff := now.Add(-childHistoryTTL)
	h.members = pruneHistoryMembers(h.members, cutoff, childHistoryMemberKeep)
	h.subs = pruneHistorySubagents(h.subs, cutoff, childHistorySubagentKeep)
}

// persistLocked writes the document atomically (the caller holds h.mu).
func (h *childHistoryStore) persistLocked() {
	if h.path == "" {
		return
	}
	directory := filepath.Dir(h.path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		h.logf("create child-agent history directory: %v", err)
		return
	}
	document := childHistoryFile{Version: childHistoryVersion, Members: h.members, Subagents: h.subs}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		h.logf("encode child-agent history: %v", err)
		return
	}
	temporary, err := os.CreateTemp(directory, ".child-agents-*.tmp")
	if err != nil {
		h.logf("create temporary child-agent history file: %v", err)
		return
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if _, err := temporary.Write(data); err != nil {
		cleanup()
		h.logf("write temporary child-agent history file: %v", err)
		return
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		h.logf("sync temporary child-agent history file: %v", err)
		return
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		h.logf("close temporary child-agent history file: %v", err)
		return
	}
	if err := os.Rename(temporaryPath, h.path); err != nil {
		_ = os.Remove(temporaryPath)
		h.logf("replace child-agent history %q: %v", h.path, err)
		return
	}
	if handle, err := os.Open(directory); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
}

// sameHistoryOwner reports whether two records describe the same backend-owned
// child (same original reference inside the same session; an unattributed
// session matches anything).
func sameHistoryOwner(leftID, leftSession, rightID, rightSession string) bool {
	if leftID == "" || leftID != rightID {
		return false
	}
	if leftSession == "" || rightSession == "" {
		return true
	}
	return leftSession == rightSession
}

// pruneHistoryMembers drops stale records and keeps the newest limit.
func pruneHistoryMembers(records []childHistoryMember, cutoff time.Time, keep int) []childHistoryMember {
	live := make([]childHistoryMember, 0, len(records))
	for _, record := range records {
		if historyRecordTime(record.Updated, record.Finished, record.Started).Before(cutoff) {
			continue
		}
		live = append(live, record)
	}
	sort.SliceStable(live, func(i, j int) bool {
		return historyRecordTime(live[i].Updated, live[i].Finished, live[i].Started).
			After(historyRecordTime(live[j].Updated, live[j].Finished, live[j].Started))
	})
	if len(live) > keep {
		live = live[:keep]
	}
	return live
}

// pruneHistorySubagents drops stale records and keeps the newest limit.
func pruneHistorySubagents(records []childHistorySubagent, cutoff time.Time, keep int) []childHistorySubagent {
	live := make([]childHistorySubagent, 0, len(records))
	for _, record := range records {
		if historyRecordTime(record.Updated, record.Finished, record.Started).Before(cutoff) {
			continue
		}
		live = append(live, record)
	}
	sort.SliceStable(live, func(i, j int) bool {
		return historyRecordTime(live[i].Updated, live[i].Finished, live[i].Started).
			After(historyRecordTime(live[j].Updated, live[j].Finished, live[j].Started))
	})
	if len(live) > keep {
		live = live[:keep]
	}
	return live
}

// historyRecordTime is the timestamp a record is pruned/ordered by: the last
// update, else the finish, else the start.
func historyRecordTime(updated, finished, started time.Time) time.Time {
	if !updated.IsZero() {
		return updated
	}
	if !finished.IsZero() {
		return finished
	}
	return started
}

// historyKey builds a row identity that cannot collide with a live manager
// reference.
func historyKey(kind string, seq uint64) string {
	return childHistoryKeyPrefix + kind + "-" + strconv.FormatUint(seq, 10) + "-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

// normalizeHistoryRunState folds a live run state onto the historical
// vocabulary: a member archived while running was interrupted by the shutdown,
// and a closed member keeps the outcome of its last run.
func normalizeHistoryRunState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case contracts.TeamRunDone:
		return contracts.TeamRunDone
	case contracts.TeamRunFailed:
		return contracts.TeamRunFailed
	case contracts.TeamRunInterrupted, contracts.TeamRunClosed:
		return contracts.TeamRunInterrupted
	default:
		return contracts.TeamRunInterrupted
	}
}

// childHistoryMemberEqual reports whether two snapshots describe the same
// record (the key/update stamp excluded).
func childHistoryMemberEqual(left, right childHistoryMember) bool {
	return left.OriginalID == right.OriginalID &&
		left.SessionID == right.SessionID &&
		left.Name == right.Name &&
		left.Model == right.Model &&
		left.Prompt == right.Prompt &&
		left.RunState == right.RunState &&
		left.Error == right.Error &&
		left.Last == right.Last &&
		left.Started.Equal(right.Started) &&
		left.Finished.Equal(right.Finished) &&
		left.UsageTokens == right.UsageTokens &&
		left.Iterations == right.Iterations &&
		len(left.Activity) == len(right.Activity)
}

// childHistorySubagentEqual reports whether two snapshots describe the same
// record (the key/update stamp excluded).
func childHistorySubagentEqual(left, right childHistorySubagent) bool {
	return left.OriginalID == right.OriginalID &&
		left.SessionID == right.SessionID &&
		left.Name == right.Name &&
		left.Model == right.Model &&
		left.Prompt == right.Prompt &&
		left.Status == right.Status &&
		left.Detail == right.Detail &&
		left.Fork == right.Fork &&
		left.Started.Equal(right.Started) &&
		left.Finished.Equal(right.Finished)
}

// --- conversions between the history records and the UI contracts -----------

// childHistoryMemberFromContract records one teammate snapshot.
func childHistoryMemberFromContract(member contracts.TeamMember, sessionID, prompt string) childHistoryMember {
	activity := make([]childHistoryActivity, 0, len(member.Activity))
	for _, entry := range member.Activity {
		activity = append(activity, childHistoryActivity{
			ID: entry.ID, Kind: entry.Kind, Text: entry.Text, Time: entry.Time, Error: entry.Error,
		})
	}
	return childHistoryMember{
		OriginalID:  member.ID,
		SessionID:   strings.TrimSpace(sessionID),
		Name:        member.Name,
		Model:       member.Model,
		Prompt:      prompt,
		RunState:    member.RunState,
		Error:       member.Error,
		Last:        member.Last,
		Started:     member.Started,
		Finished:    member.Finished,
		UsageTokens: member.UsageTokens,
		Iterations:  member.Iterations,
		Activity:    activity,
	}
}

// childHistorySubagentFromContract records one finished synchronous delegation.
func childHistorySubagentFromContract(activity contracts.SubagentActivity) childHistorySubagent {
	return childHistorySubagent{
		OriginalID: activity.ID,
		SessionID:  activity.SessionID,
		Name:       activity.Name,
		Model:      activity.Model,
		Prompt:     activity.Prompt,
		Status:     activity.Status,
		Detail:     activity.Detail,
		Fork:       activity.Fork,
		Started:    activity.Started,
		Finished:   activity.Finished,
	}
}

// teamMemberFromHistory converts one restored teammate record to the member
// contract the team overlay renders. It is historical and read-only.
func teamMemberFromHistory(record childHistoryMember) contracts.TeamMember {
	activity := make([]contracts.TeamActivity, 0, len(record.Activity))
	for _, entry := range record.Activity {
		activity = append(activity, contracts.TeamActivity{
			ID: entry.ID, Kind: entry.Kind, Text: entry.Text, Time: entry.Time, Error: entry.Error,
		})
	}
	member := contracts.TeamMember{
		ID:          record.Key,
		Name:        record.Name,
		Status:      contracts.TeamRunClosed,
		Created:     record.Started,
		Last:        record.Last,
		RunState:    record.RunState,
		Error:       record.Error,
		Model:       record.Model,
		Started:     record.Started,
		Finished:    record.Finished,
		UsageTokens: record.UsageTokens,
		Iterations:  record.Iterations,
		Activity:    activity,
		SessionID:   record.SessionID,
		Historical:  true,
	}
	return member
}

// childAgentFromArchivedMember converts one restored teammate record to the
// unified child-agent view. The mode stays continuable (it was a teammate) but
// Historical makes the UI read-only.
func childAgentFromArchivedMember(record childHistoryMember) contracts.ChildAgent {
	agent := contracts.ChildAgent{
		ID:              record.Key,
		ParentSessionID: record.SessionID,
		Name:            record.Name,
		Kind:            contracts.ChildAgentTeammate,
		Mode:            contracts.ChildAgentContinuable,
		Status:          childAgentStatusFromTeamRun(record.RunState),
		Model:           record.Model,
		Prompt:          record.Prompt,
		Started:         record.Started,
		Finished:        record.Finished,
		UsageTokens:     record.UsageTokens,
		Iterations:      record.Iterations,
		Historical:      true,
	}
	if agent.Status != contracts.ChildAgentRunning && strings.TrimSpace(record.Error) == "" {
		agent.Output = record.Last
	}
	if strings.TrimSpace(record.Error) != "" {
		agent.Error = record.Error
	}
	for _, entry := range record.Activity {
		agent.Activity = append(agent.Activity, contracts.ChildAgentActivity{
			ID: entry.ID, Kind: entry.Kind, Text: entry.Text, Time: entry.Time, Error: entry.Error,
		})
	}
	return agent
}

// childAgentFromArchivedSubagent converts one restored synchronous delegation.
func childAgentFromArchivedSubagent(record childHistorySubagent) contracts.ChildAgent {
	agent := contracts.ChildAgent{
		ID:              record.Key,
		ParentSessionID: record.SessionID,
		Name:            record.Name,
		Kind:            contracts.ChildAgentSubagent,
		Mode:            contracts.ChildAgentOneShot,
		Status:          childAgentStatusFromSubagent(record.Status),
		Model:           record.Model,
		Prompt:          record.Prompt,
		Started:         record.Started,
		Finished:        record.Finished,
		Historical:      true,
	}
	switch record.Status {
	case contracts.SubagentStatusDone:
		agent.Output = record.Detail
	case contracts.SubagentStatusError:
		agent.Error = record.Detail
	}
	if agent.Status == contracts.ChildAgentRunning {
		// A running row is only persisted after it finished, so a stale running
		// status can only come from a hand-edited file: keep it readable but not
		// running.
		agent.Status = contracts.ChildAgentInterrupted
	}
	return agent
}
