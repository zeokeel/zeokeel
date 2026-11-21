package importer

// The DSH session.v4.jsonl parser lives in the SDK; these aliases keep the
// importer's existing call sites and tests unchanged.

import (
	rigimporter "github.com/zeokeel/zeokeel-rig/session/importer"
)

type parsedMessage = rigimporter.Message
type parsedSession = rigimporter.Session

var ErrUnsupportedVersion = rigimporter.ErrUnsupportedVersion

func parseSessionFile(path string) (*parsedSession, error) {
	return rigimporter.ParseSessionFile(path)
}
