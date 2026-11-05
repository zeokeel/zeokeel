package tools

import "strings"

// pwshUTF8Preamble pins every Windows PowerShell command's redirected output to
// UTF-8. Windows PowerShell 5.1 (the fallback executable on machines without
// PowerShell 7) writes the console/ANSI code page when stdout is redirected and
// the GUI parent supplies no console, so localized errors and Chinese paths
// arrive as GBK bytes and would be persisted as U+FFFD. DSH solves the same
// problem by prepending the same two assignments before every command
// (dsh-pwsh-local/lib/index.js ENCODING_PREAMBLE); the try/catch keeps a host
// where the setter is unavailable from failing the whole command.
//
// The preamble stays on the command's first line so PowerShell error line
// numbers keep matching the model's command.
const pwshUTF8Preamble = `try { [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false) } catch {}; try { $OutputEncoding = [System.Text.UTF8Encoding]::new($false) } catch {}; `

// pwshPreambleMarker is the stable start of pwshUTF8Preamble, used to recognize
// a command that already pins its own encoding so it is not prefixed twice.
const pwshPreambleMarker = "try { [Console]::OutputEncoding"

// hasScriptHeader reports whether a PowerShell command must begin with its own
// header. A `param(...)` block, `using` statement, `#requires` directive or
// `[CmdletBinding()]` attribute is only valid at the top of a script, so
// prefixing the encoding preamble would make the command invalid. Such commands
// are left untouched (the winenc fallback still decodes their output).
func hasScriptHeader(command string) bool {
	trimmed := strings.TrimSpace(strings.TrimPrefix(command, "\uFEFF"))
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "param("), strings.HasPrefix(lower, "param ("),
		strings.HasPrefix(lower, "param\t("):
		return true
	case strings.HasPrefix(lower, "using "), strings.HasPrefix(lower, "using\t"):
		return true
	case strings.HasPrefix(lower, "#requires"):
		return true
	case strings.HasPrefix(lower, "[cmdletbinding"), strings.HasPrefix(lower, "[parameter("):
		return true
	}
	return false
}

// pwshCommandArg builds the -Command argument for goos. Only Windows needs the
// preamble, and only when the command has no script header of its own; a
// command that already carries the marker is returned unchanged.
func pwshCommandArg(goos, command string) string {
	if !strings.EqualFold(strings.TrimSpace(goos), "windows") {
		return command
	}
	if hasScriptHeader(command) {
		return command
	}
	if strings.HasPrefix(strings.TrimSpace(command), pwshPreambleMarker) {
		return command
	}
	return pwshUTF8Preamble + command
}
