package agent

// Durable child-session store.
//
// The DSH-aligned model this product follows has one identity for a delegated
// agent: a teammate is a continuable child session, and a synchronous subagent
// is a one-shot child session. The team member ID and the child-agent ID are
// therefore the same value, and it is a child-session ID that survives the app
// restart instead of a process-local manager reference. This store owns that
// identity and the durable record behind it.
//
// One small JSON document keeps every child session of the process (bounded and
// pruned by TTL): parent session, kind, display name, model, task prompt,
// status, answer/error, timestamps, usage, bounded activity and the backend
// reference (the rig manager member reference or subagent-registry ID) that only
// the backend ever resolves. The live read models keep reading the same
// contracts (ChildAgentService/TeamService): a live child is built from its
// backend plus the child-session ID; after a restart the records come back as
// historical, read-only rows with their IDs intact.
//
// The document is written atomically (temporary file + fsync + rename + directory
// fsync), a corrupt document is quarantined next to the original, and the old
// v1 history document (child-agents.json, from the first durable release) is
// migrated on first load so existing users keep their finished runs.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

const (
	// childSessionFileName is the child-session document inside the data
	// directory.
	childSessionFileName = "child-sessions.json"
	// childSessionLegacyFileName is the v1 sidecar migrated on first load.
	childSessionLegacyFileName = "child-agents.json"
	// childSessionVersion is the document schema version.
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

// childSessionFile is the persisted document.
type childSessionFile struct {
	Version  int                  `json:"version"`
	Children []childSessionRecord `json:"children,omitempty"`
}

// childSessionActivity is one persisted activity row.
type childSessionActivity struct {
	ID    string    `json:"id,omitempty"`
	Kind  string    `json:"kind,omitempty"`
	Text  string    `json:"text,omitempty"`
	Time  time.Time `json:"time,omitempty"`
	Error bool      `json:"error,omitempty"`
}

// childSessionRecord is one durable child session: a teammate or a synchronous
// subagent. ID is the unified child-session identity; BackendRef is the
// manager/registry reference only the backend resolves.
type childSessionRecord struct {
	ID              string                 `json:"id"`
	ParentSessionID string                 `json:"parentSessionId,omitempty"`
	BackendRef      string                 `json:"backendRef,omitempty"`
	Kind            string                 `json:"kind,omitempty"`
	Name            string                 `json:"name,omitempty"`
	Model           string                 `json:"model,omitempty"`
	Prompt          string                 `json:"prompt,omitempty"`
	Status          string                 `json:"status,omitempty"`
	Output          string                 `json:"output,omitempty"`
	Error           string                 `json:"error,omitempty"`
	Fork            bool                   `json:"fork,omitempty"`
	Started         time.Time              `json:"started,omitempty"`
	Finished        time.Time              `json:"finished,omitempty"`
	Updated         time.Time              `json:"updated,omitempty"`
	UsageTokens     int                    `json:"usageTokens,omitempty"`
	Iterations      int                    `json:"iterations,omitempty"`
	Activity        []childSessionActivity `json:"activity,omitempty"`
}

// childSessionStore owns the durable child-session records and their document.
type childSessionStore struct {
	mu     sync.Mutex
	path   string
	legacy string
	logger contracts.Logger
	items  []childSessionRecord
}

// newChildSessionStore returns the store for dataDir. An empty dataDir keeps
// everything in memory (embedders and focused tests).
func newChildSessionStore(dataDir string, logger contracts.Logger) *childSessionStore {
	if logger == nil {
		logger = discardLogger{}
	}
	store := &childSessionStore{logger: logger}
	if dir := strings.TrimSpace(dataDir); dir != "" {
		store.path = filepath.Join(dir, childSessionFileName)
		store.legacy = filepath.Join(dir, childSessionLegacyFileName)
	}
	store.load()
	return store
}

// load reads the document, migrating the v1 sidecar on first use. Every failure
// degrades to an empty store (the agent module must still activate); a corrupt
// document is quarantined.
func (s *childSessionStore) load() {
	if s == nil || s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.migrateLegacy()
			return
		}
		s.logf("read child-session document: %v", err)
		return
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return
	}
	var document childSessionFile
	if err := json.Unmarshal(data, &document); err != nil {
		s.quarantine("child-session document is not valid JSON: " + err.Error())
		return
	}
	if document.Version != childSessionVersion {
		s.quarantine(fmt.Sprintf("child-session document has unsupported version %d", document.Version))
		return
	}
	records := document.Children
	now := time.Now()
	for index := range records {
		// A record still marked running cannot have survived the process: the
		// app quit mid-run, so the child is honestly interrupted.
		if records[index].Status == contracts.ChildAgentRunning {
			records[index].Status = contracts.ChildAgentInterrupted
		}
	}
	s.mu.Lock()
	s.items = records
	s.pruneLocked(now)
	s.mu.Unlock()
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
	s.mu.Lock()
	s.items = records
	s.pruneLocked(time.Now())
	s.mu.Unlock()
	if len(records) > 0 {
		s.mu.Lock()
		s.persistLocked()
		s.mu.Unlock()
	}
	_ = os.Rename(s.legacy, s.legacy+".migrated")
	if len(records) > 0 {
		s.logf("migrated %d child-agent history records to child sessions", len(records))
	}
}

// newID allocates one stable child-session identity. The value is persisted, so
// it is the same after a restart and can be the row identity of both the team
// member and the child-agent views.
func (s *childSessionStore) newID() string {
	for {
		id := childSessionIDPrefix + randomHex(4)
		if s == nil || !s.hasID(id) {
			return id
		}
	}
}

// hasID reports whether the store already owns id.
func (s *childSessionStore) hasID(id string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.items {
		if record.ID == id {
			return true
		}
	}
	return false
}

// randomHex returns n random bytes as lowercase hex (falls back to the clock
// when the platform random source is unavailable).
func randomHex(n int) string {
	buffer := make([]byte, n)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%013x", time.Now().UnixNano())[:2*n]
	}
	return hex.EncodeToString(buffer)
}

// upsert stores one record snapshot, preserving its ID and writing the document
// only when something changed. It reports whether the document was updated.
func (s *childSessionStore) upsert(record childSessionRecord) bool {
	if s == nil {
		return false
	}
	if strings.TrimSpace(record.ID) == "" {
		return false
	}
	s.normalize(&record)
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := range s.items {
		if s.items[index].ID != record.ID {
			continue
		}
		if childSessionRecordEqual(s.items[index], record) {
			return false
		}
		record.Updated = time.Now()
		s.items[index] = record
		s.pruneLocked(time.Now())
		s.persistLocked()
		return true
	}
	record.Updated = time.Now()
	s.items = append(s.items, record)
	s.pruneLocked(time.Now())
	s.persistLocked()
	return true
}

// normalize bounds the stored texts and folds run-state spellings onto the
// child-agent status vocabulary.
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

// records returns the retained records, newest first.
func (s *childSessionStore) records() []childSessionRecord {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]childSessionRecord, len(s.items))
	copy(out, s.items)
	sort.SliceStable(out, func(i, j int) bool {
		return historyRecordTime(out[i].Updated, out[i].Finished, out[i].Started).
			After(historyRecordTime(out[j].Updated, out[j].Finished, out[j].Started))
	})
	return out
}

// record returns one retained record by its child-session ID.
func (s *childSessionStore) record(id string) (childSessionRecord, bool) {
	if s == nil {
		return childSessionRecord{}, false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return childSessionRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.items {
		if record.ID == id {
			return record, true
		}
	}
	return childSessionRecord{}, false
}

// byBackendRef returns the record a live backend reference belongs to, for one
// parent session and kind ("" session matches any).
func (s *childSessionStore) byBackendRef(backendRef, sessionID, kind string) (childSessionRecord, bool) {
	if s == nil {
		return childSessionRecord{}, false
	}
	backendRef = strings.TrimSpace(backendRef)
	if backendRef == "" {
		return childSessionRecord{}, false
	}
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	var fallback *childSessionRecord
	for index := range s.items {
		record := s.items[index]
		if record.BackendRef != backendRef || record.Kind != kind {
			continue
		}
		if sessionID == "" || record.ParentSessionID == "" || record.ParentSessionID == sessionID {
			copied := record
			if record.ParentSessionID == sessionID {
				return copied, true
			}
			if fallback == nil {
				fallback = &copied
			}
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return childSessionRecord{}, false
}

// clear drops every retained record and removes the document (best effort).
func (s *childSessionStore) clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.items = nil
	path := s.path
	s.mu.Unlock()
	if path != "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			s.logf("remove child-session document: %v", err)
		}
	}
}

// pruneLocked enforces the TTL and count bounds (the caller holds s.mu).
func (s *childSessionStore) pruneLocked(now time.Time) {
	cutoff := now.Add(-childSessionTTL)
	kept := make([]childSessionRecord, 0, len(s.items))
	for _, record := range s.items {
		if historyRecordTime(record.Updated, record.Finished, record.Started).Before(cutoff) {
			continue
		}
		kept = append(kept, record)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		return historyRecordTime(kept[i].Updated, kept[i].Finished, kept[i].Started).
			After(historyRecordTime(kept[j].Updated, kept[j].Finished, kept[j].Started))
	})
	if len(kept) > childSessionKeep {
		kept = kept[:childSessionKeep]
	}
	s.items = kept
}

// persistLocked writes the document atomically (the caller holds s.mu).
func (s *childSessionStore) persistLocked() {
	if s.path == "" {
		return
	}
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		s.logf("create child-session directory: %v", err)
		return
	}
	document := childSessionFile{Version: childSessionVersion, Children: s.items}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		s.logf("encode child-session document: %v", err)
		return
	}
	temporary, err := os.CreateTemp(directory, ".child-sessions-*.tmp")
	if err != nil {
		s.logf("create temporary child-session document: %v", err)
		return
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if _, err := temporary.Write(data); err != nil {
		cleanup()
		s.logf("write temporary child-session document: %v", err)
		return
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		s.logf("sync temporary child-session document: %v", err)
		return
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		s.logf("close temporary child-session document: %v", err)
		return
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		_ = os.Remove(temporaryPath)
		s.logf("replace child-session document %q: %v", s.path, err)
		return
	}
	if handle, err := os.Open(directory); err == nil {
		_ = handle.Sync()
		_ = handle.Close()
	}
}

// quarantine moves an unusable document out of the way and logs why.
func (s *childSessionStore) quarantine(reason string) {
	backup := fmt.Sprintf("%s.corrupt-%d", s.path, time.Now().UTC().UnixNano())
	message := reason
	if err := os.Rename(s.path, backup); err != nil {
		message += fmt.Sprintf(" (could not quarantine the file: %v)", err)
	} else {
		message += " (quarantined as " + backup + ")"
	}
	s.logf("%s; starting with an empty child-session store", message)
}

// logf reports one store warning through the module logger.
func (s *childSessionStore) logf(format string, args ...any) {
	if s == nil || s.logger == nil {
		return
	}
	s.logger.Printf("[agent] "+format, args...)
}

// historyRecordTime is the timestamp a record is pruned/ordered by.
func historyRecordTime(updated, finished, started time.Time) time.Time {
	if !updated.IsZero() {
		return updated
	}
	if !finished.IsZero() {
		return finished
	}
	return started
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
		left.Iterations == right.Iterations
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
