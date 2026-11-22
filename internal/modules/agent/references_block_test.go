package agent

// Test-support helper: the reference block renderer moved into the SDK
// (session/references), so the package-level block tests exercise the product
// format + markers through the SDK expander instead of a product-private
// builder.

import (
	"context"
	"strings"

	rig "github.com/zeokeel/zeokeel-rig"
	rigreferences "github.com/zeokeel/zeokeel-rig/session/references"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fixedReferenceResolver serves exactly one session to the SDK expander.
type fixedReferenceResolver struct {
	id       string
	title    string
	messages []rig.Message
}

// ResolveSession implements references.Resolver.
func (r fixedReferenceResolver) ResolveSession(_ context.Context, id string) ([]rig.Message, bool, error) {
	if id != r.id {
		return nil, false, nil
	}
	return r.messages, true, nil
}

// ResolveSessionTitle implements references.TitleResolver.
func (r fixedReferenceResolver) ResolveSessionTitle(_ context.Context, id string) (string, bool) {
	if id != r.id {
		return "", false
	}
	return r.title, true
}

// buildReferenceBlock renders one referenced session through the SDK expander
// and returns just the quoted block (the expander appends the original prompt).
func buildReferenceBlock(session contracts.ChatSession) string {
	messages := make([]rig.Message, 0, len(session.Messages))
	for _, message := range session.Messages {
		messages = append(messages, rig.NewMessage(rig.Role(message.Role), message.Text))
	}
	prompt := rigreferences.Format(session.ID)
	expander := rigreferences.Expander{
		Format:  referenceFormat(),
		Markers: referenceMarkers,
	}
	result, err := expander.ExpandWithResult(context.Background(), fixedReferenceResolver{
		id:       session.ID,
		title:    session.Title,
		messages: messages,
	}, prompt)
	if err != nil || len(result.Messages) == 0 {
		return ""
	}
	return strings.TrimSuffix(result.Messages[0].Text(), prompt)
}
