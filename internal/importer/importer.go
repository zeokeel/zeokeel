package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/store"
)

// Sentinel errors. Callers classify failures with errors.Is.
var (
	// ErrNotInstalled reports that the DSH data directory does not exist.
	ErrNotInstalled = errors.New("importer: DSH data directory not found")
	// ErrNoStore reports a real (non-dry-run) import without a destination.
	ErrNoStore = errors.New("importer: destination store is required")
	// ErrZstdUnavailable reports a compressed session when the zstd binary
	// needed to decompress it is not installed.
	ErrZstdUnavailable = errors.New("importer: zstd binary is required to read compressed DSH sessions")
)

const (
	// DefaultDSHHomeName is the DSH data directory name inside the user's home.
	DefaultDSHHomeName = ".dsh"
	// sessionsDirName/attachmentsDirName/profilesDirName are DSH subdirectories.
	sessionsDirName    = "sessions"
	credentialsName    = ".credentials.yaml"
	attachmentsDirName = "attachments"
	profilesDirName    = "profiles"
)

// Workspace is one DSH workspace bucket (the encoded directory under
// sessions/). CwdHint is a best-effort decode of the directory name; the
// authoritative path is taken from each session header during import.
type Workspace struct {
	Encoded     string
	CwdHint     string
	SessionDirs []string
}

// Layout is the detected DSH installation.
type Layout struct {
	Home            string
	SessionsRoot    string
	CredentialsPath string
	AttachmentsRoot string
	ProfilesRoot    string
	Workspaces      []Workspace
	// SessionFiles lists every discovered session JSONL file (plain or
	// zstd-compressed), absolute paths.
	SessionFiles []string
	// CompressedSessions counts files that need zstd decompression.
	CompressedSessions int
	// ZstdAvailable reports whether the zstd binary can be found.
	ZstdAvailable bool
}

// DefaultDSHHome returns the conventional DSH data directory (~/.dsh).
func DefaultDSHHome() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return DefaultDSHHomeName
	}
	return filepath.Join(home, DefaultDSHHomeName)
}

// Probe inspects dshHome and returns the detected layout. A missing directory
// returns ErrNotInstalled; a directory without sessions is a valid (empty)
// layout so the caller can report "nothing to import".
func Probe(dshHome string) (Layout, error) {
	home := strings.TrimSpace(dshHome)
	if home == "" {
		home = DefaultDSHHome()
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return Layout{}, fmt.Errorf("importer: resolve DSH home %q: %w", home, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return Layout{}, fmt.Errorf("%w: %s", ErrNotInstalled, abs)
		}
		return Layout{}, fmt.Errorf("importer: inspect DSH home %q: %w", abs, err)
	}
	if !info.IsDir() {
		return Layout{}, fmt.Errorf("%w: %s is not a directory", ErrNotInstalled, abs)
	}

	layout := Layout{
		Home:            abs,
		SessionsRoot:    filepath.Join(abs, sessionsDirName),
		CredentialsPath: filepath.Join(abs, credentialsName),
		AttachmentsRoot: filepath.Join(abs, attachmentsDirName),
		ProfilesRoot:    filepath.Join(abs, profilesDirName),
		ZstdAvailable:   zstdAvailable(),
	}
	entries, err := os.ReadDir(layout.SessionsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return layout, nil
		}
		return Layout{}, fmt.Errorf("importer: read sessions directory: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		ws, files, compressed := scanWorkspace(layout.SessionsRoot, entry.Name())
		if ws == nil {
			continue
		}
		layout.Workspaces = append(layout.Workspaces, *ws)
		layout.SessionFiles = append(layout.SessionFiles, files...)
		layout.CompressedSessions += compressed
	}
	sort.Strings(layout.SessionFiles)
	return layout, nil
}

// scanWorkspace finds the session files of one workspace bucket.
func scanWorkspace(sessionsRoot, encoded string) (*Workspace, []string, int) {
	base := filepath.Join(sessionsRoot, encoded)
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, nil, 0
	}
	ws := &Workspace{Encoded: encoded, CwdHint: decodeWorkspaceCwd(encoded)}
	var files []string
	compressed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(base, entry.Name())
		path, ok := sessionFileIn(dir)
		if !ok {
			continue
		}
		ws.SessionDirs = append(ws.SessionDirs, dir)
		files = append(files, path)
		if strings.HasSuffix(path, ".zstd") {
			compressed++
		}
	}
	if len(ws.SessionDirs) == 0 {
		return nil, nil, 0
	}
	return ws, files, compressed
}

// manifestSessionID records the real DSH session id from the session header,
// falling back to the path-derived identity only when the header has none.
func manifestSessionID(headerID, sessionsRoot, sessionFile string) string {
	if id := strings.TrimSpace(headerID); id != "" {
		return id
	}
	id, _ := sessionPathIdentity(sessionsRoot, sessionFile)
	return id
}

// sessionFileIn returns the session JSONL path inside one session directory.
// Newer DSH builds write session.v4.jsonl.zstd; uncompressed and older names
// are accepted so exported/hand-copied data also imports.
func sessionFileIn(dir string) (string, bool) {
	for _, name := range []string{
		"session.v4.jsonl.zstd",
		"session.v4.jsonl",
		"session.v3.jsonl.zstd",
		"session.v3.jsonl",
		"session.jsonl.zstd",
		"session.jsonl",
	} {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, true
		}
	}
	return "", false
}

// decodeWorkspaceCwd best-effort decodes DSH's encoded workspace directory
// ("--data-mygo--" -> "/data/mygo"). Session headers carry the authoritative
// cwd; this is only a hint for the layout report.
func decodeWorkspaceCwd(encoded string) string {
	trimmed := strings.TrimPrefix(strings.TrimSuffix(encoded, "--"), "--")
	if trimmed == "" {
		return ""
	}
	return "/" + strings.ReplaceAll(trimmed, "-", "/")
}

// Options configures one import run.
type Options struct {
	// DSHHome is the DSH data directory; empty means DefaultDSHHome().
	DSHHome string
	// Store is the destination. Required unless DryRun is set.
	Store *store.Store
	// DryRun analyzes and reports without writing anything.
	DryRun bool
	// IncludeSubagentSessions imports sessions whose header origin is
	// "subagent" (default false: they are child runs of a parent session).
	IncludeSubagentSessions bool
	// MaxSessions limits how many session files are processed (0 = all).
	MaxSessions int
	// TitlePrefix is prepended to imported session titles.
	TitlePrefix string
	// Now is the clock for synthetic message timestamps; nil means time.Now.
	Now func() time.Time
	// ManifestPath enables import idempotency. When non-empty, the importer
	// records every imported DSH session (id, source size/mtime/SHA-256) in
	// this JSON file and skips sessions that were already imported with the
	// same content on a later run. Empty keeps the historical behaviour: every
	// run imports everything again (the CLI can pass a data-directory path).
	ManifestPath string
}

// SessionError is one session that failed to import; the rest of the run
// continues (failure isolation).
type SessionError struct {
	SessionFile string `json:"session_file"`
	SessionID   string `json:"session_id,omitempty"`
	Error       string `json:"error"`
}

// WorkspaceReport summarizes one DSH workspace bucket.
type WorkspaceReport struct {
	Cwd           string `json:"cwd"`
	WorkspaceID   string `json:"workspace_id,omitempty"`
	Name          string `json:"name"`
	Sessions      int    `json:"sessions"`
	Messages      int    `json:"messages"`
	CreatedInZeo  bool   `json:"created_workspace"`
	DestinationOK bool   `json:"destination_exists"`
}

// Report is the machine-readable result of one import run.
type Report struct {
	DryRun           bool   `json:"dry_run"`
	DSHHome          string `json:"dsh_home"`
	SessionsFound    int    `json:"sessions_found"`
	SessionsImported int    `json:"sessions_imported"`
	SessionsSkipped  int    `json:"sessions_skipped"`
	SessionsFailed   int    `json:"sessions_failed"`
	MessagesImported int    `json:"messages_imported"`
	SystemMessages   int    `json:"system_messages_skipped"`
	ToolCallsSeen    int    `json:"tool_calls_seen"`
	ToolResultsSeen  int    `json:"tool_results_seen"`
	CompressedInput  bool   `json:"compressed_input"`
	// ImagesDropped counts image content parts that were not imported (the
	// minimal importer only migrates text conversations).
	ImagesDropped int `json:"images_dropped,omitempty"`
	// Skips breaks the skipped work down by reason so a report explains what
	// was filtered instead of only showing one opaque skipped count. Keys use
	// the constants below: system_injected, step_assistant, subagent,
	// bad_session, unsupported_version, images_dropped, idempotent,
	// empty_session, workspace_error. Some reasons count messages/parts and
	// others count sessions; the key names say which data was skipped.
	Skips       map[string]int    `json:"skips,omitempty"`
	Credentials []CredentialRef   `json:"credentials,omitempty"`
	Workspaces  []WorkspaceReport `json:"workspaces,omitempty"`
	Warnings    []string          `json:"warnings,omitempty"`
	Errors      []SessionError    `json:"errors,omitempty"`
}

// Skip reasons reported in Report.Skips.
const (
	// SkipSystemInjected counts user/message events with source.kind != "user"
	// (DSH-injected per-turn context) that are not imported as user messages.
	SkipSystemInjected = "system_injected"
	// SkipStepAssistant counts assistant/message events without visible text
	// (step-level reasoning/tool-call events) that are not imported.
	SkipStepAssistant = "step_assistant"
	// SkipSubagent counts subagent child sessions skipped by default.
	SkipSubagent = "subagent"
	// SkipBadSession counts sessions that could not be parsed/imported.
	SkipBadSession = "bad_session"
	// SkipUnsupportedVersion counts sessions whose header format version this
	// build does not understand.
	SkipUnsupportedVersion = "unsupported_version"
	// SkipImagesDropped counts image content parts that were not imported.
	SkipImagesDropped = "images_dropped"
	// SkipIdempotent counts sessions already imported with the same content
	// (only possible with Options.ManifestPath).
	SkipIdempotent = "idempotent"
	// SkipEmptySession counts sessions with no importable message.
	SkipEmptySession = "empty_session"
	// SkipWorkspaceError counts sessions whose destination workspace could not
	// be resolved/created.
	SkipWorkspaceError = "workspace_error"
)

// noteSkip increments one reason counter, creating the map on first use.
func (r *Report) noteSkip(reason string, n int) {
	if r == nil || n <= 0 {
		return
	}
	if r.Skips == nil {
		r.Skips = make(map[string]int)
	}
	r.Skips[reason] += n
}

// Import migrates the DSH sessions under opts.DSHHome into opts.Store.
// It never aborts the whole run for one bad session: parse/store failures are
// recorded in Report.Errors and the next session continues. A missing DSH
// installation returns ErrNotInstalled so the caller can say "nothing found".
// With Options.ManifestPath the run is idempotent across invocations.
func Import(ctx context.Context, opts Options) (*Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	layout, err := Probe(opts.DSHHome)
	if err != nil {
		return nil, err
	}
	if !opts.DryRun && opts.Store == nil {
		return nil, ErrNoStore
	}
	manifest, err := loadManifest(opts.ManifestPath)
	if err != nil {
		return nil, err
	}
	report := &Report{DryRun: opts.DryRun, DSHHome: layout.Home}
	if len(layout.SessionFiles) == 0 {
		report.Warnings = append(report.Warnings, "no DSH sessions found under "+layout.SessionsRoot)
		return report, nil
	}
	if layout.CompressedSessions > 0 {
		report.CompressedInput = true
		if !layout.ZstdAvailable {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("%d compressed session(s) need the zstd binary; none could be read (install zstd or export JSONL from DSH)", layout.CompressedSessions))
		}
	}

	// Credentials: names only. Never values, never written.
	if refs, cerr := InspectCredentials(layout.CredentialsPath); cerr != nil {
		report.Warnings = append(report.Warnings, "credentials: "+cerr.Error())
	} else {
		report.Credentials = refs
		if len(refs) > 0 {
			report.Warnings = append(report.Warnings,
				"DSH credentials found (names only, values never read); re-enter the API key in ZeoKeel Settings > Models")
		}
	}

	existing, lerr := listDestinations(opts.Store, opts.DryRun)
	if lerr != nil {
		return nil, lerr
	}
	workspaces := make(map[string]*WorkspaceReport) // cwd -> report row
	workspaceIDs := make(map[string]string)         // cwd -> ZeoKeel workspace id
	processed := 0
	for _, sessionFile := range layout.SessionFiles {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if opts.MaxSessions > 0 && processed >= opts.MaxSessions {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("stopped after %d session(s) (MaxSessions)", opts.MaxSessions))
			break
		}
		processed++
		report.SessionsFound++

		// Idempotency: the manifest records the imported source's size and
		// SHA-256, so a repeated run skips unchanged sessions before parsing
		// them (and before creating duplicate ZeoKeel sessions).
		key := manifestKey(layout.Home, layout.SessionsRoot, sessionFile)
		var identity sourceIdentity
		if manifest != nil {
			identity, err = inspectSourceFile(sessionFile)
			if err != nil {
				report.SessionsFailed++
				report.noteSkip(SkipBadSession, 1)
				report.Errors = append(report.Errors, SessionError{SessionFile: sessionFile, Error: "inspect source: " + err.Error()})
				continue
			}
			if manifest.alreadyImported(key, identity) {
				report.SessionsSkipped++
				report.noteSkip(SkipIdempotent, 1)
				continue
			}
		}

		sess, perr := ParseSessionFile(sessionFile)
		if perr != nil {
			report.Errors = append(report.Errors, SessionError{SessionFile: sessionFile, Error: perr.Error()})
			if errors.Is(perr, ErrUnsupportedVersion) {
				// A future DSH format is reported explicitly instead of being
				// half-imported under v4 assumptions.
				report.SessionsSkipped++
				report.noteSkip(SkipUnsupportedVersion, 1)
			} else {
				report.SessionsFailed++
				report.noteSkip(SkipBadSession, 1)
			}
			continue
		}
		if !opts.IncludeSubagentSessions && isSubagentSession(sess) {
			report.SessionsSkipped++
			report.noteSkip(SkipSubagent, 1)
			continue
		}
		// Message-level filtering diagnostics only count sessions that are in
		// the import scope, so a default run (subagents skipped) does not
		// report injected context from child sessions it never considered.
		report.noteSkip(SkipSystemInjected, sess.SystemInjected)
		report.noteSkip(SkipStepAssistant, sess.StepAssistant)
		report.noteSkip(SkipImagesDropped, sess.Images)
		report.ImagesDropped += sess.Images
		if len(sess.Messages) == 0 {
			report.SessionsSkipped++
			report.noteSkip(SkipEmptySession, 1)
			continue
		}

		cwd := strings.TrimSpace(sess.Cwd)
		wsReport := workspaces[cwd]
		if wsReport == nil {
			wsReport = &WorkspaceReport{
				Cwd:  cwd,
				Name: workspaceName(cwd),
			}
			if info, serr := os.Stat(cwd); cwd == "" || (serr == nil && info.IsDir()) {
				wsReport.DestinationOK = true
			}
			workspaces[cwd] = wsReport
			report.Workspaces = append(report.Workspaces, *wsReport)
		}
		if !opts.DryRun {
			id, created, werr := ensureWorkspace(opts.Store, existing, cwd, wsReport.Name)
			if werr != nil {
				report.SessionsFailed++
				report.noteSkip(SkipWorkspaceError, 1)
				report.Errors = append(report.Errors, SessionError{SessionFile: sessionFile, SessionID: sess.ID, Error: "workspace: " + werr.Error()})
				continue
			}
			workspaceIDs[cwd] = id
			updateWorkspaceReport(report, cwd, func(r *WorkspaceReport) {
				r.WorkspaceID = id
				// A later session in the same workspace must not overwrite a
				// real creation with false (previous report bug).
				r.CreatedInZeo = r.CreatedInZeo || created
			})
			if id == "" {
				report.SessionsFailed++
				report.noteSkip(SkipWorkspaceError, 1)
				report.Errors = append(report.Errors, SessionError{SessionFile: sessionFile, SessionID: sess.ID, Error: "workspace resolve returned empty id"})
				continue
			}
		}

		imported, zeoSessionID, merr := importSession(opts, sess, workspaceIDs[cwd])
		report.MessagesImported += imported
		report.SystemMessages += sess.SystemMessages
		report.ToolCallsSeen += sess.ToolCalls
		report.ToolResultsSeen += sess.ToolResults
		updateWorkspaceReport(report, cwd, func(r *WorkspaceReport) {
			r.Sessions++
			r.Messages += imported
		})
		if merr != nil {
			report.SessionsFailed++
			report.Errors = append(report.Errors, SessionError{SessionFile: sessionFile, SessionID: sess.ID, Error: merr.Error()})
			report.noteSkip(SkipBadSession, 1)
			continue
		}
		report.SessionsImported++
		if manifest != nil {
			manifest.record(key, manifestEntry{
				DSHHome:             layout.Home,
				SessionID:           manifestSessionID(sess.ID, layout.SessionsRoot, sessionFile),
				SourceFile:          sessionFile,
				SourceSize:          identity.Size,
				SourceMTimeUnixNano: identity.ModTimeUnixNano,
				SHA256:              identity.SHA256,
				ImportedAt:          opts.now().UTC(),
				ZeoKeelSessionID:    zeoSessionID,
				Title:               sess.Title,
			})
		}
	}
	sort.Slice(report.Workspaces, func(i, j int) bool { return report.Workspaces[i].Cwd < report.Workspaces[j].Cwd })
	if manifest != nil && !opts.DryRun {
		if serr := manifest.save(); serr != nil {
			report.Warnings = append(report.Warnings, "import manifest not saved: "+serr.Error())
		}
	}
	return report, nil
}

// isSubagentSession reports whether the DSH header marks a child-agent run.
func isSubagentSession(sess *Session) bool {
	return strings.EqualFold(strings.TrimSpace(sess.Origin), "subagent") || sess.DelegationDepth > 0
}

// importSession persists one parsed session. It returns the number of
// conversation rows written (0 in dry-run), the created ZeoKeel session id
// (for the idempotency manifest) and an error when the store rejects a write;
// partial sessions are reported rather than rolled back because the store has
// no cross-session transaction for imports.
func importSession(opts Options, sess *Session, workspaceID string) (int, string, error) {
	if opts.DryRun {
		return len(sess.Messages), "", nil
	}
	title := buildTitle(opts.TitlePrefix, sess)
	created, err := opts.Store.CreateSession(workspaceID, title)
	if err != nil {
		return 0, "", fmt.Errorf("create session: %w", err)
	}
	base := sess.CreatedAt
	if base.IsZero() {
		base = opts.now()
	}
	written := 0
	for i, msg := range sess.Messages {
		if _, err := opts.Store.AppendMessage(store.Message{
			SessionID: created.ID,
			Role:      msg.Role,
			Content:   msg.Content,
			Reasoning: msg.Reasoning,
			CreatedAt: base.Add(time.Duration(i) * time.Millisecond),
			TurnID:    "import",
		}); err != nil {
			return written, created.ID, fmt.Errorf("append message %d: %w", i, err)
		}
		written++
	}
	return written, created.ID, nil
}

// buildTitle renders the imported session title.
func buildTitle(prefix string, sess *Session) string {
	title := strings.TrimSpace(sess.Title)
	if title == "" {
		if first := firstUserMessage(sess); first != "" {
			title = firstLine(first, 80)
		}
	}
	if title == "" {
		title = "Imported DSH session " + sess.ID
	}
	if p := strings.TrimSpace(prefix); p != "" {
		return p + " " + title
	}
	return title
}

// firstUserMessage returns the first user message content.
func firstUserMessage(sess *Session) string {
	for _, msg := range sess.Messages {
		if msg.Role == "user" && strings.TrimSpace(msg.Content) != "" {
			return msg.Content
		}
	}
	return ""
}

// firstLine returns the first non-empty line of text, capped at limit runes.
func firstLine(text string, limit int) string {
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			runes := []rune(line)
			if len(runes) > limit {
				return string(runes[:limit]) + "…"
			}
			return line
		}
	}
	return ""
}

// workspaceName derives the ZeoKeel workspace name for a DSH cwd.
func workspaceName(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "DSH imported"
	}
	base := filepath.Base(cwd)
	if base == "" || base == "/" || base == "." {
		return "DSH imported"
	}
	return base
}

// listDestinations loads the existing ZeoKeel workspaces by root (nil for a
// dry run, which must not touch the store).
func listDestinations(st *store.Store, dryRun bool) (map[string]*store.Workspace, error) {
	if dryRun || st == nil {
		return map[string]*store.Workspace{}, nil
	}
	list, err := st.ListWorkspaces()
	if err != nil {
		return nil, fmt.Errorf("importer: list destination workspaces: %w", err)
	}
	byRoot := make(map[string]*store.Workspace, len(list))
	for i := range list {
		byRoot[filepath.Clean(list[i].Root)] = &list[i]
	}
	return byRoot, nil
}

// ensureWorkspace returns the destination workspace id for cwd, creating one
// when no ZeoKeel workspace has that root yet. The second return value reports
// whether a workspace was created.
func ensureWorkspace(st *store.Store, existing map[string]*store.Workspace, cwd, name string) (string, bool, error) {
	key := filepath.Clean(cwd)
	if ws := existing[key]; ws != nil {
		return ws.ID, false, nil
	}
	if cwd == "" {
		// Empty root means the built-in default workspace (the data
		// directory). It always exists in the app store; the store API itself
		// cannot create it, so fail clearly instead of guessing.
		return "", false, errors.New("DSH session has no cwd and ZeoKeel has no default workspace to attach it to")
	}
	ws, err := st.CreateWorkspace(name, cwd)
	if err != nil {
		return "", false, fmt.Errorf("create workspace %q: %w", name, err)
	}
	existing[key] = ws
	return ws.ID, true, nil
}

// updateWorkspaceReport updates one report row by cwd.
func updateWorkspaceReport(report *Report, cwd string, mutate func(*WorkspaceReport)) {
	for i := range report.Workspaces {
		if report.Workspaces[i].Cwd == cwd {
			mutate(&report.Workspaces[i])
			return
		}
	}
}

// now returns the configured clock or time.Now.
func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}
