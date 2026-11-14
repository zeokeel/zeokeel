package rightpanel

// Internal regression test for the file tab's editor language: mygo v0.9.0's
// editor.LanguageForText also reads a script's shebang line, so a file without
// a telling extension (a #!/bin/bash script) gets syntax highlighting instead
// of falling back to plain text.

import (
	"testing"
)

func TestEditorForReadsLanguageFromShebang(t *testing.T) {
	st := &state{}
	script := []byte("#!/bin/bash\nset -euo pipefail\necho hi\n")
	editorState := st.editorFor("run", script)
	if editorState == nil || editorState.Buffer == nil {
		t.Fatal("editorFor returned no editor state")
	}
	if got := editorState.Buffer.Language(); got == nil || got.Name != "Shell" {
		t.Fatalf("language of a #!/bin/bash file without extension = %+v, want Shell", got)
	}

	// A telling file name still wins, as before LanguageForText existed.
	python := st.editorFor("tool.py", []byte("#!/bin/bash\nprint(1)\n"))
	if got := python.Buffer.Language(); got == nil || got.Name != "Python" {
		t.Fatalf("language of tool.py = %+v, want Python", got)
	}

	// Neither a name nor a shebang tells: the editor keeps plain text.
	plain := st.editorFor("notes", []byte("just text\n"))
	if got := plain.Buffer.Language(); got != nil {
		t.Fatalf("language of a plain file = %+v, want nil (plain text)", got)
	}
}
