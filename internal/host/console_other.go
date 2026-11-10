//go:build !windows

package host

// hideWindowsConsoleForChildren is a no-op outside Windows: Unix child
// processes inherit the terminal or have no window of their own.
func hideWindowsConsoleForChildren() {}
