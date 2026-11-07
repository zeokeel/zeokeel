package home

// Home's real composer: a native multiline input with Enter-to-send, read-only
// workspace/model/mode chips, staged attachments and a primary Send action.
// It creates a fresh session for the first prompt (Home is the new-conversation
// surface), sends through the optional AttachmentSender when available, then
// navigates to the conversation screen.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

// AttachmentPicker is the Home composer's file-picker seam; tests replace it
// with a scripted selection so no desktop backend is needed.
var AttachmentPicker = func() ([]string, error) {
	return mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:    "选择附件",
		Multiple: true,
		Filters: []mygo.FileFilter{{
			Name: "文件与图片",
			Extensions: []string{
				"png", "jpg", "jpeg", "gif", "webp",
				"txt", "md", "json", "yaml", "yml", "go", "log", "csv",
			},
		}},
	})
}

const (
	homeMaxAttachmentBytes = 10 << 20
	homeAttachmentInputCap = 16
)

// homeComposerState is the Home composer's UI-thread state.
type homeComposerState struct {
	draft       string
	attachments []contracts.Attachment
	attachErr   string
	sendErr     string
	notice      string
	sending     bool
	attachSeq   int
}

// homeAttachmentMIME maps a picked file to the attachment content type.
func homeAttachmentMIME(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".txt", ".md", ".json", ".yaml", ".yml", ".go", ".log", ".csv":
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}

// stageHomeAttachments reads the picked paths into staged attachments,
// keeping the first readable error for the composer's danger line.
func stageHomeAttachments(st *homeComposerState, paths []string, tr func(zh, en string) string) {
	st.attachErr = ""
	for _, path := range paths {
		if len(st.attachments) >= homeAttachmentInputCap {
			st.attachErr = tr("附件数量已达上限", "Attachment limit reached")
			break
		}
		name := filepath.Base(path)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			st.attachErr = tr("无法添加 "+name+"：不是可读的普通文件", "Could not add "+name+": not a readable regular file")
			continue
		}
		if info.Size() > homeMaxAttachmentBytes {
			st.attachErr = tr(name+" 超过 10 MB 附件上限", name+" exceeds the 10 MB attachment limit")
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			st.attachErr = tr("无法读取 "+name+"：", "Could not read "+name+": ") + err.Error()
			continue
		}
		st.attachSeq++
		st.attachments = append(st.attachments, contracts.Attachment{
			ID:   fmt.Sprintf("home-att-%d", st.attachSeq),
			Name: name,
			MIME: homeAttachmentMIME(path),
			Data: data,
		})
	}
}

// removeHomeAttachment drops one staged attachment.
func removeHomeAttachment(st *homeComposerState, id string) {
	kept := st.attachments[:0]
	for _, attachment := range st.attachments {
		if attachment.ID != id {
			kept = append(kept, attachment)
		}
	}
	st.attachments = kept
}

// formatHomeAttachmentSize renders a chip size in B/KB/MB.
func formatHomeAttachmentSize(size int) string {
	switch {
	case size < 1<<10:
		return fmt.Sprintf("%d B", size)
	case size < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(size)/(1<<10))
	default:
		return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
	}
}

// homeModeLabel reads the effective agent/chat mode for the read-only chip.
func homeModeLabel(settings contracts.SettingsService, tr func(zh, en string) string) string {
	if settings == nil {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(settings.Get(contracts.SettingAgentMode, "")), contracts.AgentModePlan) {
		return tr("计划", "Plan")
	}
	switch strings.ToLower(strings.TrimSpace(settings.Get(contracts.SettingChatMode, ""))) {
	case contracts.ChatModeMinimal:
		return tr("精简", "Minimal")
	case contracts.ChatModeCreator:
		return tr("创作", "Creator")
	default:
		return tr("普通", "Normal")
	}
}

// effectiveHomeModel reports the active session's model (or the configured
// default) for the read-only model chip.
func effectiveHomeModel(chat contracts.ChatService) string {
	if chat == nil {
		return ""
	}
	if active := chat.ActiveSession(); active != "" {
		if session, ok := chat.Session(active); ok && strings.TrimSpace(session.Model) != "" {
			return session.Model
		}
	}
	return chat.Config().Model
}

// submitHomeComposer creates a session, sends the draft (with attachments when
// the backend supports them) and navigates to the conversation screen.
func submitHomeComposer(chat contracts.ChatService, nav contracts.Navigator, runtime contracts.UIRuntime,
	st *homeComposerState, tr func(zh, en string) string) bool {
	if chat == nil || st.sending || strings.TrimSpace(st.draft) == "" {
		return false
	}
	prompt := strings.TrimSpace(st.draft)
	attachments := append([]contracts.Attachment(nil), st.attachments...)
	st.sending = true
	st.sendErr = ""
	id, err := chat.NewSession("")
	if err != nil {
		st.sendErr = tr("发送失败：", "Send failed: ") + err.Error()
		st.sending = false
		runtime.Invalidate()
		return false
	}
	if len(attachments) > 0 {
		if sender, ok := chat.(contracts.AttachmentSender); ok {
			err = sender.SendWithAttachments(id, prompt, attachments)
		} else {
			err = chat.Send(id, prompt)
			st.notice = tr("附件需在会话中发送", "Attachments must be sent in a conversation")
		}
	} else {
		err = chat.Send(id, prompt)
	}
	if err != nil {
		st.sendErr = tr("发送失败：", "Send failed: ") + err.Error()
		st.sending = false
		runtime.Invalidate()
		return false
	}
	st.draft = ""
	st.attachments = nil
	st.attachErr = ""
	st.sendErr = ""
	st.sending = false
	_ = nav.Push("session")
	runtime.Invalidate()
	return true
}

// buildHomeComposer renders the Home composer card.
func buildHomeComposer(c *ui.Context, t *design.Tokens, runtime contracts.UIRuntime,
	navigator contracts.Navigator, chat contracts.ChatService, kctx cordis.Context,
	st *homeComposerState, placeholder string, tr func(zh, en string) string) {
	panel := design.Apply(ui.Column(c), t.Panel()).
		FillWidth().Gap(design.Space2).Padding(design.Space4)
	panel.Children(func() {
		design.Apply(ui.Text(c, tr("向 ZeoKeel 提问…", "Ask ZeoKeel…")), t.Font(design.TypeS)).
			FontWeight(600).TextColor(t.LabelPrimary)

		// Read-only context chips: workspace, effective model, chat/agent mode.
		chips := make([]string, 0, 3)
		if workspace, ok := kctx.Get(contracts.KeyWorkspace); ok && workspace != nil {
			if current, has, err := workspace.Current(context.Background()); err == nil && has {
				chips = append(chips, firstNonEmpty(current.Name, current.Root, current.ID))
			}
		}
		if model := strings.TrimSpace(effectiveHomeModel(chat)); model != "" {
			chips = append(chips, model)
		}
		if settings, ok := kctx.Get(contracts.KeySettings); ok && settings != nil {
			if mode := homeModeLabel(settings, tr); mode != "" {
				chips = append(chips, mode)
			}
		}
		if len(chips) > 0 {
			ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				for _, chip := range chips {
					design.Apply(ui.Badge(c, chip), t.Badge())
				}
			})
		}

		placeholderText := strings.TrimSpace(placeholder)
		ui.TextArea(c, &st.draft).
			Label(placeholderText).
			Placeholder(placeholderText).
			MinHeight(88).FillWidth().
			OnTextKey(func(k ui.TextKey) bool {
				if k.Key != ui.KeyEnter || k.Mods != 0 {
					return false
				}
				if submitHomeComposer(chat, navigator, runtime, st, tr) {
					k.SetText("")
				}
				return true
			})

		if len(st.attachments) > 0 {
			ui.Row(c).FillWidth().Gap(design.Space2).AlignItems(ui.Center).Children(func() {
				for _, attachment := range st.attachments {
					attachment := attachment
					design.Apply(ui.Row(c).AlignItems(ui.Center).Gap(design.Space1).
						Padding(design.Space1, design.Space2).Radius(design.RadiusSM).
						Border(1, t.BorderL2), func(el ui.Element) ui.Element { return el }).Children(func() {
						design.Apply(ui.Text(c, attachment.Name+" · "+formatHomeAttachmentSize(len(attachment.Data))),
							t.Font(design.TypeXXS)).TextColor(t.LabelSecondary).MaxLines(1).Ellipsis("…")
						if ui.Button(c, tr("移除", "Remove")).Clicked() {
							removeHomeAttachment(st, attachment.ID)
							runtime.Invalidate()
						}
					})
				}
			})
		}

		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(design.Space2).Children(func() {
			attach := ui.Button(c, tr("附件", "Attach")).
				Tooltip(tr("添加文件或图片", "Add files or images"))
			if attach.Clicked() {
				paths, err := AttachmentPicker()
				if err != nil {
					st.attachErr = tr("无法打开文件选择器：", "Could not open the file picker: ") + err.Error()
				} else {
					stageHomeAttachments(st, paths, tr)
				}
				runtime.Invalidate()
			}
			design.Apply(ui.Text(c, tr("开始新对话 · Enter 发送 · Shift+Enter 换行",
				"Start a new conversation · Enter to send · Shift+Enter for newline")),
				t.Font(design.TypeXXXS)).TextColor(t.LabelTertiary).MaxLines(1).Grow(1)
			sendLabel := tr("发送", "Send")
			if st.sending {
				sendLabel = tr("发送中…", "Sending…")
			}
			send := design.Apply(ui.PrimaryButton(c, sendLabel), t.PrimaryButton()).
				Disabled(chat == nil || st.sending || strings.TrimSpace(st.draft) == "")
			if send.Clicked() {
				submitHomeComposer(chat, navigator, runtime, st, tr)
			}
		})

		if st.attachErr != "" {
			design.Apply(ui.Text(c, st.attachErr), t.Font(design.TypeXXS)).TextColor(t.DangerText)
		}
		if st.sendErr != "" {
			design.Apply(ui.Text(c, st.sendErr), t.Font(design.TypeXXS)).TextColor(t.DangerText)
		}
		if st.notice != "" {
			design.Apply(ui.Text(c, st.notice), t.Font(design.TypeXXS)).TextColor(t.LabelTertiary)
		}
	})
}
