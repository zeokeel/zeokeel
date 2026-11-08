package agent

// RequireFinishReason resolves the product's stream-completeness policy.
//
// The OpenAI-compatible provider (github.com/zeokeel/zeokeel-rig v0.13.1+)
// implements the detection itself behind openai.Config.RequireCompleteStream:
// a stream that closes before a finish_reason or the [DONE] terminator returns
// openai.ErrIncompleteStream instead of a clean io.EOF. The product only has to
// decide whether that policy is on: Config.RequireFinishReason (enabled by both
// shipped assemblies) with the chat.requireFinishReason setting as an override
// for minimal OpenAI-compatible servers that never report finish reasons.

import (
	"strings"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// requireFinishReason reports whether streams must terminate cleanly. The
// chat.requireFinishReason setting wins when set ("true"/"1"/"on"/"yes" or
// "false"/"0"/"off"/"no"), otherwise the module config decides.
func (s *service) requireFinishReason() bool {
	if s.settings != nil {
		switch strings.ToLower(strings.TrimSpace(s.settings.Get(contracts.SettingsChatRequireFinishReason, ""))) {
		case "false", "0", "off", "no":
			return false
		case "true", "1", "on", "yes":
			return true
		}
	}
	return s.cfg.RequireFinishReason
}
