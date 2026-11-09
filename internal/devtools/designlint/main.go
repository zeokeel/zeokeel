// Command designlint runs the ZeoKeel design-contract checks from
// docs/DESIGN-LINT.md.
//
// The rules live in internal/design/conform; this command is the thin CLI
// around them: it resolves the package patterns, loads the registry allowlist
// and the baseline, filters to git-changed files for --diff, and formats the
// findings for humans (file:line:col: DS-xx [AP-xx] message → fix) or for
// agents (--json).
//
// Usage:
//
//	designlint [flags] ./...
//	designlint --diff --base=HEAD ./...
//	designlint --diff --json ./...
//	designlint --write-baseline ./...   # regenerate .design-baseline.json
//
// Exit codes (DESIGN-LINT §1.2): 0 pass, 1 violations, 2 tool error.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zeokeel/zeokeel/internal/design"
	"github.com/zeokeel/zeokeel/internal/design/conform"
)

const (
	exitOK        = 0
	exitViolation = 1
	exitToolError = 2
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// options is the parsed command line.
type options struct {
	diff          bool
	base          string
	json          bool
	rules         string
	baseline      string
	strict        bool
	writeBaseline bool
	patterns      []string
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("designlint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var opt options
	fs.BoolVar(&opt.diff, "diff", false, "only check the files changed against --base (staged + unstaged + untracked)")
	fs.StringVar(&opt.base, "base", "HEAD", "git ref to diff against with --diff")
	fs.BoolVar(&opt.json, "json", false, "print the violations as JSON")
	fs.StringVar(&opt.rules, "rules", "", "comma-separated rule IDs to run (default: all)")
	fs.StringVar(&opt.baseline, "baseline", ".design-baseline.json", "baseline file that grandfathers the current stock")
	fs.BoolVar(&opt.strict, "strict", false, "treat warn-level violations as blocking (the baseline still grandfathers stock)")
	fs.BoolVar(&opt.writeBaseline, "write-baseline", false, "write the current violations to --baseline and exit 0")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "designlint: ZeoKeel design-contract checks (docs/DESIGN-LINT.md)")
		fmt.Fprintln(stderr, "usage: designlint [flags] ./...")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitToolError
	}
	opt.patterns = fs.Args()
	if len(opt.patterns) == 0 {
		opt.patterns = []string{"./..."}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "designlint: %v\n", err)
		return exitToolError
	}
	root, err := moduleRoot(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "designlint: %v\n", err)
		return exitToolError
	}

	registry, err := design.LoadRegistry()
	if err != nil {
		fmt.Fprintf(stderr, "designlint: %v\n", err)
		return exitToolError
	}

	roots, dirFiles, scopeRoots, scopeFiles, err := resolvePatterns(cwd, root, opt.patterns)
	if err != nil {
		fmt.Fprintf(stderr, "designlint: %v\n", err)
		return exitToolError
	}

	rules := splitCSV(opt.rules)
	scanOpts := conform.ScanOptions{
		Root:      root,
		Roots:     roots,
		Files:     dirFiles,
		Rules:     rules,
		Allowlist: toAllowEntries(registry.AllowEntries()),
	}
	if opt.diff {
		changed, err := gitChangedFiles(root, opt.base)
		if err != nil {
			fmt.Fprintf(stderr, "designlint: %v\n", err)
			return exitToolError
		}
		scanOpts.Roots = nil
		scanOpts.Files = filterChanged(changed, scopeRoots, scopeFiles)
		if len(scanOpts.Files) == 0 {
			if opt.json {
				writeJSON(stdout, &conform.Result{})
			}
			fmt.Fprintln(stderr, "designlint: no changed Go files to check")
			return exitOK
		}
	}

	res, err := conform.Scan(scanOpts)
	if err != nil {
		fmt.Fprintf(stderr, "designlint: %v\n", err)
		return exitToolError
	}

	if opt.writeBaseline {
		path := absFrom(cwd, opt.baseline)
		n, err := conform.WriteBaseline(path, res.Violations)
		if err != nil {
			fmt.Fprintf(stderr, "designlint: %v\n", err)
			return exitToolError
		}
		fmt.Fprintf(stderr, "designlint: wrote %d baseline entries to %s\n", n, opt.baseline)
		return exitOK
	}

	baseline, err := conform.LoadBaseline(absFrom(cwd, opt.baseline))
	if err != nil {
		fmt.Fprintf(stderr, "designlint: %v\n", err)
		return exitToolError
	}
	marked := baseline.Mark(res.Violations)

	// A registry allowlist entry is the owner's explicit approval of a
	// self-drawn area (DESIGN.md §7.1). Registered violations stay visible in
	// --json but are not "new" and never block: they are the current,
	// approved stock. Only unregistered violations outside the baseline do.
	fresh := make([]conform.Violation, 0, len(marked))
	registered := 0
	for _, v := range res.Violations {
		if v.Allowed {
			registered++
		}
	}
	for _, v := range marked {
		if v.Allowed || v.Baseline {
			continue
		}
		fresh = append(fresh, v)
	}

	if opt.json {
		writeJSON(stdout, res)
	} else {
		for _, v := range fresh {
			writeText(stdout, v)
		}
	}

	blocking := 0
	for _, v := range fresh {
		if v.Severity == conform.SeverityError || (opt.strict && v.Severity == conform.SeverityWarn) {
			blocking++
		}
	}
	errors, warnings := countSeverity(fresh)
	fmt.Fprintf(stderr, "designlint: %d files, %d violations: %d error, %d warn | baseline %d entries, %d registered, %d new, %d blocking\n",
		res.Files, len(res.Violations), errors, warnings, baseline.Len(), registered, len(fresh), blocking)
	if blocking > 0 {
		return exitViolation
	}
	return exitOK
}

// writeText prints one violation in the agent-consumable one-line format of
// DESIGN-LINT §1.3.
func writeText(w io.Writer, v conform.Violation) {
	fmt.Fprintf(w, "%s:%d:%d: %s [%s] %s → %s\n", v.File, v.Line, v.Column, v.Rule, v.AP, v.Message, v.Fix)
}

// jsonViolation is the stable JSON schema of DESIGN-LINT §1.4.
type jsonViolation struct {
	Rule     string `json:"rule"`
	AP       string `json:"ap"`
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Message  string `json:"message"`
	Fix      string `json:"fix"`
	Doc      string `json:"doc"`
	Baseline bool   `json:"baseline"`
	Allowed  bool   `json:"allowed"`
	AllowID  string `json:"allow_id,omitempty"`
}

type jsonReport struct {
	Version    int             `json:"version"`
	Files      int             `json:"files"`
	Violations []jsonViolation `json:"violations"`
}

// writeJSON prints every scanned violation, baseline-covered or not; the
// baseline flag tells the consumer which ones are grandfathered stock.
func writeJSON(w io.Writer, res *conform.Result) {
	report := jsonReport{Version: 1, Files: res.Files, Violations: make([]jsonViolation, 0, len(res.Violations))}
	for _, v := range res.Violations {
		report.Violations = append(report.Violations, jsonViolation{
			Rule:     v.Rule,
			AP:       v.AP,
			Severity: v.Severity,
			File:     v.File,
			Line:     v.Line,
			Column:   v.Column,
			Message:  v.Message,
			Fix:      v.Fix,
			Doc:      v.Doc,
			Baseline: v.Baseline,
			Allowed:  v.Allowed,
			AllowID:  v.AllowID,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(report)
}

func countSeverity(violations []conform.Violation) (errors, warnings int) {
	for _, v := range violations {
		switch v.Severity {
		case conform.SeverityError:
			errors++
		case conform.SeverityWarn:
			warnings++
		}
	}
	return errors, warnings
}

// moduleRoot walks up from dir to the directory holding go.mod.
func moduleRoot(dir string) (string, error) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above %s", dir)
		}
		dir = parent
	}
}

// resolvePatterns expands the go-style package patterns into scan roots
// (recursive "./..." forms) and explicit files (plain package directories),
// all repo-relative. scopeRoots/scopeFiles describe the same scope for --diff
// filtering.
func resolvePatterns(cwd, root string, patterns []string) (roots, files, scopeRoots, scopeFiles []string, err error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for _, pattern := range patterns {
		if pattern == "" || pattern == "." || pattern == "./" {
			pattern = "."
		}
		recursive := strings.HasSuffix(pattern, "/...")
		base := strings.TrimSuffix(pattern, "/...")
		if base == "" {
			base = "."
		}
		abs, err := filepath.Abs(filepath.Join(cwd, filepath.FromSlash(base)))
		if err != nil {
			return nil, nil, nil, nil, err
		}
		rel, err := filepath.Rel(rootAbs, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			return nil, nil, nil, nil, fmt.Errorf("pattern %q is outside the module root %s", pattern, root)
		}
		rel = filepath.ToSlash(rel)
		if recursive {
			roots = append(roots, rel)
			scopeRoots = append(scopeRoots, rel)
			continue
		}
		entries, err := os.ReadDir(abs)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("pattern %q: %w", pattern, err)
		}
		found := false
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			found = true
			file := filepath.ToSlash(filepath.Join(rel, e.Name()))
			files = append(files, file)
			scopeFiles = append(scopeFiles, file)
		}
		if !found {
			scopeRoots = append(scopeRoots, rel)
		}
	}
	return roots, files, scopeRoots, scopeFiles, nil
}

// gitChangedFiles lists the changed (staged + unstaged + untracked) Go files
// relative to root.
func gitChangedFiles(root, base string) ([]string, error) {
	if base == "" {
		base = "HEAD"
	}
	commands := [][]string{
		{"diff", "--name-only", base},
		{"diff", "--name-only", "--cached"},
		{"ls-files", "--others", "--exclude-standard"},
	}
	seen := make(map[string]bool)
	for _, args := range commands {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exitErr.Stderr)))
			}
			return nil, fmt.Errorf("git %s: %w (is git installed?)", strings.Join(args, " "), err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || !strings.HasSuffix(line, ".go") || strings.HasSuffix(line, "_test.go") {
				continue
			}
			seen[filepath.ToSlash(line)] = true
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out, nil
}

// filterChanged keeps the changed files that fall inside the requested
// pattern scope.
func filterChanged(changed, scopeRoots, scopeFiles []string) []string {
	if len(scopeRoots) == 0 && len(scopeFiles) == 0 {
		return changed
	}
	scope := make(map[string]bool, len(scopeFiles))
	for _, f := range scopeFiles {
		scope[f] = true
	}
	var out []string
	for _, f := range changed {
		if scope[f] {
			out = append(out, f)
			continue
		}
		for _, r := range scopeRoots {
			if r == "." || f == r || strings.HasPrefix(f, r+"/") {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

func toAllowEntries(in []design.AllowEntry) []conform.AllowEntry {
	out := make([]conform.AllowEntry, 0, len(in))
	for _, a := range in {
		out = append(out, conform.AllowEntry{
			ID:     a.ID,
			Paths:  a.Paths,
			Rules:  a.Rules,
			Owner:  a.Owner,
			Reason: a.Reason,
			Until:  a.Until,
		})
	}
	return out
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// absFrom resolves a possibly-relative path against dir.
func absFrom(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, filepath.FromSlash(p))
}
