package session

// Internal tests for the conversation font-size setting and its Markdown cache.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

type fontSizeSettings struct {
	values map[string]string
}

func (s *fontSizeSettings) Get(key, fallback string) string {
	if s.values == nil {
		return fallback
	}
	if value, ok := s.values[key]; ok {
		return value
	}
	return fallback
}
func (s *fontSizeSettings) Set(key, value string) error {
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[key] = value
	return nil
}
func (s *fontSizeSettings) All() map[string]string {
	copied := make(map[string]string, len(s.values))
	for key, value := range s.values {
		copied[key] = value
	}
	return copied
}

func TestMarkdownOptionsFollowChatFontSize(t *testing.T) {
	for value, want := range map[string]float32{
		"":         14,
		"standard": 14,
		"small":    12.5,
		"large":    16,
		"garbage":  14,
	} {
		settings := &fontSizeSettings{values: map[string]string{}}
		if value != "" {
			settings.values[contracts.SettingChatFontSize] = value
		}
		st := &state{settings: settings}
		options := st.markdownOptions(design.Light())
		if options.Theme.FontSize != want {
			t.Fatalf("font size for %q = %v, want %v", value, options.Theme.FontSize, want)
		}
		if options.Theme.LineHeight != 22.0/14.0 {
			t.Fatalf("line-height multiplier for %q = %v, want 22/14", value, options.Theme.LineHeight)
		}
	}
}

func TestMarkdownOptionsCacheIncludesFontSize(t *testing.T) {
	settings := &fontSizeSettings{values: map[string]string{
		contracts.SettingChatFontSize: "small",
	}}
	st := &state{settings: settings}
	tokens := design.Light()
	small := st.markdownOptions(tokens)
	if small.Theme.FontSize != 12.5 {
		t.Fatalf("initial font size = %v", small.Theme.FontSize)
	}
	settings.values[contracts.SettingChatFontSize] = "large"
	large := st.markdownOptions(tokens)
	if large.Theme.FontSize != 16 {
		t.Fatalf("cache did not include font size: got %v", large.Theme.FontSize)
	}
	if large == small {
		t.Fatal("cache returned the same options after the setting changed")
	}
}

func TestMessageFontSize(t *testing.T) {
	settings := &fontSizeSettings{values: map[string]string{contracts.SettingChatFontSize: "large"}}
	st := &state{settings: settings}
	if got := st.messageFontSize(); got != 16 {
		t.Fatalf("messageFontSize() = %v, want 16", got)
	}
	settings.values[contracts.SettingChatFontSize] = "small"
	if got := st.messageFontSize(); got != 12.5 {
		t.Fatalf("messageFontSize() = %v, want 12.5", got)
	}
	settings.values[contracts.SettingChatFontSize] = "bogus"
	if got := st.messageFontSize(); got != 14 {
		t.Fatalf("bad messageFontSize() = %v, want 14", got)
	}
}
