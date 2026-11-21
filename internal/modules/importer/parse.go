package importer

// The DSH session.v4.jsonl parser lives in the SDK; these aliases keep the
// importer's existing call sites and tests unchanged.

import (
	rig "github.com/zeokeel/zeokeel-rig"
	rigimporter "github.com/zeokeel/zeokeel-rig/session/importer"
)

type parsedMessage = rigimporter.Message
type parsedSession = rigimporter.Session

var ErrUnsupportedVersion = rigimporter.ErrUnsupportedVersion

func parseSessionFile(path string) (*parsedSession, error) {
	return rigimporter.ParseSessionFile(path)
}

func importedTitle(messages []parsedMessage) string {
	return rigimporter.Title(messages)
}

func importedRigMessages(messages []parsedMessage) []rig.Message {
	return rigimporter.Messages(messages)
}
