package session

import (
	"fmt"
	"os"
	"strings"

	"github.com/egoist/mygo"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// ExportSaver is the session export seam: the default opens the native mygo
// save dialog and writes the content there; tests replace it so no desktop
// backend is needed. It returns the written path, "" when the user canceled,
// or an error.
var ExportSaver = func(defaultName, content string) (string, error) {
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Title:       "导出会话",
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

// exportTranscript renders a session as a Markdown document for the export
// dialog: a title heading followed by the plain transcript (the same text the
// "复制对话" action copies).
func exportTranscript(chatSession contracts.ChatSession, title string) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s\n\n", firstNonEmpty(title, "会话"))
	if createdAt := formatTimestamp(chatSession.CreatedAt); createdAt != "" && createdAt != "—" {
		fmt.Fprintf(&builder, "创建时间：%s\n\n", createdAt)
	}
	transcript := transcriptText(chatSession)
	if strings.TrimSpace(transcript) == "" {
		builder.WriteString("（此会话还没有消息）\n")
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
