package session

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ExportDialogTitle is the localized title the default ExportSaver shows; the
// header sets it from the current locale right before invoking the seam (the
// seam signature stays stable for tests).
var ExportDialogTitle = "导出会话"

// ExportJSONLDialogTitle is the localized title of the JSONL export dialog.
var ExportJSONLDialogTitle = "导出会话"

// ExportJSONLSaver is the JSONL export seam (same contract as ExportSaver):
// the default opens the native save dialog and writes the JSONL transcript.
var ExportJSONLSaver = func(defaultName, content string) (string, error) {
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Title:       ExportJSONLDialogTitle,
		DefaultPath: defaultName,
		Filters: []mygo.FileFilter{{
			Name:       "JSONL",
			Extensions: []string{"jsonl"},
		}},
	})
	if err != nil || path == "" {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// ExportSaver is the session export seam: the default opens the native mygo
// save dialog and writes the content there; tests replace it so no desktop
// backend is needed. It returns the written path, "" when the user canceled,
// or an error.
var ExportSaver = func(defaultName, content string) (string, error) {
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Title:       ExportDialogTitle,
		DefaultPath: defaultName,
		Filters: []mygo.FileFilter{{
			Name:       "Markdown",
			Extensions: []string{"md"},
		}},
	})
	if err != nil || path == "" {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// exportJSONLTranscript renders the session as JSON Lines: one session header
// line, then one record per message. Tool calls are flattened to
// {name, summary, status} so the file stays portable.
func exportJSONLTranscript(chatSession contracts.ChatSession) (string, error) {
	var builder strings.Builder
	header := map[string]any{
		"type":      "session",
		"id":        chatSession.ID,
		"title":     chatSession.Title,
		"model":     chatSession.Model,
		"createdAt": chatSession.CreatedAt.Format(time.RFC3339),
		"updatedAt": chatSession.UpdatedAt.Format(time.RFC3339),
	}
	encoded, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	builder.Write(encoded)
	builder.WriteByte('\n')
	for _, message := range chatSession.Messages {
		record := map[string]any{
			"type":      "message",
			"id":        message.ID,
			"role":      message.Role,
			"text":      message.Text,
			"createdAt": message.CreatedAt.Format(time.RFC3339),
		}
		if message.Reasoning != "" {
			record["reasoning"] = message.Reasoning
		}
		if message.Error != "" {
			record["error"] = message.Error
		}
		if feedback := normalizeSessionFeedback(message.Feedback); feedback != "" {
			record["feedback"] = feedback
		}
		if len(message.Tools) > 0 {
			tools := make([]map[string]any, 0, len(message.Tools))
			for _, tool := range message.Tools {
				tools = append(tools, map[string]any{
					"name":    tool.Name,
					"summary": tool.Summary,
					"status":  tool.Status,
				})
			}
			record["tools"] = tools
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return "", err
		}
		builder.Write(encoded)
		builder.WriteByte('\n')
	}
	return builder.String(), nil
}

// exportTranscript renders a session as a Markdown document for the export
// dialog: a title heading followed by the plain transcript (the same text the
// "复制对话" action copies).
func exportTranscript(chatSession contracts.ChatSession, title string, english bool) string {
	var builder strings.Builder
	fallback := "会话"
	if english {
		fallback = "Conversation"
	}
	fmt.Fprintf(&builder, "# %s\n\n", firstNonEmpty(title, fallback))
	if createdAt := formatTimestamp(chatSession.CreatedAt); createdAt != "" && createdAt != "—" {
		if english {
			fmt.Fprintf(&builder, "Created: %s\n\n", createdAt)
		} else {
			fmt.Fprintf(&builder, "创建时间：%s\n\n", createdAt)
		}
	}
	transcript := transcriptText(chatSession, english)
	if strings.TrimSpace(transcript) == "" {
		if english {
			builder.WriteString("(No messages in this conversation yet)\n")
		} else {
			builder.WriteString("（此会话还没有消息）\n")
		}
	} else {
		builder.WriteString(transcript)
		builder.WriteString("\n")
	}
	return builder.String()
}

// sanitizeExportFilename turns a session title into a safe Markdown file name:
// path separators and control characters become "_", surrounding dots/spaces
// are trimmed, and an empty result falls back to "session".
func sanitizeExportFilename(title string) string {
	trimmed := strings.TrimSpace(title)
	var builder strings.Builder
	for _, r := range trimmed {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\n', '\r', '\t', 0:
			builder.WriteRune('_')
		default:
			builder.WriteRune(r)
		}
	}
	name := strings.Trim(strings.TrimSpace(builder.String()), ". ")
	if name == "" {
		return "session"
	}
	return name
}
