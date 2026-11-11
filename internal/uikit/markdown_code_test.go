package uikit_test

// Tests for the product's quiet Markdown code fence: the official
// markdown.DefaultCodeBlock printed the fence's info string as a header, so a
// plain fence said "text". The product override keeps the official
// CodeBlockView but shows the language only when it names a real one.

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

func newCodeFenceTester(t *testing.T, body string) *ui.Tester {
	t.Helper()
	tokens := design.Light()
	theme := markdown.DefaultTheme(tokens.Theme())
	options := &markdown.Options{Theme: theme, CodeBlock: uikit.MarkdownCodeBlock}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(tokens.Theme())
		ui.Column(c).FillWidth().Padding(design.Space4).Background(tokens.BgBase).Children(func() {
			markdown.View(c, body, options)
		})
	}, 640, 260)
	tester.Frame()
	return tester
}

func TestMarkdownCodeBlockHidesGenericFenceLanguage(t *testing.T) {
	for _, fence := range []string{"text", "txt", "plain", ""} {
		label := fence
		if label == "" {
			label = "<empty>"
		}
		t.Run(label, func(t *testing.T) {
			body := "```" + fence + "\n/data/mygo\n```"
			tester := newCodeFenceTester(t, body)
			if tester.HasText("text") {
				t.Fatalf("generic fence (%q) still prints its info string; texts = %v", fence, tester.Texts())
			}
			if !tester.HasText("/data/mygo") {
				t.Fatalf("code body missing; texts = %v", tester.Texts())
			}
		})
	}
}

func TestMarkdownCodeBlockShowsRealFenceLanguage(t *testing.T) {
	tester := newCodeFenceTester(t, "```go\nfmt.Println(\"hi\")\n```")
	if !tester.HasText("go") {
		t.Fatalf("real fence language missing; texts = %v", tester.Texts())
	}
	if !tester.HasText("fmt.Println(\"hi\")") {
		t.Fatalf("code body missing; texts = %v", tester.Texts())
	}
}

func TestMarkdownOptionsUseQuietFence(t *testing.T) {
	tokens := design.Light()
	if uikit.MarkdownDoc(tokens, 0).CodeBlock == nil {
		t.Fatal("inspector MarkdownDoc does not use the quiet fence")
	}
	if uikit.ReasoningMarkdown(tokens, 0).CodeBlock == nil {
		t.Fatal("reasoning markdown does not use the quiet fence")
	}
}

func TestChatMarkdownReadingTheme(t *testing.T) {
	tokens := design.Light()
	options := uikit.ChatMarkdown(tokens, 14)
	if options.CodeBlock == nil {
		t.Fatal("chat markdown does not use the quiet fence")
	}
	theme := options.Theme
	if theme.LineHeight != 1.55 {
		t.Fatalf("line height = %v, want 1.55", theme.LineHeight)
	}
	if theme.CodeBackground != tokens.BgStrong {
		t.Fatal("inline code does not use the strong neutral surface")
	}
	if theme.LinkWeight != 500 {
		t.Fatalf("link weight = %d, want 500", theme.LinkWeight)
	}
	if theme.BlockSpacing != 0.72 || theme.ItemSpacing != 0.28 {
		t.Fatalf("block/item spacing = %v/%v, want 0.72/0.28", theme.BlockSpacing, theme.ItemSpacing)
	}
	if theme.Headings[0].Top != 1.1 || theme.Headings[0].Bottom != 0.4 {
		t.Fatalf("H1 margins = %v/%v, want 1.1/0.4", theme.Headings[0].Top, theme.Headings[0].Bottom)
	}
	if theme.Headings[0].Size*theme.FontSize > 18.1 {
		t.Fatalf("H1 = %.1f DIP, above the 18 DIP transcript cap", theme.Headings[0].Size*theme.FontSize)
	}
}
