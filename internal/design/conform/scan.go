package conform

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Package paths the syntactic rules resolve selectors against. The rules are
// not type-aware (no x/tools dependency), so they recognize a package by its
// import path in the scanned file.
const (
	mygoUIPath       = "github.com/egoist/mygo/ui"
	uikitPath        = "github.com/zeokeel/zeokeel/internal/uikit"
	designPath       = "github.com/zeokeel/zeokeel/internal/design"
	uikitPackageName = "uikit"
)

// skipDir reports whether a directory is outside the contract's scope: the
// go tool's own rules (hidden, "_"-prefixed, vendor, testdata) plus build
// output directories.
func skipDir(name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return true
	}
	switch name {
	case "vendor", "testdata", "node_modules", "dist", "build":
		return true
	}
	return false
}

// collectFiles expands ScanOptions into the sorted list of repo-relative Go
// files to scan. Test files are skipped: the contract exempts them
// (DESIGN-LINT §3.1) and the conformance tests assert on product code.
func collectFiles(opts ScanOptions) ([]string, error) {
	root := opts.Root
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("scan root: %w", err)
	}
	files := make(map[string]bool)
	roots := opts.Roots
	if len(roots) == 0 && len(opts.Files) == 0 {
		roots = []string{"."}
	}
	for _, r := range roots {
		abs := filepath.Clean(filepath.FromSlash(r))
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(absRoot, abs)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("scan root %q: %w", r, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("scan root %q is not a directory", r)
		}
		err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != abs && skipDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(absRoot, path)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(rel)] = true
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scan root %q: %w", r, err)
		}
	}
	for _, f := range opts.Files {
		abs := filepath.Clean(filepath.FromSlash(f))
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(absRoot, abs)
		}
		rel, err := filepath.Rel(absRoot, abs)
		if err != nil {
			return nil, fmt.Errorf("file %q: %w", f, err)
		}
		rel = filepath.ToSlash(rel)
		if rel == ".." || strings.HasPrefix(rel, "../") {
			return nil, fmt.Errorf("file %q is outside scan root %q", f, root)
		}
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		if _, err := os.Stat(abs); err != nil {
			continue // deleted or renamed in the diff: nothing to scan
		}
		files[rel] = true
	}
	out := make([]string, 0, len(files))
	for f := range files {
		out = append(out, f)
	}
	sort.Strings(out)
	return out, nil
}

// fileCtx is one parsed file plus the import resolution and source access the
// rule detectors need.
type fileCtx struct {
	rel      string // repo-relative slash path
	src      []byte
	fset     *token.FileSet
	file     *ast.File
	aliases  map[string]string // local import name -> import path
	dotPaths map[string]bool   // dot-imported package paths
}

func newFileCtx(rel string, src []byte, fset *token.FileSet, f *ast.File) *fileCtx {
	fc := &fileCtx{rel: rel, src: src, fset: fset, file: f, aliases: map[string]string{}, dotPaths: map[string]bool{}}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		switch {
		case imp.Name == nil:
			fc.aliases[importBase(path)] = path
		case imp.Name.Name == "_":
			// blank import: no selector is possible
		case imp.Name.Name == ".":
			fc.dotPaths[path] = true
		default:
			fc.aliases[imp.Name.Name] = path
		}
	}
	return fc
}

// importBase is the default local name of an import path (the last path
// element). It is exact for the three packages the rules care about.
func importBase(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// packageOf resolves the imported path a local identifier refers to.
func (fc *fileCtx) packageOf(id *ast.Ident) string {
	if id == nil {
		return ""
	}
	return fc.aliases[id.Name]
}

// callName resolves a package-qualified function call (ui.RGBA(...)) to its
// function name, and reports whether the package at path is the callee. It
// also handles dot imports.
func (fc *fileCtx) callName(call *ast.CallExpr, path string) (string, bool) {
	fun := call.Fun
	for {
		paren, ok := fun.(*ast.ParenExpr)
		if !ok {
			break
		}
		fun = paren.X
	}
	switch f := fun.(type) {
	case *ast.SelectorExpr:
		id, ok := f.X.(*ast.Ident)
		if !ok || fc.aliases[id.Name] != path {
			return "", false
		}
		return f.Sel.Name, true
	case *ast.Ident:
		if fc.dotPaths[path] {
			return f.Name, true
		}
	}
	return "", false
}

// snippet returns the normalized source text between two positions.
func (fc *fileCtx) snippet(start, end token.Pos) string {
	if !start.IsValid() || !end.IsValid() {
		return ""
	}
	from := fc.fset.Position(start).Offset
	to := fc.fset.Position(end).Offset
	if from < 0 || to < from || to > len(fc.src) {
		return ""
	}
	return normalizeSpace(string(fc.src[from:to]))
}

// position returns the line and column of a token position.
func (fc *fileCtx) position(pos token.Pos) (int, int) {
	p := fc.fset.Position(pos)
	return p.Line, p.Column
}

// violation builds a Violation for a rule at a position.
func (fc *fileCtx) violation(rule Rule, severity string, pos token.Pos, text, message, fix string) Violation {
	line, column := fc.position(pos)
	return Violation{
		Rule:     rule.ID,
		AP:       rule.AP,
		Severity: severity,
		File:     fc.rel,
		Line:     line,
		Column:   column,
		Message:  message,
		Fix:      fix,
		Doc:      rule.Doc,
		Text:     normalizeSpace(text),
	}
}

// ruleByID returns the static metadata of a rule; the IDs are compile-time
// constants of this package, so an unknown ID is a programming error.
func ruleByID(id string) Rule {
	r, ok := LookupRule(id)
	if !ok {
		panic("conform: unknown rule " + id)
	}
	return r
}

// scanFile parses one file from disk and runs the enabled rules.
func scanFile(root, rel string, enabled enabledRules, allow []AllowEntry, today time.Time) ([]Violation, error) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return scanFileSource(rel, src, enabled, allow, today)
}

// ScanSource runs the rules over one in-memory file. rel must be the
// repo-relative path the file would have, because the rule path scopes (UI
// packages, token implementation, icons) are derived from it. It exists for
// rule unit tests; production scans go through Scan.
func ScanSource(rel string, src []byte, opts ScanOptions) ([]Violation, error) {
	enabled, err := ruleSet(opts.Rules)
	if err != nil {
		return nil, err
	}
	today := opts.Today
	if today.IsZero() {
		today = time.Now()
	}
	return scanFileSource(rel, src, enabled, opts.Allowlist, today)
}

// scanFileSource parses src and runs the enabled rules that apply to rel.
func scanFileSource(rel string, src []byte, enabled enabledRules, allow []AllowEntry, today time.Time) ([]Violation, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	fc := newFileCtx(rel, src, fset, f)
	var out []Violation
	if enabled.enabled("DS-01") && ds01Applies(rel) {
		out = append(out, checkDS01(fc)...)
	}
	if enabled.enabled("DS-03") && ds03Applies(rel) {
		out = append(out, checkDS03(fc)...)
	}
	if enabled.enabled("DS-06") && ds06Applies(rel) {
		out = append(out, checkDS06(fc, allow)...)
	}
	if enabled.enabled("DS-07") {
		out = append(out, checkDS07(fc)...)
	}
	if enabled.enabled("DS-10") && ds10Applies(rel) {
		out = append(out, checkDS10(fc)...)
	}
	out = applyInlineAllow(fc, out, today)
	return out, nil
}

// Path scopes. The rules only claim the code they own (DESIGN-LINT §3): non-UI
// packages, the token implementation and the icon assets are not UI code.
func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func isUIPath(rel string) bool {
	return hasAnyPrefix(rel, "internal/ui/", "internal/uiroot/", "internal/uikit/")
}

func isInternalPath(rel string) bool {
	return strings.HasPrefix(rel, "internal/")
}

func ds01Applies(rel string) bool {
	return isUIPath(rel) && !hasAnyPrefix(rel, "internal/brand/", "internal/design/", "internal/icons/")
}

func ds03Applies(rel string) bool {
	return isUIPath(rel) && !hasAnyPrefix(rel, "internal/brand/", "internal/design/", "internal/icons/")
}

func ds06Applies(rel string) bool {
	return isInternalPath(rel)
}

func ds07AppliesEmoji(rel string) bool {
	return isUIPath(rel)
}

func ds07AppliesSVG(rel string) bool {
	return isInternalPath(rel) && !strings.HasPrefix(rel, "internal/icons/")
}

func ds10Applies(rel string) bool {
	return isInternalPath(rel) || strings.HasPrefix(rel, "cmd/")
}

// inlineAllow is one //design:allow(ed) exemption comment.
type inlineAllow struct {
	line   int // last line of the comment group
	column int
	rules  []string
	owner  string
	until  string
	reason string
	valid  bool
	why    string // why an invalid directive is invalid
	text   string // normalized comment text, for DS-06b
}

var (
	inlineAllowRe = regexp.MustCompile(`^design:allow(?:ed)?\s+((?:DS-\d{2}b?|AP-\d{2})\b.*)$`)
	inlineRuleRe  = regexp.MustCompile(`\b(DS-\d{2}b?|AP-\d{2})\b`)
	inlineOwnerRe = regexp.MustCompile(`\bowner=(\S+)`)
	inlineUntilRe = regexp.MustCompile(`\buntil=(\d{4}-\d{2}-\d{2})\b`)
)

// parseInlineAllows extracts the //design:allow directives of a file.
//
// Format (DESIGN-LINT §4.1): //design:allow DS-06 owner=<owner> until=<yyyy-mm-dd>: <reason>
// DESIGN.md §7.3's // design:allowed AP-04 owner=... until=... form is also
// accepted; without a reason on the same line it maps to DS-06.
func parseInlineAllows(fc *fileCtx, today time.Time) []inlineAllow {
	var out []inlineAllow
	for _, group := range fc.file.Comments {
		for _, c := range group.List {
			body := c.Text
			switch {
			case strings.HasPrefix(body, "//"):
				body = strings.TrimSpace(strings.TrimPrefix(body, "//"))
			case strings.HasPrefix(body, "/*"):
				body = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(body, "/*"), "*/"))
			}
			m := inlineAllowRe.FindStringSubmatch(body)
			if m == nil {
				continue
			}
			_, column := fc.position(c.Pos())
			a := inlineAllow{line: fc.fset.Position(group.End()).Line, column: column, text: normalizeSpace(c.Text)}
			rest := m[1]
			var dsRules, apRules []string
			for _, tok := range inlineRuleRe.FindAllString(rest, -1) {
				switch {
				case strings.HasPrefix(tok, "AP-"):
					apRules = append(apRules, tok)
				case tok == "DS-01" || tok == "DS-03":
					a.why = "inline exemptions cannot waive colors/metrics (DS-01/DS-03); fix the code or use the baseline"
				default:
					dsRules = append(dsRules, tok)
				}
			}
			switch {
			case len(dsRules) > 0:
				a.rules = dsRules
			case len(apRules) > 0:
				// DESIGN.md §7.3 registers self-drawn widgets by AP id.
				a.rules = []string{"DS-06"}
			default:
				a.why = "no rule ID"
			}
			if v := inlineOwnerRe.FindStringSubmatch(rest); v != nil {
				a.owner = v[1]
			} else {
				a.why = "missing owner= (DS-06b)"
			}
			if v := inlineUntilRe.FindStringSubmatch(rest); v != nil {
				a.until = v[1]
			} else {
				a.why = "missing until=<yyyy-mm-dd> (DS-06b)"
			}
			if i := strings.Index(rest, ":"); i >= 0 {
				a.reason = strings.TrimSpace(rest[i+1:])
			} else if len(apRules) == 0 {
				a.why = "missing reason after ':' (DS-06b)"
			}
			if a.until != "" {
				until, err := time.Parse("2006-01-02", a.until)
				switch {
				case err != nil:
					a.why = "until is not yyyy-mm-dd (DS-06b)"
				case until.Before(dayOf(today)):
					a.why = "exemption expired on " + a.until + " (DS-06b)"
				}
			}
			a.valid = a.why == ""
			out = append(out, a)
		}
	}
	return out
}

// applyInlineAllow attaches valid inline exemptions to the violations on the
// next code line and reports the invalid ones as DS-06b.
func applyInlineAllow(fc *fileCtx, violations []Violation, today time.Time) []Violation {
	allows := parseInlineAllows(fc, today)
	if len(allows) == 0 {
		return violations
	}
	for _, a := range allows {
		if a.valid {
			continue
		}
		rule := ruleByID("DS-06b")
		violations = append(violations, Violation{
			Rule:     rule.ID,
			AP:       rule.AP,
			Severity: SeverityError,
			File:     fc.rel,
			Line:     a.line,
			Column:   a.column,
			Message:  "无效的 design:allow 豁免：" + a.why,
			Fix:      "补全 //design:allow <规则> owner=<owner> until=<yyyy-mm-dd>: <理由>；过期/缺字段的豁免无效（DESIGN-LINT §4.1）",
			Doc:      rule.Doc,
			Text:     a.text,
		})
	}
	for i := range violations {
		for _, a := range allows {
			if !a.valid || !containsString(a.rules, violations[i].Rule) {
				continue
			}
			if violations[i].Line != a.line+1 {
				continue
			}
			violations[i].Allowed = true
			violations[i].AllowID = "inline"
			violations[i].Owner = a.owner
			violations[i].Expires = a.until
			if violations[i].Severity == SeverityError {
				violations[i].Severity = SeverityWarn
			}
			break
		}
	}
	return violations
}

// dayOf truncates a time to midnight UTC.
func dayOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
