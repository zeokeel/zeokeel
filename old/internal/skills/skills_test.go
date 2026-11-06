package skills

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "alpha", skillDoc("alpha", `"Alpha description"`, "# Alpha\nAlpha body."))
	writeSkill(t, root, "beta", skillDoc("beta", "Beta description", "Beta body."))

	// Not a skill: a directory without SKILL.md.
	if err := os.MkdirAll(filepath.Join(root, "not-a-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Hidden directories are ignored even when they look like skills.
	writeSkill(t, root, ".hidden", skillDoc(".hidden", "should not load", "hidden body"))

	loaded, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded %d skills, want 2: %+v", len(loaded), loaded)
	}
	if loaded[0].Name != "alpha" || loaded[1].Name != "beta" {
		t.Fatalf("skills are not sorted: %q, %q", loaded[0].Name, loaded[1].Name)
	}
	if loaded[0].Description != "Alpha description" {
		t.Errorf("alpha description = %q", loaded[0].Description)
	}
	if loaded[0].Body != "# Alpha\nAlpha body." {
		t.Errorf("alpha body = %q", loaded[0].Body)
	}
	if loaded[1].Description != "Beta description" || loaded[1].Body != "Beta body." {
		t.Errorf("beta = %+v", loaded[1])
	}
	for _, skill := range loaded {
		if !filepath.IsAbs(skill.Path) {
			t.Errorf("Path %q is not absolute", skill.Path)
		}
		if !strings.HasPrefix(skill.Path, root) {
			t.Errorf("Path %q is outside root %q", skill.Path, root)
		}
		if skill.Truncated {
			t.Errorf("skill %q unexpectedly truncated", skill.Name)
		}
	}
}

func TestLoadEmptyAndMissingRoot(t *testing.T) {
	loaded, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load(empty): %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("Load(empty) = %+v, want none", loaded)
	}

	if _, err := Load(""); err == nil {
		t.Fatal("Load with empty root succeeded")
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("Load with a missing root succeeded")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(file); err == nil {
		t.Fatal("Load with a file root succeeded")
	}
}

func TestLoadMalformed(t *testing.T) {
	tests := []struct {
		name    string
		dir     string
		content string
		want    string
	}{
		{"no frontmatter", "plain", "# just markdown", "missing front matter"},
		{"unclosed frontmatter", "unclosed", "---\nname: unclosed\n", "no closing"},
		{"bad line", "badline", "---\nname: badline\nnot a key value\n---\nbody", "not key: value"},
		{"missing name", "noname", "---\ndescription: no name\n---\nbody", "missing the required name"},
		{"invalid name", "evil", "---\nname: ../evil\ndescription: bad\n---\nbody", "invalid skill name"},
		{"name mismatch", "one", "---\nname: two\ndescription: mismatch\n---\nbody", "does not match"},
		{"duplicate field", "dup", "---\nname: dup\nname: dup\ndescription: d\n---\nbody", "duplicated"},
		{"unterminated quote", "quote", "---\nname: quote\ndescription: \"oops\n---\nbody", "unterminated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeSkill(t, root, tt.dir, tt.content)
			_, err := Load(root)
			if err == nil {
				t.Fatalf("Load accepted %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}

func TestToolListAndRead(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "alpha", skillDoc("alpha", "Alpha description", "# Alpha\nAlpha body."))
	writeSkill(t, root, "beta", skillDoc("beta", "", "Beta body."))

	tool, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if tool.Name() != "skill" {
		t.Fatalf("Name() = %q", tool.Name())
	}
	if !strings.Contains(tool.Description(), `"action"`) {
		t.Fatalf("Description lacks action schema: %q", tool.Description())
	}

	list, err := invoke(t, tool, map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []string{"Available skills:", "- alpha: Alpha description", "- beta: (no description)"} {
		if !strings.Contains(list, want) {
			t.Errorf("catalog %q does not contain %q", list, want)
		}
	}
	if strings.Contains(list, "Alpha body.") || strings.Contains(list, "Beta body.") {
		t.Errorf("catalog leaked bodies: %q", list)
	}

	body, err := invoke(t, tool, map[string]any{"action": "read", "name": "alpha"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if body != "# Alpha\nAlpha body." {
		t.Fatalf("read body = %q", body)
	}

	if got, err := invoke(t, tool, map[string]any{"action": "READ", "name": "beta"}); err != nil || got != "Beta body." {
		t.Fatalf("case-insensitive read = %q, %v", got, err)
	}
}

func TestToolActionValidation(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "alpha", skillDoc("alpha", "d", "body"))
	tool, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{"nil args", nil, "action is required"},
		{"missing action", json.RawMessage(`{"name":"alpha"}`), "action is required"},
		{"unknown action", json.RawMessage(`{"action":"delete"}`), "unknown action"},
		{"read without name", json.RawMessage(`{"action":"read"}`), "name is required"},
		{"unknown skill", json.RawMessage(`{"action":"read","name":"nope"}`), "unknown skill"},
		{"unknown field", json.RawMessage(`{"action":"list","extra":1}`), "invalid arguments"},
		{"bad json", json.RawMessage(`{`), "invalid arguments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tool.Invoke(context.Background(), tc.raw); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
	if _, err := (*Tool)(nil).Invoke(context.Background(), json.RawMessage(`{"action":"list"}`)); err == nil {
		t.Fatal("nil tool Invoke succeeded")
	}
}

func TestToolNameTraversalRejected(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "alpha", skillDoc("alpha", "d", "body"))
	tool, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, name := range []string{"../alpha", "..", ".", "a/b", `a\b`, "/etc/passwd", ".hidden", "-alpha"} {
		args, err := json.Marshal(map[string]any{"action": "read", "name": name})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tool.Invoke(context.Background(), args); err == nil {
			t.Errorf("read accepted unsafe name %q", name)
		}
	}
}

func TestBodyCap(t *testing.T) {
	root := t.TempDir()
	body := strings.Repeat("x", MaxBodyBytes+4096)
	writeSkill(t, root, "big", skillDoc("big", "big skill", body))

	loaded, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d skills", len(loaded))
	}
	skill := loaded[0]
	if !skill.Truncated {
		t.Fatal("oversized skill is not marked truncated")
	}
	if len(skill.Body) > MaxBodyBytes {
		t.Fatalf("body is %d bytes, want <= %d", len(skill.Body), MaxBodyBytes)
	}
	if !strings.Contains(skill.Body, "[truncated: skill body exceeds") {
		t.Fatalf("body lacks truncation marker: %q", skill.Body[len(skill.Body)-80:])
	}

	tool := NewFromSkills(loaded)
	read, err := invoke(t, tool, map[string]any{"action": "read", "name": "big"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(read) > MaxBodyBytes {
		t.Fatalf("read body is %d bytes, want <= %d", len(read), MaxBodyBytes)
	}
	if read != skill.Body {
		t.Fatal("tool returned a body different from Load")
	}
}

func TestLoadVeryLargeFileTruncated(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "huge", skillDoc("huge", "huge", strings.Repeat("y", maxFileReadBytes+1024)))
	loaded, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 || !loaded[0].Truncated {
		t.Fatalf("very large skill not marked truncated: %+v", loaded)
	}
	if len(loaded[0].Body) > MaxBodyBytes {
		t.Fatalf("body is %d bytes", len(loaded[0].Body))
	}
}

func TestCRLFAndQuotedFrontmatter(t *testing.T) {
	root := t.TempDir()
	content := "\ufeff---\r\nname: crlf\r\ndescription: 'It''s quoted'\r\n---\r\n# Body\r\nline two\r\n"
	writeSkill(t, root, "crlf", content)

	loaded, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d skills", len(loaded))
	}
	if loaded[0].Description != "It's quoted" {
		t.Fatalf("description = %q", loaded[0].Description)
	}
	if loaded[0].Body != "# Body\nline two" {
		t.Fatalf("body = %q", loaded[0].Body)
	}
}

func TestNewFromSkillsFiltersInvalidAndDuplicate(t *testing.T) {
	tool := NewFromSkills([]Skill{
		{Name: "../evil", Body: "nope"},
		{Name: "ok", Body: "fine"},
		{Name: "ok", Body: "duplicate"},
		{Name: "bad name", Body: "nope"},
	})
	skills := tool.Skills()
	if len(skills) != 1 || skills[0].Name != "ok" || skills[0].Body != "fine" {
		t.Fatalf("Skills() = %+v", skills)
	}
	if _, err := invoke(t, tool, map[string]any{"action": "read", "name": "../evil"}); err == nil {
		t.Fatal("filtered skill is still readable")
	}

	// Hand-built catalogs are capped too.
	huge := NewFromSkills([]Skill{{Name: "huge", Body: strings.Repeat("z", MaxBodyBytes+100)}})
	body, err := invoke(t, huge, map[string]any{"action": "read", "name": "huge"})
	if err != nil {
		t.Fatalf("read huge: %v", err)
	}
	if len(body) > MaxBodyBytes || !strings.Contains(body, "[truncated:") {
		t.Fatalf("hand-built body was not capped (%d bytes)", len(body))
	}
}

func TestEmptyCatalog(t *testing.T) {
	tool, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	list, err := invoke(t, tool, map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if list != "No skills available." {
		t.Fatalf("empty catalog = %q", list)
	}
}

func skillDoc(name, description, body string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\n" + body
}

func writeSkill(t *testing.T, root, dir, content string) string {
	t.Helper()
	full := filepath.Join(root, dir)
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", full, err)
	}
	path := filepath.Join(full, SkillFile)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func invoke(t *testing.T, tool *Tool, args any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return tool.Invoke(context.Background(), raw)
}
