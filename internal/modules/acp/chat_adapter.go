package acp

// chatAdapter adapts the product ChatService to the host-agnostic Chat interface
// of the rig acp/server package. The SDK owns prompt method mapping and the
// streaming diffing; this adapter owns the product side of one turn: subscribe
// to the chat read model, send the prompt (with attachments when needed),
// forward snapshots until the run settles, and expose session/cancel.

import (
	"context"
	"errors"
	"fmt"
	"sync"

	rigserver "github.com/zeokeel/zeokeel-rig/acp/server"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// chatAdapter implements rigserver.Chat over contracts.ChatService.
type chatAdapter struct {
	chat contracts.ChatService
}

var _ rigserver.Chat = (*chatAdapter)(nil)

// NewSession implements rigserver.Chat.
func (a *chatAdapter) NewSession(ctx context.Context) (string, error) {
	return a.chat.NewSession("")
}

// Session implements rigserver.Chat.
func (a *chatAdapter) Session(sessionID string) (rigserver.Session, bool) {
	session, ok := a.chat.Session(sessionID)
	if !ok {
		return rigserver.Session{}, false
	}
	return referenceSession(session), true
}

// Cancel implements rigserver.Chat.
func (a *chatAdapter) Cancel(sessionID string) error {
	return a.chat.Cancel(sessionID)
}

// Prompt implements rigserver.Chat: it subscribes to the session before sending,
// forwards every observed snapshot to the SDK, and returns once the run is no
// longer running (or the context ends).
func (a *chatAdapter) Prompt(ctx context.Context, sessionID string, request rigserver.PromptRequest, onSnapshot func(rigserver.Session)) error {
	settled := make(chan struct{})
	var settleOnce sync.Once
	settle := func() { settleOnce.Do(func() { close(settled) }) }
	forward := func() {
		snapshot, ok := a.chat.Session(sessionID)
		if !ok {
			return
		}
		onSnapshot(referenceSession(snapshot))
		if !snapshot.Running {
			settle()
		}
	}
	unsubscribe, err := a.chat.Subscribe(func(changed string) {
		if changed != sessionID {
			return
		}
		forward()
	})
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer unsubscribe()

	var sendErr error
	if len(request.Attachments) == 0 {
		sendErr = a.chat.Send(sessionID, request.Text)
	} else {
		sender, ok := a.chat.(contracts.AttachmentSender)
		if !ok {
			return errors.New("chat backend does not support image attachments")
		}
		sendErr = sender.SendWithAttachments(sessionID, request.Text, productAttachments(request.Attachments))
	}
	if sendErr != nil {
		return fmt.Errorf("send: %w", sendErr)
	}
	// A backend may settle synchronously without notifying subscribers.
	forward()
	select {
	case <-settled:
		return nil
	case <-ctx.Done():
		_ = a.chat.Cancel(sessionID)
		return ctx.Err()
	}
}

// referenceSession converts a product read-model snapshot into the SDK snapshot.
func referenceSession(session contracts.ChatSession) rigserver.Session {
	out := rigserver.Session{
		ID:      session.ID,
		Title:   session.Title,
		Model:   session.Model,
		Running: session.Running,
		Error:   session.Error,
	}
	if len(session.Messages) == 0 {
		return out
	}
	out.Messages = make([]rigserver.Message, 0, len(session.Messages))
	for _, message := range session.Messages {
		mapped := rigserver.Message{Role: message.Role, Text: message.Text}
		if len(message.Tools) > 0 {
			mapped.Tools = make([]rigserver.ToolCall, 0, len(message.Tools))
			for _, tool := range message.Tools {
				mapped.Tools = append(mapped.Tools, rigserver.ToolCall{
					ID:        tool.ID,
					Name:      tool.Name,
					Arguments: tool.Arguments,
					Status:    tool.Status,
					Summary:   tool.Summary,
				})
			}
		}
		out.Messages = append(out.Messages, mapped)
	}
	return out
}

// productAttachments converts SDK prompt attachments into product attachments.
func productAttachments(attachments []rigserver.Attachment) []contracts.Attachment {
	out := make([]contracts.Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		out = append(out, contracts.Attachment{
			ID:   attachment.ID,
			Name: attachment.Name,
			MIME: attachment.MIME,
			Data: attachment.Data,
		})
	}
	return out
}
