package session

// Chat-mode policy presets: the selector writes both agent.mode and chat.mode;
// the plan agent mode keeps gate priority over any chat mode.

import (
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// chatModeOption is one combined agent/chat mode choice shown in the composer
// selector and the header badge.
type chatModeOption struct {
	key     string
	agent   string
	chat    string
	shortZh string
	shortEn string
	menuZh  string
	menuEn  string
	badgeZh string
	badgeEn string
	warn    bool
}

// chatModeOptions is the selector's fixed order: normal, plan, minimal, creator.
var chatModeOptions = []chatModeOption{
	{
		key: "normal", agent: contracts.AgentModeNormal, chat: contracts.ChatModeStandard,
		shortZh: "普通", shortEn: "Normal", menuZh: "普通模式", menuEn: "Normal mode",
	},
	{
		key: "plan", agent: contracts.AgentModePlan, chat: contracts.ChatModeStandard,
		shortZh: "计划", shortEn: "Plan", menuZh: "计划模式", menuEn: "Plan mode",
		badgeZh: "计划模式", badgeEn: "Plan mode", warn: true,
	},
	{
		key: "minimal", agent: contracts.AgentModeNormal, chat: contracts.ChatModeMinimal,
		shortZh: "精简", shortEn: "Minimal", menuZh: "精简模式", menuEn: "Minimal mode",
		badgeZh: "精简模式", badgeEn: "Minimal mode",
	},
	{
		key: "creator", agent: contracts.AgentModeNormal, chat: contracts.ChatModeCreator,
		shortZh: "创作", shortEn: "Creator", menuZh: "创作模式", menuEn: "Creator mode",
		badgeZh: "创作模式", badgeEn: "Creator mode", warn: true,
	},
}

// normalizeChatMode returns the stored chat mode or standard for unknown/empty
// values (whitespace and case are normalized).
func normalizeChatMode(settings contracts.SettingsService) string {
	if settings == nil {
		return contracts.ChatModeStandard
	}
	switch strings.ToLower(strings.TrimSpace(settings.Get(contracts.SettingChatMode, ""))) {
	case contracts.ChatModeMinimal:
		return contracts.ChatModeMinimal
	case contracts.ChatModeCreator:
		return contracts.ChatModeCreator
	default:
		return contracts.ChatModeStandard
	}
}

// currentChatModeKey resolves the effective selector option: the plan agent mode
// wins, otherwise the chat mode maps to minimal/creator/normal.
func currentChatModeKey(settings contracts.SettingsService) string {
	if settings == nil {
		return "normal"
	}
	if agentMode(settings) == contracts.AgentModePlan {
		return "plan"
	}
	switch normalizeChatMode(settings) {
	case contracts.ChatModeMinimal:
		return "minimal"
	case contracts.ChatModeCreator:
		return "creator"
	default:
		return "normal"
	}
}

// currentChatModeOption returns the option matching currentChatModeKey.
func currentChatModeOption(settings contracts.SettingsService) chatModeOption {
	key := currentChatModeKey(settings)
	for _, option := range chatModeOptions {
		if option.key == key {
			return option
		}
	}
	return chatModeOptions[0]
}

// applyChatMode writes both settings for one option.
func applyChatMode(settings contracts.SettingsService, option chatModeOption) {
	if settings == nil {
		return
	}
	_ = settings.Set(contracts.SettingAgentMode, option.agent)
	_ = settings.Set(contracts.SettingChatMode, option.chat)
}
