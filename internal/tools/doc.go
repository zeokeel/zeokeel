// Package tools implements ZeoKeel's built-in agent tools.
//
// Every tool is a pure-Go implementation of runtime.Tool. File-system tools
// are confined to a single workspace root: absolute paths, ".." components and
// symlinks that resolve outside the root are rejected. The optional shell tool
// is disabled unless Config.AllowShell is set, and web_fetch refuses to talk to
// loopback, private, link-local or otherwise non-public addresses.
//
// Builtins assembles the tool set and Register installs it into a
// runtime.Registry:
//
//	ts, err := tools.Builtins(tools.Config{WorkspaceRoot: "/home/me/project"})
//	...
//	reg := runtime.NewRegistry()
//	if err := tools.Register(reg, cfg); err != nil { ... }
//
// Tool arguments are JSON objects. Each tool's Description carries the JSON
// schema the model should follow.
package tools
