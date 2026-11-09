package session

// Internal tests for the conversation font-size setting and its Markdown cache.

import (
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
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

// TestMarkdownHeadingScale covers the transcript hierarchy: the Markdown
// headings follow the app type scale (H1 17–18, H2 16, H3 15 at weight 600) and
// stay below the 20/600 page title, at every chat font size.
func TestMarkdownHeadingScale(t *testing.T) {
	pageTitle := float32(20)
	for _, value := range []string{"small", "standard", "large"} {
		settings := &fontSizeSettings{values: map[string]string{contracts.SettingChatFontSize: value}}
		st := &state{settings: settings}
		options := st.markdownOptions(design.Light())
		theme := options.Theme

		h1 := theme.Headings[0].Size * theme.FontSize
		h2 := theme.Headings[1].Size * theme.FontSize
		h3 := theme.Headings[2].Size * theme.FontSize
		if h1 < 17 || h1 > 18.001 {
			t.Fatalf("%s: H1 = %.2f, want 17–18", value, h1)
		}
		if h2 > 16.001 || h3 > 15.001 {
			t.Fatalf("%s: H2/H3 = %.2f/%.2f, want at most 16/15", value, h2, h3)
		}
		if h1 >= pageTitle {
			t.Fatalf("%s: H1 = %.2f must stay below the %.0f page title", value, h1, pageTitle)
		}
		for index, heading := range theme.Headings {
			if heading.Weight != 600 {
				t.Fatalf("%s: heading %d weight = %d, want 600", value, index+1, heading.Weight)
			}
		}
	}
}

// localeStub is a minimal contracts.LocaleService for copy/date tests.
type localeStub struct{ lang string }

func (l localeStub) Language() string            { return l.lang }
func (l localeStub) SetLanguage(language string) {}
func (l localeStub) T(key string) string         { return key }

// TestFormatTimestampFollowsLocale covers R2-05: the memory/time rows use
// uikit.FormatDateTime instead of a hard-coded layout, and an unset timestamp
// keeps its em dash.
func TestFormatTimestampFollowsLocale(t *testing.T) {
	instant := time.Date(2023, 11, 15, 6, 13, 0, 0, time.Local)
	if got := formatTimestamp(nil, instant); got != "2023-11-15 06:13" {
		t.Fatalf("zh timestamp = %q, want 2023-11-15 06:13", got)
	}
	english := localeStub{lang: "en"}
	if got := formatTimestamp(english, instant); got != "Nov 15, 2023 6:13 AM" {
		t.Fatalf("en timestamp = %q, want the readable English form", got)
	}
	if got := formatTimestamp(english, time.Time{}); got != "—" {
		t.Fatalf("unset timestamp = %q, want the em dash", got)
	}
}

// TestReasoningMarkdownOptions is the thinking panel's theme: it is smaller than
// the reply body (one DIP, never below 11), muted, keeps strong text at the
// primary color, and its headings never outrank the reply's. It follows the
// chat font-size setting and is cached separately from the prose options.
func TestReasoningMarkdownOptions(t *testing.T) {
	for value, want := range map[string]float32{
		"":         13,
		"standard": 13,
		"small":    11.5,
		"large":    15,
	} {
		settings := &fontSizeSettings{values: map[string]string{}}
		if value != "" {
			settings.values[contracts.SettingChatFontSize] = value
		}
		tokens := design.Light()
		st := &state{settings: settings}
		options := st.reasoningMarkdownOptions(tokens)
		theme := options.Theme
		if theme.FontSize != want {
			t.Fatalf("reasoning font size for %q = %v, want %v", value, theme.FontSize, want)
		}
		if theme.Text != tokens.LabelSecondary || theme.Muted != tokens.LabelTertiary {
			t.Fatalf("%q: reasoning theme is not muted: text %v muted %v", value, theme.Text, theme.Muted)
		}
		if theme.Strong != tokens.LabelPrimary {
			t.Fatalf("%q: reasoning strong text color = %v, want the primary label", value, theme.Strong)
		}
		// The panel's H1 must stay below the reply's 17–18 DIP H1.
		h1 := theme.Headings[0].Size * theme.FontSize
		proseH1 := st.markdownOptions(tokens).Theme.Headings[0].Size * st.messageFontSize()
		if h1 >= proseH1 || h1 > 16.5 {
			t.Fatalf("%q: reasoning H1 = %.2f DIP, want below the reply's %.2f", value, h1, proseH1)
		}
		// The prose options keep their own cache entry.
		if prose := st.markdownOptions(tokens); prose.Theme.FontSize != st.messageFontSize() {
			t.Fatalf("%q: the reasoning theme overwrote the prose options: %v", value, prose.Theme.FontSize)
		}
	}
}

func TestReasoningLineCount(t *testing.T) {
	// The folded reasoning hint counts lines in the shared ReasoningTrace entry,
	// so the clone that used to live in this package is gone.
	for text, want := range map[string]int{
		"":                          0,
		"一行":                        1,
		"第一行\n\n第二行\n":              2,
		"  \n line \n \n second \n": 2,
	} {
		if got := uikit.ReasoningLineCount(text); got != want {
			t.Fatalf("ReasoningLineCount(%q) = %d, want %d", text, got, want)
		}
	}
}
