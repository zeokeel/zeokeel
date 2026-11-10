package host

// Tests for the hidden detached update-helper modes (F1). The updater spawns
// this same executable with a plain-argv mode + target, the helper waits for the
// old process to quit, then starts the installer / reopens the app without a
// shell. The delay and the process launch are injected seams: no real process is
// started in these tests.

import (
	"errors"
	"strings"
	"testing"
)

// helperLaunch is one recorded detachedProcess call.
type helperLaunch struct {
	target     string
	hideWindow bool
	launched   bool
}

// stubUpdateHelperSeams replaces the helper delay and process seams for one test
// and returns the recorded launches plus the call order.
func stubUpdateHelperSeams(t *testing.T) (*[]helperLaunch, *[]string, *error) {
	t.Helper()
	previousDelay := updateHelperDelay
	previousProcess := detachedProcess
	launches := make([]helperLaunch, 0)
	order := make([]string, 0)
	launchErr := error(nil)
	updateHelperDelay = func() {
		order = append(order, "delay")
	}
	detachedProcess = func(target string, hideWindow bool) (func() error, error) {
		launch := helperLaunch{target: target, hideWindow: hideWindow}
		launches = append(launches, launch)
		return func() error {
			launches[len(launches)-1].launched = true
			order = append(order, "launch")
			return launchErr
		}, nil
	}
	t.Cleanup(func() {
		updateHelperDelay = previousDelay
		detachedProcess = previousProcess
	})
	return &launches, &order, &launchErr
}

// TestUpdateHelperInstallMode covers the installer helper: the mode is handled
// before flag parsing, the delay runs before the setup starts, and the setup is
// started visibly (HideWindow must not hide the interactive NSIS UI).
func TestUpdateHelperInstallMode(t *testing.T) {
	launches, order, _ := stubUpdateHelperSeams(t)
	target := `C:\Users\Müller\AppData\Roaming\ZeoKeel Germany\updates\0.2.8-zeokeel-setup-0.2.8-amd64.exe`

	handled, code := runUpdateHelper([]string{updateHelperInstallAfterExit, target})
	if !handled || code != 0 {
		t.Fatalf("runUpdateHelper = handled %v, code %d, want true/0", handled, code)
	}
	if len(*launches) != 1 {
		t.Fatalf("launches = %+v, want one", *launches)
	}
	launch := (*launches)[0]
	if launch.target != target {
		t.Fatalf("helper target = %q, want %q (plain argv, no shell quoting)", launch.target, target)
	}
	if launch.hideWindow {
		t.Fatal("installer helper hides the setup window; the interactive NSIS UI must stay visible")
	}
	if !launch.launched {
		t.Fatal("helper recorded the installer but never started it")
	}
	if strings.Join(*order, ",") != "delay,launch" {
		t.Fatalf("helper order = %v, want the delay before the launch", *order)
	}
}

// TestUpdateHelperOpenMode covers the relaunch helper: it reopens the updated
// app/executable through the owner-facing process seam.
func TestUpdateHelperOpenMode(t *testing.T) {
	launches, order, _ := stubUpdateHelperSeams(t)
	target := "/Applications/ZeoKeel.app"

	handled, code := runUpdateHelper([]string{updateHelperOpenAfterExit, target})
	if !handled || code != 0 {
		t.Fatalf("runUpdateHelper = handled %v, code %d, want true/0", handled, code)
	}
	if len(*launches) != 1 || (*launches)[0].target != target {
		t.Fatalf("launches = %+v, want one launch of %q", *launches, target)
	}
	if !(*launches)[0].hideWindow {
		t.Fatal("relaunch helper should start the target hidden and let the app show its own window")
	}
	if strings.Join(*order, ",") != "delay,launch" {
		t.Fatalf("helper order = %v, want the delay before the launch", *order)
	}
}

// TestUpdateHelperIgnoresNormalArgs pins that a regular launch with flags is not
// mistaken for a helper mode (host.Main must fall through to flag parsing).
func TestUpdateHelperIgnoresNormalArgs(t *testing.T) {
	launches, order, _ := stubUpdateHelperSeams(t)
	cases := [][]string{
		nil,
		{"--version"},
		{updateHelperInstallAfterExit},
		{"--zeokeel-unknown-helper", "target"},
		{"--product", "desktop"},
	}
	for _, args := range cases {
		if handled, code := runUpdateHelper(args); handled || code != 0 {
			t.Fatalf("runUpdateHelper(%v) = handled %v, code %d, want false/0", args, handled, code)
		}
	}
	if len(*launches) != 0 || len(*order) != 0 {
		t.Fatalf("non-helper args started a process (%+v) or delayed (%v)", *launches, *order)
	}
}

// TestUpdateHelperLaunchFailureReportsExitCode covers the helper's failure
// contract: a target that cannot start exits nonzero (the detached helper is
// fire-and-forget, so this is the only status the OS sees).
func TestUpdateHelperLaunchFailureReportsExitCode(t *testing.T) {
	_, _, launchErr := stubUpdateHelperSeams(t)
	*launchErr = errors.New("target missing")

	handled, code := runUpdateHelper([]string{updateHelperInstallAfterExit, "/missing/setup.exe"})
	if !handled {
		t.Fatal("helper mode was not handled")
	}
	if code == 0 {
		t.Fatal("failed helper launch reported exit code 0")
	}
}

// TestUpdateHelperProcessSeamError covers the seam's construction error path.
func TestUpdateHelperProcessSeamError(t *testing.T) {
	previousDelay := updateHelperDelay
	previousProcess := detachedProcess
	updateHelperDelay = func() {}
	detachedProcess = func(string, bool) (func() error, error) {
		return nil, errors.New("cannot build the launch")
	}
	t.Cleanup(func() {
		updateHelperDelay = previousDelay
		detachedProcess = previousProcess
	})

	handled, code := runUpdateHelper([]string{updateHelperOpenAfterExit, "/Applications/ZeoKeel.app"})
	if !handled || code == 0 {
		t.Fatalf("runUpdateHelper = handled %v, code %d, want true/nonzero", handled, code)
	}
}
