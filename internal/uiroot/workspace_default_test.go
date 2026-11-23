package uiroot

// The UI root has no startup workspace picker: the host opens the single
// default workspace before the first frame (internal/host/defaultworkspace.go),
// so the shell renders directly whether or not a workspace is current. These
// frames keep the removal honest: the old full-window folder gate and its copy
// are gone.

import (
	"context"
	"strings"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// shellWorkspaceStub answers the read calls the shell makes (the sidebar's
// current-folder header); the file methods are unused in these frames.
type shellWorkspaceStub struct {
	current *contracts.Workspace
}

func (s *shellWorkspaceStub) List(context.Context) ([]contracts.Workspace, error) {
	if s.current == nil {
		return nil, nil
	}
	return []contracts.Workspace{*s.current}, nil
}

func (s *shellWorkspaceStub) Current(context.Context) (contracts.Workspace, bool, error) {
	if s.current == nil {
		return contracts.Workspace{}, false, nil
	}
	return *s.current, true, nil
}

func (s *shellWorkspaceStub) Create(context.Context, string, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}

func (s *shellWorkspaceStub) Open(context.Context, string) (contracts.Workspace, error) {
	return contracts.Workspace{}, nil
}

func (s *shellWorkspaceStub) Delete(context.Context, string) error { return nil }

func (s *shellWorkspaceStub) Files(context.Context, string) ([]contracts.FileEntry, error) {
	return nil, nil
}

func (s *shellWorkspaceStub) ReadFile(context.Context, string, int) ([]byte, error) {
	return nil, nil
}

func (s *shellWorkspaceStub) Search(context.Context, string, int) ([]contracts.FileEntry, error) {
	return nil, nil
}

// TestShellRendersWithoutStartupWorkspacePicker covers the removal: with the
// workspace backend present, a frame is the shell (the screen body shows) and
// never the deleted "打开工作文件夹" launch page — even when no workspace is
// current, because creating the default one is the host's job before the first
// frame.
func TestShellRendersWithoutStartupWorkspacePicker(t *testing.T) {
	for _, test := range []struct {
		name    string
		current *contracts.Workspace
	}{
		{name: "current workspace", current: &contracts.Workspace{ID: "ws-1", Name: "proj", Root: "/work/proj"}},
		{name: "no current workspace"},
	} {
		t.Run(test.name, func(t *testing.T) {
			kernel := cordis.Root()
			t.Cleanup(func() {
				if err := kernel.DisposeAll(); err != nil {
					t.Errorf("DisposeAll: %v", err)
				}
			})
			workspace := &shellWorkspaceStub{current: test.current}
			if _, err := kernel.Provide(contracts.KeyWorkspace, contracts.WorkspaceService(workspace)); err != nil {
				t.Fatalf("provide workspace: %v", err)
			}
			r := New(Config{AppName: "ZeoKeelTest", Kernel: kernel})
			mustScreen(t, r, textScreen("home", "首页", "body-home", 10))

			tt := newTestTester(t, r, 1200, 800)
			tt.Frame()

			texts := strings.Join(tt.Texts(), "\n")
			if !strings.Contains(texts, "body-home") {
				t.Fatalf("the shell body did not render; texts = %v", tt.Texts())
			}
			for _, gone := range []string{"打开工作文件夹", "Open a workspace folder", "每次启动都会询问"} {
				if strings.Contains(texts, gone) {
					t.Fatalf("the deleted startup workspace picker still rendered %q; texts = %v", gone, tt.Texts())
				}
			}
		})
	}
}
