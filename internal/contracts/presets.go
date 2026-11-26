package contracts

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// PromptPreset is one reusable prompt/persona template offered in the composer
// (the old shell's prompt presets). Selecting it inserts its prompt into the
// draft; presets are user-editable.
type PromptPreset struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

// SettingPromptPresets stores the user's prompt presets as a JSON array.
const SettingPromptPresets = "prompt.presets"

// SettingModelCatalogModels stores user-edited model presets as a JSON array of
// ModelInfo; the modelcatalog module merges them over its configured list.
const SettingModelCatalogModels = "modelcatalog.models"

// ParseModelInfoList decodes the modelcatalog.models settings row.
func ParseModelInfoList(raw string) ([]ModelInfo, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var models []ModelInfo
	if err := json.Unmarshal([]byte(raw), &models); err != nil {
		return nil, err
	}
	return models, nil
}

// EncodeModelInfoList renders the modelcatalog.models settings row.
func EncodeModelInfoList(models []ModelInfo) (string, error) {
	if models == nil {
		models = []ModelInfo{}
	}
	encoded, err := json.Marshal(models)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// AgentPreset is one declarative agent preset: a persona/prompt fragment, an
// optional model default and an optional model-visible tool allowlist. A
// session stores only the preset ID; the agent backend applies the preset on
// every run.
type AgentPreset struct {
	// ID is the stable identifier a session stores. Required and unique.
	ID string `json:"id"`
	// Name is the display label.
	Name string `json:"name"`
	// Tools is the tool allowlist (empty = every registered tool).
	Tools []string `json:"tools,omitempty"`
	// Prompt is extra system-prompt guidance appended after the persona.
	Prompt string `json:"prompt,omitempty"`
	// Persona is the persona prefix text appended after the base prompt and
	// workspace instructions.
	Persona string `json:"persona,omitempty"`
	// Model is the preset's default model (a session model override wins).
	Model string `json:"model,omitempty"`
	// Order sorts presets in the picker; ties fall back to ID.
	Order int `json:"order,omitempty"`
}

// SettingAgentPresets stores the user-edited agent presets as a JSON array.
const SettingAgentPresets = "presets.agents"

// ParseAgentPresets decodes the presets.agents settings row. Empty returns
// nil; malformed JSON is an error.
func ParseAgentPresets(raw string) ([]AgentPreset, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var presets []AgentPreset
	if err := json.Unmarshal([]byte(raw), &presets); err != nil {
		return nil, err
	}
	return presets, nil
}

// EncodeAgentPresets renders the presets.agents settings row.
func EncodeAgentPresets(presets []AgentPreset) (string, error) {
	if presets == nil {
		presets = []AgentPreset{}
	}
	encoded, err := json.Marshal(presets)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// ValidateAgentPresets is the WRITE-side guard for the presets.agents row
// (2026-11-26 audit, 高).
//
// The row used to be stored verbatim. A truncated edit or a row array without
// ids went in silently, the agent's read side degraded to "no user presets", and
// a session that had selected one of those roles was left with no tools at all —
// every call refused, with nothing on screen explaining why. The write is the
// moment the value is still attributable to somebody, so a value that cannot
// mean what the reader will make of it is refused here, with the reason, instead
// of being discovered later as a broken session.
//
// An empty value is legal (it clears the row), and so is every row the reader
// accepts: an id and a name, ids unique (a second row with the same id would
// silently replace the first), and tool names that are real names. Unknown JSON
// fields are ignored, so a newer client's row still writes through an older
// build: this validates meaning, not the schema's shape.
func ValidateAgentPresets(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var rows []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return fmt.Errorf("%s must be a JSON array of presets: %w", SettingAgentPresets, err)
	}
	seen := make(map[string]bool, len(rows))
	for index, row := range rows {
		var preset AgentPreset
		if err := json.Unmarshal(row, &preset); err != nil {
			return fmt.Errorf("%s[%d] is not a preset object: %w", SettingAgentPresets, index, err)
		}
		id := strings.TrimSpace(preset.ID)
		if id == "" {
			return fmt.Errorf("%s[%d] has no id: a preset without an id cannot be selected, and a session that references it would have no tools", SettingAgentPresets, index)
		}
		if strings.TrimSpace(preset.Name) == "" {
			return fmt.Errorf("%s[%d] (%s) has no name: the role would be selectable but unlabelled", SettingAgentPresets, index, id)
		}
		if seen[id] {
			return fmt.Errorf("%s[%d] reuses the id %q: the second row would silently replace the first", SettingAgentPresets, index, id)
		}
		seen[id] = true
		for _, tool := range preset.Tools {
			if strings.TrimSpace(tool) == "" {
				return fmt.Errorf("%s[%d] (%s) lists a blank tool name: it is not a tool and cannot be allowed", SettingAgentPresets, index, id)
			}
		}
	}
	return nil
}

// PresetProblemUnknown is the PresetProblem code a session reports when the role
// it stored does not resolve in this build (the preset was deleted, or the row
// was written by an older document). It is a stable machine code, not a message:
// the client owns the wording, and nothing in it names a file or an internal
// error. The gate does not read it — enforcement stays with the role itself.
const PresetProblemUnknown = "unknown-preset"

// SessionPresetSetter is the optional chat half that selects one agent preset
// per session ("" clears the selection). The UI's persona picker uses it.
type SessionPresetSetter interface {
	SetSessionPreset(sessionID, presetID string) error
	SessionPreset(sessionID string) string
}

// ErrSessionPresetInvalid means a preset request cannot be satisfied as asked:
// an empty session id, an unknown session, or a preset id this build does not
// have. It is a *request* problem (the gateway answers -32600), never a reason
// to change the stored selection.
var ErrSessionPresetInvalid = errors.New("session preset: invalid")

// SessionPresetSettingKey is the settings row storing the agent preset one
// session selected. It follows the lifetime of every other `session.<id>.*`
// row (written next to the conversation, gone with it) and is the reason a
// role survives a restart: the selection used to live in a process map, so a
// gateway restart silently turned an orchestrating session back into a coding
// one.
//
// An empty or missing row means "no preset": the session behaves exactly like
// a fresh conversation, which is what keeps the role optional rather than a
// new default.
func SessionPresetSettingKey(sessionID string) string {
	return "session." + sessionID + ".preset"
}

// ParseSessionPresetSettingKey is the inverse of SessionPresetSettingKey: it
// reports the session a `session.<id>.preset` row belongs to.
//
// The generic settings surface (`settings.set`) accepts any key, so a client can
// write this row directly and bypass SetSessionPreset's validation — which used
// to turn a typo into "no role" and, through the empty-allowlist rule, into the
// full tool surface. The gateway uses this helper to send such a write through
// the validating setter instead of the raw store; the agent treats an
// unresolvable row fail-closed regardless, because an old stored row can be
// broken too.
func ParseSessionPresetSettingKey(key string) (string, bool) {
	key = strings.TrimSpace(key)
	const prefix, suffix = "session.", ".preset"
	if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, suffix) || len(key) <= len(prefix)+len(suffix) {
		return "", false
	}
	sessionID := strings.TrimSpace(key[len(prefix) : len(key)-len(suffix)])
	if sessionID == "" {
		return "", false
	}
	return sessionID, true
}

// Agent role IDs (2026-11-26).
//
// A role *is* an AgentPreset — the shape (persona + prompt + tool allowlist +
// model) was already a role, it just had no name the product could depend on.
// Three roles are built in:
//
//   - lead     orchestrates: reads context, splits work, dispatches and audits.
//     Its tool allowlist excludes every write and every shell tool, so "the
//     lead must not do the work itself" is enforced, not merely requested.
//   - executor does the work; no allowlist, so it keeps the full surface.
//   - auditor  reviews a submitted result; read-only, so a review cannot turn
//     into a second implementation.
//
// "Who reviews" is a ROLE, not a second natural person. This product is used
// by one human, so "reviewer ≠ executor" can never mean "two people": it means
// the audit role is not the execution role (see TaskLeaseService.Accept /
// Reject and contracts.TaskReview). A rule phrased around natural persons
// would be unsatisfiable by construction.
const (
	PresetLeadID     = "lead"
	PresetExecutorID = "executor"
	PresetAuditorID  = "auditor"
)

// BuiltinAgentPresets returns the roles this product ships. They are merged
// under the user's presets.agents row (the user's row wins on the same ID), so
// they are always selectable and never overwrite a user edit.
//
// The lead preset is deliberately NOT the default: a session with no preset
// selection keeps today's behaviour, and no setting points at "lead".
func BuiltinAgentPresets() []AgentPreset {
	return []AgentPreset{
		{
			ID:    PresetLeadID,
			Name:  "编排 lead（只派活与验收）",
			Order: -30,
			Persona: "你是编排者（lead），不是执行者。你的职责只有四件事：整理上下文、把目标拆成可委派的子任务、" +
				"用团队/子智能体工具把每个子任务派给执行者、以及在结果回来时调用审计角色验收。\n" +
				"- 你不写代码、不改文件、不执行命令：你的工具集里没有这些工具，看到需要动手的步骤就派出去。\n" +
				"- 派单时写清目标、验收标准与写入范围，让执行者不需要回来问。\n" +
				"- 审计与执行必须由不同角色完成：验收时用审计角色（auditor）或由用户确认，绝不以执行者身份自审。\n" +
				"- 汇报时给出证据（命令与输出原文、测试名、文件路径），不要只说\"已完成\"。",
			Tools: []string{
				// 编排：派单、等待、协作、共享任务板。
				"spawn_teammate", "list_agents", "wait_agent", "send_message",
				"team_task_create", "team_task_list", "team_task_get", "team_task_update",
				"subagent", "subagent_fork",
				// 只读上下文：读代码/文件/记忆/技能目录，用于整理上下文与审计。
				"read_file", "list_dir", "glob", "grep",
				"memory", "skill", "skills",
				"spill_read", "tool_search",
				// 与用户交互（提问/展示结论）。
				"ask_user", "present",
			},
		},
		{
			ID:    PresetExecutorID,
			Name:  "执行 executor（动手实现）",
			Order: -20,
			Persona: "你是执行者（executor）：负责把分到的子任务真正做完。动手改文件、跑命令、跑测试，" +
				"完成后提交证据（命令与输出原文、测试名、改动文件路径）。遇到范围外的改动不要顺手做，" +
				"在结果里说明并交回编排者。",
			// 空 allowlist = 全部工具：执行者需要完整表面（写文件、shell、测试）。
		},
		{
			ID:    PresetAuditorID,
			Name:  "审计 auditor（只读复核）",
			Order: -10,
			Persona: "你是审计者（auditor）：只读复核别人的结果，不做修改。逐条核对验收标准，" +
				"给出 pass / warn / fail 结论与证据（读到的原文、跑过的只读命令输出）。" +
				"发现问题时写清位置与复现方式，交回执行者修改，而不是自己改。\n" +
				"你的工具集是只读的：没有写文件、没有 shell，所以审计不会悄悄变成第二次实现。",
			Tools: []string{
				"read_file", "list_dir", "glob", "grep",
				"memory", "skill", "skills",
				"spill_read", "tool_search",
				"team_task_list", "team_task_get",
				"send_message", "present",
			},
		},
	}
}
