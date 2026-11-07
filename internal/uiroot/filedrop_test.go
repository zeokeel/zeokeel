package uiroot

// File-drop bridge: the window dispatches paths to the module handler.

import "testing"

func TestFileDropDispatch(t *testing.T) {
	root := newTestRoot(t)
	var delivered []string
	root.FileDrop().SetHandler(func(paths []string) { delivered = paths })

	paths := []string{"/tmp/a.txt", "/tmp/b.txt"}
	root.FileDrop().Dispatch(paths)
	paths[0] = "/tmp/changed.txt"
	if len(delivered) != 2 || delivered[0] != "/tmp/a.txt" {
		t.Fatalf("delivered = %v, want a copied slice", delivered)
	}

	root.FileDrop().Dispatch(nil) // no-op, handler keeps the last call
	root.FileDrop().SetHandler(nil)
	root.FileDrop().Dispatch([]string{"/tmp/c.txt"}) // nil handler: no panic
}
