package uikit_test

// 2026-11 UI review B6: a Markdown image that did not load (the document/artifact
// previews set no LoadImage) reads as an image glyph + its alt text + a quiet
// "图片未加载" note, not a bare grey alt chip that looks like leftover text.

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/uikit"
)

func TestMarkdownImageFallbackNamesImageWithAltAndNote(t *testing.T) {
	tester := ui.NewTester(func(c *ui.Context) {
		markdown.View(c, "![发布截图](shot.png)", uikit.MarkdownDoc(design.Of(c), 0))
	}, 600, 200)
	tester.SetLocale("zh-Hans")
	tester.Frame()

	// The fallback keeps the image role and the alt as its accessible name.
	if _, ok := tester.AccessByRole(ui.RoleImage, "发布截图"); !ok {
		t.Fatalf("the fallback is not an image named by its alt; texts = %v", tester.Texts())
	}
	if !tester.HasText("发布截图") || !tester.HasText("图片未加载") {
		t.Fatalf("the fallback lacks the alt text or the not-loaded note; texts = %v", tester.Texts())
	}
}

func TestMarkdownImageFallbackEnglishNote(t *testing.T) {
	tester := ui.NewTester(func(c *ui.Context) {
		markdown.View(c, "![diagram](d.png)", uikit.MarkdownDoc(design.Of(c), 0))
	}, 600, 200)
	tester.SetLocale("en")
	tester.Frame()
	if !tester.HasText("Image not loaded") {
		t.Fatalf("the English fallback note is missing; texts = %v", tester.Texts())
	}
}

func TestMarkdownImageFallbackLeavesProseBlocks(t *testing.T) {
	// A paragraph that mixes text and an inline image is not an image-only block:
	// the fallback must not take it (the prose still renders).
	tester := ui.NewTester(func(c *ui.Context) {
		markdown.View(c, "见下图 ![示意图](x.png) 的说明。", uikit.MarkdownDoc(design.Of(c), 0))
	}, 600, 200)
	tester.SetLocale("zh-Hans")
	tester.Frame()
	if !tester.HasText("见下图") || !tester.HasText("的说明。") {
		t.Fatalf("the prose around an inline image was swallowed; texts = %v", tester.Texts())
	}
}
