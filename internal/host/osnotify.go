package host

// The OS notification sink, without a UI framework.
//
// The browser client is the window, so "system" notifications are the platform's
// own notifier, reached through its command-line tool. This is deliberately the
// smallest possible surface: no daemon, no library, no state. A platform we do
// not know how to notify reports an error that the caller logs and swallows —
// the in-app toast is always there as the floor.

import (
	"fmt"
	"os/exec"
	goruntime "runtime"
)

// osNotifyCommand builds the platform command for one notification. It is a
// separate function so the three platforms can be pinned by a test on any one
// of them.
func osNotifyCommand(goos, title, body string) (string, []string, error) {
	switch goos {
	case "darwin":
		// AppleScript needs the strings quoted; a body with a quote would break
		// the script, so the two are escaped rather than interpolated raw.
		script := fmt.Sprintf("display notification %s with title %s",
			appleScriptString(body), appleScriptString(title))
		return "osascript", []string{"-e", script}, nil
	case "linux", "freebsd", "openbsd", "netbsd":
		return "notify-send", []string{title, body}, nil
	default:
		return "", nil, fmt.Errorf("system notifications are not supported on %s", goos)
	}
}

// appleScriptString renders s as an AppleScript string literal.
func appleScriptString(s string) string {
	quoted := make([]rune, 0, len(s)+2)
	quoted = append(quoted, '"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			quoted = append(quoted, '\\')
		}
		quoted = append(quoted, r)
	}
	quoted = append(quoted, '"')
	return string(quoted)
}

// osNotifyRun is the execution seam (tests replace it).
var osNotifyRun = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// systemNotifier returns the sink installSystemNotifier installs: it ignores the
// notification id (the platform tools do not dedupe) and reports nothing else.
func systemNotifier() func(id, title, body string) error {
	return func(_, title, body string) error {
		name, args, err := osNotifyCommand(goruntime.GOOS, title, body)
		if err != nil {
			return err
		}
		return osNotifyRun(name, args...)
	}
}
