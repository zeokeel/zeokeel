package agent

// Tests for the standard/minimal/creator chat-mode tool policies.

import (
	"context"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func chatModeService(t *testing.T, agentMode, chatMode string) *service {
	t.Helper()
	settings := newFakeSettings(map[string]string{
		contracts.SettingAgentMode: agentMode,
		contracts.SettingChatMode:  chatMode,
	})
	return newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
}

func TestGateChatModePolicies(t *testing.T) {
	cases := []struct {
		name      string
		agentMode string
		chatMode  string
		allows    []string
		denies    []string
	}{
		{
			name: "standard keeps approvals", chatMode: contracts.ChatModeStandard,
			allows: []string{"read_file", "write_file", "bash", "unknown_tool"},
		},
		{
			name: "empty chat mode is standard", chatMode: "",
			allows: []string{"bash", "write_file"},
		},
		{
			name: "unknown chat mode is standard", chatMode: "bogus",
			allows: []string{"bash", "write_file"},
		},
		{
			name:     "minimal allows read-only exploration",
			chatMode: contracts.ChatModeMinimal,
			allows: []string{"read_file", "list_dir", "glob", "grep", "memory", "skill", "skills", "web_fetch",
				"ask_user", "present", "goal", "plan_review", "exit_plan_mode", "team_task_list", "team_task_get"},
			denies: []string{"write_file", "edit_file", "bash", "pwsh", "job_run", "team_task_create", "unknown_tool"},
		},
		{
			name:     "minimal is case and whitespace normalized",
			chatMode: " MINIMAL ",
			allows:   []string{"read_file"},
			denies:   []string{"write_file", "bash"},
		},
		{
			name:     "creator allows persona/planning helpers only",
			chatMode: contracts.ChatModeCreator,
			allows:   []string{"ask_user", "present", "goal", "plan_review", "exit_plan_mode", "memory", "skill", "skills"},
			denies:   []string{"read_file", "list_dir", "glob", "grep", "write_file", "bash", "web_fetch", "team_task_list", "unknown_tool"},
		},
		{
			name: "plan mode outranks creator", agentMode: contracts.AgentModePlan, chatMode: contracts.ChatModeCreator,
			allows: []string{"read_file", "list_dir", "goal"},
			denies: []string{"bash", "write_file"},
		},
		{
			name: "plan mode outranks minimal", agentMode: contracts.AgentModePlan, chatMode: contracts.ChatModeMinimal,
			allows: []string{"read_file", "web_fetch"},
			denies: []string{"write_file", "bash"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := chatModeService(t, tc.agentMode, tc.chatMode)
			gate := svc.gate()
			for _, name := range tc.allows {
				decision, err := gate.Approve(context.Background(), &gateTestTool{name: name}, rig.ToolCall{Name: name})
				if err != nil || decision != rig.DecisionAllow {
					t.Fatalf("%q decision = %q/%v, want allow", name, decision, err)
				}
			}
			for _, name := range tc.denies {
				decision, err := gate.Approve(context.Background(), &gateTestTool{name: name}, rig.ToolCall{Name: name})
				if err != nil || decision != rig.DecisionDeny {
					t.Fatalf("%q decision = %q/%v, want deny", name, decision, err)
				}
			}
		})
	}
}

func TestGateChatModeSwitches(t *testing.T) {
	settings := newFakeSettings(map[string]string{})
	svc := newService(Config{}, rigtools.NewRegistry(), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	tool := &gateTestTool{name: "bash"}
	approve := func() rig.Decision {
		decision, err := svc.gate().Approve(context.Background(), tool, rig.ToolCall{Name: "bash"})
		if err != nil {
			t.Fatalf("Approve: %v", err)
		}
		return decision
	}
	if got := approve(); got != rig.DecisionAllow {
		t.Fatalf("default bash = %q, want approvals allow", got)
	}
	_ = settings.Set(contracts.SettingChatMode, contracts.ChatModeMinimal)
	if got := approve(); got != rig.DecisionDeny {
		t.Fatalf("minimal bash = %q, want deny", got)
	}
	_ = settings.Set(contracts.SettingChatMode, contracts.ChatModeStandard)
	if got := approve(); got != rig.DecisionAllow {
		t.Fatalf("standard bash = %q, want allow", got)
	}
	_ = settings.Set(contracts.SettingChatMode, contracts.ChatModeCreator)
	read := &gateTestTool{name: "read_file"}
	if decision, _ := svc.gate().Approve(context.Background(), read, rig.ToolCall{Name: "read_file"}); decision != rig.DecisionDeny {
		t.Fatalf("creator read_file = %q, want deny", decision)
	}
}

func TestGateIntegrationMinimalBlocksWriteTool(t *testing.T) {
	writeTool := &gateTestTool{name: "write_file"}
	provider := &toolCallScriptProvider{tool: "write_file", toolID: "call-write"}
	settings := newFakeSettings(map[string]string{contracts.SettingChatMode: contracts.ChatModeMinimal})
	svc := newService(Config{}, rigtools.NewRegistry(writeTool), contracts.SettingsService(settings),
		&fakeLogger{}, newFakeStore(), fakeApprovals{})
	svc.providerFactory = func(contracts.ChatConfig) (rig.Provider, error) { return provider, nil }
	id, err := svc.NewSession("minimal gate")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := svc.Send(id, "write something"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitIdle(t, svc, id)
	if writeTool.count != 0 {
		t.Fatalf("minimal denied write_file executed %d times", writeTool.count)
	}
}
