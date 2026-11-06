package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zeokeel/zeokeel/old/internal/mcp"
	"github.com/zeokeel/zeokeel/old/internal/runtime"
	"github.com/zeokeel/zeokeel/old/internal/store"
)

const (
	// mcpResourcesMaxOutput caps every MCP resource/prompt tool result. It is
	// the same 64 KiB limit the other built-in tools use.
	mcpResourcesMaxOutput = DefaultMaxOutputBytes

	// mcpWarningMaxLen bounds one per-server warning line.
	mcpWarningMaxLen = 2048

	// mcpMaxWarnings bounds how many per-server warning lines are reported.
	mcpMaxWarnings = 16
)

// MCPResourceConfig configures the read-only MCP resource and prompt tools.
type MCPResourceConfig struct {
	// Manager is the MCP manager holding the connected servers. It is required
	// at call time; a tool constructed without it reports a clear error.
	Manager *mcp.Manager

	// Store is optional and currently unused by these read-only tools. It is
	// accepted so callers can share one config literal with the other tool
	// groups.
	Store *store.Store
}

const mcpListResourcesDescription = `List resources exposed by the connected MCP servers.

Call it with an empty JSON object:
  {}

The tool returns {"kind":"mcp_list_resources","servers":[...],"count":N,
"resources":[{"server","uri","name","mimeType","size"}],"warnings":[...]}.
Results are aggregated across every connected server and sorted by server and
URI. A server that fails to list resources is reported in "warnings" without
hiding the other servers' resources. The complete JSON result is capped at
64 KiB; "truncated":true marks a shortened list.`

const mcpReadResourceDescription = `Read one resource from one MCP server.

Call it with a JSON object:
  {"server":"files","uri":"file:///notes.txt"}

"server" and "uri" are both required. Text content is returned in "text";
binary content is returned base64-encoded in "blob". Each content item is capped
at 256 KiB and the whole read at 1 MiB by the MCP layer, and "truncated":true
marks shortened content. The tool returns
{"kind":"mcp_read_resource","server":...,"uri":...,"count":N,
"contents":[{...}]} capped at 64 KiB.`

const mcpListPromptsDescription = `List prompts exposed by the connected MCP servers.

Call it with an empty JSON object:
  {}

The tool returns {"kind":"mcp_list_prompts","servers":[...],"count":N,
"prompts":[{"server","name","description","arguments":[{"name",
"description","required"}]}],"warnings":[...]}. Results are aggregated across
every connected server and sorted by server and name. A server that fails to
list prompts is reported in "warnings" without hiding the other servers'
prompts. The complete JSON result is capped at 64 KiB.`

const mcpGetPromptDescription = `Render one prompt from one MCP server.

Call it with a JSON object:
  {"server":"assistant","name":"code_review","arguments":{"language":"go"}}

"server" and "name" are required; "arguments" is an optional object of string
argument values. The tool returns {"kind":"mcp_get_prompt","server":...,
"name":...,"count":N,"messages":[{"role":"user","text":"..."}]} capped at
64 KiB. Non-text prompt content is rendered as a short descriptive marker.`

// NewMCPResourceTools returns the four read-only MCP resource/prompt tools.
func NewMCPResourceTools(cfg MCPResourceConfig) []runtime.Tool {
	base := mcpResourceToolBase{manager: cfg.Manager}
	return []runtime.Tool{
		&mcpListResourcesTool{base: base},
		&mcpReadResourceTool{base: base},
		&mcpListPromptsTool{base: base},
		&mcpGetPromptTool{base: base},
	}
}

// mcpResourceToolBase carries the shared manager handle.
type mcpResourceToolBase struct {
	manager *mcp.Manager
}

// requireManager returns a clear failure when no manager or no connected
// servers are available.
func (b *mcpResourceToolBase) requireManager(tool string) error {
	if b == nil || b.manager == nil {
		return fmt.Errorf("%s: no MCP manager configured", tool)
	}
	if len(b.manager.Servers()) == 0 {
		return fmt.Errorf("%s: no MCP servers are connected", tool)
	}
	return nil
}

type mcpListResourcesTool struct{ base mcpResourceToolBase }

func (t *mcpListResourcesTool) Name() string { return "mcp_list_resources" }

func (t *mcpListResourcesTool) Description() string { return mcpListResourcesDescription }

func (t *mcpListResourcesTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := t.base.requireManager(t.Name()); err != nil {
		return "", err
	}
	var in struct{}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	resources, listErr := t.base.manager.ListResources(ctx)
	out := &mcpListResourcesOutput{
		Kind:      t.Name(),
		Servers:   t.base.manager.Servers(),
		Resources: append([]mcp.ResourceInfo{}, resources...),
		Warnings:  mcpWarnings(listErr),
	}
	out.Count = len(out.Resources)
	return encodeMCPOutput(t.Name(), out, func(total, excess int) bool {
		return shrinkMCPResourceList(out, total)
	}), nil
}

type mcpReadResourceTool struct{ base mcpResourceToolBase }

func (t *mcpReadResourceTool) Name() string { return "mcp_read_resource" }

func (t *mcpReadResourceTool) Description() string { return mcpReadResourceDescription }

func (t *mcpReadResourceTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := t.base.requireManager(t.Name()); err != nil {
		return "", err
	}
	var in struct {
		Server string `json:"server"`
		URI    string `json:"uri"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	in.Server = strings.TrimSpace(in.Server)
	in.URI = strings.TrimSpace(in.URI)
	if in.Server == "" {
		return "", fmt.Errorf(`%s: "server" is required`, t.Name())
	}
	if in.URI == "" {
		return "", fmt.Errorf(`%s: "uri" is required`, t.Name())
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	contents, err := t.base.manager.ReadResource(ctx, in.Server, in.URI)
	if err != nil {
		return "", err
	}
	out := &mcpReadResourceOutput{
		Kind:     t.Name(),
		Server:   in.Server,
		URI:      in.URI,
		Contents: append([]mcp.ResourceContent{}, contents...),
		Count:    len(contents),
	}
	return encodeMCPOutput(t.Name(), out, func(total, excess int) bool {
		return shrinkMCPResourceContents(out, total, excess)
	}), nil
}

type mcpListPromptsTool struct{ base mcpResourceToolBase }

func (t *mcpListPromptsTool) Name() string { return "mcp_list_prompts" }

func (t *mcpListPromptsTool) Description() string { return mcpListPromptsDescription }

func (t *mcpListPromptsTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := t.base.requireManager(t.Name()); err != nil {
		return "", err
	}
	var in struct{}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	prompts, listErr := t.base.manager.ListPrompts(ctx)
	out := &mcpListPromptsOutput{
		Kind:     t.Name(),
		Servers:  t.base.manager.Servers(),
		Prompts:  append([]mcp.PromptInfo{}, prompts...),
		Warnings: mcpWarnings(listErr),
	}
	out.Count = len(out.Prompts)
	return encodeMCPOutput(t.Name(), out, func(total, excess int) bool {
		return shrinkMCPPromptList(out, total)
	}), nil
}

type mcpGetPromptTool struct{ base mcpResourceToolBase }

func (t *mcpGetPromptTool) Name() string { return "mcp_get_prompt" }

func (t *mcpGetPromptTool) Description() string { return mcpGetPromptDescription }

func (t *mcpGetPromptTool) Invoke(ctx context.Context, args json.RawMessage) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := t.base.requireManager(t.Name()); err != nil {
		return "", err
	}
	var in struct {
		Server    string            `json:"server"`
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := decodeArgs(t.Name(), args, &in); err != nil {
		return "", err
	}
	in.Server = strings.TrimSpace(in.Server)
	in.Name = strings.TrimSpace(in.Name)
	if in.Server == "" {
		return "", fmt.Errorf(`%s: "server" is required`, t.Name())
	}
	if in.Name == "" {
		return "", fmt.Errorf(`%s: "name" is required`, t.Name())
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	messages, err := t.base.manager.GetPrompt(ctx, in.Server, in.Name, in.Arguments)
	if err != nil {
		return "", err
	}
	out := &mcpGetPromptOutput{
		Kind:     t.Name(),
		Server:   in.Server,
		Name:     in.Name,
		Messages: append([]mcp.PromptMessage{}, messages...),
		Count:    len(messages),
	}
	return encodeMCPOutput(t.Name(), out, func(total, excess int) bool {
		return shrinkMCPPromptMessages(out, total, excess)
	}), nil
}

type mcpListResourcesOutput struct {
	Kind      string             `json:"kind"`
	Servers   []string           `json:"servers"`
	Count     int                `json:"count"`
	Resources []mcp.ResourceInfo `json:"resources"`
	Warnings  []string           `json:"warnings,omitempty"`
	Truncated bool               `json:"truncated,omitempty"`
}

type mcpReadResourceOutput struct {
	Kind      string                `json:"kind"`
	Server    string                `json:"server"`
	URI       string                `json:"uri"`
	Count     int                   `json:"count"`
	Contents  []mcp.ResourceContent `json:"contents"`
	Truncated bool                  `json:"truncated,omitempty"`
}

type mcpListPromptsOutput struct {
	Kind      string           `json:"kind"`
	Servers   []string         `json:"servers"`
	Count     int              `json:"count"`
	Prompts   []mcp.PromptInfo `json:"prompts"`
	Warnings  []string         `json:"warnings,omitempty"`
	Truncated bool             `json:"truncated,omitempty"`
}

type mcpGetPromptOutput struct {
	Kind      string              `json:"kind"`
	Server    string              `json:"server"`
	Name      string              `json:"name"`
	Count     int                 `json:"count"`
	Messages  []mcp.PromptMessage `json:"messages"`
	Truncated bool                `json:"truncated,omitempty"`
}

// encodeMCPOutput marshals v, calling shrink with the current total size and
// overflow until the document fits mcpResourcesMaxOutput. If v cannot be shrunk
// enough, a small valid JSON fallback is returned instead. It never returns an
// invalid document.
func encodeMCPOutput(kind string, v any, shrink func(total, excess int) bool) string {
	for i := 0; i < 64; i++ {
		raw, err := json.Marshal(v)
		if err != nil {
			return mcpFallbackOutput(kind, "result could not be encoded as JSON")
		}
		if len(raw) <= mcpResourcesMaxOutput {
			return string(raw)
		}
		if shrink == nil || !shrink(len(raw), len(raw)-mcpResourcesMaxOutput) {
			break
		}
	}
	if raw, err := json.Marshal(v); err == nil && len(raw) <= mcpResourcesMaxOutput {
		return string(raw)
	}
	return mcpFallbackOutput(kind, fmt.Sprintf("result exceeds %d bytes and was truncated", mcpResourcesMaxOutput))
}

// mcpKeepCount estimates how many of n items fit in the output budget given a
// current document of total bytes. It always returns fewer than n so callers
// make progress.
func mcpKeepCount(n, total int) int {
	if n <= 0 || total <= mcpResourcesMaxOutput {
		return n
	}
	keep := n * (mcpResourcesMaxOutput - 1024) / total
	if keep < 0 {
		keep = 0
	}
	if keep >= n {
		keep = n - 1
	}
	return keep
}

func mcpFallbackOutput(kind, note string) string {
	fallback := struct {
		Kind      string `json:"kind"`
		Truncated bool   `json:"truncated"`
		Note      string `json:"note"`
	}{
		Kind:      kind,
		Truncated: true,
		Note:      truncateBytes(note, 512, "note truncated"),
	}
	raw, err := json.Marshal(fallback)
	if err != nil {
		return `{"kind":"mcp","truncated":true}`
	}
	return string(raw)
}

// shrinkMCPResourceList drops a proportional number of resources, then
// warnings, then server names.
func shrinkMCPResourceList(out *mcpListResourcesOutput, total int) bool {
	switch {
	case len(out.Resources) > 0:
		keep := mcpKeepCount(len(out.Resources), total)
		out.Resources = out.Resources[:keep]
		out.Count = keep
	case len(out.Warnings) > 0:
		out.Warnings = out.Warnings[:mcpKeepCount(len(out.Warnings), total)]
	case len(out.Servers) > 0:
		out.Servers = out.Servers[:mcpKeepCount(len(out.Servers), total)]
	default:
		return false
	}
	out.Truncated = true
	return true
}

// shrinkMCPPromptList drops a proportional number of prompts, then warnings,
// then server names.
func shrinkMCPPromptList(out *mcpListPromptsOutput, total int) bool {
	switch {
	case len(out.Prompts) > 0:
		keep := mcpKeepCount(len(out.Prompts), total)
		out.Prompts = out.Prompts[:keep]
		out.Count = keep
	case len(out.Warnings) > 0:
		out.Warnings = out.Warnings[:mcpKeepCount(len(out.Warnings), total)]
	case len(out.Servers) > 0:
		out.Servers = out.Servers[:mcpKeepCount(len(out.Servers), total)]
	default:
		return false
	}
	out.Truncated = true
	return true
}

// shrinkMCPResourceContents shortens the largest text or blob field of res, or
// drops a proportional number of metadata-only items.
func shrinkMCPResourceContents(res *mcpReadResourceOutput, total, excess int) bool {
	best := -1
	bestLen := 0
	bestBlob := false
	for i := range res.Contents {
		content := &res.Contents[i]
		if len(content.Text) > bestLen {
			best, bestLen, bestBlob = i, len(content.Text), false
		}
		if len(content.Blob) > bestLen {
			best, bestLen, bestBlob = i, len(content.Blob), true
		}
	}
	if best < 0 {
		if len(res.Contents) == 0 {
			return false
		}
		keep := mcpKeepCount(len(res.Contents), total)
		res.Contents = res.Contents[:keep]
		res.Count = keep
		res.Truncated = true
		return true
	}

	target := bestLen - excess - 128
	if bestBlob {
		res.Contents[best].Blob = truncateBase64(res.Contents[best].Blob, target)
	} else if target <= 0 {
		res.Contents[best].Text = ""
	} else {
		res.Contents[best].Text = truncateBytes(res.Contents[best].Text, target, "content truncated")
	}
	res.Contents[best].Truncated = true
	res.Truncated = true
	return true
}

// shrinkMCPPromptMessages shortens the largest message text of res, or drops a
// proportional number of empty messages.
func shrinkMCPPromptMessages(res *mcpGetPromptOutput, total, excess int) bool {
	best := -1
	bestLen := 0
	for i := range res.Messages {
		if len(res.Messages[i].Text) > bestLen {
			best, bestLen = i, len(res.Messages[i].Text)
		}
	}
	if best < 0 {
		if len(res.Messages) == 0 {
			return false
		}
		keep := mcpKeepCount(len(res.Messages), total)
		res.Messages = res.Messages[:keep]
		res.Count = keep
		res.Truncated = true
		return true
	}
	target := bestLen - excess - 128
	if target <= 0 {
		res.Messages[best].Text = ""
	} else {
		res.Messages[best].Text = truncateBytes(res.Messages[best].Text, target, "message truncated")
	}
	res.Truncated = true
	return true
}

// truncateBase64 shortens a standard base64 string to roughly limit characters
// while keeping it decodable. When s is not valid base64 it falls back to a
// 4-character boundary prefix.
func truncateBase64(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	if limit < 4 {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		cut := limit - limit%4
		if cut <= 0 {
			return ""
		}
		return strings.TrimRight(s[:cut], "=")
	}
	keep := (limit / 4) * 3
	if keep > len(raw) {
		keep = len(raw)
	}
	return base64.StdEncoding.EncodeToString(raw[:keep])
}

// mcpWarnings converts a joined per-server error into bounded warning lines.
func mcpWarnings(err error) []string {
	if err == nil {
		return nil
	}
	lines := strings.Split(err.Error(), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > mcpWarningMaxLen {
			line = truncateBytes(line, mcpWarningMaxLen, "warning truncated")
		}
		out = append(out, line)
		if len(out) >= mcpMaxWarnings {
			break
		}
	}
	if len(out) == 0 {
		out = append(out, "MCP request failed")
	}
	return out
}
