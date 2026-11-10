// Package conform implements the deterministic design-contract checks from
// docs/DESIGN-LINT.md, as a library the designlint CLI and the go test
// conformance suite share.
//
// It uses only the Go standard library (go/parser + go/ast): the repository is
// offline-first and the design contract must not grow a dependency just to be
// linted. The trade-off is that the rules are syntactic, not type-checked:
// imports are resolved by their paths in the file being scanned, so a
// selector is recognized as "mygo ui" only when the file imports
// github.com/egoist/mygo/ui under that local name.
//
// Rules currently implemented (phase 1 of the roll-out in DESIGN-LINT §7):
//
//	DS-01 颜色字面量（ui.RGBA/RGB/Hex 调用 + hex 字符串字面量）
//	DS-03 裸数值度量（固定方法集 + 数值字面量实参）
//	DS-06 未登记自绘（ui.ButtonBase/DialogBase 调用点不在 allowlist）
//	DS-06b 无效的行内 design:allow 豁免
//	DS-07 emoji/Unicode 图标字面量、internal/icons 之外的 ParseSVG
//	DS-10 uikit.DialogAction{Danger: true, Default: true}
//
// DS-02/04/05/08/09 are documented in DESIGN-LINT §3 but enforced elsewhere
// (frame tests, conformance tests, human review) for now; see docs/DESIGN-LINT.md
// §7 for the staged plan.
package conform

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Severity levels (DESIGN-LINT §1.5). An error blocks the gate; a warn is
// reported but only blocks with --strict; info is JSON-only statistics.
const (
	SeverityError = "error"
	SeverityWarn  = "warn"
	SeverityInfo  = "info"
)

// Rule is the static metadata of one design-contract rule.
type Rule struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	AP       string `json:"ap"`
	Severity string `json:"severity"`
	Doc      string `json:"doc"`
}

// ruleList is the rule catalog, in reporting order.
var ruleList = []Rule{
	{ID: "DS-01", Name: "颜色字面量禁止", AP: "AP-01", Severity: SeverityError, Doc: "docs/DESIGN-LINT.md §3.1"},
	{ID: "DS-03", Name: "裸数值度量", AP: "AP-03", Severity: SeverityError, Doc: "docs/DESIGN-LINT.md §3.3"},
	{ID: "DS-06", Name: "未登记自绘", AP: "AP-04", Severity: SeverityError, Doc: "docs/DESIGN-LINT.md §3.6"},
	{ID: "DS-06b", Name: "无效的 design:allow 豁免", AP: "AP-15", Severity: SeverityError, Doc: "docs/DESIGN-LINT.md §4.1"},
	{ID: "DS-07", Name: "图标来源/尺寸", AP: "AP-10", Severity: SeverityError, Doc: "docs/DESIGN-LINT.md §3.7"},
	{ID: "DS-10", Name: "对话框语义", AP: "AP-12", Severity: SeverityError, Doc: "docs/DESIGN-LINT.md §3.10"},
	// DS-11..DS-16 are review contracts (v0.2.19): frame tests and snapshots
	// enforce them; they are listed so the registry's anti-pattern map stays
	// complete and designlint can report them as info statistics later.
	{ID: "DS-11", Name: "危险确认安全默认", AP: "AP-24", Severity: SeverityInfo, Doc: "docs/DESIGN-LINT.md §2"},
	{ID: "DS-12", Name: "选择态两级", AP: "AP-19", Severity: SeverityInfo, Doc: "docs/DESIGN-LINT.md §2"},
	{ID: "DS-13", Name: "强调色预算", AP: "AP-20", Severity: SeverityInfo, Doc: "docs/DESIGN-LINT.md §2"},
	{ID: "DS-14", Name: "状态徽章解剖", AP: "AP-21", Severity: SeverityInfo, Doc: "docs/DESIGN-LINT.md §2"},
	{ID: "DS-15", Name: "字号/高度纪律", AP: "AP-22", Severity: SeverityInfo, Doc: "docs/DESIGN-LINT.md §2"},
	{ID: "DS-16", Name: "表面 ramp / elevation", AP: "AP-23", Severity: SeverityInfo, Doc: "docs/DESIGN-LINT.md §2"},
}

// Rules returns the rule catalog.
func Rules() []Rule {
	out := make([]Rule, len(ruleList))
	copy(out, ruleList)
	return out
}

// LookupRule returns the rule with the given ID.
func LookupRule(id string) (Rule, bool) {
	for _, r := range ruleList {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}

// Violation is one reported contract violation.
type Violation struct {
	Rule     string `json:"rule"`
	AP       string `json:"ap"`
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Message  string `json:"message"`
	Fix      string `json:"fix"`
	Doc      string `json:"doc"`
	// Text is the normalized source snippet used as the baseline identity
	// (DESIGN-LINT §4.2: rule + file + text; the line may move).
	Text string `json:"text"`
	// Allowed is set when a registry allowlist entry or a valid inline
	// design:allow comment covers the violation.
	Allowed bool `json:"allowed"`
	// AllowID is the allowlist entry id ("" for inline exemptions).
	AllowID string `json:"allow_id,omitempty"`
	// Owner and Expires carry the allowlist accountability into the baseline.
	Owner   string `json:"owner,omitempty"`
	Expires string `json:"expires,omitempty"`
	// Baseline is set by the CLI when .design-baseline.json covers the
	// violation; it is not part of the scan output.
	Baseline bool `json:"baseline"`
}

// ID renders the stable rule key of a violation: rule + file + text.
func (v Violation) ID() string { return violationKey(v.Rule, v.File, v.Text) }

func violationKey(rule, file, text string) string {
	return rule + "\x00" + file + "\x00" + normalizeSpace(text)
}

// AllowEntry registers a legal self-drawn area (docs/DESIGN.md §7.1). It
// mirrors internal/design/registry.json's allowlist without importing the
// design package, so the library stays independent of the app code it scans.
type AllowEntry struct {
	ID     string
	Paths  []string
	Rules  []string
	Owner  string
	Reason string
	Until  string
}

// matches reports whether the entry covers file under one of the enabled
// rules. Paths are prefixes: "internal/ui/session" covers the directory *and*
// a file of that exact name.
func (a AllowEntry) matches(rule, file string) bool {
	ruleOK := false
	for _, r := range a.Rules {
		if r == rule {
			ruleOK = true
			break
		}
	}
	if !ruleOK {
		return false
	}
	for _, p := range a.Paths {
		p = strings.TrimSuffix(p, "/")
		if file == p || strings.HasPrefix(file, p+"/") {
			return true
		}
	}
	return false
}

// ScanOptions configures Scan.
type ScanOptions struct {
	// Root is the repository root: every reported path is relative to it.
	// Empty means the current directory.
	Root string
	// Roots are directories under Root to walk recursively. Empty means the
	// whole module (".", with hidden/vendor/testdata directories skipped).
	Roots []string
	// Files are explicit repo-relative Go files to scan (used by --diff).
	// When set they are scanned in addition to Roots.
	Files []string
	// Rules limits the scan to these rule IDs. Empty means all rules.
	Rules []string
	// Allowlist is the registry allowlist (DS-06 registrations).
	Allowlist []AllowEntry
	// Today is the reference day for inline exemption expiry. Zero means
	// time.Now.
	Today time.Time
}

// Result is the outcome of a scan.
type Result struct {
	// Files is the number of non-test Go files scanned.
	Files int
	// Violations is sorted by file, line, column and rule.
	Violations []Violation
}

// Scan implements the design contract checks.
func Scan(opts ScanOptions) (*Result, error) {
	enabled, err := ruleSet(opts.Rules)
	if err != nil {
		return nil, err
	}
	files, err := collectFiles(opts)
	if err != nil {
		return nil, err
	}
	today := opts.Today
	if today.IsZero() {
		today = time.Now()
	}
	res := &Result{}
	for _, rel := range files {
		violations, err := scanFile(opts.Root, rel, enabled, opts.Allowlist, today)
		if err != nil {
			return nil, err
		}
		res.Files++
		res.Violations = append(res.Violations, violations...)
	}
	sort.Slice(res.Violations, func(i, j int) bool {
		a, b := res.Violations[i], res.Violations[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		return ruleOrder(a.Rule) < ruleOrder(b.Rule)
	})
	return res, nil
}

// enabledRules is the set of rule IDs a scan should run; nil means all rules.
type enabledRules map[string]bool

func (s enabledRules) enabled(id string) bool {
	if s == nil {
		return true
	}
	return s[id]
}

// ruleSet validates a --rules filter and returns the enabled set (nil means
// all rules).
func ruleSet(ids []string) (enabledRules, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	set := make(enabledRules, len(ids))
	for _, id := range ids {
		if _, ok := LookupRule(id); !ok {
			return nil, fmt.Errorf("unknown rule %q (want one of %s)", id, strings.Join(ruleIDs(), ", "))
		}
		set[id] = true
	}
	return set, nil
}
func ruleOrder(id string) int {
	for i, r := range ruleList {
		if r.ID == id {
			return i
		}
	}
	return len(ruleList)
}

func ruleIDs() []string {
	out := make([]string, len(ruleList))
	for i, r := range ruleList {
		out[i] = r.ID
	}
	return out
}

func normalizeSpace(s string) string { return strings.Join(strings.Fields(s), " ") }
