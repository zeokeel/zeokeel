package rightpanel_test

// Locale frame tests: the right sidebar's tabs, guide and page copy follow the
// host language per frame, and switch back to Chinese immediately.

import (
	"testing"
)

func TestEnglishTabsAndGuide(t *testing.T) {
	h := newHarness(t, harnessOptions{Language: "en"})
	tester := h.tester()
	h.assertTexts(tester, "Start", "Files", "Commands", "Workspace files", "Run command")
	h.assertNoTexts(tester, "开始", "文件", "命令", "工作区文件", "命令运行")
}

func TestLanguageSwitchUpdatesCopy(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	tester := h.tester()
	h.assertTexts(tester, "开始", "工作区文件")

	h.uiRoot.Locale().SetLanguage("en")
	tester.Frame()
	h.assertTexts(tester, "Start", "Workspace files", "Run command")
	h.assertNoTexts(tester, "开始", "工作区文件")

	h.uiRoot.Locale().SetLanguage("zh")
	tester.Frame()
	h.assertTexts(tester, "开始", "工作区文件")
	h.assertNoTexts(tester, "Start", "Workspace files")
}

func TestEnglishFilesPage(t *testing.T) {
	workspace := newFakeWorkspace()
	workspace.setCurrent()
	workspace.setDir("", fileEntry("README.md", false))
	workspace.setFile("README.md", []byte("# doc\n"))
	h := newHarness(t, harnessOptions{Workspace: workspace, Language: "en"})
	tester := h.tester()
	h.openPage(tester, "Files")
	h.assertTexts(tester, "Workspace", "proj", "README.md")
	h.click(tester, "README.md")
	h.assertTexts(tester, "Close README.md", "doc")
	h.click(tester, "Files")
	if err := tester.Click("Refresh files"); err != nil {
		t.Fatalf("English refresh action missing: %v; texts = %v", err, tester.Texts())
	}
}
