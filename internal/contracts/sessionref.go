package contracts

import (
	rigreferences "github.com/zeokeel/zeokeel-rig/session/references"
)

// SessionReferencePrefix marks a reference to another conversation in a prompt
// (the old shell's @session reference). The composer inserts
// "@session:<session-id>" and the agent expands referenced transcripts into the
// model input for that turn. The tokenizer itself is the SDK's
// session/references package, so the composer and the agent cannot drift apart.
const SessionReferencePrefix = rigreferences.Prefix

// Session effort settings: the per-session reasoning effort override.
func SessionEffortSettingKey(sessionID string) string {
	return "session." + sessionID + ".effort"
}

// SessionEffortLow/Medium/High are the accepted effort values.
const (
	SessionEffortLow    = "low"
	SessionEffortMedium = "medium"
	SessionEffortHigh   = "high"
)
