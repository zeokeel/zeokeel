package session

// AR-10 (2026-11 audit): attachment staging (≤10 MiB ReadFile + preview decode)
// runs in a background goroutine; UIRuntime.Post carries only the results. This
// internal test gates the reader seam to prove the frame never waits on the
// read: while the reader is blocked the staging call has returned, no
// attachment is applied, and frames still build; releasing the reader applies
// the result through the Post.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/uiroot"
)

func TestAttachmentStagingReadsOffTheUIThread(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.txt")
	data := make([]byte, 4<<20) // 4 MiB: a read big enough to matter
	for i := range data {
		data[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write staged file: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	previous := readAttachmentFiles
	readAttachmentFiles = func(paths []string) []attachmentFileResult {
		close(started)
		<-release
		return previous(paths)
	}
	t.Cleanup(func() { readAttachmentFiles = previous })

	uiRoot := uiroot.New(uiroot.Config{AppName: "ZeoKeel"})
	st := &state{
		draftSession: "sess-1",
		thumbnails:   make(map[string]*ui.Bitmap),
	}
	stageAttachmentFilesAsync(uiRoot.Runtime(), st, "sess-1", []string{path}, nil)

	// The reader started in its own goroutine; the staging call returned
	// without waiting for it.
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("AR-10: the reader never started in the background")
	}
	if len(st.pendingAttachments) != 0 {
		t.Fatalf("AR-10: attachment applied before the read finished")
	}

	// Frames build while the read is still blocked (the UI thread is free).
	tester := ui.NewTester(uiRoot.View, 900, 600)
	done := make(chan struct{})
	go func() {
		tester.Frame()
		tester.Frame()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("AR-10: frame build blocked on the attachment read")
	}

	// Release the read: the result lands through the Post, not the call stack.
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for len(st.pendingAttachments) == 0 && time.Now().Before(deadline) {
		tester.Frame()
		time.Sleep(2 * time.Millisecond)
	}
	if len(st.pendingAttachments) != 1 {
		t.Fatalf("AR-10: staged attachment never applied; pending=%d", len(st.pendingAttachments))
	}
	if st.pendingAttachments[0].Name != "big.txt" || len(st.pendingAttachments[0].Data) != len(data) {
		t.Fatalf("staged attachment = %+v, want the full file", st.pendingAttachments[0])
	}
}
