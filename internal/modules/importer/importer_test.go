package importer

// Tests for the DSH importer: JSONL parsing/filtering, preview counts/notes,
// store/workspace writes, idempotency, default source dir and module wiring.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeLogger records log lines.
type fakeLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *fakeLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

// --- fake durable store ----------------------------------------------------

type fakeSession struct {
	title    string
	messages []rig.Message
	created  time.Time
}

type fakeStore struct {
	mu         sync.Mutex
	sessions   map[string]*fakeSession
	events     []rig.SessionEvent
	createErr  map[string]error
	renameErr  error
	replaceErr error
	appendErr  error
}

func newFakeStore() *fakeStore {
	return &fakeStore{sessions: map[string]*fakeSession{}, createErr: map[string]error{}}
}

func (s *fakeStore) History() rig.History                 { return fakeHistory{store: s} }
func (s *fakeStore) CheckpointStore() rig.CheckpointStore { return fakeCheckpoints{} }

func (s *fakeStore) CreateSession(ctx context.Context, id, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.createErr[id]; err != nil {
		return err
	}
	if _, exists := s.sessions[id]; exists {
		return nil
	}
	s.sessions[id] = &fakeSession{title: title, created: time.Now()}
	return nil
}

func (s *fakeStore) DeleteSession(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	return nil
}

func (s *fakeStore) RenameSession(ctx context.Context, id, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.renameErr != nil {
		return s.renameErr
	}
	state, ok := s.sessions[id]
	if !ok {
		return errors.New("fake store: unknown session")
	}
	state.title = title
	return nil
}

func (s *fakeStore) Sessions(ctx context.Context) ([]contracts.SessionMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contracts.SessionMeta, 0, len(s.sessions))
	for id, state := range s.sessions {
		out = append(out, contracts.SessionMeta{
			ID: id, Title: state.title, MessageCount: len(state.messages),
			CreatedAt: state.created, UpdatedAt: state.created,
		})
	}
	return out, nil
}

func (s *fakeStore) GetSessionMeta(ctx context.Context, id string) (contracts.SessionMeta, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[id]
	if !ok {
		return contracts.SessionMeta{}, false, nil
	}
	return contracts.SessionMeta{ID: id, Title: state.title, MessageCount: len(state.messages)}, true, nil
}

func (s *fakeStore) ReplaceHistory(ctx context.Context, sessionID string, messages []rig.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.replaceErr != nil {
		return s.replaceErr
	}
	state, ok := s.sessions[sessionID]
	if !ok {
		return errors.New("fake store: unknown session")
	}
	state.messages = append([]rig.Message(nil), messages...)
	return nil
}

func (s *fakeStore) AppendEvent(ctx context.Context, event rig.SessionEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.appendErr != nil {
		return s.appendErr
	}
	event.Seq = int64(len(s.events) + 1)
	s.events = append(s.events, event)
	return nil
}

func (s *fakeStore) Events(ctx context.Context, sessionID string, from, to int64) ([]rig.SessionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]rig.SessionEvent, 0)
	for _, event := range s.events {
		if event.SessionID == sessionID && event.Seq >= from && (to <= 0 || event.Seq <= to) {
			out = append(out, event)
		}
	}
	return out, nil
}

func (s *fakeStore) Search(ctx context.Context, query string, limit int) ([]rig.SessionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]rig.SessionEvent(nil), s.events...), nil
}

func (s *fakeStore) Last(ctx context.Context, sessionID string, limit int) ([]rig.SessionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]rig.SessionEvent, 0)
	for _, event := range s.events {
		if event.SessionID == sessionID {
			out = append(out, event)
		}
	}
	return out, nil
}

func (s *fakeStore) messageTexts(sessionID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return nil
	}
	out := make([]string, 0, len(state.messages))
	for _, message := range state.messages {
		out = append(out, message.Text())
	}
	return out
}

func (s *fakeStore) sessionTitle(sessionID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state, ok := s.sessions[sessionID]; ok {
		return state.title
	}
	return ""
}

// fakeHistory implements rig.History.
type fakeHistory struct{ store *fakeStore }

func (h fakeHistory) Load(ctx context.Context, sessionID string) ([]rig.Message, error) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	state, ok := h.store.sessions[sessionID]
	if !ok {
		return nil, nil
	}
	return append([]rig.Message(nil), state.messages...), nil
}

func (h fakeHistory) Append(ctx context.Context, sessionID string, messages ...rig.Message) error {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	state, ok := h.store.sessions[sessionID]
	if !ok {
		return errors.New("fake store: unknown session")
	}
	state.messages = append(state.messages, messages...)
	return nil
}

// fakeCheckpoints is a no-op rig.CheckpointStore.
type fakeCheckpoints struct{}

func (fakeCheckpoints) Load(context.Context, string) (*rig.Checkpoint, error) { return nil, nil }
func (fakeCheckpoints) Save(context.Context, string, *rig.Checkpoint) error   { return nil }
func (fakeCheckpoints) Clear(context.Context, string) error                   { return nil }

// --- fake workspace --------------------------------------------------------

type workspaceCall struct {
	Name string
	Root string
}

type fakeWorkspace struct {
	mu         sync.Mutex
	calls      []workspaceCall
	workspaces []contracts.Workspace
	createErr  map[string]error
}

func newFakeWorkspace() *fakeWorkspace {
	return &fakeWorkspace{createErr: map[string]error{}}
}

func (w *fakeWorkspace) Create(ctx context.Context, name, root string) (contracts.Workspace, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, workspaceCall{Name: name, Root: root})
	if err := w.createErr[root]; err != nil {
		return contracts.Workspace{}, err
	}
	for _, workspace := range w.workspaces {
		if workspace.Root == root {
			return workspace, nil
		}
	}
	workspace := contracts.Workspace{ID: fmt.Sprintf("ws-%d", len(w.workspaces)+1), Name: name, Root: root, CreatedAt: time.Now()}
	w.workspaces = append(w.workspaces, workspace)
	return workspace, nil
}

func (w *fakeWorkspace) List(context.Context) ([]contracts.Workspace, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]contracts.Workspace(nil), w.workspaces...), nil
}

func (w *fakeWorkspace) Current(context.Context) (contracts.Workspace, bool, error) {
	return contracts.Workspace{}, false, nil
}

func (w *fakeWorkspace) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, errors.New("fake workspace: not implemented")
}

func (w *fakeWorkspace) Delete(context.Context, string) error { return nil }

func (w *fakeWorkspace) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (w *fakeWorkspace) ReadFile(context.Context, string, int) ([]byte, error) { return nil, nil }

func (w *fakeWorkspace) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

// --- DSH fixture helpers ---------------------------------------------------

// jsonLine encodes one JSONL fixture line.
func jsonLine(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(encoded) + "\n"
}

// writeSession writes one DSH session log below the DSH home.
func writeSession(t *testing.T, home, workspace, sessionID string, lines ...string) string {
	t.Helper()
	path := filepath.Join(home, "sessions", workspace, sessionID, sessionFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir session: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	return path
}

// sessionHeaderLine builds a DSH session header.
func sessionHeaderLine(t *testing.T, version int, id, cwd string) string {
	t.Helper()
	return jsonLine(t, map[string]any{
		"type": "session", "version": version, "id": id,
		"createdAt": int64(1700000000000), "cwd": cwd,
	})
}

// userLine builds a user/message event.
func userLine(t *testing.T, seq int, kind string, parts ...map[string]any) string {
	t.Helper()
	content := make([]any, 0, len(parts))
	for _, part := range parts {
		content = append(content, part)
	}
	return jsonLine(t, map[string]any{
		"type": "user/message", "seq": seq,
		"data": map[string]any{
			"role": "user", "content": content,
			"source": map[string]any{"kind": kind},
		},
	})
}

// assistantLine builds an assistant/message event with nested content.
func assistantLine(t *testing.T, seq int, parts ...map[string]any) string {
	t.Helper()
	content := make([]any, 0, len(parts))
	for _, part := range parts {
		content = append(content, part)
	}
	return jsonLine(t, map[string]any{
		"type": "assistant/message", "seq": seq,
		"data": map[string]any{
			"turn": 1,
			"message": map[string]any{
				"role": "assistant", "content": content,
			},
		},
	})
}

func textPart(text string) map[string]any  { return map[string]any{"type": "text", "text": text} }
func thinkPart(text string) map[string]any { return map[string]any{"type": "reasoning", "text": text} }
func imagePart() map[string]any            { return map[string]any{"type": "image", "text": ""} }

// goodSessionLines builds a standard conversation fixture.
func goodSessionLines(t *testing.T, id, cwd string) []string {
	t.Helper()
	return []string{
		sessionHeaderLine(t, 4, id, cwd),
		userLine(t, 1, "user", textPart("Hello world")),
		assistantLine(t, 2, textPart("Hi there"), thinkPart("think")),
		userLine(t, 3, "time-context", textPart("injected context")),
		assistantLine(t, 4, thinkPart("step only")),
		jsonLine(t, map[string]any{"type": "tool/call", "seq": 5, "data": map[string]any{"name": "x"}}),
	}
}

// newTestService builds a service directly.
func newTestService(t *testing.T, cfg Config, store contracts.Store, workspace contracts.WorkspaceService) *service {
	t.Helper()
	return newService(cfg, store, workspace, &fakeLogger{})
}

func TestModuleDescriptorAndValidation(t *testing.T) {
	descriptor := Module()
	if descriptor.ID != ModuleID || ModuleID != "zeokeel/importer" {
		t.Fatalf("module ID = %q", descriptor.ID)
	}
	if descriptor.Name == "" || descriptor.Version != Version {
		t.Fatalf("name/version = %q/%q", descriptor.Name, descriptor.Version)
	}
	if len(descriptor.Provides) != 1 || descriptor.Provides[0].Name != contracts.KeyImporter.Name() {
		t.Fatalf("provides = %+v, want KeyImporter", descriptor.Provides)
	}
	required := make(map[string]bool, len(descriptor.Requires))
	for _, requirement := range descriptor.Requires {
		required[requirement.Name] = true
	}
	for _, name := range []string{contracts.KeyStore.Name(), contracts.KeyWorkspace.Name(), contracts.KeyLogger.Name()} {
		if !required[name] {
			t.Fatalf("requires = %+v, missing %q", descriptor.Requires, name)
		}
	}
	if descriptor.Validate == nil {
		t.Fatal("module has no config validation hook")
	}
	if err := descriptor.Validate(Config{}); err != nil {
		t.Fatalf("Validate(empty) = %v", err)
	}
	if err := descriptor.Validate(Config{SourceDir: "bad\x00dir"}); err == nil {
		t.Fatal("Validate accepted a NUL sourceDir")
	}
}

func TestModuleActivationProvidesImporter(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeSession(t, home, "ws", "s1", goodSessionLines(t, "s1", cwd)...)
	root := cordis.Root()
	defer func() { _ = root.DisposeAll() }()
	store := newFakeStore()
	workspace := newFakeWorkspace()
	if _, err := root.Provide(contracts.KeyStore, contracts.Store(store)); err != nil {
		t.Fatalf("provide store: %v", err)
	}
	if _, err := root.Provide(contracts.KeyWorkspace, contracts.WorkspaceService(workspace)); err != nil {
		t.Fatalf("provide workspace: %v", err)
	}
	logger := &fakeLogger{}
	if _, err := root.Provide(contracts.KeyLogger, contracts.Logger(logger)); err != nil {
		t.Fatalf("provide logger: %v", err)
	}
	registry := module.NewRegistry()
	registry.AddBuiltin(contracts.KeyStore).AddBuiltin(contracts.KeyWorkspace).AddBuiltin(contracts.KeyLogger)
	if err := registry.Register(Module()); err != nil {
		t.Fatalf("register: %v", err)
	}
	loader, err := module.Load(root, registry, []module.Entry{{
		ID: "importer", Module: ModuleID, Config: map[string]any{"sourceDir": home},
	}})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	defer func() { _ = loader.Close() }()
	importer, err := root.Require(contracts.KeyImporter)
	if err != nil {
		t.Fatalf("require KeyImporter: %v", err)
	}
	preview, err := importer.Scan(context.Background(), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if preview.Sessions != 1 || preview.Messages != 2 {
		t.Fatalf("preview = %+v", preview)
	}
	result, err := importer.Import(context.Background(), "")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.ImportedSessions != 1 || result.ImportedMessages != 2 {
		t.Fatalf("result = %+v", result)
	}
}

func TestScanPreview(t *testing.T) {
	home := t.TempDir()
	cwdA := t.TempDir()
	cwdB := t.TempDir()
	writeSession(t, home, "ws-a", "s1", goodSessionLines(t, "s1", cwdA)...)
	writeSession(t, home, "ws-b", "s2",
		sessionHeaderLine(t, 4, "s2", cwdB),
		userLine(t, 1, "user", textPart("Second question")),
		assistantLine(t, 2, textPart("Second answer")),
	)
	// A visible user message with an image part: message imported, attachment
	// counted as skipped with a note.
	writeSession(t, home, "ws-a", "s3",
		sessionHeaderLine(t, 4, "s3", cwdA),
		userLine(t, 1, "user", textPart("look at this"), imagePart()),
	)
	// Unsupported version.
	writeSession(t, home, "bad", "v3",
		sessionHeaderLine(t, 3, "v3", cwdA),
		userLine(t, 1, "user", textPart("old format")),
	)
	// Corrupt JSONL.
	writeSession(t, home, "bad", "corrupt",
		sessionHeaderLine(t, 4, "corrupt", cwdA),
		"{not json\n",
	)
	// Compressed session.
	compressedDir := filepath.Join(home, "sessions", "zip", "zstd")
	if err := os.MkdirAll(compressedDir, 0o700); err != nil {
		t.Fatalf("mkdir compressed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(compressedDir, sessionFileName+".zstd"), []byte("compressed"), 0o600); err != nil {
		t.Fatalf("write compressed: %v", err)
	}

	service := newTestService(t, Config{SourceDir: home}, newFakeStore(), newFakeWorkspace())
	preview, err := service.Scan(context.Background(), "")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if preview.SourceDir != home {
		t.Fatalf("SourceDir = %q, want %q", preview.SourceDir, home)
	}
	if preview.Sessions != 3 || preview.Messages != 5 {
		t.Fatalf("preview sessions/messages = %d/%d, want 3/5", preview.Sessions, preview.Messages)
	}
	if preview.Workspaces != 2 {
		t.Fatalf("preview workspaces = %d, want 2", preview.Workspaces)
	}
	if preview.Skipped != 4 {
		t.Fatalf("preview skipped = %d, want 4 (version+corrupt+zstd+attachment)", preview.Skipped)
	}
	notes := strings.Join(preview.Notes, "\n")
	for _, want := range []string{"unsupported", "skipped", "zstd", "attachment"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("notes %q missing %q", notes, want)
		}
	}

	// An existing but empty DSH home is a valid preview with zeros.
	emptyPreview, err := service.Scan(context.Background(), t.TempDir())
	if err != nil || emptyPreview.Sessions != 0 || emptyPreview.Messages != 0 || emptyPreview.Skipped != 0 {
		t.Fatalf("empty scan = %+v/%v", emptyPreview, err)
	}
	// A missing/non-directory source fails readably.
	if _, err := service.Scan(context.Background(), filepath.Join(home, "missing")); err == nil {
		t.Fatal("Scan(missing dir) returned nil error")
	}
	if _, err := service.Scan(context.Background(), writeSession(t, home, "ws-a", "s1", goodSessionLines(t, "s1", cwdA)...)); err == nil {
		t.Fatal("Scan(file) returned nil error")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Scan(cancelled, home); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Scan = %v, want context.Canceled", err)
	}
}

func TestParseSessionFiltering(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	path := writeSession(t, home, "ws", "s1", goodSessionLines(t, "s1", cwd)...)
	parsed, err := parseSessionFile(path)
	if err != nil {
		t.Fatalf("parseSessionFile: %v", err)
	}
	if parsed.ID != "s1" || parsed.Cwd != cwd || parsed.CreatedAt.IsZero() {
		t.Fatalf("parsed session = %+v", parsed)
	}
	if len(parsed.Messages) != 2 {
		t.Fatalf("messages = %+v, want user+assistant only", parsed.Messages)
	}
	if parsed.Messages[0].Role != "user" || parsed.Messages[0].Text != "Hello world" {
		t.Fatalf("user message = %+v", parsed.Messages[0])
	}
	if parsed.Messages[1].Role != "assistant" || parsed.Messages[1].Text != "Hi there" || parsed.Messages[1].Reasoning != "think" {
		t.Fatalf("assistant message = %+v", parsed.Messages[1])
	}
	if parsed.DroppedAttachments != 0 {
		t.Fatalf("dropped attachments = %d", parsed.DroppedAttachments)
	}

	// A missing source.kind is not a genuine user message.
	path = writeSession(t, home, "ws", "s2",
		sessionHeaderLine(t, 4, "s2", cwd),
		userLine(t, 1, "", textPart("no source kind")),
	)
	parsed, err = parseSessionFile(path)
	if err != nil || len(parsed.Messages) != 0 {
		t.Fatalf("empty source kind = %+v/%v, want no messages", parsed, err)
	}
	if _, err := parseSessionFile(writeSession(t, home, "ws", "s3", sessionHeaderLine(t, 3, "s3", cwd))); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("version 3 = %v, want ErrUnsupportedVersion", err)
	}
	if _, err := parseSessionFile(writeSession(t, home, "ws", "s4", "{broken\n")); err == nil {
		t.Fatal("corrupt header parsed without error")
	}
}

func TestImportWritesSessionsHistoryAndWorkspaces(t *testing.T) {
	home := t.TempDir()
	cwdA := t.TempDir()
	cwdB := t.TempDir()
	longPrompt := strings.Repeat("长", 100)
	writeSession(t, home, "ws-a", "s1", goodSessionLines(t, "s1", cwdA)...)
	writeSession(t, home, "ws-b", "s2",
		sessionHeaderLine(t, 4, "s2", cwdB),
		userLine(t, 1, "user", textPart(longPrompt)),
		assistantLine(t, 2, textPart("answer"), thinkPart("why")),
	)
	store := newFakeStore()
	workspace := newFakeWorkspace()
	service := newTestService(t, Config{SourceDir: home}, store, workspace)

	result, err := service.Import(context.Background(), "")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.ImportedSessions != 2 || result.ImportedMessages != 4 ||
		result.ImportedWorkspaces != 2 || result.Skipped != 0 || len(result.Errors) != 0 {
		t.Fatalf("result = %+v", result)
	}
	if title := store.sessionTitle("dsh-s1"); title != "Hello world" {
		t.Fatalf("s1 title = %q", title)
	}
	if title := store.sessionTitle("dsh-s2"); len([]rune(title)) != titleRunes {
		t.Fatalf("long title runes = %d, want %d", len([]rune(title)), titleRunes)
	}
	texts := store.messageTexts("dsh-s1")
	if len(texts) != 2 || texts[0] != "Hello world" || texts[1] != "Hi there" {
		t.Fatalf("s1 history = %v", texts)
	}
	store.mu.Lock()
	reasoning := store.sessions["dsh-s1"].messages[1].Reasoning
	store.mu.Unlock()
	if reasoning != "think" {
		t.Fatalf("assistant reasoning = %q, want think", reasoning)
	}
	// The import event records the DSH source ID.
	events, err := store.Events(context.Background(), "dsh-s1", 0, 0)
	if err != nil || len(events) != 1 || events[0].Type != "import" {
		t.Fatalf("import events = %+v/%v", events, err)
	}
	var payload map[string]string
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil || payload["source"] != "s1" {
		t.Fatalf("import payload = %s/%v", events[0].Payload, err)
	}
	workspace.mu.Lock()
	calls := append([]workspaceCall(nil), workspace.calls...)
	workspace.mu.Unlock()
	if len(calls) != 2 || calls[0].Root != cwdA || calls[1].Root != cwdB {
		t.Fatalf("workspace calls = %+v", calls)
	}

	// A second import is idempotent: every parsed session is skipped.
	second, err := service.Import(context.Background(), home)
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if second.ImportedSessions != 0 || second.ImportedMessages != 0 || second.Skipped != 2 {
		t.Fatalf("second import = %+v, want all sessions skipped", second)
	}
	workspace.mu.Lock()
	afterCalls := len(workspace.calls)
	workspace.mu.Unlock()
	if afterCalls != 2 {
		t.Fatalf("second import registered workspaces again: %d calls", afterCalls)
	}
}

func TestImportAttachmentSkipAndDefaults(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeSession(t, home, "ws", "s1",
		sessionHeaderLine(t, 4, "s1", cwd),
		userLine(t, 1, "user", textPart("see image"), imagePart()),
		userLine(t, 2, "user", textPart("after image")),
		assistantLine(t, 3, textPart("ok")),
	)
	store := newFakeStore()
	service := newTestService(t, Config{}, store, nil)
	result, err := service.Import(context.Background(), home)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.ImportedSessions != 1 || result.ImportedMessages != 3 {
		t.Fatalf("result = %+v", result)
	}
	if result.Skipped != 1 {
		t.Fatalf("skipped = %d, want the dropped image part", result.Skipped)
	}

	// The default source directory fills in an empty dir argument.
	if preview, err := service.Scan(context.Background(), ""); err == nil || preview.Sessions != 0 {
		if err == nil {
			t.Fatalf("Scan without configured or passed dir = %+v, want an error", preview)
		}
	}
	service.sourceDir = home
	if _, err := service.Scan(context.Background(), ""); err != nil {
		t.Fatalf("Scan(default dir): %v", err)
	}
}

func TestImportPerSessionErrorsDoNotAbort(t *testing.T) {
	home := t.TempDir()
	cwdA := t.TempDir()
	cwdB := t.TempDir()
	writeSession(t, home, "ws-a", "s1", goodSessionLines(t, "s1", cwdA)...)
	writeSession(t, home, "ws-b", "s2", goodSessionLines(t, "s2", cwdB)...)
	store := newFakeStore()
	store.createErr["dsh-s1"] = errors.New("disk full")
	workspace := newFakeWorkspace()
	workspace.createErr[cwdB] = errors.New("workspace locked")
	service := newTestService(t, Config{SourceDir: home}, store, workspace)

	result, err := service.Import(context.Background(), "")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.ImportedSessions != 1 || result.ImportedMessages != 2 {
		t.Fatalf("result = %+v, want s2 imported despite s1 failing", result)
	}
	joined := strings.Join(result.Errors, "\n")
	if !strings.Contains(joined, "disk full") || !strings.Contains(joined, "workspace locked") {
		t.Fatalf("errors = %v", result.Errors)
	}
	if result.ImportedWorkspaces != 0 {
		// s1 failed before workspace registration and s2's workspace call failed.
		t.Fatalf("imported workspaces = %d, want 0", result.ImportedWorkspaces)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Import(cancelled, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Import = %v, want context.Canceled", err)
	}
}

func TestImportedTitleFallback(t *testing.T) {
	if title := importedTitle(nil); title != defaultImportedTitle {
		t.Fatalf("empty title = %q, want %q", title, defaultImportedTitle)
	}
	if title := importedTitle([]parsedMessage{{Role: "assistant", Text: "only assistant"}}); title != defaultImportedTitle {
		t.Fatalf("assistant-only title = %q", title)
	}
	if title := importedTitle([]parsedMessage{{Role: "user", Text: "  spaced  "}}); title != "spaced" {
		t.Fatalf("spaced title = %q", title)
	}
}

// TestImportUnknownDir covers the resolveDir error from Import.
func TestImportUnknownDir(t *testing.T) {
	service := newTestService(t, Config{}, newFakeStore(), nil)
	if _, err := service.Import(context.Background(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("Import(missing dir) returned nil error")
	}
}
