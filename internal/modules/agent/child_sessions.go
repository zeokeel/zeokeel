package agent

// Durable child sessions: teammates and synchronous subagents a parent session
// spawned. The record document itself (atomic persistence, ordering, retention,
// receipt flags, restart recovery) is the SDK's session/children store; this
// file keeps the product's vocabulary (team/subagent status mapping, activity
// shapes, the legacy sidecar migration) and adapts the store to the agent's
// existing call sites.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	rigchildren "github.com/zeokeel/zeokeel-rig/session/children"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// childSessionFileName is the child-session document inside the data
	// directory.
	childSessionFileName = "child-sessions.json"
	// childSessionLegacyFileName is the v1 sidecar migrated on first load.
	childSessionLegacyFileName = "child-agents.json"
	// childSessionVersion is the document schema version (the SDK store writes
	// and accepts exactly this one).
	childSessionVersion = 2
	// childSessionLegacyVersion is the version of the migrated document.
	childSessionLegacyVersion = 1
	// childSessionKeep bounds the retained records (oldest pruned).
	childSessionKeep = 64
	// childSessionTTL drops records older than this.
	childSessionTTL = 14 * 24 * time.Hour
	// Bounds on one stored text.
	childSessionNameRunes   = 120
	childSessionPromptRunes = 240
	childSessionOutputRunes = 1600
	childSessionErrorRunes  = 400
	// childSessionIDPrefix names the stable child-session identity.
	childSessionIDPrefix = "child-"
)

// childSessionRecord / childSessionActivity are the SDK document types; the
// product code keeps its historical names so every call site stays unchanged.
type childSessionRecord = rigchildren.Record
type childSessionActivity = rigchildren.Activity

// childSessionStore adapts the SDK child-session document to the agent module.
type childSessionStore struct {
	inner  *rigchildren.Store
	legacy string
	logger contracts.Logger
}

// newChildSessionStore opens the store for dataDir (empty keeps it in memory)
// and migrates the v1 sidecar on first use.
func newChildSessionStore(dataDir string, logger contracts.Logger) *childSessionStore {
	if logger == nil {
		logger = discardLogger{}
	}
	store := &childSessionStore{logger: logger}
	path := ""
	if dir := strings.TrimSpace(dataDir); dir != "" {
		path = filepath.Join(dir, childSessionFileName)
		store.legacy = filepath.Join(dir, childSessionLegacyFileName)
	}
	store.inner = rigchildren.Open(rigchildren.Options{
		Path:      path,
		Logger:    store.logf,
		IDPrefix:  childSessionIDPrefix,
		Keep:      childSessionKeep,
		TTL:       childSessionTTL,
		Version:   childSessionVersion,
		Normalize: store.normalize,
		// A record still marked running cannot have survived the process: the
		// app quit mid-run, so the child is honestly interrupted.
		OnLoad: func(records []childSessionRecord) []childSessionRecord {
			for index := range records {
				if records[index].Status == contracts.ChildAgentRunning {
					records[index].Status = contracts.ChildAgentInterrupted
				}
			}
			return records
		},
	})
	if store.legacy != "" {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			store.migrateLegacy()
		}
	}
	return store
}

// newID allocates one stable child-session identity.
func (s *childSessionStore) newID() string {
	if s == nil || s.inner == nil {
		return childSessionIDPrefix + randomHex(4)
	}
	return s.inner.NewID()
}

// randomHex returns n random bytes as hex (fallback id when no store exists).
func randomHex(n int) string {
	buffer := make([]byte, n)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}

// hasID reports whether the identity is already retained.
func (s *childSessionStore) hasID(id string) bool {
	return s != nil && s.inner != nil && s.inner.HasID(id)
}

// upsert stores one record and reports whether anything changed.
func (s *childSessionStore) upsert(record childSessionRecord) bool {
	return s != nil && s.inner != nil && s.inner.Upsert(record)
}

// markNotified flags a finished child as receipt-delivered.
func (s *childSessionStore) markNotified(id string, finished time.Time) bool {
	return s != nil && s.inner != nil && s.inner.MarkNotified(id, finished)
}

// records returns the retained records, newest first.
func (s *childSessionStore) records() []childSessionRecord {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.Records()
}

// record returns one retained record by id.
func (s *childSessionStore) record(id string) (childSessionRecord, bool) {
	if s == nil || s.inner == nil {
		return childSessionRecord{}, false
	}
	return s.inner.Record(id)
}

// byBackendRef resolves a live backend reference to its record.
func (s *childSessionStore) byBackendRef(backendRef, sessionID, kind string) (childSessionRecord, bool) {
	if s == nil || s.inner == nil {
		return childSessionRecord{}, false
	}
	return s.inner.ByBackendRef(backendRef, sessionID, kind)
}

// clear drops every retained record and removes the document.
func (s *childSessionStore) clear() {
	if s != nil && s.inner != nil {
		s.inner.Clear()
	}
}

// prune applies the TTL/count bounds now.
func (s *childSessionStore) prune(now time.Time) {
	if s != nil && s.inner != nil {
		s.inner.Prune(now)
	}
}

// close stops further writes (module disposal).
func (s *childSessionStore) close() {
	if s != nil && s.inner != nil {
		s.inner.Close()
	}
}

// normalize applies the product's text bounds and status vocabulary.
func (s *childSessionStore) normalize(record *childSessionRecord) {
	if record == nil {
		return
	}
	record.Name = truncateTeamText(strings.TrimSpace(record.Name), childSessionNameRunes)
	record.Prompt = collapseTeamText(record.Prompt, childSessionPromptRunes)
	record.Output = truncateTeamText(strings.TrimSpace(record.Output), childSessionOutputRunes)
	record.Error = truncateTeamText(strings.TrimSpace(record.Error), childSessionErrorRunes)
	record.Status = normalizeChildStatus(record.Status, record.Kind)
}

// logf reports one store warning through the module logger.
func (s *childSessionStore) logf(format string, args ...any) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Printf("[agent] "+format, args...)
}

// legacyDocument is the v1 sidecar schema migrated on first load.
type legacyDocument struct {
	Version   int                     `json:"version"`
	Members   []legacyHistoryMember   `json:"members,omitempty"`
	Subagents []legacyHistorySubagent `json:"subagents,omitempty"`
}

type legacyHistoryMember struct {
	Key         string                 `json:"key"`
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
	Activity    []childSessionActivity `json:"activity,omitempty"`
	Updated     time.Time              `json:"updated,omitempty"`
}

type legacyHistorySubagent struct {
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

// migrateLegacy imports the v1 history document, then moves it aside so the
// migration runs once.
func (s *childSessionStore) migrateLegacy() {
	if s == nil || s.legacy == "" {
		return
	}
	data, err := os.ReadFile(s.legacy)
	if err != nil {
		return
	}
	var document legacyDocument
	if err := json.Unmarshal(data, &document); err != nil || document.Version != childSessionLegacyVersion {
		s.logf("legacy child-agent history is unreadable; skipping migration")
		return
	}
	var records []childSessionRecord
	for _, member := range document.Members {
		records = append(records, childSessionRecord{
			ID:              s.newID(),
			ParentSessionID: member.SessionID,
			BackendRef:      member.OriginalID,
			Kind:            contracts.ChildAgentTeammate,
			Name:            member.Name,
			Model:           member.Model,
			Prompt:          member.Prompt,
			Status:          childStatusFromRunState(member.RunState),
			Output:          member.Last,
			Error:           member.Error,
			Started:         member.Started,
			Finished:        member.Finished,
			Updated:         member.Updated,
			UsageTokens:     member.UsageTokens,
			Iterations:      member.Iterations,
			Activity:        member.Activity,
		})
	}
	for _, subagent := range document.Subagents {
		record := childSessionRecord{
			ID:              s.newID(),
			ParentSessionID: subagent.SessionID,
			BackendRef:      subagent.OriginalID,
			Kind:            contracts.ChildAgentSubagent,
			Name:            subagent.Name,
			Model:           subagent.Model,
			Prompt:          subagent.Prompt,
			Status:          childStatusFromSubagentStatus(subagent.Status),
			Fork:            subagent.Fork,
			Started:         subagent.Started,
			Finished:        subagent.Finished,
			Updated:         subagent.Updated,
		}
		switch subagent.Status {
		case contracts.SubagentStatusDone:
			record.Output = subagent.Detail
		case contracts.SubagentStatusError:
			record.Error = subagent.Detail
		}
		records = append(records, record)
	}
	for _, record := range records {
		s.upsert(record)
	}
	_ = os.Rename(s.legacy, s.legacy+".migrated")
	if len(records) > 0 {
		s.logf("migrated %d child-agent history records to child sessions", len(records))
	}
}

// normalizeChildStatus keeps a value already in the child-agent vocabulary and
// folds a backend run state/subagent status onto it (the store is written from
// both live trackers and restored records, so it accepts both spellings).
func normalizeChildStatus(status, kind string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case contracts.ChildAgentRunning, contracts.ChildAgentCompleted,
		contracts.ChildAgentFailed, contracts.ChildAgentInterrupted, contracts.ChildAgentIdle:
		return strings.ToLower(strings.TrimSpace(status))
	}
	if kind == contracts.ChildAgentTeammate {
		return childStatusFromRunState(status)
	}
	return childStatusFromSubagentStatus(status)
}

// childSessionRecordEqual reports whether two snapshots describe the same
// record (the update stamp excluded).
func childSessionRecordEqual(left, right childSessionRecord) bool {
	if len(left.Activity) != len(right.Activity) {
		return false
	}
	for index := range left.Activity {
		a, b := left.Activity[index], right.Activity[index]
		if a.ID != b.ID || a.Kind != b.Kind || a.Text != b.Text || a.Error != b.Error || !a.Time.Equal(b.Time) {
			return false
		}
	}
	return left.ID == right.ID &&
		left.ParentSessionID == right.ParentSessionID &&
		left.BackendRef == right.BackendRef &&
		left.Kind == right.Kind &&
		left.Name == right.Name &&
		left.Model == right.Model &&
		left.Prompt == right.Prompt &&
		left.Status == right.Status &&
		left.Output == right.Output &&
		left.Error == right.Error &&
		left.Fork == right.Fork &&
		left.Started.Equal(right.Started) &&
		left.Finished.Equal(right.Finished) &&
		left.UsageTokens == right.UsageTokens &&
		left.Iterations == right.Iterations &&
		left.Notified == right.Notified
}

// --- status vocabulary conversions -----------------------------------------

// childStatusFromRunState folds a teammate run state onto the child-agent
// status vocabulary.
func childStatusFromRunState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case contracts.TeamRunRunning:
		return contracts.ChildAgentRunning
	case contracts.TeamRunDone:
		return contracts.ChildAgentCompleted
	case contracts.TeamRunFailed:
		return contracts.ChildAgentFailed
	case contracts.TeamRunInterrupted, contracts.TeamRunClosed:
		return contracts.ChildAgentInterrupted
	default:
		return contracts.ChildAgentInterrupted
	}
}

// runStateFromChildStatus folds a child-agent status onto the teammate run
// state vocabulary the team overlay renders.
func runStateFromChildStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case contracts.ChildAgentRunning:
		return contracts.TeamRunRunning
	case contracts.ChildAgentCompleted:
		return contracts.TeamRunDone
	case contracts.ChildAgentFailed:
		return contracts.TeamRunFailed
	case contracts.ChildAgentIdle:
		return contracts.TeamRunIdle
	default:
		return contracts.TeamRunInterrupted
	}
}

// childStatusFromSubagentStatus folds a subagent activity status onto the
// child-agent status vocabulary.
func childStatusFromSubagentStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case contracts.SubagentStatusRunning:
		return contracts.ChildAgentRunning
	case contracts.SubagentStatusDone:
		return contracts.ChildAgentCompleted
	case contracts.SubagentStatusError:
		return contracts.ChildAgentFailed
	case contracts.SubagentStatusCancelled:
		return contracts.ChildAgentInterrupted
	default:
		return contracts.ChildAgentInterrupted
	}
}

// subagentStatusFromChildStatus is the inverse mapping used when a live
// delegation is recorded.
func subagentStatusFromChildStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case contracts.ChildAgentRunning:
		return contracts.SubagentStatusRunning
	case contracts.ChildAgentCompleted:
		return contracts.SubagentStatusDone
	case contracts.ChildAgentFailed:
		return contracts.SubagentStatusError
	default:
		return contracts.SubagentStatusCancelled
	}
}

// childRecordResumable reports whether a historical record has enough durable
// material for a cold resume: an identity plus the original prompt or answer to
// seed the child session with. Live rows never report Resumable.
func childRecordResumable(record childSessionRecord) bool {
	if strings.TrimSpace(record.ID) == "" {
		return false
	}
	return strings.TrimSpace(record.Prompt) != "" || strings.TrimSpace(record.Output) != ""
}

// --- conversions between the store records and the UI contracts -----------

// childSessionFromTeamMember builds one teammate record from a live contract
// snapshot.
func childSessionFromTeamMember(member contracts.TeamMember, sessionID, prompt, childID, backendRef string) childSessionRecord {
	activity := make([]childSessionActivity, 0, len(member.Activity))
	for _, entry := range member.Activity {
		activity = append(activity, childSessionActivity{
			ID: entry.ID, Kind: entry.Kind, Text: entry.Text, Time: entry.Time, Error: entry.Error,
		})
	}
	return childSessionRecord{
		ID:              childID,
		ParentSessionID: strings.TrimSpace(sessionID),
		BackendRef:      strings.TrimSpace(backendRef),
		Kind:            contracts.ChildAgentTeammate,
		Name:            member.Name,
		Model:           member.Model,
		Prompt:          prompt,
		Status:          childStatusFromRunState(member.RunState),
		Output:          member.Last,
		Error:           member.Error,
		Started:         member.Started,
		Finished:        member.Finished,
		UsageTokens:     member.UsageTokens,
		Iterations:      member.Iterations,
		Activity:        activity,
	}
}

// childSessionFromSubagentActivity builds one subagent record from a finished
// delegation snapshot.
func childSessionFromSubagentActivity(activity contracts.SubagentActivity, childID string) childSessionRecord {
	record := childSessionRecord{
		ID:              childID,
		ParentSessionID: activity.SessionID,
		BackendRef:      activity.ID,
		Kind:            contracts.ChildAgentSubagent,
		Name:            activity.Name,
		Model:           activity.Model,
		Prompt:          activity.Prompt,
		Status:          childStatusFromSubagentStatus(activity.Status),
		Fork:            activity.Fork,
		Started:         activity.Started,
		Finished:        activity.Finished,
	}
	switch activity.Status {
	case contracts.SubagentStatusDone:
		record.Output = activity.Detail
	case contracts.SubagentStatusError:
		record.Error = activity.Detail
	}
	return record
}

// childSessionActivityFromTeam converts a live activity slice to the persisted
// row shape.
func childSessionActivityFromTeam(activity []contracts.TeamActivity) []childSessionActivity {
	if len(activity) == 0 {
		return nil
	}
	out := make([]childSessionActivity, 0, len(activity))
	for _, entry := range activity {
		out = append(out, childSessionActivity{
			ID: entry.ID, Kind: entry.Kind, Text: entry.Text, Time: entry.Time, Error: entry.Error,
		})
	}
	return out
}

// teamMemberFromChildSession converts one restored teammate record.
func teamMemberFromChildSession(record childSessionRecord) contracts.TeamMember {
	activity := make([]contracts.TeamActivity, 0, len(record.Activity))
	for _, entry := range record.Activity {
		activity = append(activity, contracts.TeamActivity{
			ID: entry.ID, Kind: entry.Kind, Text: entry.Text, Time: entry.Time, Error: entry.Error,
		})
	}
	return contracts.TeamMember{
		ID:          record.ID,
		Name:        record.Name,
		Status:      contracts.TeamRunClosed,
		Created:     record.Started,
		Last:        record.Output,
		RunState:    runStateFromChildStatus(record.Status),
		Error:       record.Error,
		Model:       record.Model,
		Started:     record.Started,
		Finished:    record.Finished,
		UsageTokens: record.UsageTokens,
		Iterations:  record.Iterations,
		Activity:    activity,
		SessionID:   record.ParentSessionID,
		Historical:  true,
		Resumable:   childRecordResumable(record),
	}
}

// childAgentFromChildSession converts one restored child-session record to the
// unified read model. Teammates stay continuable in mode but Historical makes
// the view read-only; subagents are one-shot.
func childAgentFromChildSession(record childSessionRecord) contracts.ChildAgent {
	agent := contracts.ChildAgent{
		ID:              record.ID,
		ParentSessionID: record.ParentSessionID,
		Name:            record.Name,
		Kind:            record.Kind,
		Status:          record.Status,
		Model:           record.Model,
		Prompt:          record.Prompt,
		Started:         record.Started,
		Finished:        record.Finished,
		UsageTokens:     record.UsageTokens,
		Iterations:      record.Iterations,
		Historical:      true,
		Resumable:       childRecordResumable(record),
	}
	if record.Kind == contracts.ChildAgentTeammate {
		agent.Mode = contracts.ChildAgentContinuable
	} else {
		agent.Mode = contracts.ChildAgentOneShot
	}
	if agent.Status == contracts.ChildAgentCompleted {
		agent.Output = record.Output
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
