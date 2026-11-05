package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
)

// The deep mode is eino's "deep agent" topology assembled from the ADK
// primitives that the eino module exports without extra modules: one
// ChatModelAgent plus the two tools that make deep agents deep —
//
//   - write_todos keeps a structured task list in the eino session, exactly
//     like eino's prebuilt deep agent does.
//   - task delegates one self-contained request to a focused subagent through
//     the same AgentTool path as the subagent tool (restricted tools and the
//     depth limit apply).
//
// eino's prebuilt/deep package would give the same shape, but it imports
// adk/filesystem, which needs github.com/bmatcuk/doublestar/v4 in go.mod. That
// dependency is frozen in this task, so the topology is composed here from the
// ADK instead. The behavior that matters (planning tools + subagent
// orchestration) is the same; the prebuilt's built-in default instructions and
// its background-task/shell capabilities are not available in this mode.

// deepTodosSessionKey is the eino session key the write_todos tool writes to.
const deepTodosSessionKey = "zeokeel_deep_todos"

// deepTaskDescription documents the task tool for the model.
const deepTaskDescription = `Delegate one self-contained task to a focused subagent.

Call it with a JSON object:
  {"request":"List the failing tests and explain each one"}

"request" is required and must be self-contained: the subagent sees only this
text plus the conversation context, and it has a restricted tool subset. The
tool returns the subagent's final text.`

// deepTodosDescription documents the write_todos tool for the model.
const deepTodosDescription = `Keep the plan for the current task up to date.

Call it with a JSON object:
  {"todos":[{"content":"Read the failing test","activeForm":"Reading the failing test","status":"in_progress"}]}

Each todo has "content", "activeForm" (present continuous wording) and "status"
(pending, in_progress, completed). Replace the whole list on every call: send
every task, not only the changed one. The list is stored in the session.`

// deepTodo is one entry of the deep-mode plan.
type deepTodo struct {
	Content    string `json:"content"`
	ActiveForm string `json:"activeForm"`
	Status     string `json:"status" jsonschema:"enum=pending,enum=in_progress,enum=completed"`
}

type deepWriteTodosArgs struct {
	Todos []deepTodo `json:"todos"`
}

type deepTaskArgs struct {
	Request string `json:"request"`
}

// deepTools returns the two deep-agent tools. A tool construction failure is
// impossible for these schemas but is still propagated.
func (a *Agent) deepTools() ([]tool.BaseTool, error) {
	taskTool, err := utils.InferTool("task", deepTaskDescription, func(ctx context.Context, in deepTaskArgs) (string, error) {
		request := strings.TrimSpace(in.Request)
		if request == "" {
			return "", errors.New(`task: "request" is required`)
		}
		depth := SubagentDepth(ctx) + 1
		result, err := a.RunSubagent(ctx, SubagentRequest{Task: request, Depth: depth})
		if err != nil {
			return "", err
		}
		return result.Output, nil
	})
	if err != nil {
		return nil, fmt.Errorf("runtime: build deep task tool: %w", err)
	}

	todosTool, err := utils.InferTool("write_todos", deepTodosDescription, func(ctx context.Context, in deepWriteTodosArgs) (string, error) {
		for _, todo := range in.Todos {
			switch todo.Status {
			case "pending", "in_progress", "completed":
			default:
				return "", fmt.Errorf("write_todos: unknown status %q (want pending, in_progress, completed)", todo.Status)
			}
		}
		adk.AddSessionValue(ctx, deepTodosSessionKey, in.Todos)
		data, err := json.Marshal(in.Todos)
		if err != nil {
			return "", fmt.Errorf("write_todos: encode todos: %w", err)
		}
		return fmt.Sprintf("Updated todo list to %s", data), nil
	})
	if err != nil {
		return nil, fmt.Errorf("runtime: build write_todos tool: %w", err)
	}

	return []tool.BaseTool{taskTool, todosTool}, nil
}

// buildDeepAgent builds the deep topology: a ChatModelAgent with the registered
// tools plus the task and write_todos orchestration tools.
func (a *Agent) buildDeepAgent(snap agentSnapshot) (adk.Agent, error) {
	deepTools, err := a.deepTools()
	if err != nil {
		return nil, err
	}
	tools := append(a.einoToolsForMode(snap.mode), deepTools...)

	instruction := snap.instruction
	if instruction != "" {
		instruction += "\n\n"
	}
	instruction += "You are a deep agent: plan before acting. Keep a short task list" +
		" with write_todos, update it as you go, and delegate a self-contained" +
		" piece of work to a subagent with task when that is cheaper than doing it" +
		" yourself. Answer with the final result once the plan is complete."

	agent, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
		Name:          a.name,
		Description:   "ZeoKeel deep agent",
		Instruction:   instruction,
		Model:         snap.chatModel,
		MaxIterations: a.maxIterations,
		Handlers:      a.modelHandlers(),
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("runtime: build deep agent: %w", err)
	}
	return agent, nil
}
