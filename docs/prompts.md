# Prompt composition (`internal/prompt`)

`internal/prompt` is ZeoKeel's missing system-prompt layer. It turns the
session's runtime facts and registered tool names into one deterministic
`Instruction` string, using the section order and semantics of DeepSeek
Harness's `@deepseek-ai/dsh-system-prompt` registry.

The package is pure Go and has no dependencies outside the standard library.
It does not read the session, discover tools, or call a model; the caller owns
those facts and passes them in a `prompt.Context`.

## API

```go
type Context struct {
    WorkDir        string   // session working directory; "" omits the section
    Platform       string   // runtime GOOS/GOARCH, e.g. "windows/amd64"; "" falls back to the build
    SandboxPolicy  string   // read-only | workspace-write | danger-full-access
    ApprovalPolicy string   // ask | never
    Skills         []string // skill names; rendered sorted and deduplicated
    AgentsMD       string   // workspace AGENTS.md content
    Memories       []string // workspace memories; newest first, bounded
    Language       string   // zh / zh-CN / zh-Hans / chinese select ZH; else EN
    Mode           string   // plan / team / subagent collaboration variants
    AgentMode      string   // standard / ptc / minimal / creator agent preset
    Time           time.Time
    Tools          []string // model-visible tool names (aliases accepted)
}

type Section struct {
    ID      string
    Order   int
    EN, ZH  string
    Dynamic func(Context) string
}

func Compose(c Context) string
func Sections() []Section // ordered snapshot for inspection/tests
```

`Compose` applies these rules:

- sections are sorted by `Order`, then by `ID`;
- tool sections render only when one of their gate tools (or an alias) is
  present; tool-free sections are always on unless a mode gate or a dynamic
  renderer omits them;
- the caller is responsible for passing the mode's *effective* tool names
  (`runtime.ModeTools(agentMode, registered)`), so the prompt and the model's
  actual tool set cannot contradict each other; the shell stacks are platform
  gated by the app (Windows exposes `pwsh` only, POSIX `bash` only), so
  `tool:bash` / `tool:pwsh` and `runtime:environment` always describe the shell
  the build actually has;
- dynamic sections render their text from `Context`; they return `""` to omit
  themselves (empty working directory, empty AGENTS.md, empty skills, zero
  time, etc.);
- language selects `EN` or `ZH`; every section carries both;
- output is trimmed, sections are joined by exactly one blank line, and runs of
  three or more newlines are collapsed, so no duplicate blank lines appear;
- an empty `Context` composes the always-on sections with the default
  read-only file policy and ask approval policy.

Typical wiring (the parent owns this change):

```go
instruction := prompt.Compose(prompt.Context{
    WorkDir:        cwd,
    Platform:       runtime.GOOS + "/" + runtime.GOARCH,
    SandboxPolicy:  sandbox,
    ApprovalPolicy: approval,
    Skills:         skillNames,
    AgentsMD:       agentsMD,
    Memories:       memories,
    Language:       settings.Language,
    Mode:           collaborationMode,
    AgentMode:      agentMode,
    Time:           time.Now(),
    // The caller filters the registry through runtime.ModeTools(agentMode, ...)
    // so a Minimal prompt never describes tools that mode hides.
    Tools:          runtime.ModeTools(agentMode, registry.Names()),
})
agent, err := runtime.New(runtime.Config{Instruction: instruction, /* ... */})
```

## Parity map

DSH keeps system-prompt sections (`SECTION_ORDERS`) and runtime context
(`CONTEXT_ORDERS`) in separate snapshots. ZeoKeel passes one `Instruction`
string, so context sections are inlined at their DSH context order (100–150),
before the plan policy. Every row below ships EN and ZH text.

| Section ID | DSH order | Local order | Included when | Locale |
| --- | --- | ---: | --- | --- |
| `harness:identity` | `HARNESS_IDENTITY` −1000 | −1000 | always | EN + ZH |
| `deployment:persona-prefix` | `DEPLOYMENT_PERSONA_PREFIX` 0 | 0 | always | EN + ZH |
| `working-directory:current` | `CONTEXT_ORDERS.WORKING_DIRECTORY` 100 | 100 | `WorkDir` non-empty | EN + ZH |
| `runtime:environment` | extension (DSH encodes the platform only in its per-platform tool composition) | 105 | always | EN + ZH |
| `sandbox:policy` | `CONTEXT_ORDERS.SANDBOX_POLICY` 110 | 110 | always (empty → read-only) | EN + ZH |
| `approval:policy` | `CONTEXT_ORDERS.APPROVAL_POLICY` 115 | 115 | always (empty → ask) | EN + ZH |
| `subagent:delegation` | `CONTEXT_ORDERS.SUBAGENT_DELEGATION` 120 | 120 | `Mode` = subagent/delegated/child | EN + ZH |
| `context:agents-md` | extension (DSH message) | 130 | `AgentsMD` non-empty | EN + ZH |
| `context:skills-catalog` | extension (DSH message) | 140 | `Skills` non-empty and `skill` tool present | EN + ZH |
| `context:global-memory` | extension (global store, 2026-11-06) | 144 | `GlobalMemories` non-empty (≤12 items, ≤1500 chars) | EN + ZH |
| `context:memory` | extension (workspace store) | 145 | `Memories` non-empty (≤20 items, ≤4000 chars) | EN + ZH |
| `context:memory-policy` | extension (auto-memory, 2026-11-06) | 147 | `memory` tool present AND (`MemoryWorkspaceMode` or `MemoryGlobalMode` ≠ off) | EN + ZH |
| `context:time` | extension (DSH history reading) | 150 | `Time` non-zero | EN + ZH |
| `harness:self-knowledge` | extension (no DSH constant) | 160 | always | EN + ZH |
| `plan:policy` | `PLAN_POLICY` 500 | 500 | `Mode` = plan/plan-mode/plan_mode | EN + ZH |
| `team:policy` | `TEAM_POLICY` 600 | 600 | `Mode` = team/teams/team-lead/teammate | EN + ZH |
| `mode:minimal` | extension | 810 | `AgentMode` (or `Mode`) = minimal/minimal-mode/minimal_mode | EN + ZH |
| `mode:creator` | extension | 820 | `AgentMode` (or `Mode`) = creator/creator-mode/creator_mode/cordis | EN + ZH |
| `context:file-reference` | `FILE_REFERENCE` 900 | 900 | `read` or `read_file` | EN + ZH |
| `tool:bash` | `TOOL_BASH` 1000 | 1000 | `bash` | EN + ZH |
| `tool:pwsh` | `TOOL_PWSH` 1010 | 1010 | `pwsh` | EN + ZH |
| `tool:read` | `TOOL_READ` 1100 | 1100 | `read` or `read_file` | EN + ZH |
| `tool:write` | `TOOL_WRITE` 1200 | 1200 | `write` or `write_file` | EN + ZH |
| `tool:edit` | `TOOL_EDIT` 1300 | 1300 | `edit` or `edit_file` | EN + ZH |
| `tool:glob` | `TOOL_GLOB` 1400 | 1400 | `glob` | EN + ZH |
| `tool:grep` | `TOOL_GREP` 1500 | 1500 | `grep` | EN + ZH |
| `tool:jobs` | `TOOL_JOBS` 1600 | 1600 | `jobs`, `job_output`, `job_list`, `job_kill` | EN + ZH |
| `tool:terminal` | `TOOL_PTY` 1700 | 1700 | `terminal*` | EN + ZH |
| `tool:skill` | extension | 1800 | `skill` | EN + ZH |
| `tool:todo` | extension | 1900 | `todo` or `todo_write` | EN + ZH |
| `tool:ask-user` | extension | 1950 | `ask_user` or `ask_user_question` | EN + ZH |
| `tool:web-search` | `TOOL_WEB_SEARCH` 2000 | 2000 | `web_search` | EN + ZH |
| `tool:web-fetch` | `TOOL_WEB_FETCH` 2100 | 2100 | `web_fetch` | EN + ZH |
| `tool:session-query` | `TOOL_SESSION_QUERY` 2300 | 2300 | `session_search`/`session_event_search`/related | EN + ZH |
| `tool:goal` | `TOOL_GOAL` 2400 | 2400 | `goal`, `get_goal`, `create_goal`, `update_goal` | EN + ZH |
| `tool:workflow` | `TOOL_WORKFLOW` 2600 | 2600 | `workflow` | EN + ZH |
| `tool:ralph` | `TOOL_RALPH` 2700 | 2700 | `ralph` | EN + ZH |
| `tool:subagent` | `TOOL_SUBAGENT` 2800 | 2800 | `subagent`, `task`, `subagent_fork` | EN + ZH |
| `tool:report` | `TOOL_REPORT` 2900 | 2900 | `report` | EN + ZH |
| `tool:present` | `TOOL_COMPUTER_USE` slot 3000 | 3000 | `present` | EN + ZH |
| `tool:mcp` | `MCP_SERVERS` 3100 | 3100 | any tool named `mcp` / `mcp_*` / `mcp-*` / `mcp:*` | EN + ZH |
| `ui:deliverable-file-references` | `DELIVERABLE_FILE_REFERENCES` 9000 | 9000 | `present` | EN + ZH |
| `tool:structured_output` | `STRUCTURED_OUTPUT` 9900 | 9900 | `structured_output` | EN + ZH |
| `harness:note` | `HARNESS_SOURCE` 10000 | 10000 | always | EN + ZH |
| `app:web-surface` | `WEB_SURFACE` 10100 | 10100 | always | EN + ZH |
| `deployment:persona-suffix` | `DEPLOYMENT_PERSONA_SUFFIX` 10200 | 10200 | always | EN + ZH |

## Dynamic context sections

- **Working directory** — `Current working directory: %q.` Uses the exact DSH
  wording and only renders when `WorkDir` is set.
- **Sandbox policy** — mirrors DSH's read-only / workspace-write /
  danger-full-access paragraphs. `workspace-write` names the workspace root
  when known; unknown or empty values fall back to the safe deployment default
  (read-only).
- **Approval policy** — mirrors DSH's `ask` and `never` sentences. Any value
  other than `never` renders `ask`.
- **AGENTS.md** — wrapped in DSH's `<system-reminder>` frame with the
  "workspace instructions do not override system/developer/user instructions"
  precedence note. Content is capped at 64 KiB (UTF-8 safe) and a literal
  `</system-reminder>` in the content is escaped.
- **Skills catalog** — DSH renders this as a `<system-reminder>` message; here
  it is inlined after AGENTS.md. Only skill names are available in `Context`,
  so entries are names only (sorted, deduplicated, backticks neutralized). The
  catalog renders only when `Skills` is non-empty *and* the `skill` tool is in
  `Tools`; without a loader the section is omitted instead of advertising
  guidance the model cannot follow.
- **Time** — `Current time: <ISO>+offset[Zone].` DSH appends a per-step reading
  to history; this layer inlines the same clock fact because it has no turn or
  step number. Zero `Time` omits the section.
- **Subagent delegation** — mirrors DSH's fixed-permission-scope paragraph and
  renders only in delegated/subagent modes.

## Mode gates

Mode matching is case-insensitive. Collaboration gates read `Mode`; agent
preset gates read `AgentMode` first and fall back to `Mode` so prompt-only
callers remain simple.

| Gate | Field | Accepted values | Effect |
| --- | --- | --- | --- |
| plan | `Mode` | `plan`, `plan-mode`, `plan_mode` | adds the read-only planning policy |
| team | `Mode` | `team`, `teams`, `team-lead`, `team_lead`, `teammate`, `team-member`, `team_member` | adds the shared-filesystem team policy |
| subagent | `Mode` | `subagent`, `delegated`, `child`, `subagent-child`, `subagent_child` | adds the fixed-permission-scope note |
| ptc | `AgentMode` / `Mode` | `ptc`, `ptc-mode`, `ptc_mode` | describes the pipeline-style batch tool |
| minimal | `AgentMode` / `Mode` | `minimal`, `minimal-mode`, `minimal_mode` | restricts and describes the terminal-only tool set |
| creator | `AgentMode` / `Mode` | `creator`, `creator-mode`, `creator_mode`, `cordis` | describes settings authoring and SKILL.md writes |

`standard` and the legacy topology aliases (`deep`, `supervisor`,
`planexecute`) and an empty value add no agent-mode policy. DSH's plan mode is
an orthogonal collaboration state, so callers should pass `Mode: "plan"` when
that state is active rather than a topology name.

## Tool name adaptation

ZeoKeel's registered names differ from DSH's. Sections accept both, so the
registry works for current and DSH-style names:

| Section | ZeoKeel names | DSH names / aliases |
| --- | --- | --- |
| read / file reference | `read_file` | `read` |
| write | `write_file` | `write` |
| edit | `edit_file` | `edit` |
| todo | `todo` | `todo_write` |
| goal | `goal` | `get_goal`, `create_goal`, `update_goal` |
| ask user | `ask_user` | `ask_user_question` |
| jobs | (not registered today) | `job_output`, `job_list`, `job_kill` |
| terminal | (not registered today) | `terminal`, `terminal_*` |
| session query | (not registered today) | `session_search`, `session_event_search`, ... |
| MCP | arbitrary server tool names | `mcp`, `mcp_*`, `mcp-*`, `mcp:*` |

Because MCP tool names are server-defined, callers that mount MCP servers
should include the marker name `mcp` in `Context.Tools` when no actual tool name
starts with `mcp`.

## Intentional divergences and skips

Skipped DSH sections:

- `TOOL_LSP` (2200) — no language-server integration exists to describe.
- `TOOLS_SDK` (5000) — no programmatic tool-calling SDK. `PTC_ONLY` (800) is
  **not implemented: PTC is an explicit product decision (2026-11-06, do not
  reuse this slot)** — the mode, its prompt section and its tools were removed
  from the build; see `docs/design/known-limitations.md` §2.1.
- `TOOL_COMPUTER_USE` (3000) — no computer-use/GUI-automation tool. The unused
  slot is reused for `tool:present`, which keeps present guidance adjacent to
  deliverable references.
- `TEAM_IDENTITY` context (125) — `Context` has no team role, name, or
  membership, so only the shared team policy is rendered.
- Per-MCP-server instruction sections — `Context` carries no server
  instructions; `tool:mcp` provides the generic trust and schema guidance.
- DSH's harness checkout path and Web GUI URL contexts — ZeoKeel is a native
  desktop app, so `harness:note` and `app:web-surface` carry product-appropriate
  orientation instead of DSH paths/URLs.

Text adaptations (semantics preserved, claims made truthful for this codebase):

- read guidance drops DSH's `offset`/`limit` advice because ZeoKeel's
  `read_file` has no such arguments.
- tool guidance names the registered variant: `read_file`/`write_file`/
  `edit_file` when present, otherwise DSH's `read`/`write`/`edit`. The write
  paragraph mentions an edit tool only when one is registered, and the grep
  paragraph follows the same read-name choice.
- write/edit guidance drops DSH's "the default fs-observation-policy requires
  it" claim because ZeoKeel does not enforce read-before-write; it remains a
  recommendation.
- workflow, web, goal, jobs, terminal, subagent, and structured-output
  paragraphs are DSH's text adapted to ZeoKeel tool names.
- `tool:ralph` is DSH's `TOOL_RALPH` (2700) guidance adapted to the tool
  ZeoKeel registers: the loop is experimental and not a self-test mechanism,
  keeps the default 3-round budget visible, and reports cancellation as a
  structured `cancelled` result.
- `harness:self-knowledge` has no DSH counterpart: it tells the model to answer
  capability questions from the runtime context and the visible tool list,
  not by building a team/Ralph/workflow self-test harness, and to treat
  workspace reports and probe logs as historical data.
- `team:policy` keeps DSH's shared-filesystem facts but tightens the action
  guidance: teammates are only started when the user asks for a team or the
  task clearly needs concurrent work, never to test a feature.
- plan policy is authored here because DSH takes it from deployment
  configuration; team policy is a concise summary of DSH's experimental team
  guidance.
- the persona prefix/suffix are non-empty defaults so every section is
  renderable in both locales; a deployment may replace them later.

## Verification

```bash
export PATH=/usr/local/go/bin:$PATH GOCACHE=/data/mygo/.cache/go-build \
  GOMODCACHE=/data/mygo/.cache/gomod GOSUMDB=off CGO_ENABLED=0
gofmt -l internal/prompt
go vet ./internal/prompt
go test ./internal/prompt
go build ./...
```

The tests cover ordering, locale switching and aliases, tool-gated inclusion,
mode gating, AGENTS.md/skills/time/working-directory/policy injection, empty
and no-tools behavior, determinism (including input-order independence), and
non-empty EN+ZH text for every registered section.
