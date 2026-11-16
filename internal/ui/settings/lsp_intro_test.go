package settings_test

// Frame test for US-08 (2026-11 wave D): the LSP settings page carries a
// plain-language subtitle so a non-developer learns what language servers do
// (code diagnostics for the agent) instead of facing a bare "LSP" acronym.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/ui/settings"
)

// TestLSPPagePlainSubtitle covers the page header's teaching subtitle.
func TestLSPPagePlainSubtitle(t *testing.T) {
	h := newHarness(t, &fakeChat{}, newFakeSettings(nil), settings.Config{})
	tester := h.tester()
	tester.Frame()
	h.click(tester, "LSP")

	h.assertTexts(tester, "LSP", "语言服务", "代码诊断")
}
