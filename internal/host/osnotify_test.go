package host

// The OS notification sink (M4 follow-up): the command per platform, the
// escaping that keeps a quote in a title from breaking the AppleScript, and the
// honest error for a platform we cannot notify.

import (
	"strings"
	"testing"
)

func TestOSNotifyCommandPerPlatform(t *testing.T) {
	name, args, err := osNotifyCommand("linux", "任务完成", "报告已生成")
	if err != nil || name != "notify-send" {
		t.Fatalf("linux = (%q, %v, %v)", name, args, err)
	}
	if len(args) != 2 || args[0] != "任务完成" {
		t.Fatalf("linux args = %v", args)
	}

	name, args, err = osNotifyCommand("darwin", "Done", `has "quotes"`)
	if err != nil || name != "osascript" {
		t.Fatalf("darwin = (%q, %v, %v)", name, args, err)
	}
	script := strings.Join(args, " ")
	if !strings.Contains(script, `has \"quotes\"`) {
		t.Fatalf("script = %q, want the quote escaped", script)
	}

	if _, _, err := osNotifyCommand("plan9", "t", "b"); err == nil {
		t.Fatal("unsupported platform should report an error, not a silent no-op")
	}
}

func TestSystemNotifierRunsThePlatformCommand(t *testing.T) {
	var ran string
	original := osNotifyRun
	osNotifyRun = func(name string, _ ...string) error {
		ran = name
		return nil
	}
	defer func() { osNotifyRun = original }()

	if err := systemNotifier()("id-1", "标题", "正文"); err != nil {
		t.Fatalf("systemNotifier: %v", err)
	}
	if ran == "" {
		t.Fatal("systemNotifier did not run a command")
	}
}
