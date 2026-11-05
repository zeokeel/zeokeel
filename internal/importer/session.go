package importer

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// ContentPart is one DSH message content part:
//
//	{"type":"text","text":"..."}
//	{"type":"reasoning","text":"..."}
//	{"type":"tool-call","id":"...","name":"...","arguments":"{...}"}
type ContentPart struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	ID        string          `json:"id"`
}

// ErrUnsupportedVersion reports a DSH session header whose format version this
// importer does not understand. A future v5 log must be reported explicitly
// instead of being parsed as v4 and silently losing events (PD-04).
var ErrUnsupportedVersion = errors.New("importer: unsupported DSH session version")

// supportedSessionVersion is the DSH session-log format this package parses.
// DSH 0.2.x writes version 4 (session.v4.jsonl[.zstd]).
const supportedSessionVersion = 4

// Message is one imported conversation row (store-agnostic).
type Message struct {
	Role      string
	Content   string
	Reasoning string
	Turn      int
}

// Session is one parsed DSH session. Messages holds only the rows that can be
// replayed safely in ZeoKeel (user/assistant text+reasoning); tool/system
// events are counted so the report can explain what was dropped.
type Session struct {
	ID              string
	Cwd             string
	ParentSession   string
	Origin          string
	AgentPreset     string
	DelegationDepth int
	CreatedAt       time.Time
	Title           string
	Messages        []Message
	SystemMessages  int
	// SystemInjected counts user/message events whose source.kind is not
	// "user": DSH injects per-turn context (time-context, runtime-context,
	// agent-message, subagent-settled, ...) through user events. They are
	// context, not the human's words, so they are not imported.
	SystemInjected int
	// StepAssistant counts assistant/message events with no visible text
	// (reasoning/tool-call-only step events). Only real replies are kept.
	StepAssistant int
	ToolCalls     int
	ToolResults   int
	// Images counts content parts that were dropped (image import is out of
	// scope for the minimal importer) so the report can surface them.
	Images   int
	Warnings []string
}

// sessionHeader is the first JSONL line of a DSH session log.
type sessionHeader struct {
	Type            string `json:"type"`
	Version         int    `json:"version"`
	ID              string `json:"id"`
	CreatedAt       int64  `json:"createdAt"`
	Cwd             string `json:"cwd"`
	ParentSession   string `json:"parentSession"`
	IsSeeded        bool   `json:"isSeeded"`
	Origin          string `json:"origin"`
	DelegationDepth int    `json:"delegationDepth"`
	AgentPreset     string `json:"agentPreset"`
}

// eventEnvelope is one {type, seq, time, data} JSONL line.
type eventEnvelope struct {
	Type string          `json:"type"`
	Seq  int64           `json:"seq"`
	Time int64           `json:"time"`
	Data json.RawMessage `json:"data"`
}

// messageData is the data payload shared by user/message, assistant/message,
// tool/result and system/message events.
type messageData struct {
	Turn    int `json:"turn"`
	Message struct {
		Role       string        `json:"role"`
		Content    []ContentPart `json:"content"`
		ToolCallID string        `json:"toolCallId"`
		IsError    bool          `json:"isError"`
		ID         string        `json:"id"`
	} `json:"message"`
}

// userMessageData is the flat data payload of user/message events:
// {content:[...], role, source, id} (no nested "message" wrapper).
type userMessageData struct {
	Content []ContentPart `json:"content"`
	Role    string        `json:"role"`
	ID      string        `json:"id"`
	Source  struct {
		Kind string `json:"kind"`
	} `json:"source"`
}

// titleData is the data payload of session/title.
type titleData struct {
	Title string `json:"title"`
}

// toolCallData is the data payload of tool/call.
type toolCallData struct {
	CallID    string `json:"callId"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ParseSessionFile opens (decompressing when needed) and parses one DSH
// session JSONL file.
func ParseSessionFile(path string) (*Session, error) {
	stream, err := openSessionStream(path)
	if err != nil {
		return nil, err
	}
	defer stream.Close() //nolint:errcheck // read error wins
	sess, err := ParseSession(stream, path)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// ParseSession parses one DSH session JSONL stream. source is used in errors
// and warnings only. A torn final line (crash during append) is tolerated: DSH
// itself ignores it, and failing a whole migration for one partial write would
// lose otherwise intact history.
func ParseSession(r io.Reader, source string) (*Session, error) {
	reader := bufio.NewReaderSize(r, 64*1024)
	sess := &Session{}
	headerSeen := false
	lineNo := 0
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			lineNo++
			trimmed := strings.TrimSpace(string(line))
			if trimmed != "" {
				if perr := parseLine(sess, trimmed, headerSeen, source, lineNo); perr != nil {
					// Tolerate only a torn tail: a last line without a newline
					// that does not parse.
					if err == io.EOF && !strings.HasSuffix(string(line), "\n") {
						sess.Warnings = append(sess.Warnings, fmt.Sprintf("%s: ignoring torn final line %d: %v", source, lineNo, perr))
					} else {
						return nil, perr
					}
				}
				headerSeen = headerSeen || sess.ID != ""
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("importer: read %s: %w", source, err)
		}
	}
	if !headerSeen {
		return nil, fmt.Errorf("importer: %s: missing session header line", source)
	}
	if sess.Title == "" {
		sess.Title = "Imported DSH session " + sess.ID
	}
	return sess, nil
}

// parseLine decodes one JSONL line into sess.
func parseLine(sess *Session, line string, headerSeen bool, source string, lineNo int) error {
	if !headerSeen {
		var header sessionHeader
		if err := json.Unmarshal([]byte(line), &header); err != nil {
			return fmt.Errorf("importer: %s:%d: session header: %w", source, lineNo, err)
		}
		if header.Type != "session" {
			return fmt.Errorf("importer: %s:%d: first line is %q, want session header", source, lineNo, header.Type)
		}
		if header.Version != supportedSessionVersion {
			return fmt.Errorf("%w: %s: header version %d; this build reads version %d",
				ErrUnsupportedVersion, source, header.Version, supportedSessionVersion)
		}
		sess.ID = header.ID
		sess.Cwd = header.Cwd
		sess.ParentSession = header.ParentSession
		sess.Origin = header.Origin
		sess.AgentPreset = header.AgentPreset
		sess.DelegationDepth = header.DelegationDepth
		if header.CreatedAt > 0 {
			sess.CreatedAt = time.UnixMilli(header.CreatedAt).UTC()
		}
		return nil
	}

	var envelope eventEnvelope
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		return fmt.Errorf("importer: %s:%d: event: %w", source, lineNo, err)
	}
	switch envelope.Type {
	case "session/title":
		var data titleData
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			return fmt.Errorf("importer: %s:%d: session/title: %w", source, lineNo, err)
		}
		if title := strings.TrimSpace(data.Title); title != "" {
			sess.Title = title
		}
	case "user/message":
		var data userMessageData
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			return fmt.Errorf("importer: %s:%d: user/message: %w", source, lineNo, err)
		}
		// source.kind identifies where the text came from. DSH writes most
		// per-turn context as user/message with kind time-context /
		// runtime-context / agent-message / subagent-settled / tool-jobs /
		// goal / ... (89% of real user events). Those are injected context,
		// not the human's words: importing them renders fake user bubbles.
		// An empty kind means an exported/hand-written fixture, kept as user
		// for backward compatibility.
		if kind := strings.TrimSpace(data.Source.Kind); kind != "" && !strings.EqualFold(kind, "user") {
			sess.SystemInjected++
			return nil
		}
		content, reasoning, images := flattenContent(data.Content)
		sess.Images += images
		if strings.TrimSpace(content) != "" {
			sess.Messages = append(sess.Messages, Message{Role: "user", Content: content, Reasoning: reasoning})
		}
	case "assistant/message":
		var data messageData
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			return fmt.Errorf("importer: %s:%d: assistant/message: %w", source, lineNo, err)
		}
		content, reasoning, images := flattenContent(data.Message.Content)
		sess.Images += images
		// DSH emits one assistant/message per step; step events carry
		// reasoning and tool-call parts but no visible reply (95.7% of real
		// assistant events). Turning them into empty bubbles or synthetic
		// "[tool call] ..." transcript lines makes the migrated conversation
		// unreadable, so only messages with real text are imported (reasoning
		// is kept alongside the text).
		if strings.TrimSpace(content) == "" {
			sess.StepAssistant++
			return nil
		}
		sess.Messages = append(sess.Messages, Message{
			Role:      "assistant",
			Content:   content,
			Reasoning: reasoning,
			Turn:      data.Turn,
		})
	case "system/message":
		var data messageData
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			return fmt.Errorf("importer: %s:%d: system/message: %w", source, lineNo, err)
		}
		sess.SystemMessages++
	case "tool/call":
		sess.ToolCalls++
	case "tool/result":
		sess.ToolResults++
	}
	return nil
}

// flattenContent renders DSH content parts into the text/reasoning pair
// ZeoKeel persists. Tool-call and tool-result parts are not turned into
// transcript text: tool activity is counted separately and synthesized
// "[tool call] ..." lines made migrated conversations unreadable (PD-04).
// Parts that carry an image are counted as dropped so the report can surface
// them; image import itself is out of scope for the minimal importer.
func flattenContent(parts []ContentPart) (content, reasoning string, images int) {
	var text, think strings.Builder
	for _, part := range parts {
		switch {
		case part.Type == "text":
			text.WriteString(part.Text)
		case part.Type == "reasoning":
			think.WriteString(part.Text)
		case part.Type == "tool-call", part.Type == "tool-result":
			// Counted from tool/call and tool/result events.
		case strings.Contains(part.Type, "image"):
			images++
		}
	}
	return strings.TrimSpace(text.String()), strings.TrimSpace(think.String()), images
}
