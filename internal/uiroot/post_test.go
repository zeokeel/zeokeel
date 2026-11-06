package uiroot

import (
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestPostFromGoroutineRendersAtNextFrame covers the cross-goroutine Post
// contract: Post queues work and asks the window for a frame, but never builds
// one itself; the next frame drains the queue on the UI thread before the
// frame is built, so that frame already renders the posted state.
func TestPostFromGoroutineRendersAtNextFrame(t *testing.T) {
	r := newTestRoot(t)
	var state counter
	var invalidations counter
	r.SetInvalidator(func() { invalidations.Inc() })
	mustScreen(t, r, contracts.ScreenSpec{ID: "home", Title: "首页", Order: 10,
		Build: func(c *ui.Context) { ui.Textf(c, "state=%d", state.Load()) }})

	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	requireHasText(t, tt, "state=0")
	baseFrames := r.Frames()
	baseInvalidations := invalidations.Load()

	queued := make(chan struct{})
	go func() {
		r.Runtime().Post(func() { state.Set(1) })
		close(queued)
	}()
	<-queued

	// Post asked the host for a frame without building one.
	if got, want := invalidations.Load(), baseInvalidations+1; got != want {
		t.Fatalf("invalidations = %d after a background Post, want %d", got, want)
	}
	if got, want := r.Frames(), baseFrames; got != want {
		t.Fatalf("Frames() = %d after a background Post, want %d (no frame built yet)", got, want)
	}
	requireHasText(t, tt, "state=0") // the test window has not rebuilt yet

	tt.Frame()
	requireHasText(t, tt, "state=1")
	if got, want := r.Frames(), baseFrames+1; got != want {
		t.Fatalf("Frames() = %d after the next frame, want %d", got, want)
	}
	if got, want := r.Runtime().Frame(), r.Frames(); got != want {
		t.Fatalf("UIRuntime.Frame() = %d, Root.Frames() = %d, want the same count", got, want)
	}
}

// TestPostQueueIsFIFO covers the frame-start queue order: posts run on the UI
// thread in the order they were queued, before the view builds.
func TestPostQueueIsFIFO(t *testing.T) {
	r := newTestRoot(t)
	var mu sync.Mutex
	var order []int
	latest := 0
	mustScreen(t, r, contracts.ScreenSpec{ID: "home", Title: "首页", Order: 10,
		Build: func(c *ui.Context) { ui.Textf(c, "latest=%d", latest) }})

	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	baseFrames := r.Frames()

	rt := r.Runtime()
	for i := 1; i <= 3; i++ {
		i := i
		rt.Post(func() {
			mu.Lock()
			order = append(order, i)
			latest = i
			mu.Unlock()
		})
	}
	requireHasText(t, tt, "latest=0") // nothing queued has run yet
	tt.Frame()

	mu.Lock()
	gotOrder := append([]int(nil), order...)
	gotLatest := latest
	mu.Unlock()
	wantOrder := []int{1, 2, 3}
	if len(gotOrder) != len(wantOrder) {
		t.Fatalf("posts ran %v, want %v", gotOrder, wantOrder)
	}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("posts ran %v, want %v", gotOrder, wantOrder)
		}
	}
	if gotLatest != 3 {
		t.Fatalf("latest applied post = %d, want 3", gotLatest)
	}
	requireHasText(t, tt, "latest=3")
	if got, want := r.Frames(), baseFrames+1; got != want {
		t.Fatalf("Frames() = %d after one drain frame, want %d", got, want)
	}
}

// TestPostPanicIsContained covers a panicking Post: the frame drain contains it
// and the remaining queued work plus the UI keep running.
func TestPostPanicIsContained(t *testing.T) {
	r := newTestRoot(t)
	var state counter
	mustScreen(t, r, contracts.ScreenSpec{ID: "home", Title: "首页", Order: 10,
		Build: func(c *ui.Context) { ui.Textf(c, "state=%d", state.Load()) }})

	tt := newTestTester(t, r, 1200, 800)
	tt.Frame()
	requireHasText(t, tt, "state=0")
	baseFrames := r.Frames()

	// A panic from a background goroutine must not escape the UI thread.
	queued := make(chan struct{})
	go func() {
		r.Runtime().Post(func() { panic("post boom") })
		close(queued)
	}()
	<-queued
	r.Runtime().Post(func() { state.Set(2) })

	tt.Frame() // must not panic
	requireHasText(t, tt, "state=2")
	if got, want := r.Frames(), baseFrames+1; got != want {
		t.Fatalf("Frames() = %d after the panic frame, want %d", got, want)
	}

	// The UI is still live afterwards.
	tt.Frame()
	requireHasText(t, tt, "state=2")
	requireHasText(t, tt, "ZeoKeelTest")
}

// TestInvalidatorThreadSafety covers SetInvalidator/Invalidate being callable
// from any goroutine: swapping the callback while it is being requested must be
// race free (-race), and no frame is built by the host itself.
func TestInvalidatorThreadSafety(t *testing.T) {
	r := newTestRoot(t)
	var fired counter
	r.SetInvalidator(func() { fired.Inc() })

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			r.Runtime().Invalidate()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if i%2 == 0 {
				r.SetInvalidator(nil)
			} else {
				r.SetInvalidator(func() { fired.Inc() })
			}
		}
	}()
	wg.Wait()
	if got := r.Frames(); got != 0 {
		t.Fatalf("Frames() = %d after invalidations, want 0 (no frame was built)", got)
	}

	// Invalidate after the callback was cleared must be a no-op.
	r.SetInvalidator(nil)
	before := fired.Load()
	r.Runtime().Invalidate()
	if got := fired.Load(); got != before {
		t.Fatalf("invalidations = %d after clearing the invalidator, want %d", got, before)
	}
}
