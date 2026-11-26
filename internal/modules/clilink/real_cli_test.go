package clilink

// Real-machine check for the CLI link. It is skipped unless
// ZEOKEEL_REAL_CLI names an actual CLI executable, so CI (and any machine
// without the vendor CLI) stays hermetic:
//
//	ZEOKEEL_REAL_CLI=/path/to/dws \
//	ZEOKEEL_REAL_SKILLS_DIR=/path/to/skills/multi \
//	  go test ./internal/modules/clilink/ -run TestRealCLIProbe -v
//
// Everything else in this package uses stub scripts; this one proves the probe
// (and the model-facing line) against a real binary.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestRealCLIProbe resolves, probes and renders one real CLI.
func TestRealCLIProbe(t *testing.T) {
	path := strings.TrimSpace(os.Getenv("ZEOKEEL_REAL_CLI"))
	if path == "" {
		t.Skip("set ZEOKEEL_REAL_CLI=/path/to/<cli> to probe a real executable")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("ZEOKEEL_REAL_CLI=%s: %v", path, err)
	}
	settings := mapSettings{contracts.SettingCLIDingtalkPath: path}
	if skillsDir := strings.TrimSpace(os.Getenv("ZEOKEEL_REAL_SKILLS_DIR")); skillsDir != "" {
		settings[contracts.SettingSkillsDirs] = skillsDir
	}
	service := newService(settings, "", &fakeLogger{}, 5*time.Second, contracts.CLILinkOptions{
		LookPath: func(string) (string, error) { return "", os.ErrNotExist },
	})
	statuses := service.Refresh(t.Context())
	status := statuses[0]
	t.Logf("resolved: state=%s source=%s path=%s version=%q error=%q",
		status.State, status.Source, status.Path, status.Version, status.Error)
	t.Logf("skills: state=%s roots=%v count=%d", status.Skills.State, status.Skills.Roots, status.Skills.Count)
	t.Logf("model-facing section:\n%s", contracts.CLILinkContextSection(contracts.ConfiguredCLITools(settings, "", contracts.CLILinkOptions{})))
	if status.State != contracts.CLIToolReady {
		t.Fatalf("state = %q (%s), want ready", status.State, status.Error)
	}
	if status.Path != path {
		t.Fatalf("path = %q, want %q", status.Path, path)
	}
	if strings.TrimSpace(status.Version) == "" {
		t.Fatal("no version reported")
	}
}
