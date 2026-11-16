package conform_test

import (
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/design/conform"
)

// fixedDay keeps the inline-exemption expiry tests independent of the wall
// clock.
var fixedDay = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

func scan(t *testing.T, rel, src string, opts conform.ScanOptions) []conform.Violation {
	t.Helper()
	if opts.Today.IsZero() {
		opts.Today = fixedDay
	}
	violations, err := conform.ScanSource(rel, []byte(src), opts)
	if err != nil {
		t.Fatalf("ScanSource(%s): %v", rel, err)
	}
	return violations
}

func rulesOf(violations []conform.Violation) []string {
	out := make([]string, len(violations))
	for i, v := range violations {
		out[i] = v.Rule
	}
	return out
}

func countRule(violations []conform.Violation, rule string) int {
	n := 0
	for _, v := range violations {
		if v.Rule == rule {
			n++
		}
	}
	return n
}

const uiImport = `import "github.com/egoist/mygo/ui"`

func TestDS01ColorLiterals(t *testing.T) {
	tests := []struct {
		name string
		rel  string
		src  string
		want int
	}{
		{
			name: "constructor call",
			rel:  "internal/ui/probe.go",
			src:  "package probe\n" + uiImport + "\nvar c = ui.Hex(\"#333\")\n",
			want: 1,
		},
		{
			name: "rgba constructor",
			rel:  "internal/uiroot/probe.go",
			src:  "package probe\n" + uiImport + "\nvar c = ui.RGBA(0, 0, 0, 0.12)\n",
			want: 1,
		},
		{
			name: "bare hex string",
			rel:  "internal/ui/probe.go",
			src:  "package probe\n\nvar s = \"#AABBCC\"\n",
			want: 1,
		},
		{
			name: "constructor plus literal is one violation",
			rel:  "internal/uikit/probe.go",
			src:  "package probe\n" + uiImport + "\nvar c = ui.Hex(\"#fff\")\n",
			want: 1,
		},
		{
			name: "token implementation is exempt",
			rel:  "internal/design/probe.go",
			src:  "package probe\n" + uiImport + "\nvar c = ui.Hex(\"#333\")\n",
			want: 0,
		},
		{
			name: "brand is exempt",
			rel:  "internal/brand/probe.go",
			src:  "package probe\n" + uiImport + "\nvar c = ui.RGBA(0, 0, 0, 0.5)\n",
			want: 0,
		},
		{
			name: "non-UI package is out of scope",
			rel:  "internal/modules/agent/probe.go",
			src:  "package probe\n" + uiImport + "\nvar c = ui.Hex(\"#333\")\n",
			want: 0,
		},
		{
			name: "aliased import is recognized",
			rel:  "internal/ui/probe.go",
			src:  "package probe\nimport myui \"github.com/egoist/mygo/ui\"\n\nvar c = myui.Hex(\"#333\")\n",
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countRule(scan(t, tt.rel, tt.src, conform.ScanOptions{}), "DS-01")
			if got != tt.want {
				t.Fatalf("DS-01 count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDS03RawMetrics(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		want     int
		severity string
	}{
		{
			name: "numeric gap",
			src:  "package probe\n" + uiImport + "\nfunc f(c *ui.Context) { ui.Column(c).Gap(8) }\n",
			want: 1, severity: conform.SeverityError,
		},
		{
			name: "numeric size in second arg",
			src:  "package probe\n" + uiImport + "\nfunc f(c *ui.Context) { ui.Column(c).Padding(0, 8) }\n",
			want: 1, severity: conform.SeverityError,
		},
		{
			name: "font weight is a warn",
			src:  "package probe\n" + uiImport + "\nfunc f(c *ui.Context) { ui.Text(c, \"x\").FontWeight(600) }\n",
			want: 1, severity: conform.SeverityWarn,
		},
		{
			name: "tokens are clean",
			src:  "package probe\n" + uiImport + "\nfunc f(c *ui.Context) { ui.Column(c).Gap(design.Space2).PaddingX(design.Space4) }\n",
			want: 0,
		},
		{
			name: "non-monitored method is clean",
			src:  "package probe\n" + uiImport + "\nfunc f(c *ui.Context) { ui.Text(c, \"x\").MaxLines(2) }\n",
			want: 0,
		},
		{
			name: "design package is exempt",
			src:  "package probe\n" + uiImport + "\nfunc f(c *ui.Context) { ui.Column(c).Gap(8) }\n",
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rel := "internal/ui/probe.go"
			if strings.Contains(tt.name, "exempt") {
				rel = "internal/design/probe.go"
			}
			violations := scan(t, rel, tt.src, conform.ScanOptions{})
			got := countRule(violations, "DS-03")
			if got != tt.want {
				t.Fatalf("DS-03 count = %d, want %d", got, tt.want)
			}
			if tt.want == 1 && violations[0].Severity != tt.severity {
				t.Fatalf("DS-03 severity = %q, want %q", violations[0].Severity, tt.severity)
			}
		})
	}
}

func TestDS06UnregisteredSelfDrawn(t *testing.T) {
	src := "package probe\n" + uiImport + "\nfunc f(c *ui.Context) { ui.ButtonBase(c) }\n"

	t.Run("unregistered blocks", func(t *testing.T) {
		violations := scan(t, "internal/ui/session/probe.go", src, conform.ScanOptions{})
		if got := countRule(violations, "DS-06"); got != 1 {
			t.Fatalf("DS-06 count = %d, want 1 (%v)", got, rulesOf(violations))
		}
		if violations[0].Allowed || violations[0].Severity != conform.SeverityError {
			t.Fatalf("unregistered ButtonBase = allowed=%v severity=%q, want an error", violations[0].Allowed, violations[0].Severity)
		}
	})

	t.Run("registered is allowed", func(t *testing.T) {
		violations := scan(t, "internal/ui/session/probe.go", src, conform.ScanOptions{
			Allowlist: []conform.AllowEntry{{
				ID: "session.icon-buttons", Paths: []string{"internal/ui/session"}, Rules: []string{"DS-06"},
				Owner: "session", Reason: "no official IconButton", Until: "2027-06-30",
			}},
		})
		if got := countRule(violations, "DS-06"); got != 1 {
			t.Fatalf("DS-06 count = %d, want 1", got)
		}
		v := violations[0]
		if !v.Allowed || v.AllowID != "session.icon-buttons" || v.Severity != conform.SeverityWarn {
			t.Fatalf("registered ButtonBase = %+v, want allowed warn from session.icon-buttons", v)
		}
	})

	t.Run("allowlist is path scoped", func(t *testing.T) {
		violations := scan(t, "internal/ui/other/probe.go", src, conform.ScanOptions{
			Allowlist: []conform.AllowEntry{{
				ID: "session.icon-buttons", Paths: []string{"internal/ui/session"}, Rules: []string{"DS-06"},
				Owner: "session", Reason: "r", Until: "2027-06-30",
			}},
		})
		if violations[0].Allowed {
			t.Fatalf("ButtonBase outside the allowlisted path was marked allowed")
		}
	})
}

// TestDS06SiteAnchors covers the site-level allowlist granularity (2026-11
// audit UD-11): an entry may anchor to the enclosing function symbol and/or a
// normalized source-text snippet, so one registered rich-row function cannot
// silently exempt every other ButtonBase in the same file (the rail row used
// to ride the command-palette entry's file prefix). Paths-only entries keep
// the historical file-level behavior.
func TestDS06SiteAnchors(t *testing.T) {
	src := "package probe\n" + uiImport + "\n" +
		"func palette(c *ui.Context) { ui.ButtonBase(c) }\n" +
		"func railSessionRow(c *ui.Context) { ui.ButtonBase(c) }\n"

	byFunc := func(violations []conform.Violation, line int) conform.Violation {
		t.Helper()
		for _, v := range violations {
			if v.Rule == "DS-06" && v.Line == line {
				return v
			}
		}
		t.Fatalf("no DS-06 violation on line %d: %+v", line, violations)
		return conform.Violation{}
	}

	t.Run("symbol anchor covers only its function", func(t *testing.T) {
		violations := scan(t, "internal/uiroot/probe.go", src, conform.ScanOptions{
			Allowlist: []conform.AllowEntry{{
				ID: "command-palette.rows", Paths: []string{"internal/uiroot/probe.go"}, Rules: []string{"DS-06"},
				Symbol: "palette",
				Owner:  "uiroot", Reason: "r", Until: "2027-06-30",
			}},
		})
		if got := countRule(violations, "DS-06"); got != 2 {
			t.Fatalf("DS-06 count = %d, want 2", got)
		}
		paletteRow := byFunc(violations, 3)
		if !paletteRow.Allowed || paletteRow.AllowID != "command-palette.rows" {
			t.Fatalf("palette row = %+v, want allowed by command-palette.rows", paletteRow)
		}
		railRow := byFunc(violations, 4)
		if railRow.Allowed || railRow.Severity != conform.SeverityError {
			t.Fatalf("rail row = %+v, want an unregistered error (symbol anchor must not leak)", railRow)
		}
	})

	t.Run("text anchor covers only the matching snippet", func(t *testing.T) {
		textSrc := "package probe\n" + uiImport + "\n" +
			"func f(c *ui.Context) { ui.ButtonBase(c.Key(\"palette.row\")) }\n" +
			"func g(c *ui.Context) { ui.ButtonBase(c.Key(\"other\")) }\n"
		violations := scan(t, "internal/uiroot/probe.go", textSrc, conform.ScanOptions{
			Allowlist: []conform.AllowEntry{{
				ID: "palette.rows", Paths: []string{"internal/uiroot/probe.go"}, Rules: []string{"DS-06"},
				Text:  "ButtonBase(c.Key(\"palette.row\"))",
				Owner: "uiroot", Reason: "r", Until: "2027-06-30",
			}},
		})
		if got := countRule(violations, "DS-06"); got != 2 {
			t.Fatalf("DS-06 count = %d, want 2", got)
		}
		if v := byFunc(violations, 3); !v.Allowed || v.AllowID != "palette.rows" {
			t.Fatalf("anchored snippet = %+v, want allowed", v)
		}
		if v := byFunc(violations, 4); v.Allowed {
			t.Fatalf("non-matching snippet = %+v, want unregistered", v)
		}
	})

	t.Run("symbol and text anchors combine", func(t *testing.T) {
		violations := scan(t, "internal/uiroot/probe.go", src, conform.ScanOptions{
			Allowlist: []conform.AllowEntry{{
				ID: "palette.rows", Paths: []string{"internal/uiroot/probe.go"}, Rules: []string{"DS-06"},
				Symbol: "railSessionRow", Text: "ButtonBase(c)",
				Owner: "uiroot", Reason: "r", Until: "2027-06-30",
			}},
		})
		if v := byFunc(violations, 3); v.Allowed {
			t.Fatalf("palette row = %+v, want unregistered (symbol mismatch)", v)
		}
		if v := byFunc(violations, 4); !v.Allowed || v.AllowID != "palette.rows" {
			t.Fatalf("rail row = %+v, want allowed by both anchors", v)
		}
	})

	t.Run("paths-only entry keeps file-level behavior", func(t *testing.T) {
		violations := scan(t, "internal/uiroot/probe.go", src, conform.ScanOptions{
			Allowlist: []conform.AllowEntry{{
				ID: "legacy.file-level", Paths: []string{"internal/uiroot/probe.go"}, Rules: []string{"DS-06"},
				Owner: "uiroot", Reason: "r", Until: "2027-06-30",
			}},
		})
		for _, line := range []int{3, 4} {
			if v := byFunc(violations, line); !v.Allowed || v.AllowID != "legacy.file-level" {
				t.Fatalf("line %d = %+v, want allowed by the paths-only entry", line, v)
			}
		}
	})
}

// TestDS03IndirectFontWeight covers the UD-10 indirect-weight rule (2026-11
// audit): a FontWeight argument that is not a numeric literal must still name
// a design.Weight* token — directly, or through a local alias/variable whose
// every assignment comes from the tokens. A local const/var holding a raw
// number is the bypass the literal-only rule missed, and warns (DS-03 family).
func TestDS03IndirectFontWeight(t *testing.T) {
	designImport := `import "github.com/zeokeel/zeokeel/internal/design"`
	tests := []struct {
		name     string
		src      string
		want     int
		severity string
		prefix   string
	}{
		{
			name: "local variable holding a raw weight warns",
			src: "package probe\n" + uiImport + "\n" +
				"func f(c *ui.Context) { weight := 600; ui.Text(c, \"x\").FontWeight(weight) }\n",
			want: 1, severity: conform.SeverityWarn, prefix: "间接字重",
		},
		{
			name: "local const holding a raw weight warns",
			src: "package probe\n" + uiImport + "\n" +
				"const headingWeight = 600\nfunc f(c *ui.Context) { ui.Text(c, \"x\").FontWeight(headingWeight) }\n",
			want: 1, severity: conform.SeverityWarn, prefix: "间接字重",
		},
		{
			name: "design token selector is clean",
			src: "package probe\n" + uiImport + "\n" + designImport + "\n" +
				"func f(c *ui.Context) { ui.Text(c, \"x\").FontWeight(design.WeightSemibold) }\n",
			want: 0,
		},
		{
			name: "conditional token variable is clean",
			src: "package probe\n" + uiImport + "\n" + designImport + "\n" +
				"func f(c *ui.Context, active bool) {\n" +
				"	weight := design.WeightRegular\n" +
				"	if active { weight = design.WeightSemibold }\n" +
				"	ui.Text(c, \"x\").FontWeight(weight)\n" +
				"}\n",
			want: 0,
		},
		{
			name: "const alias of a token is clean",
			src: "package probe\n" + uiImport + "\n" + designImport + "\n" +
				"const agentHeadingWeight = design.WeightSemibold\n" +
				"func f(c *ui.Context) { ui.Text(c, \"x\").FontWeight(agentHeadingWeight) }\n",
			want: 0,
		},
		{
			name: "mixed token and raw assignments warn",
			src: "package probe\n" + uiImport + "\n" + designImport + "\n" +
				"func f(c *ui.Context, active bool) {\n" +
				"	weight := design.WeightRegular\n" +
				"	if active { weight = 600 }\n" +
				"	ui.Text(c, \"x\").FontWeight(weight)\n" +
				"}\n",
			want: 1, severity: conform.SeverityWarn, prefix: "间接字重",
		},
		{
			name: "foreign selector warns",
			src: "package probe\n" + uiImport + "\n" +
				"func f(c *ui.Context) { ui.Text(c, \"x\").FontWeight(other.Weight) }\n",
			want: 1, severity: conform.SeverityWarn, prefix: "间接字重",
		},
		{
			name: "call expression warns",
			src: "package probe\n" + uiImport + "\n" +
				"func f(c *ui.Context) { ui.Text(c, \"x\").FontWeight(weightOf(c)) }\n",
			want: 1, severity: conform.SeverityWarn, prefix: "间接字重",
		},
		{
			name: "unresolved parameter warns",
			src: "package probe\n" + uiImport + "\n" +
				"func f(c *ui.Context, weight int) { ui.Text(c, \"x\").FontWeight(weight) }\n",
			want: 1, severity: conform.SeverityWarn, prefix: "间接字重",
		},
		{
			name: "bare literal keeps the existing warn",
			src: "package probe\n" + uiImport + "\n" +
				"func f(c *ui.Context) { ui.Text(c, \"x\").FontWeight(600) }\n",
			want: 1, severity: conform.SeverityWarn, prefix: "裸字重",
		},
		{
			name: "indirect metrics other than weight stay out of scope",
			src: "package probe\n" + uiImport + "\n" +
				"func f(c *ui.Context) { h := 36; ui.Column(c).Height(h) }\n",
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			violations := scan(t, "internal/ui/probe.go", tt.src, conform.ScanOptions{})
			got := countRule(violations, "DS-03")
			if got != tt.want {
				t.Fatalf("DS-03 count = %d, want %d (%+v)", got, tt.want, violations)
			}
			if tt.want == 1 {
				v := violations[0]
				if v.Severity != tt.severity {
					t.Fatalf("severity = %q, want %q", v.Severity, tt.severity)
				}
				if !strings.HasPrefix(v.Message, tt.prefix) {
					t.Fatalf("message = %q, want prefix %q", v.Message, tt.prefix)
				}
			}
		})
	}
}

func TestDS07Icons(t *testing.T) {
	tests := []struct {
		name string
		rel  string
		src  string
		want int
	}{
		{
			name: "emoji literal",
			rel:  "internal/ui/probe.go",
			src:  "package probe\n\nvar s = \"🚀\"\n",
			want: 1,
		},
		{
			name: "typographic arrow is fine",
			rel:  "internal/ui/probe.go",
			src:  "package probe\n\nvar s = \"→\"\n",
			want: 0,
		},
		{
			name: "handwritten svg outside icons",
			rel:  "internal/ui/probe.go",
			src:  "package probe\n" + uiImport + "\nvar s = ui.MustParseSVG(nil)\n",
			want: 1,
		},
		{
			name: "svg parsing inside icons is fine",
			rel:  "internal/icons/probe.go",
			src:  "package probe\n" + uiImport + "\nvar s = ui.MustParseSVG(nil)\n",
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countRule(scan(t, tt.rel, tt.src, conform.ScanOptions{}), "DS-07"); got != tt.want {
				t.Fatalf("DS-07 count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDS10DangerDefault(t *testing.T) {
	uikitImport := `import "github.com/zeokeel/zeokeel/internal/uikit"`
	tests := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "explicit literal",
			src:  "package probe\n" + uikitImport + "\nvar a = uikit.DialogAction{Label: \"x\", Danger: true, Default: true}\n",
			want: 1,
		},
		{
			name: "elided slice element",
			src:  "package probe\n" + uikitImport + "\nvar a = []uikit.DialogAction{{Label: \"x\", Danger: true, Default: true}}\n",
			want: 1,
		},
		{
			name: "danger only is fine",
			src:  "package probe\n" + uikitImport + "\nvar a = uikit.DialogAction{Label: \"x\", Danger: true}\n",
			want: 0,
		},
		{
			name: "default only is fine",
			src:  "package probe\n" + uikitImport + "\nvar a = uikit.DialogAction{Label: \"x\", Default: true}\n",
			want: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countRule(scan(t, "internal/ui/probe.go", tt.src, conform.ScanOptions{}), "DS-10"); got != tt.want {
				t.Fatalf("DS-10 count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestInlineAllow(t *testing.T) {
	tests := []struct {
		name        string
		comment     string
		wantAllow   bool
		wantDS06b   bool
		wantDS06Sev string
	}{
		{
			name:        "valid exemption",
			comment:     "//design:allow DS-06 owner=ui until=2027-06-30: no official IconButton",
			wantAllow:   true,
			wantDS06Sev: conform.SeverityWarn,
		},
		{
			name:      "missing owner is invalid",
			comment:   "//design:allow DS-06 until=2027-06-30: no owner",
			wantDS06b: true,
		},
		{
			name:      "expired exemption is invalid",
			comment:   "//design:allow DS-06 owner=ui until=2025-01-01: stale",
			wantDS06b: true,
		},
		{
			name:      "colors cannot be waived",
			comment:   "//design:allow DS-01 owner=ui until=2027-06-30: nope",
			wantDS06b: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "package probe\n" + uiImport + "\n" + tt.comment + "\nvar b = ui.ButtonBase(c)\n"
			violations := scan(t, "internal/ui/probe.go", src, conform.ScanOptions{})
			var ds06 *conform.Violation
			for i := range violations {
				if violations[i].Rule == "DS-06" {
					ds06 = &violations[i]
				}
			}
			if ds06 == nil {
				t.Fatalf("no DS-06 violation: %v", rulesOf(violations))
			}
			if ds06.Allowed != tt.wantAllow {
				t.Errorf("Allowed = %v, want %v", ds06.Allowed, tt.wantAllow)
			}
			if tt.wantDS06Sev != "" && ds06.Severity != tt.wantDS06Sev {
				t.Errorf("severity = %q, want %q", ds06.Severity, tt.wantDS06Sev)
			}
			if got := countRule(violations, "DS-06b"); (got > 0) != tt.wantDS06b {
				t.Errorf("DS-06b count = %d, want presence=%v", got, tt.wantDS06b)
			}
		})
	}
}

func TestRulesFilter(t *testing.T) {
	src := "package probe\n" + uiImport + "\nvar c = ui.Hex(\"#333\")\nvar b = ui.ButtonBase(c)\n"
	all := scan(t, "internal/ui/probe.go", src, conform.ScanOptions{})
	if countRule(all, "DS-01") != 1 || countRule(all, "DS-06") != 1 {
		t.Fatalf("unfiltered scan = %v, want DS-01 and DS-06", rulesOf(all))
	}
	only := scan(t, "internal/ui/probe.go", src, conform.ScanOptions{Rules: []string{"DS-06"}})
	if countRule(only, "DS-06") != 1 || len(only) != 1 {
		t.Fatalf("filtered scan = %v, want only DS-06", rulesOf(only))
	}
	if _, err := conform.ScanSource("internal/ui/probe.go", []byte(src), conform.ScanOptions{Rules: []string{"DS-99"}}); err == nil {
		t.Fatal("unknown rule was accepted")
	}
}

func TestBaselineMatching(t *testing.T) {
	violations := []conform.Violation{
		{Rule: "DS-03", File: "internal/ui/a.go", Line: 10, Text: "Size(24, 24)", Severity: conform.SeverityError},
		{Rule: "DS-03", File: "internal/ui/a.go", Line: 20, Text: "Size(24,   24)", Severity: conform.SeverityError},
		{Rule: "DS-01", File: "internal/ui/b.go", Line: 5, Text: `"#333"`, Severity: conform.SeverityError},
	}
	baseline := &conform.Baseline{Version: 1, Ignored: []conform.BaselineEntry{
		// The line is deliberately wrong: matching is rule+file+text.
		{Rule: "DS-03", File: "internal/ui/a.go", Line: 999, Text: "Size(24, 24)"},
	}}
	fresh := baseline.Mark(violations)
	if len(fresh) != 2 {
		t.Fatalf("fresh = %d, want 2 (one entry covers one occurrence): %+v", len(fresh), fresh)
	}
	if !violations[0].Baseline {
		t.Error("first Size(24, 24) should be marked baseline")
	}
	if violations[1].Baseline {
		t.Error("second Size(24, 24) must stay fresh (each entry covers one occurrence)")
	}

	// A missing baseline file is an empty ledger, not an error.
	if b, err := conform.LoadBaseline("testdata/does-not-exist.json"); err != nil || b.Len() != 0 {
		t.Fatalf("LoadBaseline(missing) = %+v, %v; want empty baseline, nil", b, err)
	}
}
