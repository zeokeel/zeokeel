package contracts

import cordis "github.com/zeokeel/zeokeel-kernel"

// FileDropService delivers native window file drops to the UI: the process
// host forwards dropped paths from the window, a module registers the handler
// that stages them (the chat composer turns them into attachments).
type FileDropService interface {
	// SetHandler installs the drop handler (nil clears it).
	SetHandler(fn func(paths []string))
	// Dispatch delivers one drop to the installed handler.
	Dispatch(paths []string)
}

// KeyFileDrop is provided by the desktop host and consumed by UI modules that
// accept dropped files.
var KeyFileDrop = cordis.NewServiceKey[FileDropService]("zeokeel/filedrop")
