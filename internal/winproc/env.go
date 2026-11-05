package winproc

import "strings"

// Child-process environment scrubbing.
//
// Tool children (bash, pwsh, terminal, jobs, worktrees) and MCP servers inherit
// the desktop app's environment. That environment usually contains provider API
// keys, cloud credentials and CI tokens; a prompt-injected tool call (or an MCP
// server started by configuration) could read them with plain `env`. Scrubbing
// the well-known secret names closes that exfiltration path without turning the
// child environment into a whitelist, which would break ordinary commands that
// rely on PATH, HOME, language, terminal and build-tool variables.

// secretEnvPrefixes are namespaces whose variables are credentials or internal
// ZeoKeel state. Matching is case-insensitive.
var secretEnvPrefixes = []string{
	"ZEOKEEL_",
	"DEEPSEEK",
}

// secretEnvFragments catch the common credential naming patterns across
// providers and tools: OPENAI_API_KEY, AWS_SECRET_ACCESS_KEY, GITHUB_TOKEN,
// AZURE_CLIENT_SECRET, GOOGLE_APPLICATION_CREDENTIALS, ...
var secretEnvFragments = []string{
	"API_KEY",
	"APIKEY",
	"TOKEN",
	"SECRET",
	"PASSWORD",
	"PASSWD",
	"CREDENTIAL",
	"PRIVATE_KEY",
	"ACCESS_KEY",
	"CLIENT_KEY",
	"ENCRYPTION_KEY",
	"SIGNING_KEY",
	"MASTER_KEY",
	"SESSION_KEY",
	"AUTH_KEY",
}

// secretEnvNames are exact names that carry credentials without matching a
// fragment above (locations of credential files, database URLs with embedded
// passwords, connection sockets).
var secretEnvNames = map[string]bool{
	"SSH_AUTH_SOCK": true,
	"KUBECONFIG":    true,
	"MYSQL_PWD":     true,
	"PGPASSWORD":    true,
	"REDIS_URL":     true,
	"DATABASE_URL":  true,
	"MONGODB_URI":   true,
	"POSTGRES_URL":  true,
	"POSTGRES_URI":  true,
}

// IsSecretEnvName reports whether name is a variable that may carry a
// credential or ZeoKeel private state and must not be inherited by child
// processes. Ordinary names (PATH, HOME, LANG, TERM, GIT_AUTHOR_NAME, ...) are
// preserved.
func IsSecretEnvName(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	if upper == "" {
		return false
	}
	for _, prefix := range secretEnvPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	for _, fragment := range secretEnvFragments {
		if strings.Contains(upper, fragment) {
			return true
		}
	}
	return secretEnvNames[upper]
}

// ScrubEnv returns env without credential-bearing entries. Entries keep their
// original order, values and case; only whole NAME=VALUE entries are removed.
// Non-Windows callers use it the same way, so the tools and the MCP client
// share one definition of "secret environment".
func ScrubEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name := entry
		if i := strings.IndexByte(entry, '='); i >= 0 {
			name = entry[:i]
		}
		if IsSecretEnvName(name) {
			continue
		}
		out = append(out, entry)
	}
	return out
}
