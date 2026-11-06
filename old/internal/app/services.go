package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/old/internal/agentteam"
	"github.com/zeokeel/zeokeel/old/internal/lsp"
	"github.com/zeokeel/zeokeel/old/internal/mcp"
	"github.com/zeokeel/zeokeel/old/internal/presets"
	"github.com/zeokeel/zeokeel/old/internal/prompt"
	"github.com/zeokeel/zeokeel/old/internal/provider/openai"
	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/session"
	"github.com/zeokeel/zeokeel/old/internal/skills"
	"github.com/zeokeel/zeokeel/old/internal/state"
	"github.com/zeokeel/zeokeel/old/internal/store"
	"github.com/zeokeel/zeokeel/old/internal/tools"
	"github.com/zeokeel/zeokeel/old/internal/websearch"
)

// DefaultInstruction is the agent's system prompt until prompts are editable.
const DefaultInstruction = `You are ZeoKeel, a helpful desktop AI agent. Answer concisely and accurately. Use tools when they help.`

// defaultApprovalTools is the setting's fallback for human-approval modes:
// only the tools that can escape the workspace sandbox (or have side effects
// outside it) prompt. Entries are prefix patterns; a trailing "*" is optional
// ("mcp__*" and "mcp__" are equivalent). The list is read when the agent is
// built, so a change applies to the next build.
//
// Workspace-confined file tools (write_file, edit_file, str_replace_editor) are
// deliberately absent: the workspace sandbox already confines them to the root,
// so an approval prompt there adds friction without adding a boundary.
// Read-only terminal/job operations are absent because the prompt tells the
// model to poll job_output and terminal_read.
const defaultApprovalTools = "bash,pwsh,terminal_open,terminal_send,terminal_signal,terminal_close,job_start,job_kill,enter_worktree,exit_worktree,web_search,web_fetch,mcp__*"

// autoReviewApprovalTools is the Auto review permission's fallback. Auto review
// is an experimental oversight mode: the model reviewer decides the calls that
// would otherwise prompt the user. It keeps the wider pre-convergence pattern
// set (including the sandbox-confined file editors) so converging the
// human-approval default above does not silently stop the reviewer from seeing
// file mutations. Read-only str_replace_editor "view" calls are still exempt, in
// both sets (see toolNeedsApprovalWithArgs).
const autoReviewApprovalTools = "bash,write_file,edit_file,str_replace_editor,terminal_open,terminal_send,terminal_signal,terminal_close,job_start,job_kill,pwsh,enter_worktree,exit_worktree,web_search,web_fetch,mcp__*"

// AgentBundle is the wired agent plus the resources it owns (MCP servers and
// the async team manager when Agent Teams is enabled).
type AgentBundle struct {
	Agent *runtime.Agent
	Gate  *runtime.ApprovalQueue
	// TitleGenerator writes automatic session titles with the bundle's
	// provider. Nil falls back to the truncated first user message.
	TitleGenerator session.TitleGenerator
	// Close releases owned resources (MCP sessions and the team manager, which
	// cancels and waits for running teammates); may be nil.
	Close func() error
	// ToolNames is the registered tool-name list of this build's registry,
	// before preset filtering: the app installs it as the controller's
	// KnownTools so user preset allowlists are validated at load time.
	ToolNames []string
	// PresetWarnings holds build-time preset problems (for example an allowlist
	// that matched no registered tool) for the UI/log.
	PresetWarnings []string
}

// AgentFactory builds the runtime agent against the opened store. It may
// return (nil, nil) when no provider is configured.
type AgentFactory func(st *store.Store) (*AgentBundle, error)

// BuildServices opens the persistence, event and session layers for dir and
// returns a Controller plus a cleanup function. Agent may be nil; the
// Controller still browses history while Submit reports session.ErrNoAgent.
func BuildServices(dir string, agent *runtime.Agent) (*session.Controller, func() error, error) {
	return BuildServicesWith(dir, func(*store.Store) (*AgentBundle, error) {
		return &AgentBundle{Agent: agent}, nil
	})
}

// BuildServicesWith is BuildServices with a factory: it opens the store first,
// lets the factory build the agent against it (settings, tools, history), and
// then wires the Controller with the same store and the returned approval gate.
func BuildServicesWith(dir string, factory AgentFactory) (*session.Controller, func() error, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil, fmt.Errorf("app: build services: empty directory")
	}
	st, err := store.Open(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("app: build services: open store %q: %w", dir, err)
	}
	var bundle *AgentBundle
	if factory != nil {
		bundle, err = factory(st)
		if err != nil {
			_ = st.Close()
			return nil, nil, fmt.Errorf("app: build services: %w", err)
		}
	}
	var (
		agent    *runtime.Agent
		gate     *runtime.ApprovalQueue
		titleGen session.TitleGenerator
	)
	if bundle != nil {
		agent, gate, titleGen = bundle.Agent, bundle.Gate, bundle.TitleGenerator
	}
	broker := state.NewBroker()
	// Pass the registered tool names into the initial preset load so a user
	// preset with a misspelled tool fails closed at New (the previous
	// no-validation load would let it become active).
	var presetKnownTools []string
	if bundle != nil {
		presetKnownTools = bundle.ToolNames
	}
	ctrl, err := session.New(session.Config{
		Store:            st,
		Broker:           broker,
		Agent:            agent,
		Gate:             gate,
		TitleGenerator:   titleGen,
		PresetKnownTools: presetKnownTools,
		// Active goals drive continuation turns until the model marks them
		// complete, the per-session round budget (session.<id>.goal_rounds,
		// default 6, cap 12) is exhausted, or the user stops the session.
		GoalDriver: session.NewGoalRoundDriver(),
	})
	if err != nil {
		broker.Close()
		_ = st.Close()
		return nil, nil, fmt.Errorf("app: build services: %w", err)
	}
	// Keep the known-tool list on the controller for later ReloadPresets calls
	// (the settings UI reload path) and report an initial fail-closed load.
	if bundle != nil {
		ctrl.SetPresetKnownTools(bundle.ToolNames)
		if rerr := ctrl.LastPresetLoadError(); rerr != nil {
			log.Printf("zeokeel: presets: user presets rejected, built-ins active: %v", rerr)
		}
	}
	var (
		once       sync.Once
		cleanupErr error
	)
	cleanup := func() error {
		once.Do(func() {
			var errs []error
			if err := ctrl.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close session controller: %w", err))
			}
			if bundle != nil && bundle.Close != nil {
				if err := bundle.Close(); err != nil {
					errs = append(errs, fmt.Errorf("close agent bundle: %w", err))
				}
			}
			broker.Close()
			if err := st.Close(); err != nil {
				errs = append(errs, fmt.Errorf("close store: %w", err))
			}
			cleanupErr = errors.Join(errs...)
		})
		return cleanupErr
	}
	return ctrl, cleanup, nil
}

// storeHistory is the runtime.History adapter over the messages and
// tool_calls tables. The session controller owns both tables: it persists the
// user prompt before the turn's events are folded into the snapshot, the
// assistant text when the turn reaches a terminal phase, and every tool call
// as it starts and ends. Append is deliberately a no-op: letting the runtime
// append the turn as well wrote each user and assistant message twice, so the
// next provider request contained duplicates.
type storeHistory struct {
	st *store.Store
}

func (h *storeHistory) Messages(ctx context.Context, sessionID string) ([]runtime.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	msgs, err := h.st.ListMessages(sessionID)
	if err != nil {
		return nil, err
	}
	calls, err := h.st.ToolCallsForSession(sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]runtime.Message, 0, len(msgs)+2*len(calls))

	// Attachments were persisted with their owning user message. Rebuild the
	// same model-visible content the turn originally sent: bounded text blocks
	// plus image data URLs. A missing file is skipped rather than making the
	// whole session unreadable.
	attachmentInputs := make(map[string][]runtime.UserAttachment)
	attachments, aerr := h.st.ListAttachments(sessionID)
	if aerr != nil {
		return nil, aerr
	}
	for _, a := range attachments {
		if a.MessageID == "" {
			continue
		}
		switch a.Kind {
		case store.AttachmentKindText:
			data, rerr := h.st.ReadAttachmentData(a, session.MaxTextAttachmentBytes)
			if rerr != nil || len(data) == 0 {
				continue
			}
			attachmentInputs[a.MessageID] = append(attachmentInputs[a.MessageID],
				runtime.UserAttachment{Name: a.Name, Text: string(data)})
		case store.AttachmentKindImage:
			data, rerr := h.st.ReadAttachmentData(a, 0)
			if rerr != nil || len(data) == 0 {
				continue
			}
			url := "data:" + a.MIME + ";base64," + base64.StdEncoding.EncodeToString(data)
			attachmentInputs[a.MessageID] = append(attachmentInputs[a.MessageID],
				runtime.UserAttachment{Name: a.Name, Image: &runtime.ImagePart{URL: url, Detail: "auto"}})
		case store.AttachmentKindFile:
			// Binary/large attachment: the stored read-only copy is referenced
			// by path, exactly like the live turn sends it (FP-14). Without
			// this branch a replayed turn silently dropped the file, so the
			// model lost context the user had attached.
			path := strings.TrimSpace(a.Path)
			if path == "" {
				continue
			}
			attachmentInputs[a.MessageID] = append(attachmentInputs[a.MessageID],
				runtime.UserAttachment{Name: a.Name, Path: path, MIME: normalizeAttachmentMIME(a.MIME), Size: a.Size})
		}
	}

	// A tool call starts after its turn's user message and ends before that
	// turn's assistant text, so ordering the two tables by time reconstructs
	// the assistant(tool_calls) -> tool(result) exchange the provider needs.
	cursor := 0
	emitCall := func(call store.ToolCall) {
		out = append(out, runtime.Message{
			Role: runtime.RoleAssistant,
			ToolCalls: []runtime.ToolCall{{
				ID:        call.ID,
				Name:      call.Name,
				Arguments: json.RawMessage(call.Args),
			}},
		})
		out = append(out, runtime.Message{
			Role:       runtime.RoleTool,
			Content:    call.Result,
			ToolCallID: call.ID,
			ToolName:   call.Name,
		})
	}
	for _, m := range msgs {
		for cursor < len(calls) && calls[cursor].StartedAt.Before(m.CreatedAt) {
			emitCall(calls[cursor])
			cursor++
		}
		switch runtime.Role(m.Role) {
		case runtime.RoleUser:
			atts := attachmentInputs[m.ID]
			out = append(out, runtime.Message{
				Role:      runtime.RoleUser,
				Content:   runtime.AttachmentPrompt(m.Content, atts),
				Reasoning: m.Reasoning,
				Images:    runtime.AttachmentImages(atts),
			})
		case runtime.RoleAssistant:
			out = append(out, runtime.Message{
				Role:      runtime.RoleAssistant,
				Content:   m.Content,
				Reasoning: m.Reasoning,
			})
		}
	}
	for ; cursor < len(calls); cursor++ {
		emitCall(calls[cursor])
	}
	return out, nil
}

// normalizeAttachmentMIME mirrors the session attachment normalization: strip
// parameters, lowercase, and default to application/octet-stream, so a
// replayed file attachment carries the same MIME the live turn sent.
func normalizeAttachmentMIME(mediaType string) string {
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = mediaType[:i]
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType == "" {
		return "application/octet-stream"
	}
	return mediaType
}

// Append is intentionally read-only. See storeHistory's comment: the
// controller is the single writer for the messages table.
func (h *storeHistory) Append(ctx context.Context, sessionID string, msgs ...runtime.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// storeCheckpoint adapts the store's durable checkpoints table to the
// runtime.CheckpointStore contract. The store keeps the conversation as JSON
// because it must not depend on the runtime's message types; this adapter owns
// the encoding.
type storeCheckpoint struct {
	st *store.Store
}

var _ runtime.CheckpointStore = (*storeCheckpoint)(nil)

func (c *storeCheckpoint) Load(ctx context.Context, sessionID string) (*runtime.Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || c.st == nil {
		return nil, nil
	}
	cp, err := c.st.LoadCheckpoint(sessionID)
	if errors.Is(err, store.ErrNotFound) {
		// The runtime contract treats a missing checkpoint as a fresh
		// conversation, not an error.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var history []runtime.Message
	if len(cp.History) > 0 {
		if err := json.Unmarshal(cp.History, &history); err != nil {
			return nil, fmt.Errorf("decode checkpoint history for session %q: %w", sessionID, err)
		}
	}
	return &runtime.Checkpoint{
		History:   history,
		TurnID:    cp.TurnID,
		UpdatedAt: cp.UpdatedAt,
		Phase:     cp.Phase,
	}, nil
}

func (c *storeCheckpoint) Save(ctx context.Context, sessionID string, cp *runtime.Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.st == nil {
		return nil
	}
	if cp == nil {
		return nil
	}
	history, err := json.Marshal(cp.History)
	if err != nil {
		return fmt.Errorf("encode checkpoint history for session %q: %w", sessionID, err)
	}
	return c.st.SaveCheckpoint(store.Checkpoint{
		SessionID: sessionID,
		History:   history,
		TurnID:    cp.TurnID,
		UpdatedAt: cp.UpdatedAt,
		Phase:     cp.Phase,
	})
}

func (c *storeCheckpoint) Delete(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c == nil || c.st == nil {
		return nil
	}
	return c.st.DeleteCheckpoint(sessionID)
}

// buildAgent reads provider settings from st, registers the built-in tools
// confined to root, and builds the runtime agent with store-backed history and
// an approval gate for mutating tools. No API key means (nil, nil, nil).
// pluginEnabled reports a plugin.<name>.enabled toggle. The shell persists
// these keys; the runtime is the only reader, so a UI switch always has a
// concrete effect on the next agent build.
func pluginEnabled(st *store.Store, name string, def bool) bool {
	if st == nil {
		return def
	}
	v, err := st.GetSetting("plugin." + name + ".enabled")
	if err != nil || strings.TrimSpace(v) == "" {
		return def
	}
	return strings.EqualFold(strings.TrimSpace(v), "true")
}

// permissionMode reads the persisted access mode. Unknown or missing values
// fall back to Workspace Write, the safe default.
func permissionMode(st *store.Store) string {
	if st != nil {
		if v, err := st.GetSetting(session.SettingPermission); err == nil {
			switch strings.TrimSpace(v) {
			case session.PermissionFull:
				return session.PermissionFull
			case session.PermissionAsk:
				return session.PermissionAsk
			case session.PermissionWorkspace:
				return session.PermissionWorkspace
			case session.PermissionAutoReview:
				return session.PermissionAutoReview
			}
		}
	}
	return session.PermissionWorkspace
}

// autoReviewContext builds the automatic reviewer's environment: the session's
// current working directory, resolved per review so a working_directory move
// within a turn is visible.
//
// It deliberately does not read the workspace AGENTS.md. That file travels with
// cloned repositories and was previously passed to the review model as
// "authorization evidence"; untrusted repository content could then try to
// weaken the automatic-approval boundary (RT-14). The reviewer receives only
// the user's own request as authorization evidence plus its fixed policy.
func autoReviewContext(root string, dirState *tools.WorkingDirectoryState) func() (workDir, constraints string) {
	return func() (string, string) {
		workDir := root
		if dirState != nil {
			if abs, _ := dirState.Cwd(); strings.TrimSpace(abs) != "" {
				workDir = abs
			}
		}
		return workDir, ""
	}
}

// outputCapBytes resolves plugin.shell.output_cap_kb the way the registry
// build does, so the app can tell when an app-owned terminal/job manager was
// built for a stale cap.
func outputCapBytes(st *store.Store) int {
	const def = 256 * 1024
	if st == nil {
		return def
	}
	if raw, err := st.GetSetting("plugin.shell.output_cap_kb"); err == nil {
		if kb, perr := strconv.Atoi(strings.TrimSpace(raw)); perr == nil && kb > 0 {
			return kb * 1024
		}
	}
	return def
}

// budgetWarnThresholds parses a comma-separated soft-budget threshold list
// from the agent-loop plugin settings. Empty means the runtime defaults (nil);
// "0", "off" or "none" disables the dimension (empty non-nil slice); any
// invalid or non-positive entry falls back to the defaults, so a typo can never
// silently turn the soft warnings off.
func budgetWarnThresholds(raw string) []int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	switch strings.ToLower(raw) {
	case "0", "off", "none":
		return []int{}
	}
	parts := strings.Split(raw, ",")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n <= 0 {
			return nil
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// settingInt parses an integer setting, returning def when it is missing,
// unreadable, or outside [min,max]. A zero limit means "no bound".
func settingInt(get func(key, env, def string) string, key, env string, def, min, max int) int {
	raw := strings.TrimSpace(get(key, env, ""))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if min != 0 && n < min {
		n = min
	}
	if max != 0 && n > max {
		n = max
	}
	return n
}

// providerURLScheme returns the lowercased URL scheme of raw, or "" when the
// value is empty or unparsable.
func providerURLScheme(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Scheme)
}

// keylessLocalProvider reports whether baseURL points at a local or private
// network endpoint that typically needs no API key (Ollama, llama.cpp, LM Studio,
// vLLM). Malformed or public URLs report false, so a missing key there still
// means "no model configured".
func keylessLocalProvider(baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" {
		return false
	}
	// Only real HTTP(S) providers can run without a key: ftp://, ws://, file://
	// etc. are rejected here so the build reports "no model configured" instead
	// of handing an unusable scheme to the OpenAI-compatible client.
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "localhost", "localhost.localdomain", "host.docker.internal", "gateway.docker.internal":
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// skillsRootsAvailable reports whether any skill root of the configured layout
// exists as a directory. A custom skills.root keeps exactly that one root;
// the default "<workspace>/skills" layout also covers the project-local and
// user roots, so a user-level skill catalog still works in a workspace without
// its own skills/ directory (FP-17).
func skillsRootsAvailable(configuredRoot, base string) bool {
	for _, root := range skills.RootsFor(configuredRoot, base) {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// Context budget defaults. The budget is the approximate prompt size in tokens
// (estimated as chars/4) above which the runtime compacts the prior
// conversation. 24000 is the historical fixed value and stays the fallback when
// the user has not configured one (FP-10: the window itself is not exposed by
// the provider, so there is no reliable model-window number to derive it from).
const (
	defaultContextBudgetTokens = 24000
	minContextBudgetTokens     = 1000
	maxContextBudgetTokens     = 2000000
)

// contextBudgetTokens resolves the prompt budget from settings, preferring the
// documented context.budget_tokens key and falling back to the namespaced
// agent.context_budget_tokens for older installs. ZEOKEEL_CONTEXT_BUDGET_TOKENS
// is the environment override. Invalid values log and fall back to the default;
// out-of-range values are clamped instead of silently disabling compaction.
func contextBudgetTokens(get func(key, env, def string) string) int {
	keys := []struct{ key, env string }{
		{"context.budget_tokens", "ZEOKEEL_CONTEXT_BUDGET_TOKENS"},
		{"agent.context_budget_tokens", ""},
	}
	for _, candidate := range keys {
		raw := strings.TrimSpace(get(candidate.key, candidate.env, ""))
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			log.Printf("zeokeel: invalid %s=%q; using the default %d-token context budget", candidate.key, raw, defaultContextBudgetTokens)
			return defaultContextBudgetTokens
		}
		if n < minContextBudgetTokens {
			return minContextBudgetTokens
		}
		if n > maxContextBudgetTokens {
			return maxContextBudgetTokens
		}
		return n
	}
	return defaultContextBudgetTokens
}

// registerAllTools registers the production tool set for the agent: file and
// shell tools, interactive tools, goal/subagent/workflow, optional web search,
// skills and MCP servers. It is also the seam tool-invocation tests use.
//
// pluginOn gates every built-in feature the Plugins screen exposes, and the
// persisted Read Only permission omits mutating tools instead of merely
// gating them behind an approval prompt.
func registerAllTools(st *store.Store, root string, get func(key, env, def string) string) (*runtime.Registry, func() error, error) {
	return registerAllToolsWithPermission(st, root, get, "")
}

// registryOptions carries the long-lived, app-owned state a registry build
// must reuse. A rebuild (model, plugin, permission or workspace change) would
// otherwise start with a fresh working directory, no terminals and no jobs,
// even though the model still holds their ids from earlier turns.
type registryOptions struct {
	// PermissionOverride is the session's effective access mode; empty uses
	// the persisted default.
	PermissionOverride string
	// Mode is the agent preset this build targets. Empty means Standard at
	// build time; the app supplies the current session's mode and ModeFunc.
	Mode string
	// ModeFunc, when set, resolves the agent preset for each turn so a mode
	// switch is reflected in the recomposed instruction without rebuilding the
	// registry. The agent's topology is still switched by Agent.SetMode.
	ModeFunc func() string
	// PlanModeFunc, when set, resolves the current session's plan mode for each
	// turn. Plan mode is a soft workflow: it feeds only the plan:policy prompt
	// section and the shell's plan review, not a runtime tool gate, so toggling
	// it rebuilds neither the registry nor the agent topology.
	PlanModeFunc func() bool
	// PlanModeForSession resolves a session's plan mode from the session id the
	// runtime attaches to a tool call. The exit_plan_mode tool uses it to stay
	// inert outside plan mode; unlike PlanModeFunc it does not go through the
	// app's current-session selection, so a background session's turn reads its
	// own flag (and tests can inject a fixed resolver).
	PlanModeForSession func(sessionID string) bool
	// WorkspaceID is the controller's current workspace. When set it scopes
	// session history tools directly instead of re-deriving the selection
	// from the store and the filesystem root.
	WorkspaceID string
	// DirState is the session's working-directory state. Nil creates one.
	DirState *tools.WorkingDirectoryState
	// Terminal and Jobs are the app-owned managers. Nil creates new ones.
	Terminal *tools.TerminalManager
	Jobs     *tools.JobsManager
	// AdoptManagers receives the managers this build actually used, so the
	// caller can hold on to newly created ones for the next rebuild.
	AdoptManagers func(*tools.WorkingDirectoryState, *tools.TerminalManager, *tools.JobsManager)
	// AdoptTeam receives the async team manager this build created, so the app
	// can show live members and stop a session's members when the user stops.
	// Nil when the Agent Teams plugin is off (no team manager is built).
	AdoptTeam func(*agentteam.Manager)
	// Presets is the controller's declarative preset registry (FP-06). Nil
	// disables preset filtering/injection (the historical all-tools build).
	Presets *presets.Registry
	// PresetID is the effective preset for this build (already resolved with
	// fallback). Empty disables preset filtering/injection.
	PresetID string
	// QuestionGate, when set, lets ask_user block inside the turn (FP-16).
	QuestionGate runtime.QuestionGate
}

// registerAllToolsWithPermission is registerAllTools with an optional access
// mode override for the session being opened. An empty override uses the
// persisted default.
func registerAllToolsWithPermission(st *store.Store, root string, get func(key, env, def string) string, permissionOverride string) (*runtime.Registry, func() error, error) {
	return registerAllToolsWithOptions(st, root, get, registryOptions{PermissionOverride: permissionOverride})
}

// registerAllToolsWithOptions registers the production tool set, reusing the
// working-directory state and terminal/job managers in opts when provided.
func registerAllToolsWithOptions(st *store.Store, root string, get func(key, env, def string) string, opts registryOptions) (*runtime.Registry, func() error, error) {
	var closeFn func() error
	pluginOn := func(name string, def bool) bool { return pluginEnabled(st, name, def) }
	// read resolves one setting from the injected getter first (env or test
	// seam) and then from the store. Plugin editors persist plain keys with no
	// environment fallback, so a caller that only supplies defaults still sees
	// the saved endpoint, limits and iteration caps.
	read := func(key, env, def string) string {
		if v := strings.TrimSpace(get(key, env, "")); v != "" {
			return v
		}
		if st != nil {
			if v, err := st.GetSetting(key); err == nil && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		return def
	}
	// The outbound proxy setting reaches every tool this registry builds
	// (web_fetch), so it is applied before the first tool constructor runs.
	networkProxy := networkProxyFromSetting(read)
	tools.SetNetworkProxy(networkProxy)
	permission := permissionMode(st)
	if opts.PermissionOverride != "" {
		permission = opts.PermissionOverride
	}
	readOnly := permission == session.PermissionAsk
	shellOn := pluginOn("shell", true)

	dirState := opts.DirState
	if dirState == nil {
		var derr error
		dirState, derr = tools.NewWorkingDirectoryState(root)
		if derr != nil {
			return nil, nil, fmt.Errorf("register tools: working directory: %w", derr)
		}
	}
	// The plugin editor's default output cap is 256 KB; use it when the
	// setting was never saved so the UI value and the runtime agree.
	maxOutput := 256 * 1024
	if kb := settingInt(read, "plugin.shell.output_cap_kb", "", 0, 1, 10000); kb > 0 {
		maxOutput = kb * 1024
	}
	// The bash tool documents a 300s cap; clamp the configured default here so
	// the setting and the runtime agree instead of allowing an effective
	// timeout the tool description denies.
	bashTimeout := time.Duration(settingInt(read, "plugin.shell.timeout_seconds", "", 120, 1, 300)) * time.Second

	// The shell stacks are platform gated like DSH's composition: a POSIX
	// build exposes bash, a Windows build exposes pwsh, never both. The prompt
	// and the runtime prompt section read the same selection through the
	// registered tool names.
	allowBash, allowPwsh := shellStackFor(goruntime.GOOS)

	reg := runtime.NewRegistry()
	if err := tools.Register(reg, tools.Config{
		WorkspaceRoot:        root,
		AllowShell:           shellOn,
		AllowBash:            allowBash,
		ReadOnly:             readOnly,
		DisableFileMutations: !shellOn,
		MaxOutputBytes:       maxOutput,
		BashTimeout:          bashTimeout,
		DirState:             dirState,
	}); err != nil {
		return nil, nil, fmt.Errorf("register tools: %w", err)
	}
	// Interactive tools carry per-session state. The todo and goal tools are
	// backed by the store so a registry rebuild (model switch, plugin toggle,
	// workspace switch, permission chip) rehydrates the plan and objective
	// instead of starting empty.
	interactiveState := storeToolState{st: st}
	_ = reg.Register(tools.NewTodoToolWithStore(interactiveState))
	_ = reg.Register(tools.NewAskUserTool())
	_ = reg.Register(tools.NewPresentTool())
	// exit_plan_mode is always registered (a stable tool catalog) but the tool
	// rejects calls unless the calling session is in plan mode. The shell
	// renders the plan review card from the tool result; see ExitPlanModeTool.
	_ = reg.Register(tools.NewExitPlanModeTool(tools.ExitPlanModeConfig{PlanMode: opts.PlanModeForSession}))
	// Read Only builds omit the goal tool: a durable goal keeps the goal
	// driver submitting automatic continuation turns (up to its round cap)
	// after the user has put the session in Read Only, so the model could
	// schedule unattended work from a session whose promise is "no lasting
	// side effects" (RT-03).
	if !readOnly {
		_ = reg.Register(tools.NewGoalToolWithStore(interactiveState))
	}

	// Agent-mode tools are registered once and exposed by mode routing in the
	// runtime: standard/legacy hide them, and Creator adds the settings
	// authoring pair. Registering the concrete instances keeps the registry, tool
	// tests and settings screen discoverable while Agent.ToolNames still reports
	// the model-visible set.
	var settingsStore runtime.SettingsStore
	if st != nil {
		settingsStore = st
	}
	_ = reg.Register(runtime.NewSettingsGetTool(settingsStore))
	_ = reg.Register(runtime.NewSettingsSetTool(settingsStore))

	register := func(list ...runtime.Tool) {
		for _, t := range list {
			_ = reg.Register(t)
		}
	}

	// Subagent and its orchestration family are one plugin: disabling it
	// removes every way the model can spawn code-level child agents. Agent
	// Teams is a separate plugin: its team tools and prompt section must be
	// consistent whether or not the Subagent plugin is on, so register them
	// whenever teamOn is set.
	subagentOn := pluginOn("subagent", true)
	subagentDepth := settingInt(read, "plugin.subagent.depth", "", tools.DefaultSubagentMaxDepth, 1, 8)
	teamOn := pluginOn("agent_teams", false)
	if subagentOn {
		_ = reg.Register(tools.NewSubagentTool(tools.SubagentConfig{MaxDepth: subagentDepth}))
		_ = reg.Register(tools.NewWorkflowTool(tools.WorkflowConfig{MaxDepth: subagentDepth}))
		_ = reg.Register(tools.NewRalphTool(tools.RalphConfig{}))
	}
	if teamOn {
		teamMgr := agentteam.New(agentteam.Config{})
		if opts.AdoptTeam != nil {
			opts.AdoptTeam(teamMgr)
		}
		register(tools.NewTeamTools(tools.TeamConfig{Manager: teamMgr, Store: st})...)
		register(tools.NewTeamSpawnTools(tools.TeamSpawnConfig{Manager: teamMgr})...)
	}

	// The mutation-capable shell group follows the Shell plugin and the Read
	// Only permission; read-only runs still get inspection and preview tools.
	termMgr, jobsMgr := opts.Terminal, opts.Jobs
	if shellOn && !readOnly {
		if termMgr == nil {
			termMgr = tools.NewTerminalManager(tools.TerminalConfig{
				WorkspaceRoot: root, MaxOutputBytes: maxOutput,
			})
		}
		if jobsMgr == nil {
			jobsMgr = tools.NewJobsManager(tools.JobsConfig{
				WorkspaceRoot: root, MaxOutputBytes: maxOutput,
			})
		}
		register(termMgr.Tools()...)
		register(jobsMgr.Tools()...)
		// Windows exposes the pwsh tool instead of bash; POSIX builds keep the
		// bash stack off so the model only ever sees the platform shell.
		if allowPwsh {
			register(tools.NewPwshTool(tools.PwshConfig{WorkspaceRoot: root, MaxOutputBytes: maxOutput}))
		}
		register(tools.NewStrReplaceEditorTool(tools.EditorConfig{WorkspaceRoot: root}))
		register(tools.NewWorktreeTools(tools.WorktreeConfig{WorkspaceRoot: root})...)
	}
	if opts.AdoptManagers != nil {
		opts.AdoptManagers(dirState, termMgr, jobsMgr)
	}
	register(tools.NewReadImageTool(tools.ImageConfig{WorkspaceRoot: root}))
	// The lsp tool is read-only code intelligence from a configured language
	// server. It stays registered without any server so a call gets a stable
	// "not configured" error instead of a missing-tool error, and a malformed
	// configuration is reported by the tool instead of failing the whole agent
	// build. The workspace file .zeokeel/lsp.json is untrusted data: starting
	// the commands it declares is third-party code execution, so it is only
	// read after an explicit lsp.workspace_config=allow opt-in (the same
	// fail-closed policy as mcp.workspace_config).
	if pluginOn("lsp", true) {
		lspServers, lspErr := lsp.LoadServers(
			read("lsp.servers", "ZEOKEEL_LSP_SERVERS", ""),
			root,
			read("lsp.workspace_config", "ZEOKEEL_LSP_WORKSPACE_CONFIG", lsp.WorkspaceConfigOff),
		)
		if lspErr != nil {
			log.Printf("zeokeel: lsp: %v (fix lsp.servers or %s)", lspErr, lsp.WorkspaceConfigFile)
		}
		register(tools.NewLSPTool(tools.LSPConfig{
			WorkspaceRoot: root,
			Provider:      lsp.NewStdioProvider(lsp.ProviderConfig{Servers: lspServers, Root: root}),
			ConfigError:   lspErr,
		}))
	}
	// Session history tools are scoped to the selected workspace, matching
	// the prompt's "workspace-scoped" claim and the shell's search. The app
	// passes the controller's current workspace; a direct caller without it
	// falls back to the persisted selection and the workspace root.
	workspaceID := strings.TrimSpace(opts.WorkspaceID)
	if workspaceID == "" {
		workspaceID = currentWorkspaceID(st, root)
	}
	// Auto-memory (docs/design/auto-memory.md): the agent writes durable notes
	// deliberately through the explicit memory tool, which covers two scopes
	// (workspace project context and global user-level preferences) and follows
	// the per-scope Settings write mode (auto=active, suggest=pending review,
	// off=refuse). The optional background extractor (M3) feeds the same store
	// through internal/memory's scrub/dedupe pipeline instead of a second hidden
	// prompt path; Settings > Memory lists, reviews and deletes every row.
	//
	// Read Only omits the tool entirely: a memory written here is injected
	// into every later turn's system prompt, which is exactly the persistent
	// side effect Read Only promises not to have (RT-03). The prompt catalog
	// is derived from the registry, so the memory policy text disappears with
	// the tool instead of advertising a capability the session lacks.
	if !readOnly {
		register(tools.NewMemoryTool(tools.MemoryConfig{
			Store:       st,
			WorkspaceID: workspaceIDResolver(st, root, opts.WorkspaceID),
			Modes:       func() (string, string) { return memoryWriteModes(st) },
		}))
	}
	register(tools.NewSessionQueryTools(tools.SessionQueryConfig{Store: st, WorkspaceID: workspaceID})...)
	// Scheduling tools create durable automations that run their prompt in a
	// future session (under that session's permission), so Read Only keeps
	// only the read-only schedule_list and drops create/update/delete; the
	// approval gate cannot express "this tool is a persistent side effect" for
	// a session the current approval prompt never covers (RT-03).
	for _, scheduleTool := range tools.NewScheduleTools(tools.ScheduleConfig{Store: st}) {
		if readOnly && scheduleTool.Name() != "schedule_list" {
			continue
		}
		_ = reg.Register(scheduleTool)
	}
	register(tools.NewWorkingDirectoryTool(tools.WorkingDirectoryConfig{WorkspaceRoot: root, State: dirState}))
	register(tools.NewReportTool())
	if pluginOn("developer_tools", false) {
		register(tools.NewInspectTool(tools.InspectConfig{Store: st, Root: root, WorkspaceID: workspaceID}))
	}

	// web_search is optional: enabled when an endpoint is configured. The
	// plugin detail editor persists plugin.web_search.*; the legacy
	// websearch.* / web_search.* keys remain readable for existing installs.
	endpoint := read("plugin.web_search.endpoint", "", "")
	if strings.TrimSpace(endpoint) == "" {
		endpoint = read("websearch.endpoint", "ZEOKEEL_WEBSEARCH_ENDPOINT", read("web_search.endpoint", "ZEOKEEL_WEB_SEARCH_ENDPOINT", ""))
	}
	if endpoint != "" && pluginOn("web_search", true) {
		maxResults := settingInt(read, "plugin.web_search.max_results", "", 0, 0, websearch.MaxResults)
		if maxResults == 0 {
			maxResults, _ = strconv.Atoi(read("websearch.max_results", "ZEOKEEL_WEBSEARCH_MAX_RESULTS", ""))
		}
		apiKey2 := read("websearch.api_key", "ZEOKEEL_WEBSEARCH_API_KEY", read("web_search.api_key", "ZEOKEEL_WEB_SEARCH_API_KEY", ""))
		searchConfig := websearch.Config{Endpoint: endpoint, APIKey: apiKey2, MaxResults: maxResults}
		if networkProxy != nil {
			searchConfig.HTTPClient = webSearchProxyHTTPClient(networkProxy)
		}
		wt, werr := websearch.New(searchConfig)
		if werr != nil {
			return nil, nil, fmt.Errorf("build web_search: %w", werr)
		}
		if err := reg.Register(wt); err != nil {
			return nil, nil, fmt.Errorf("register web_search: %w", err)
		}
	}

	// Skills: every <skills.root>/<name>/SKILL.md is a built-in capability.
	// The default workspace layout expands to the project, project-local and
	// user roots (FP-17); the skill tool rescans them on change.
	skillsDir := read("skills.root", "ZEOKEEL_SKILLS_ROOT", filepath.Join(root, "skills"))
	if pluginOn("skills", true) && skillsRootsAvailable(skillsDir, root) {
		skillTool, skillErrs := skills.NewTolerantIn(root, skillsDir)
		for _, serr := range skillErrs {
			// A missing primary root is expected in the multi-root layout (a
			// workspace without skills/ but with user-level skills); it is not
			// a diagnostic.
			if errors.Is(serr, fs.ErrNotExist) {
				continue
			}
			log.Printf("zeokeel: skills: %v", serr)
		}
		_ = reg.Register(skillTool)
	}

	// MCP servers: settings key mcp.servers (JSON array) or <root>/mcp.json.
	// A disabled plugin must not connect, and a malformed config is surfaced
	// instead of silently dropping the whole server list.
	//
	// The workspace file is untrusted data: it travels with cloned
	// repositories, so starting the commands it declares is third-party code
	// execution without approval. It only applies after an explicit opt-in
	// through mcp.workspace_config=allow (RT-01); the default is off.
	if pluginOn("mcp", true) {
		rawSetting := read("mcp.servers", "ZEOKEEL_MCP_SERVERS", "")
		workspaceConfig := read(workspaceMCPConfigKey, "ZEOKEEL_MCP_WORKSPACE_CONFIG", workspaceMCPConfigOff)
		mcpTools, mcpMgr, closeMCP, merr := connectMCP(root, rawSetting, mcpServersSource(rawSetting, st), workspaceConfig)
		if merr != nil {
			return nil, nil, fmt.Errorf("mcp: %w", merr)
		}
		if mcpMgr != nil {
			for _, t := range mcpTools {
				_ = reg.Register(t)
			}
			// The manager is non-nil even when every configured server failed
			// to connect. Only expose the resource/prompt tools when at least
			// one server is actually connected; otherwise they would be four
			// always-failing tools in the model's registry.
			if len(mcpMgr.Servers()) > 0 {
				register(tools.NewMCPResourceTools(tools.MCPResourceConfig{Manager: mcpMgr})...)
			}
			closeFn = closeMCP
		}
	}
	return reg, closeFn, nil
}

// buildAgent reads provider settings from st, registers the built-in tools
// confined to root, and builds the runtime agent with store-backed history and
// an approval gate for mutating tools. No API key means (nil, nil).
//
// History is authoritative for seeding turns; CheckpointStore mirrors the
// completed (and possibly compacted) conversation after each successful turn
// for cold-start recovery and diagnostics, but is not read while History is
// configured (see runtime.loadPriorConversation).
func buildAgent(st *store.Store, root, instruction string) (*AgentBundle, error) {
	return buildAgentWithPermission(st, root, instruction, "")
}

// buildAgentWithPermission builds the agent for one effective access mode.
// The mode selects which tools are registered and whether the approval gate
// asks, so a per-session Read Only session cannot run bash even when the
// global default is Workspace Write.
func buildAgentWithPermission(st *store.Store, root, instruction, permissionOverride string) (*AgentBundle, error) {
	return buildAgentWithOptions(st, root, instruction, registryOptions{PermissionOverride: permissionOverride})
}

// buildAgentWithOptions is buildAgentWithPermission with app-owned state: the
// working directory and the terminal/job managers survive a rebuild instead of
// resetting whenever the agent is rebuilt.
func buildAgentWithOptions(st *store.Store, root, instruction string, opts registryOptions) (*AgentBundle, error) {
	get := func(key, env, def string) string {
		if v, err := st.GetSetting(key); err == nil && strings.TrimSpace(v) != "" {
			return v
		}
		if env != "" {
			if v := strings.TrimSpace(os.Getenv(env)); v != "" {
				return v
			}
		}
		return def
	}
	// One network.proxy setting configures the provider, web_search and
	// web_fetch (FP-19). Empty keeps each client's environment/default
	// behavior; the tool registry reads the configured URL process-wide.
	networkProxy := networkProxyFromSetting(get)
	tools.SetNetworkProxy(networkProxy)
	baseURL := get("provider.base_url", "ZEOKEEL_PROVIDER_BASE_URL", openai.DefaultBaseURL)
	// An API key is required except for local/loopback providers (Ollama,
	// llama.cpp, LM Studio, vLLM): those servers accept unauthenticated
	// requests, and the DSH parity target lets users run them without a key.
	// Without this a saved local provider badge said "configured" while Send
	// reported "no agent" and rolled the turn back (PD-02).
	apiKey := get("provider.api_key", "DEEPSEEK_API_KEY", "")
	if apiKey == "" && !keylessLocalProvider(baseURL) {
		// A configured but unusable endpoint scheme is a configuration error,
		// not "no model configured": report it clearly instead of sending the
		// user to the provider settings without a reason.
		if scheme := providerURLScheme(baseURL); scheme != "" && scheme != "http" && scheme != "https" {
			return nil, fmt.Errorf("build provider: provider.base_url %q uses unsupported scheme %q; use an http or https endpoint (or configure a provider.api_key for a remote model)", baseURL, scheme)
		}
		return nil, nil
	}
	model := get("provider.model", "ZEOKEEL_PROVIDER_MODEL", openai.DefaultModel)
	reasoningEffort := get("provider.reasoning_effort", "", "")
	// Zero (the default) means no overall request deadline: streaming a long
	// reasoning turn must not be cut off by a fixed HTTP timeout. A positive
	// provider.timeout_seconds / ZEOKEEL_PROVIDER_TIMEOUT_SECONDS restores a
	// user-chosen cap.
	requestTimeout := 0
	if n := settingInt(get, "provider.timeout_seconds", "ZEOKEEL_PROVIDER_TIMEOUT_SECONDS", 0, 0, 3600); n > 0 {
		requestTimeout = n
	}
	// DSH defaults: a stream may stay idle for up to 5 minutes, there is no
	// overall request deadline, and transient failures are retried up to five
	// times with 500ms..10s exponential backoff and 10% jitter.
	streamIdleSeconds := settingInt(get, "provider.stream_idle_timeout_seconds",
		"ZEOKEEL_PROVIDER_STREAM_IDLE_TIMEOUT_SECONDS",
		int(openai.DefaultStreamIdleTimeout/time.Second), 1, 3600)
	retryMax := settingInt(get, "provider.retry_max_retries",
		"ZEOKEEL_PROVIDER_RETRY_MAX_RETRIES", openai.DefaultRetryPolicy.MaxRetries, 0, 10)
	retryInitialMs := settingInt(get, "provider.retry_initial_ms",
		"ZEOKEEL_PROVIDER_RETRY_INITIAL_MS", int(openai.DefaultRetryPolicy.InitialDelay/time.Millisecond), 1, 60000)
	retryMaxMs := settingInt(get, "provider.retry_max_ms",
		"ZEOKEEL_PROVIDER_RETRY_MAX_MS", int(openai.DefaultRetryPolicy.MaxDelay/time.Millisecond), 1, 300000)
	retry := &openai.RetryPolicy{
		MaxRetries:   retryMax,
		InitialDelay: time.Duration(retryInitialMs) * time.Millisecond,
		MaxDelay:     time.Duration(retryMaxMs) * time.Millisecond,
		JitterRatio:  0.1,
	}
	providerConfig := openai.Config{
		BaseURL:           baseURL,
		APIKey:            apiKey,
		Model:             model,
		ReasoningEffort:   reasoningEffort,
		Timeout:           time.Duration(requestTimeout) * time.Second,
		StreamIdleTimeout: time.Duration(streamIdleSeconds) * time.Second,
		Retry:             retry,
	}
	if networkProxy != nil {
		providerConfig.HTTPClient = providerProxyHTTPClient(networkProxy)
	}
	prov, err := openai.New(providerConfig)
	if err != nil {
		return nil, fmt.Errorf("build provider: %w", err)
	}
	// Capture the team manager this build creates so Close can stop its
	// members when the agent is replaced (plugin/model/workspace rebuild) or
	// the app quits. The app's adopter sees it too for the live roster/Stop.
	var teamManager *agentteam.Manager
	adoptTeam := opts.AdoptTeam
	opts.AdoptTeam = func(m *agentteam.Manager) {
		teamManager = m
		if adoptTeam != nil {
			adoptTeam(m)
		}
	}
	reg, mcpClose, err := registerAllToolsWithOptions(st, root, get, opts)
	if err != nil {
		return nil, err
	}
	// Declarative agent presets (FP-06): a non-empty preset tool allowlist
	// narrows the model-visible registry (registration-order preserving); the
	// empty allowlist keeps every tool (the historical standard behaviour).
	presetID := strings.TrimSpace(opts.PresetID)
	var presetWarnings []string
	if opts.Presets != nil && presetID != "" {
		filtered, warning := filterRegistryByPreset(reg, opts.Presets, presetID)
		reg = filtered
		if warning != "" {
			presetWarnings = append(presetWarnings, warning)
			log.Printf("zeokeel: preset: %s", warning)
		}
	}
	if adoptTeam != nil && teamManager == nil {
		// The build has no team manager (Agent Teams off): tell the adopter so
		// a replaced manager's roster is not kept around.
		adoptTeam(nil)
	}
	// The runtime-context sections (working directory, reference time) are
	// recomposed at the start of every turn: the working_directory tool may
	// have moved since build time, and a long-lived session must not report
	// its build time as the current time. The Auto review reviewer uses the
	// same current directory, so it is resolved here for both.
	dirState := workingDirectoryFromRegistry(reg)
	bundle := &AgentBundle{
		Close: func() error {
			if teamManager != nil {
				teamManager.Close()
			}
			if mcpClose != nil {
				return mcpClose()
			}
			return nil
		},
		// The same provider that runs turns writes automatic session titles
		// after the first completed turn. Keeping it on the bundle makes a
		// rebuild (model change, permission change) replace the titler too.
		TitleGenerator: session.LLMTitleGenerator{Provider: prov, Model: model},
	}
	// Registered tool names (before preset filtering) validate user preset
	// allowlists at load time; build warnings (fail-closed filtering) are
	// exposed to the session UI by the app.
	bundle.ToolNames = make([]string, 0, len(reg.List()))
	for _, tool := range reg.List() {
		bundle.ToolNames = append(bundle.ToolNames, tool.Name())
	}
	if len(presetWarnings) > 0 {
		bundle.PresetWarnings = append(bundle.PresetWarnings, presetWarnings...)
	}
	permission := permissionMode(st)
	if opts.PermissionOverride != "" {
		permission = opts.PermissionOverride
	}
	// Auto review keeps the mutating tool set and its own (wider) review
	// patterns, but a model reviewer decides each call that would otherwise
	// prompt. The reviewer gets its own provider clone with temperature 0 for
	// deterministic verdicts; if that clone somehow fails, the turn provider is
	// still used so the mode never silently degrades to prompting.
	var reviewer runtime.ToolReviewer
	if permission == session.PermissionAutoReview {
		reviewProvider := runtime.Provider(prov)
		zero := float32(0)
		reviewConfig := openai.Config{
			BaseURL: baseURL, APIKey: apiKey, Model: model, Temperature: &zero,
			Retry: &openai.RetryPolicy{MaxRetries: 0},
		}
		if networkProxy != nil {
			reviewConfig.HTTPClient = providerProxyHTTPClient(networkProxy)
		}
		if p, perr := openai.New(reviewConfig); perr == nil {
			reviewProvider = p
		} else {
			log.Printf("zeokeel: auto review provider: %v", perr)
		}
		reviewer = runtime.LLMToolReviewer{
			Provider: reviewProvider,
			Model:    model,
			Timeout:  runtime.DefaultToolReviewTimeout,
		}.Review
	}
	// The reviewer judges whether the user authorized the action, so the runtime
	// attaches this turn's user request and this environment to every review.
	// The review environment deliberately excludes workspace files such as
	// AGENTS.md: repository content is untrusted and must not become
	// authorization evidence (RT-14).
	var reviewContext func() (workDir, constraints string)
	if reviewer != nil {
		reviewContext = autoReviewContext(root, dirState)
	}
	approvalPatterns := approvalPatternsForPermission(st, permission)
	// Full access bypasses approval entirely; Read Only never reaches the gate
	// because mutating tools are not registered; Workspace Write asks only for
	// the tools that can leave the workspace sandbox. Auto review keeps its
	// wider pattern set, then its reviewer decides instead of prompting; a
	// reviewer error falls back to the prompt. The gate predicate also inspects
	// the arguments, so a read-only str_replace_editor "view" never asks.
	needsApproval := func(name string, args json.RawMessage) bool {
		if permission == session.PermissionFull {
			return false
		}
		return toolNeedsApprovalWithArgs(name, args, approvalPatterns)
	}
	gate := runtime.NewApprovalQueue(needsApproval)
	skillsDir := ""
	if pluginEnabled(st, "skills", true) {
		candidate := get("skills.root", "ZEOKEEL_SKILLS_ROOT", filepath.Join(root, "skills"))
		if skillsRootsAvailable(candidate, root) {
			skillsDir = candidate
		}
	}
	// A declarative preset owns the topology: built-in preset ids map to the
	// historical runtime modes, a custom preset runs the standard topology (its
	// tool allowlist and prompt do the customization).
	if opts.Presets != nil && presetID != "" {
		opts.Mode = presetModeForID(presetID)
		opts.ModeFunc = nil
	}
	// The agent preset is part of the dynamic runtime context: switching mode
	// must change the prompt policy on the next turn, not only the tool set.
	agentMode := strings.TrimSpace(opts.Mode)
	if agentMode == "" && opts.ModeFunc != nil {
		agentMode = strings.TrimSpace(opts.ModeFunc())
	}
	modeForTurn := func() string {
		if opts.ModeFunc != nil {
			if mode := strings.TrimSpace(opts.ModeFunc()); mode != "" {
				return mode
			}
		}
		return agentMode
	}
	planModeForTurn := func() bool {
		return opts.PlanModeFunc != nil && opts.PlanModeFunc()
	}
	// Memory scope follows the selected workspace like the memory tool and
	// the session-history tools. The resolver is consulted per turn, so a
	// workspace change is visible without freezing the build-time selection.
	memoryWorkspaceID := workspaceIDResolver(st, root, opts.WorkspaceID)
	instructionForTurn := func() string {
		cwd := root
		if dirState != nil {
			if abs, _ := dirState.Cwd(); strings.TrimSpace(abs) != "" {
				cwd = abs
			}
		}
		workspaceMemories, globalMemories := memoriesForPrompt(st, memoryWorkspaceID())
		workspaceMemoryMode, globalMemoryMode := memoryWriteModes(st)
		text := composeInstructionForModesWithMemories(st, root, cwd, reg, skillsDir, permission,
			collaborationMode(st), modeForTurn(), planModeForTurn(),
			memoryPromptTexts(workspaceMemories), memoryPromptTexts(globalMemories),
			workspaceMemoryMode, globalMemoryMode)
		if text == "" {
			return instruction
		}
		if opts.Presets != nil && presetID != "" {
			text = injectPresetPrompt(text, opts.Presets, presetID, uiLanguage(st))
		}
		// Record which stored memories this turn's prompt injected, so the
		// consolidation policy can rank by real use instead of updated_at.
		// Failure is logged only: a memory bookkeeping error must not fail the
		// turn or lose the instruction.
		if ids := memoryPromptIDs(workspaceMemories, globalMemories); len(ids) > 0 {
			if err := markPromptMemoriesUsed(st, ids); err != nil {
				log.Printf("zeokeel: mark memories used: %v", err)
			}
		}
		return text
	}
	composed := instructionForTurn()
	var settings runtime.SettingsStore
	if st != nil {
		settings = st
	}
	// 0 (the runtime default) means no fixed per-turn ceiling: the model decides
	// when to end the turn, like DSH. A positive setting is a user-chosen safety
	// cap; negatives are clamped to 0 so the setting can never mean "one step"
	// by accident. Disabling the agent-loop plugin keeps its old single-round-trip
	// meaning.
	maxIterations := runtime.DefaultMaxIterations
	if !pluginEnabled(st, "agent_loop", true) {
		maxIterations = 1
	} else {
		n := settingInt(get, "plugin.agent_loop.max_iterations", "", runtime.DefaultMaxIterations, 0, runtime.MaxIterationsLimit)
		if n < 0 {
			n = 0
		}
		maxIterations = n
	}
	subagentModel := strings.TrimSpace(get("plugin.subagent.model", "", ""))
	agent, err := runtime.New(runtime.Config{
		Name:            "zeokeel",
		Instruction:     composed,
		InstructionFunc: instructionForTurn,
		ModelName:       model,
		Provider:        prov,
		Tools:           reg,
		Mode:            agentMode,
		Settings:        settings,
		MaxIterations:   maxIterations,
		StepLimitNotice: stepLimitNoticeFor(get("ui.language", "", session.LanguageChinese)),
		SubagentModel:   subagentModel,
		History:         &storeHistory{st: st},
		Gate:            gate,
		QuestionGate:    opts.QuestionGate,
		ToolReviewer:    reviewer,
		ReviewContext:   reviewContext,
		Compactor: runtime.LLMCompactor{
			Provider:           prov,
			Model:              model,
			Timeout:            runtime.DefaultLLMSummaryTimeout,
			KeepLast:           12,
			ToolResultKeepLast: runtime.DefaultToolResultKeepLast,
			Fallback:           runtime.WindowCompactor{KeepLast: 12},
		},
		ContextBudget:   contextBudgetTokens(get),
		CheckpointStore: &storeCheckpoint{st: st},
		// Soft cost warnings (never a hard stop) follow the agent-loop plugin
		// settings; empty falls back to the runtime defaults.
		BudgetWarnSteps:  budgetWarnThresholds(get("plugin.agent_loop.budget_warn_steps", "", "")),
		BudgetWarnTokens: budgetWarnThresholds(get("plugin.agent_loop.budget_warn_tokens", "", "")),
		SubagentTools:    nil,
		MaxSubagentDepth: settingInt(get, "plugin.subagent.depth", "", tools.DefaultSubagentMaxDepth, 1, 8),
	})
	if err != nil {
		if bundle.Close != nil {
			_ = bundle.Close()
		}
		return nil, fmt.Errorf("build agent: %w", err)
	}
	bundle.Agent = agent
	bundle.Gate = gate
	return bundle, nil
}

// collaborationMode resolves the prompt's mode gate: Agent Teams wins when its
// plugin is on, otherwise the persisted agent.collaboration_mode ("plan" or
// "subagent"). It is empty for ordinary turns, which adds no mode policy.
func collaborationMode(st *store.Store) string {
	if pluginEnabled(st, "agent_teams", false) {
		return "team"
	}
	if st != nil {
		if v, err := st.GetSetting("agent.collaboration_mode"); err == nil {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "plan", "plan-mode", "plan_mode":
				return "plan"
			case "subagent", "delegated", "child":
				return "subagent"
			case "team", "teams":
				return "team"
			}
		}
	}
	return ""
}

// shellStackFor reports which model-facing shell tools a build exposes for
// goos. The stacks are mutually exclusive, mirroring DSH's per-platform
// composition: Windows gets pwsh only, every other platform gets bash only.
// goos is a parameter so tests can pin both branches on one machine.
func shellStackFor(goos string) (allowBash, allowPwsh bool) {
	windows := strings.EqualFold(strings.TrimSpace(goos), "windows")
	return !windows, windows
}

// registryHasMutationTools reports whether the registered set can modify
// files or run commands. It mirrors the tool names registerAllTools gates on
// the Shell plugin and Read Only mode, so the prompt's file policy comes from
// the effective registry instead of the persisted permission alone. The shell
// names include both stacks even though registerAllTools only mounts one per
// platform: the function reads whatever the registry actually holds.
func registryHasMutationTools(reg *runtime.Registry) bool {
	if reg == nil {
		return false
	}
	for _, t := range reg.List() {
		switch name := t.Name(); {
		case name == "write_file", name == "edit_file", name == "bash",
			name == "pwsh", name == "str_replace_editor":
			return true
		case strings.HasPrefix(name, "terminal_"),
			strings.HasPrefix(name, "job_"),
			name == "enter_worktree", name == "exit_worktree":
			return true
		}
	}
	return false
}

// workingDirectoryFromRegistry finds the shared directory state of the
// registered working_directory tool, so the per-turn prompt sees the same cwd
// the tool does.
func workingDirectoryFromRegistry(reg *runtime.Registry) *tools.WorkingDirectoryState {
	if reg == nil {
		return nil
	}
	for _, t := range reg.List() {
		wt, ok := t.(*tools.WorkingDirectoryTool)
		if !ok {
			continue
		}
		state, err := wt.State()
		if err == nil {
			return state
		}
	}
	return nil
}

// composeInstruction renders the DSH-style system prompt for the registered
// tool set, workspace, settings and optional AGENTS.md. skillsDir is the
// resolved catalog the registered skill tool reads ("" when the plugin is off
// or the directory is missing), so the prompt and the tool cannot disagree.
func composeInstruction(st *store.Store, root string, reg *runtime.Registry, skillsDir, permission, mode string) string {
	return composeInstructionAt(st, root, root, reg, skillsDir, permission, mode)
}

// composeInstructionAt is composeInstruction with a separately resolved
// current working directory. The agent's per-turn instruction function uses it
// so a working_directory change and the time are recomposed for each turn
// instead of staying frozen at build time.
func composeInstructionAt(st *store.Store, root, cwd string, reg *runtime.Registry, skillsDir, permission, mode string) string {
	return composeInstructionForModes(st, root, cwd, reg, skillsDir, permission, mode, "", false)
}

// composeInstructionForModes is composeInstructionAt with every policy gate:
// collaborationMode is plan/team/subagent, agentMode is the selected agent
// preset (standard, minimal, creator), and planMode is the per-session
// plan review gate. Empty/false values add no policy.
func composeInstructionForModes(st *store.Store, root, cwd string, reg *runtime.Registry, skillsDir, permission, collaborationMode, agentMode string, planMode bool) string {
	return composeInstructionForModesWithMemories(st, root, cwd, reg, skillsDir, permission,
		collaborationMode, agentMode, planMode, nil, nil, "", "")
}

// composeInstructionForModesWithMemories is composeInstructionForModes with the
// memory material the caller loaded for this turn: the workspace notes, the
// user-level global notes and the two configured write modes (which enable the
// memory policy section). The prompt package bounds each list again, so a large
// store cannot crowd out the rest of the instruction.
func composeInstructionForModesWithMemories(st *store.Store, root, cwd string, reg *runtime.Registry, skillsDir, permission, collaborationMode, agentMode string, planMode bool, memories, globalMemories []string, memoryWorkspaceMode, memoryGlobalMode string) string {
	names := make([]string, 0, 64)
	for _, t := range reg.List() {
		names = append(names, t.Name())
	}
	// The prompt must describe only the tools the selected mode actually
	// exposes. Passing the raw registry would tell Minimal mode to use bash,
	// read/edit, glob or grep even though ModeTools hides them from the model.
	// ModeTools also adds the tools a mode synthesizes (settings_*) so the
	// prompt and the live topology agree.
	names = runtime.ModeTools(agentMode, names)
	var skillNames []string
	if strings.TrimSpace(skillsDir) != "" {
		// The prompt catalog must match the skill tool, which loads every root
		// of the default layout (project + .zeokeel + user, FP-17).
		loaded, errs := skills.LoadRootsTolerant(skills.RootsFor(skillsDir, root))
		for _, err := range errs {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			log.Printf("zeokeel: skills: %v", err)
		}
		for _, sk := range loaded {
			skillNames = append(skillNames, sk.Name)
		}
	}
	agentsMD := ""
	if data, err := os.ReadFile(filepath.Join(root, "AGENTS.md")); err == nil {
		agentsMD = string(data)
	}
	lang := "zh"
	if st != nil {
		if v, err := st.GetSetting("ui.language"); err == nil && strings.TrimSpace(v) != "" {
			lang = v
		}
	}
	sandboxPolicy, approvalPolicy := "workspace-write", "ask"
	switch permission {
	case session.PermissionAsk:
		sandboxPolicy, approvalPolicy = "read-only", "ask"
	case session.PermissionFull:
		sandboxPolicy, approvalPolicy = "danger-full-access", "never"
	}
	// The prompt must not claim mutation capability that no registered tool
	// provides. The Shell plugin (or Read Only) can omit write_file, edit_file
	// and bash while the persisted permission is still Workspace Write.
	if sandboxPolicy != "read-only" && !registryHasMutationTools(reg) {
		sandboxPolicy = "read-only"
	}
	return prompt.Compose(prompt.Context{
		WorkDir:             cwd,
		Platform:            goruntime.GOOS + "/" + goruntime.GOARCH,
		SandboxPolicy:       sandboxPolicy,
		ApprovalPolicy:      approvalPolicy,
		Skills:              skillNames,
		AgentsMD:            agentsMD,
		Memories:            memories,
		GlobalMemories:      globalMemories,
		MemoryWorkspaceMode: memoryWorkspaceMode,
		MemoryGlobalMode:    memoryGlobalMode,
		Language:            lang,
		Mode:                collaborationMode,
		PlanMode:            planMode,
		AgentMode:           agentMode,
		Time:                time.Now(),
		Tools:               names,
	})
}

// approvalPatternsForPermission resolves the pattern list that defines which
// tool calls require approval in permission mode. An explicit approval.tools
// setting wins in every mode (the user's list is the override). Otherwise
// Workspace Write uses the converged defaultApprovalTools list and Auto review
// keeps its wider experimental set, because the reviewer stands in for the
// human prompt and should still see file mutations. Read-only call exemptions
// are applied separately by toolNeedsApprovalWithArgs, so both sets exempt
// str_replace_editor "view".
func approvalPatternsForPermission(st *store.Store, permission string) []string {
	if patterns, ok := configuredApprovalToolPatterns(st); ok {
		return patterns
	}
	if permission == session.PermissionAutoReview {
		return splitApprovalPatterns(autoReviewApprovalTools)
	}
	return splitApprovalPatterns(defaultApprovalTools)
}

// configuredApprovalToolPatterns returns the approval.tools setting as parsed
// patterns and whether the user configured it. A missing, blank or unreadable
// setting reports false, so a broken settings row can never silently disable
// the gate.
func configuredApprovalToolPatterns(st *store.Store) ([]string, bool) {
	if st == nil {
		return nil, false
	}
	raw, err := st.GetSetting("approval.tools")
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, false
	}
	return splitApprovalPatterns(raw), true
}

// splitApprovalPatterns splits a comma-separated approval.tools value into
// trimmed, non-empty prefix patterns.
func splitApprovalPatterns(raw string) []string {
	patterns := make([]string, 0, strings.Count(raw, ",")+1)
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			patterns = append(patterns, part)
		}
	}
	return patterns
}

// storeToolState persists the interactive tools' per-session state in the
// settings table. JSON keeps the shape stable without a schema migration; the
// settings row is per session, so two sessions never share a plan or goal.
type storeToolState struct{ st *store.Store }

func toolStateKey(prefix, sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		// Direct invocations (background context, probes) still need a
		// stable bucket.
		sessionID = "_default"
	}
	return prefix + sessionID
}

func (s storeToolState) loadJSON(key string, out any) error {
	if s.st == nil {
		return store.ErrNotFound
	}
	raw, err := s.st.GetSetting(key)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), out)
}

func (s storeToolState) saveJSON(key string, value any) error {
	if s.st == nil {
		return store.ErrNotFound
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.st.SetSetting(key, string(raw))
}

func (s storeToolState) LoadTodos(sessionID string) ([]tools.Todo, error) {
	var tasks []tools.Todo
	err := s.loadJSON(toolStateKey("tool.todo.", sessionID), &tasks)
	return tasks, err
}

func (s storeToolState) SaveTodos(sessionID string, tasks []tools.Todo) error {
	return s.saveJSON(toolStateKey("tool.todo.", sessionID), tasks)
}

func (s storeToolState) LoadGoal(sessionID string) (*tools.Goal, error) {
	var goal *tools.Goal
	err := s.loadJSON(toolStateKey("tool.goal.", sessionID), &goal)
	return goal, err
}

func (s storeToolState) SaveGoal(sessionID string, goal *tools.Goal) error {
	return s.saveJSON(toolStateKey("tool.goal.", sessionID), goal)
}

// memoryPromptLimit is how many workspace notes the app loads per turn;
// memoryGlobalPromptLimit is the smaller cap for the cross-workspace user
// scope (docs/design/auto-memory.md §3). The prompt package applies its own
// per-scope caps afterwards, so a larger store never turns the instruction into
// an unbounded dump.
const (
	memoryPromptLimit       = 20
	memoryGlobalPromptLimit = 12
)

// workspaceIDResolver returns the workspace id used to scope memories. An
// explicit override from the controller wins; otherwise the id is re-derived
// from the persisted sidebar selection and the tool root on every call, so a
// workspace switch does not leave the tools or the prompt on the old scope.
func workspaceIDResolver(st *store.Store, root, override string) func() string {
	override = strings.TrimSpace(override)
	return func() string {
		if override != "" {
			return override
		}
		return currentWorkspaceID(st, root)
	}
}

// memoryPromptItem is one memory the turn's prompt injects: its stable id (used
// to record last_used_at after a successful composition) and its rendered text.
type memoryPromptItem struct {
	ID      string
	Content string
}

// memoriesForPrompt loads the notes the turn's prompt should inject: the
// selected workspace's active notes plus the user-level (cross-workspace)
// global notes (docs/design/auto-memory.md §3). A missing store, an unresolved
// workspace or a read failure yields nil for that scope: the memory sections
// are optional and must never fail a turn. Pending suggestions are not injected
// (they wait for review); the prompt package bounds both lists again. The
// returned items carry the row ids so the caller can mark them used.
func memoriesForPrompt(st *store.Store, workspaceID string) (workspace, global []memoryPromptItem) {
	if st == nil {
		return nil, nil
	}
	if id := strings.TrimSpace(workspaceID); id != "" {
		list, err := st.ListMemoriesScoped(store.MemoryScopeWorkspace, id, store.MemoryStatusActive, memoryPromptLimit)
		if err != nil {
			log.Printf("zeokeel: load workspace memories: %v", err)
		} else {
			workspace = memoryPromptItems(list)
		}
	}
	if list, err := st.ListMemoriesScoped(store.MemoryScopeGlobal, "", store.MemoryStatusActive, memoryGlobalPromptLimit); err != nil {
		log.Printf("zeokeel: load global memories: %v", err)
	} else {
		global = memoryPromptItems(list)
	}
	return workspace, global
}

// memoryPromptItems projects stored rows to prompt items, dropping blank
// content (the prompt would omit it too, so it must not be marked used).
func memoryPromptItems(list []store.Memory) []memoryPromptItem {
	out := make([]memoryPromptItem, 0, len(list))
	for _, memory := range list {
		content := strings.TrimSpace(memory.Content)
		if content == "" {
			continue
		}
		out = append(out, memoryPromptItem{ID: memory.ID, Content: content})
	}
	return out
}

// memoryPromptTexts extracts the rendered note texts for the prompt.
func memoryPromptTexts(items []memoryPromptItem) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Content)
	}
	return out
}

// memoryPromptIDs concatenates the injected row ids across scopes.
func memoryPromptIDs(workspace, global []memoryPromptItem) []string {
	if len(workspace)+len(global) == 0 {
		return nil
	}
	out := make([]string, 0, len(workspace)+len(global))
	for _, item := range workspace {
		out = append(out, item.ID)
	}
	for _, item := range global {
		out = append(out, item.ID)
	}
	return out
}

// markPromptMemoriesUsed records the memories a turn's prompt injected. It is a
// package variable so tests can force a store failure without a corrupt
// database; production failures are logged and never fail the turn.
var markPromptMemoriesUsed = func(st *store.Store, ids []string) error {
	return st.MarkMemoriesUsed(ids)
}

// memoryWriteModes resolves the configured auto-memory write modes. The memory
// tool reads them per invocation and the prompt renders the policy section from
// them, so a Settings change applies to the next turn/tool call without
// rebuilding the agent. Unset or unreadable keys keep the documented defaults
// (workspace auto, global suggest).
func memoryWriteModes(st *store.Store) (workspace, global string) {
	workspace, global = session.MemoryModeAuto, session.MemoryModeSuggest
	if st == nil {
		return workspace, global
	}
	if v, err := st.GetSetting(session.SettingMemoryWorkspaceMode); err == nil {
		if mode := strings.TrimSpace(v); mode != "" {
			workspace = mode
		}
	}
	if v, err := st.GetSetting(session.SettingMemoryGlobalMode); err == nil {
		if mode := strings.TrimSpace(v); mode != "" {
			global = mode
		}
	}
	return workspace, global
}

// currentWorkspaceID resolves the workspace the tool set should scope history// reads to: the sidebar's persisted selection, else the workspace whose root
// matches the tools' root. An empty id means the scope could not be resolved.
func currentWorkspaceID(st *store.Store, root string) string {
	if st == nil {
		return ""
	}
	if id, err := st.GetSetting(session.SettingWorkspace); err == nil {
		if id = strings.TrimSpace(id); id != "" {
			if ws, werr := st.GetWorkspace(id); werr == nil && ws != nil {
				return id
			}
		}
	}
	list, err := st.ListWorkspaces()
	if err != nil {
		return ""
	}
	want := strings.TrimSpace(root)
	for _, ws := range list {
		wsRoot := strings.TrimSpace(ws.Root)
		// The default workspace has an empty root. It used to be skipped,
		// which made history searches unscoped (global) whenever another
		// workspace existed.
		if wsRoot == "" && want == "" {
			return ws.ID
		}
		if wsRoot == "" || want == "" {
			continue
		}
		if filepath.Clean(wsRoot) == filepath.Clean(want) {
			return ws.ID
		}
	}
	if len(list) == 1 {
		// A single-workspace store can only mean that workspace.
		return list[0].ID
	}
	return ""
}

// toolNeedsApproval reports whether name matches one of the configured prefix
// patterns. A pattern's trailing "*" is optional; every pattern matches as a
// prefix, so "mcp__*", "mcp__" and "mcp__demo" all gate the demo server's
// tools. An empty pattern list gates nothing.
func toolNeedsApproval(name string, patterns []string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, pattern := range patterns {
		prefix := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(pattern), "*"))
		if prefix == "" {
			// A bare "*" gates every tool.
			if strings.TrimSpace(pattern) == "*" {
				return true
			}
			continue
		}
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// toolNeedsApprovalWithArgs is the approval gate's predicate. It applies the
// configured patterns to the tool name, then exempts the read-only calls that
// share a tool name with mutating calls: str_replace_editor's "view" command
// only reads the workspace, so it never asks for approval, even under a
// "str_replace_editor" or "*" pattern. Arguments the gate cannot parse fall
// through to the name-based decision, so an unclassifiable call is treated as
// mutating (fail-closed).
func toolNeedsApprovalWithArgs(name string, args json.RawMessage, patterns []string) bool {
	name = strings.TrimSpace(name)
	if name == "str_replace_editor" && editorCallIsReadOnly(args) {
		return false
	}
	return toolNeedsApproval(name, patterns)
}

// editorCallIsReadOnly reports whether args select str_replace_editor's
// read-only "view" command. Invalid JSON, a missing command and non-object
// payloads all report false so the call keeps its name-based approval decision.
func editorCallIsReadOnly(args json.RawMessage) bool {
	if len(args) == 0 {
		return false
	}
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return false
	}
	return strings.TrimSpace(parsed.Command) == "view"
}

// workspaceMCPConfigKey aliases the canonical setting name from the mcp
// package so the app, the inspect diagnostics and the docs cannot drift.
const workspaceMCPConfigKey = mcp.WorkspaceConfigKey

// workspaceMCPConfigOff is the default: workspace-declared MCP servers are
// treated as data, not executable configuration.
const workspaceMCPConfigOff = mcp.WorkspaceConfigOff

// workspaceMCPConfigAllowed reports whether the mcp.workspace_config value
// opts into running <root>/mcp.json. The canonical parser lives in the mcp
// package so the runtime gate and the inspect diagnostics agree.
func workspaceMCPConfigAllowed(value string) bool {
	return mcp.WorkspaceConfigAllowed(value)
}

// mcpServersSource labels where the effective mcp.servers value came from.
// The store wins over the environment (see buildAgentWithOptions' getter), so
// the label is diagnostic only; an unknown source falls back to a generic
// label instead of claiming a channel that was not used.
func mcpServersSource(raw string, st *store.Store) string {
	if strings.TrimSpace(raw) == "" {
		return "none"
	}
	if st != nil {
		if v, err := st.GetSetting("mcp.servers"); err == nil && strings.TrimSpace(v) != "" {
			return "settings mcp.servers"
		}
	}
	if strings.TrimSpace(os.Getenv("ZEOKEEL_MCP_SERVERS")) != "" {
		return "env ZEOKEEL_MCP_SERVERS"
	}
	return "injected mcp.servers"
}

// mcpArgSecretPattern matches an argument key that conventionally carries a
// credential, so its value is redacted before anything is logged.
var mcpArgSecretPattern = regexp.MustCompile(`(?i)(api[_-]?key|apikey|token|secret|passw|credential|auth|key)`)

// mcpArgTailPattern matches "flag=SECRET" and "--flag SECRET" style arguments.
// The value is redacted but the flag stays visible, which keeps the startup log
// useful without writing tokens to <data dir>/logs (RT-01 diagnosability).
func redactMCPArgs(args []string) []string {
	out := make([]string, 0, len(args))
	redactNext := false
	for _, arg := range args {
		if redactNext {
			out = append(out, "[redacted]")
			redactNext = false
			continue
		}
		if key, value, ok := strings.Cut(arg, "="); ok && value != "" && mcpArgSecretPattern.MatchString(key) {
			out = append(out, key+"=[redacted]")
			continue
		}
		trimmed := strings.TrimLeft(arg, "-")
		if mcpArgSecretPattern.MatchString(trimmed) {
			out = append(out, arg)
			redactNext = true
			continue
		}
		if len(arg) > 128 {
			out = append(out, arg[:64]+fmt.Sprintf("…[%d chars]", len(arg)))
			continue
		}
		out = append(out, arg)
	}
	return out
}

// logMCPServers records which server processes are about to run and where
// their configuration came from. Names, commands, redacted arguments and env
// key names are logged; env values and credential-looking argument values are
// never written, because <data dir>/logs persists and a declared server's
// environment can carry keys.
func logMCPServers(source string, servers []mcp.ServerConfig) {
	for _, server := range servers {
		if mcp.IsStreamableHTTP(server) {
			headerKeys := make([]string, 0, len(server.Headers))
			for key := range server.Headers {
				headerKeys = append(headerKeys, key)
			}
			sort.Strings(headerKeys)
			log.Printf("zeokeel: mcp: starting server %q from %s: transport=streamable-http url=%q header_keys=%v",
				server.Name, source, redactMCPURL(server.URL), headerKeys)
			continue
		}
		envKeys := make([]string, 0, len(server.Env))
		for _, entry := range server.Env {
			key, _, _ := strings.Cut(entry, "=")
			envKeys = append(envKeys, key)
		}
		log.Printf("zeokeel: mcp: starting server %q from %s: command=%q args=%q env_keys=%v",
			server.Name, source, server.Command, redactMCPArgs(server.Args), envKeys)
	}
}

// redactMCPURL hides the parts of a remote MCP endpoint that can carry
// credentials (userinfo and query string) before the endpoint is written to the
// startup log.
func redactMCPURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "[invalid url]"
	}
	if parsed.User != nil {
		parsed.User = url.User("[redacted]")
	}
	if parsed.RawQuery != "" {
		parsed.RawQuery = "[redacted]"
	}
	return parsed.String()
}

// connectMCP starts the configured stdio MCP servers. Configuration comes
// from the mcp.servers value when it is non-empty (settingSource labels where
// that value was resolved from); otherwise the workspace file <root>/mcp.json
// is used, but only when mcp.workspace_config opts in (see
// workspaceMCPConfigAllowed). A missing file and a disabled workspace config
// are not errors (returns nil tooling).
func connectMCP(root, rawSetting, settingSource, workspaceConfig string) ([]runtime.Tool, *mcp.Manager, func() error, error) {
	var cfg mcp.Config
	source := settingSource
	if strings.TrimSpace(rawSetting) != "" {
		var servers []mcp.ServerConfig
		if err := json.Unmarshal([]byte(rawSetting), &servers); err != nil {
			return nil, nil, nil, fmt.Errorf("parse mcp.servers: %w", err)
		}
		cfg = mcp.Config{Servers: servers}
		if source == "" || source == "none" {
			source = "settings mcp.servers"
		}
	} else {
		path := filepath.Join(root, "mcp.json")
		if !workspaceMCPConfigAllowed(workspaceConfig) {
			// Never read, let alone execute, workspace-declared commands by
			// default: a cloned repository can carry mcp.json, so trusting it
			// would be unapproved remote code execution (RT-01). Log the
			// ignored file so a user who expected its servers can see why
			// nothing started.
			if _, statErr := os.Lstat(path); statErr == nil {
				log.Printf("zeokeel: mcp: ignoring workspace config %s (mcp.workspace_config=%q; set it to %q to enable workspace MCP servers)",
					path, mcp.NormalizeWorkspaceConfig(workspaceConfig), mcp.WorkspaceConfigAllow)
			}
			return nil, nil, nil, nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil, nil, nil
			}
			return nil, nil, nil, err
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, nil, nil, fmt.Errorf("parse %s: %w", path, err)
		}
		source = "untrusted workspace file " + path + " (mcp.workspace_config=allow)"
	}
	if len(cfg.Servers) == 0 {
		return nil, nil, nil, nil
	}
	logMCPServers(source, cfg.Servers)
	mgr := mcp.NewManager(context.Background())
	if err := mgr.Connect(context.Background(), cfg); err != nil {
		// Per-server isolation: the manager keeps every server that did
		// connect. Report the failure instead of failing the whole agent build
		// (which would also drop all built-in tools).
		log.Printf("zeokeel: mcp: %v (kept %d connected server(s))", err, len(mgr.Servers()))
	}
	return mgr.Tools(), mgr, mgr.Close, nil
}

// hasConfiguredModel mirrors buildAgentWithOptions' provider gating for UI
// decisions (first-run setup card, empty-session "add a model" hint): a model is
// configured when a provider key is saved (or comes from the environment) or
// when the endpoint is a keyless local provider (PD-02). A default model row
// alone is not enough if the provider cannot authenticate.
func (a *app) hasConfiguredModel() bool {
	var st *store.Store
	if a != nil && a.services != nil {
		st = a.services.Store()
	}
	get := func(key, env string) string {
		if st != nil {
			if v, err := st.GetSetting(key); err == nil && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		if env != "" {
			if v := strings.TrimSpace(os.Getenv(env)); v != "" {
				return v
			}
		}
		return ""
	}
	baseURL := get("provider.base_url", "ZEOKEEL_PROVIDER_BASE_URL")
	if baseURL == "" {
		baseURL = openai.DefaultBaseURL
	}
	if get("provider.api_key", "DEEPSEEK_API_KEY") != "" {
		return true
	}
	return keylessLocalProvider(baseURL)
}

// filterRegistryByPreset narrows reg to the preset's tool allowlist, keeping
// registration order. Contract (P1 fail-closed):
//   - an unknown preset or an empty allowlist keeps every tool (historical
//     standard behaviour);
//   - a non-empty allowlist that matches NO registered tool yields an EMPTY
//     registry plus a warning: the typo must not silently grant the full tool
//     set. A partially matching allowlist keeps only its matches, and names
//     that matched nothing are reported in the warning.
func filterRegistryByPreset(reg *runtime.Registry, registry *presets.Registry, presetID string) (*runtime.Registry, string) {
	if reg == nil || registry == nil {
		return reg, ""
	}
	preset, ok := registry.Get(presetID)
	if !ok {
		return reg, ""
	}
	if len(preset.Tools) == 0 {
		return reg, ""
	}
	names := make([]string, 0, len(reg.List()))
	for _, tool := range reg.List() {
		names = append(names, tool.Name())
	}
	visible, err := registry.VisibleTools(presetID, names)
	if err != nil {
		return runtime.NewRegistry(), fmt.Sprintf("preset %q tool allowlist could not be applied (%v); no tools are exposed for this session", presetID, err)
	}
	if len(visible) == 0 {
		return runtime.NewRegistry(), fmt.Sprintf("preset %q tool allowlist (%s) matched no registered tool; no tools are exposed for this session", presetID, strings.Join(preset.Tools, ", "))
	}
	keep := make(map[string]bool, len(visible))
	for _, name := range visible {
		keep[name] = true
	}
	filtered := runtime.NewRegistry()
	matched := make(map[string]bool, len(visible))
	for _, tool := range reg.List() {
		if keep[tool.Name()] {
			_ = filtered.Register(tool)
			matched[tool.Name()] = true
		}
	}
	// Report allowlist names that matched no registered tool, even when the
	// rest matched: a typo must be visible to the user, not silently dropped.
	var missing []string
	for _, name := range preset.Tools {
		if !matched[strings.TrimSpace(name)] {
			missing = append(missing, strings.TrimSpace(name))
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return filtered, fmt.Sprintf("preset %q tool allowlist names unregistered tool(s) %s; they were dropped from this session", presetID, strings.Join(missing, ", "))
	}
	return filtered, ""
}

// presetModeForID maps a preset id to the runtime topology it selects. Built-in
// preset ids mirror the historical modes; custom presets run the standard
// topology and customize through their allowlist/prompt.
func presetModeForID(presetID string) string {
	if _, err := runtime.NormalizeMode(presetID); err == nil {
		return presetID
	}
	return runtime.ModeStandard
}

// uiLanguage returns the prompt language code ("en"/"zh") selected in settings.
func uiLanguage(st *store.Store) string {
	lang := "en"
	if st != nil {
		if raw, err := st.GetSetting("ui.language"); err == nil && strings.TrimSpace(raw) != "" {
			lang = strings.TrimSpace(raw)
		}
	}
	return lang
}

// injectPresetPrompt prepends the preset's persona/prompt sections to the
// composed instruction in their declared order, before the built-in sections
// (the preset persona is meant to lead). Dynamic sections render with the
// deployment prompt context projected onto a minimal language/tools context.
func injectPresetPrompt(instruction string, registry *presets.Registry, presetID, lang string) string {
	if registry == nil || strings.TrimSpace(instruction) == "" {
		return instruction
	}
	sections := registry.PersonaSections(presetID)
	if len(sections) == 0 {
		return instruction
	}
	zh := strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "zh")
	parts := make([]string, 0, len(sections))
	for _, section := range sections {
		text := ""
		if section.Dynamic != nil {
			text = section.Dynamic(prompt.Context{Language: lang})
		} else if zh {
			text = section.ZH
		} else {
			text = section.EN
		}
		if text = strings.TrimSpace(text); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return instruction
	}
	return strings.Join(parts, "\n\n") + "\n\n" + instruction
}
