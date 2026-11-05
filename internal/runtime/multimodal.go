package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// Multimodal image handling.
//
// Tools such as read_image return their metadata as a JSON object that may
// carry a base64 image_data_url. OpenAI-compatible providers accept images only
// as content parts of a user/assistant message, so a raw tool result is text to
// the model. The runtime therefore watches tool results in the eino agent state
// and, before each model call, appends a synthetic user message carrying the
// image as an image_url part.
//
// Limits:
//   - MaxImagesPerTurn caps how many tool images one agent run attaches. Images
//     already present in the run's message state count toward the cap, and the
//     same data URL is never attached twice.
//   - MaxImageDataURLBytes caps one data URL. Anything larger is dropped; the
//     tool result text (with metadata) still reaches the model.
//
// Images are runtime-only for now: the synthetic user messages live in the eino
// run state and are not written to History/checkpoints, so they are not replayed
// verbatim on later turns. A tool result persisted in history still carries its
// JSON (including image_data_url), so a later turn's first model call re-derives
// the image from that tool message for as long as it remains in the conversation.
//
// Support is provider-dependent. The OpenAI mapping always sends the parts when
// Images is set; a text-only model or gateway may reject the request. There is
// no capability negotiation.
const (
	// MaxImagesPerTurn is the maximum number of tool-returned images attached to
	// one agent run.
	MaxImagesPerTurn = 4

	// MaxImageDataURLBytes is the maximum size of one attached data URL.
	MaxImageDataURLBytes = 2 << 20

	// injectedImageText is the text part of the synthetic user message that
	// accompanies a tool-returned image. %s is the tool name.
	injectedImageText = "Image returned by tool %s (attached)."
)

// injectToolImages returns messages extended with synthetic user messages for
// images carried by tool results. It never mutates the incoming messages; when
// no image is added it returns the input slice unchanged.
//
// A tool result may carry one direct image (read_image's JSON object) or, for a
// PTC batch, several images nested in the results entries; both forms are
// attached in message order. The scan is idempotent: image URLs already present
// in any message are not attached again, so calling it before every model call
// of a run does not grow the conversation.
func injectToolImages(messages []*schema.Message) []*schema.Message {
	if len(messages) == 0 {
		return messages
	}

	seen := make(map[string]bool)
	attached := 0
	for _, msg := range messages {
		for _, part := range imagePartsFromEino(msg) {
			if part.URL == "" || seen[part.URL] {
				continue
			}
			seen[part.URL] = true
			attached++
		}
	}
	if attached >= MaxImagesPerTurn {
		return messages
	}

	out := messages
	for _, msg := range messages {
		if msg == nil || msg.Role != schema.Tool {
			continue
		}
		for _, part := range imagePartsFromToolResult(msg.Content) {
			if part.URL == "" || seen[part.URL] {
				continue
			}
			seen[part.URL] = true
			out = append(out, newInjectedImageMessage(msg.ToolName, part))
			attached++
			if attached >= MaxImagesPerTurn {
				return out
			}
		}
	}
	return out
}

// imagePartsFromToolResult returns every attachable image carried by a tool
// result. A direct tool result has at most one top-level image; a PTC batch
// ("ptc" tool) nests each nested tool's output as a JSON string, so its images
// are collected from the successful result entries in batch order.
func imagePartsFromToolResult(content string) []ImagePart {
	if part, ok := imagePartFromToolResult(content); ok {
		return []ImagePart{part}
	}
	return imagePartsFromPTCResults(content)
}

// imagePartsFromPTCResults extracts images from a PTC batch result's nested
// outputs. Each PTCResult.Output is the raw result text of one nested tool, so
// an image-returning tool such as read_image reaches the model exactly as it
// does on a direct call instead of only as escaped text.
func imagePartsFromPTCResults(content string) []ImagePart {
	content = strings.TrimSpace(content)
	if content == "" || !strings.HasPrefix(content, "{") || !strings.Contains(content, "image_data_url") {
		return nil
	}
	var response struct {
		Results []struct {
			Output string `json:"output"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(content), &response); err != nil {
		return nil
	}
	var parts []ImagePart
	for _, result := range response.Results {
		if part, ok := imagePartFromToolResult(result.Output); ok {
			parts = append(parts, part)
		}
	}
	return parts
}

// imagePartFromToolResult extracts a valid data URL from a tool result's JSON
// object. Anything malformed, non-image or oversized is ignored.
func imagePartFromToolResult(content string) (ImagePart, bool) {
	content = strings.TrimSpace(content)
	if content == "" || !strings.HasPrefix(content, "{") || !strings.Contains(content, "image_data_url") {
		return ImagePart{}, false
	}
	var payload struct {
		ImageDataURL string `json:"image_data_url"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return ImagePart{}, false
	}
	url := strings.TrimSpace(payload.ImageDataURL)
	if !validImageDataURL(url) {
		return ImagePart{}, false
	}
	return ImagePart{URL: url, Detail: string(schema.ImageURLDetailAuto)}, true
}

// validImageDataURL reports whether url is a base64 data URL whose media type is
// an image and whose size fits MaxImageDataURLBytes.
func validImageDataURL(url string) bool {
	if url == "" || len(url) > MaxImageDataURLBytes {
		return false
	}
	lower := strings.ToLower(url)
	if !strings.HasPrefix(lower, "data:image/") {
		return false
	}
	comma := strings.IndexByte(url, ',')
	if comma < 0 {
		return false
	}
	meta := lower[len("data:"):comma]
	if !strings.HasSuffix(meta, ";base64") {
		return false
	}
	payload := url[comma+1:]
	if payload == "" {
		return false
	}
	if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
		return false
	}
	return true
}

// newInjectedImageMessage builds the synthetic user message the model sees. The
// text names the originating tool; the image travels as an eino multimodal input
// part so the provider adapter can map it onto an OpenAI content array.
func newInjectedImageMessage(toolName string, part ImagePart) *schema.Message {
	name := strings.TrimSpace(toolName)
	if name == "" {
		name = "tool"
	}
	return &schema.Message{
		Role:                  schema.User,
		Content:               fmt.Sprintf(injectedImageText, name),
		UserInputMultiContent: imageInputParts([]ImagePart{part}),
	}
}

// userMessageWithAttachments builds the user message for one turn. The text
// (already extended with attachment blocks) stays in Content; images travel as
// multimodal input parts so the provider emits an OpenAI content array.
func userMessageWithAttachments(text string, images []ImagePart) *schema.Message {
	msg := schema.UserMessage(text)
	if parts := imageInputParts(images); len(parts) > 0 {
		msg.UserInputMultiContent = parts
	}
	return msg
}

// imageInputParts converts ImageParts into eino's multimodal input parts.
func imageInputParts(images []ImagePart) []schema.MessageInputPart {
	if len(images) == 0 {
		return nil
	}
	parts := make([]schema.MessageInputPart, 0, len(images))
	for _, part := range images {
		url := part.URL
		if strings.TrimSpace(url) == "" {
			continue
		}
		detail := strings.TrimSpace(part.Detail)
		if detail == "" {
			detail = string(schema.ImageURLDetailAuto)
		}
		parts = append(parts, schema.MessageInputPart{
			Type: schema.ChatMessagePartTypeImageURL,
			Image: &schema.MessageInputImage{
				MessagePartCommon: schema.MessagePartCommon{URL: &url},
				Detail:            schema.ImageURLDetail(detail),
			},
		})
	}
	return parts
}

// imagePartsFromEino extracts image parts from an eino message, covering the
// current multimodal field and the deprecated MultiContent spelling.
func imagePartsFromEino(msg *schema.Message) []ImagePart {
	if msg == nil {
		return nil
	}
	var out []ImagePart
	for _, part := range msg.UserInputMultiContent {
		if part.Type != schema.ChatMessagePartTypeImageURL || part.Image == nil {
			continue
		}
		url := ""
		if part.Image.URL != nil {
			url = *part.Image.URL
		} else if part.Image.Base64Data != nil && part.Image.MIMEType != "" {
			url = "data:" + part.Image.MIMEType + ";base64," + *part.Image.Base64Data
		}
		if url == "" {
			continue
		}
		detail := string(part.Image.Detail)
		if detail == "" {
			detail = string(schema.ImageURLDetailAuto)
		}
		out = append(out, ImagePart{URL: url, Detail: detail})
	}
	for _, part := range msg.MultiContent {
		if part.Type != schema.ChatMessagePartTypeImageURL || part.ImageURL == nil {
			continue
		}
		if part.ImageURL.URL == "" {
			continue
		}
		detail := string(part.ImageURL.Detail)
		if detail == "" {
			detail = string(schema.ImageURLDetailAuto)
		}
		out = append(out, ImagePart{URL: part.ImageURL.URL, Detail: detail})
	}
	return out
}

// imageInjector is an eino ChatModelAgent middleware that attaches images found
// in tool results to the conversation before each model call. It runs for the
// standard, deep, supervisor and planexecute topologies because they all build a
// ChatModelAgent.
type imageInjector struct {
	adk.BaseChatModelAgentMiddleware
}

var _ adk.ChatModelAgentMiddleware = (*imageInjector)(nil)

// newImageInjector returns the middleware added to every ChatModelAgent the
// runtime builds.
func newImageInjector() adk.ChatModelAgentMiddleware {
	return &imageInjector{}
}

// BeforeModelRewriteState appends synthetic user messages for tool images. The
// rewritten state is persisted by eino, so later calls in the same run still see
// the images and the injector does not re-add them.
func (i *imageInjector) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.ChatModelAgentState,
	_ *adk.ModelContext,
) (context.Context, *adk.ChatModelAgentState, error) {
	if state == nil {
		return ctx, state, nil
	}
	state.Messages = injectToolImages(state.Messages)
	return ctx, state, nil
}
