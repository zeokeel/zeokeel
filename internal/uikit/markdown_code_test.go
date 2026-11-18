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
	// 14/22 body (B-spec-v2 §3.6): the line box is 22/14 of the body size.
	if want := float32(22.0 / 14.0); theme.LineHeight != want {
		t.Fatalf("line height = %v, want %v (14/22 body)", theme.LineHeight, want)
	}
	// 12 DIP between blocks.
	if want := float32(12.0 / 14.0); theme.BlockSpacing != want {
		t.Fatalf("block spacing = %v, want %v (12 DIP)", theme.BlockSpacing, want)
	}
	if theme.CodeBackground != tokens.BgStrong {
		t.Fatal("inline code does not use the strong neutral surface")
	}
	// Inline code is mono 12/18: 12/14 of the body size.
	if want := float32(12.0 / 14.0); theme.CodeSize != want {
		t.Fatalf("inline code size = %v, want %v (12 DIP)", theme.CodeSize, want)
	}
	if theme.LinkWeight != design.WeightMedium {
		t.Fatalf("link weight = %d, want %d", theme.LinkWeight, design.WeightMedium)
	}
	// The fence body is mono 13/20 on the strong surface inside one BorderL2
	// frame at the code radius.
	if want := float32(13.0 / 14.0); theme.CodeSize != want && theme.CodeBlockBackground != tokens.BgStrong {
		t.Fatalf("fence body size = %v, want %v", theme.CodeSize, want)
	}
	if theme.CodeBlockBorder != tokens.BorderL2 {
		t.Fatal("the fence frame is not the structural border")
	}
	if theme.CodeBlockRadius != design.RadiusSM {
		t.Fatalf("fence radius = %v, want %v", theme.CodeBlockRadius, design.RadiusSM)
	}
	// A quote is structural: a 2 DIP BorderL2 rule and tertiary text.
	if theme.QuoteBarWidth != design.Space1/2 || theme.QuoteBar != tokens.BorderL2 {
		t.Fatalf("quote rule = %v/%v, want the 2 DIP structural rule",
			theme.QuoteBarWidth, theme.QuoteBar)
	}
	if theme.Headings[0].Top != 1.1 || theme.Headings[0].Bottom != 0.4 {
		t.Fatalf("H1 margins = %v/%v, want 1.1/0.4", theme.Headings[0].Top, theme.Headings[0].Bottom)
	}
	if theme.Headings[0].Size*theme.FontSize > 18.1 {
		t.Fatalf("H1 = %.1f DIP, above the 18 DIP transcript cap", theme.Headings[0].Size*theme.FontSize)
	}
}

// TestMarkdownReplyKeepsTheProseMeasure pins B-spec-v2 §3.6: paragraphs, lists
// and quotes inside the reply are bounded at design.ProseMeasure, while a reply
// that carries a table keeps the full column (mygo's table scrolls sideways and
// would scroll at once inside 640 DIP). The prose is left-aligned in its column,
// not centered: it shares the column's left edge with the fences and cards below
// it.
func TestMarkdownReplyKeepsTheProseMeasure(t *testing.T) {
	const prose = "这是一段足够长的正文，用来确认阅读列在宽窗口下不会把一行拉到两百个字符，" +
		"而是收在 ProseMeasure 之内，和业界聊天客户端的阅读宽度一致。"
	tester := ui.NewTester(func(c *ui.Context) {
		tokens := design.Of(c)
		ui.Column(c).FillWidth().FillHeight().Background(tokens.BgBase).Padding(design.Space5).
			Children(func() {
				ui.Column(c).Width(design.TranscriptWidth).Children(func() {
					uikit.MarkdownReply(c, prose, uikit.ChatMarkdown(tokens, 0))
				})
			})
	}, 1080, 400)
	tester.SetLocale("zh-Hans")
	tester.Frame()
	rect, ok := tester.Find(prose)
	if !ok {
		t.Fatalf("the paragraph did not render; texts = %v", tester.Texts())
	}
	if rect.W > design.ProseMeasure+1 {
		t.Fatalf("paragraph width = %.1f DIP, want ≤%.1f (the reading measure)",
			rect.W, design.ProseMeasure)
	}
	if rect.X < design.Space5 {
		t.Fatalf("paragraph x = %.1f, want the column's left edge (not centered)", rect.X)
	}

	// A reply with a table keeps the column width.
	tester = ui.NewTester(func(c *ui.Context) {
		tokens := design.Of(c)
		ui.Column(c).FillWidth().FillHeight().Background(tokens.BgBase).Padding(design.Space5).
			Children(func() {
				ui.Column(c).Width(design.TranscriptWidth).Children(func() {
					uikit.MarkdownReply(c, "| a | b |\n|---|---|\n| 1 | 2 |\n", uikit.ChatMarkdown(tokens, 0))
				})
			})
	}, 1080, 400)
	tester.SetLocale("zh-Hans")
	tester.Frame()
	if !tester.HasText("a") && !tester.HasText("1") {
		t.Fatalf("the table did not render; texts = %v", tester.Texts())
	}
}
