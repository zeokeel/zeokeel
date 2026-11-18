package main

// a4qa is the A4 interaction-QA runner: it boots the real desktop assembly
// headlessly (the same boot internal/devtools/ui-drive uses) and executes the
// declarative cases in docs/research/redesign-2026-11/A4-qa/interaction-drive.txt.
//
// It is a documentation-side tool: it lives under testdata so the go tool's
// ./... patterns ignore it and it never joins the product build.
//
//	go run ./docs/research/redesign-2026-11/A4-qa/testdata/a4qa \
//	    --scenario docs/research/redesign-2026-11/A4-qa/interaction-drive.txt \
//	    --out .cache/a4-qa
//
// Exit code 0 when every non-guarded case passes; guarded cases are the known
// missing behaviours from the A4 audit and report PASS(fixed) once implemented.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// status is one case's verdict.
type status string

const (
	statusPass     status = "PASS"
	statusFail     status = "FAIL"
	statusFixed    status = "PASS(fixed)"   // a guarded case that now passes
	statusExpected status = "EXPECTED-FAIL" // a guarded case still missing
	statusKnown    status = "KNOWN-LIMIT"   // a `known` case failing on a documented platform limit
)

// caseResult is one executed case.
type caseResult struct {
	ID       string   `json:"id"`
	Surface  string   `json:"surface"`
	Title    string   `json:"title"`
	Guarded  bool     `json:"guarded"`
	Known    bool     `json:"knownLimit,omitempty"`
	Status   status   `json:"status"`
	Failures []string `json:"failures,omitempty"`
	Notes    []string `json:"notes,omitempty"`
	Shots    []string `json:"shots,omitempty"`
	Duration string   `json:"duration"`
}

// op is one parsed scenario operation.
type op struct {
	name string
	args string
	line int
}

// qaCase is one parsed case.
type qaCase struct {
	id      string
	surface string
	title   string
	guarded bool
	known   bool
	line    int
	ops     []op
}

func main() {
	scenario := flag.String("scenario", "docs/research/redesign-2026-11/A4-qa/interaction-drive.txt", "scenario file")
	outDir := flag.String("out", ".cache/a4-qa", "output directory")
	only := flag.String("only", "", "comma-separated case-id prefixes to run")
	list := flag.Bool("list", false, "list the cases and exit")
	flag.Parse()

	cases, err := parseScenario(*scenario)
	if err != nil {
		fmt.Fprintf(os.Stderr, "a4qa: %v\n", err)
		os.Exit(2)
	}
	if *list {
		for _, c := range cases {
			mark := ""
			if c.known {
				mark = "  [known-limit]"
			} else if c.guarded {
				mark = "  [guarded]"
			}
			fmt.Printf("%-28s %-10s %s%s\n", c.id, c.surface, c.title, mark)
		}
		return
	}
	filters := splitFilter(*only)
	var results []caseResult
	failures := 0
	for _, c := range cases {
		if !matches(c.id, filters) {
			continue
		}
		start := time.Now()
		result := runCase(c, *outDir)
		result.Duration = time.Since(start).Round(10 * time.Millisecond).String()
		results = append(results, result)
		if result.Status == statusFail {
			failures++
		}
		fmt.Printf("%-14s %-28s %s\n", result.Status, result.ID, result.Title)
		for _, failure := range result.Failures {
			fmt.Printf("    ✗ %s\n", failure)
		}
	}
	if err := writeResults(*outDir, results); err != nil {
		fmt.Fprintf(os.Stderr, "a4qa: write results: %v\n", err)
	}
	printSummary(results)
	if failures > 0 {
		os.Exit(1)
	}
}

func splitFilter(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func matches(id string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		if strings.HasPrefix(id, filter) {
			return true
		}
	}
	return false
}

// parseScenario reads the declarative file:
//
//	# comment
//	case <id> | <surface> | <title> | open|guarded|known
//	  boot session
//	  key Esc
//	  expect-running false
func parseScenario(path string) ([]qaCase, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []qaCase
	var current *qaCase
	for index, line := range strings.Split(string(raw), "\n") {
		lineNo := index + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.SplitN(trimmed, " ", 2)
		name := strings.TrimSpace(fields[0])
		rest := ""
		if len(fields) > 1 {
			rest = strings.TrimSpace(fields[1])
		}
		if name == "case" {
			parts := strings.Split(rest, "|")
			if len(parts) != 4 {
				return nil, fmt.Errorf("%s:%d: case needs id | surface | title | open|guarded|known", path, lineNo)
			}
			c := qaCase{
				id:      strings.TrimSpace(parts[0]),
				surface: strings.TrimSpace(parts[1]),
				title:   strings.TrimSpace(parts[2]),
				guarded: strings.EqualFold(strings.TrimSpace(parts[3]), "guarded"),
				known:   strings.EqualFold(strings.TrimSpace(parts[3]), "known"),
				line:    lineNo,
			}
			cases = append(cases, c)
			current = &cases[len(cases)-1]
			continue
		}
		if current == nil {
			return nil, fmt.Errorf("%s:%d: operation before the first case", path, lineNo)
		}
		current.ops = append(current.ops, op{name: name, args: rest, line: lineNo})
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("%s: no cases", path)
	}
	return cases, nil
}

// runCase boots a fresh product and executes one case's operations.
func runCase(c qaCase, outDir string) caseResult {
	result := caseResult{ID: c.id, Surface: c.surface, Title: c.title, Guarded: c.guarded, Known: c.known}
	h, err := newHarnessWith(filepath.Join(outDir, "shots"), harnessOptions{NoModel: caseWantsNoModel(c.ops)})
	if err != nil {
		result.Failures = append(result.Failures, "boot: "+err.Error())
		result.Status = statusFor(c, false)
		return result
	}
	defer h.close()
	state := &caseState{harness: h, outDir: filepath.Join(outDir, "shots")}
	for _, operation := range c.ops {
		if failure := state.exec(operation); failure != "" {
			result.Failures = append(result.Failures, fmt.Sprintf("line %d (%s %s): %s", operation.line, operation.name, operation.args, failure))
		}
	}
	result.Notes = state.notes
	result.Shots = state.shots
	result.Status = statusFor(c, len(result.Failures) == 0)
	return result
}

// caseWantsNoModel reports whether the case boots without a chat model.
func caseWantsNoModel(ops []op) bool {
	for _, operation := range ops {
		if operation.name == "boot" && strings.TrimSpace(operation.args) == "session-nomodel" {
			return true
		}
	}
	return false
}

func statusFor(c qaCase, passed bool) status {
	switch {
	case passed && (c.guarded || c.known):
		return statusFixed
	case passed:
		return statusPass
	case c.known:
		// A documented platform limit (evidence in A4-QA-REPORT §7.5): still red
		// for the behaviour, but not a regression and not counted as a gap.
		return statusKnown
	case c.guarded:
		return statusExpected
	default:
		return statusFail
	}
}

func writeResults(outDir string, results []caseResult) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "results.json"), raw, 0o644)
}

func printSummary(results []caseResult) {
	counts := map[status]int{}
	for _, result := range results {
		counts[result.Status]++
	}
	fmt.Printf("\n== A4 interaction QA: %d cases — %d PASS, %d PASS(fixed), %d EXPECTED-FAIL, %d KNOWN-LIMIT, %d FAIL\n",
		len(results), counts[statusPass], counts[statusFixed], counts[statusExpected], counts[statusKnown], counts[statusFail])
	var lines []string
	for _, result := range results {
		if result.Status == statusFail || result.Status == statusFixed || result.Status == statusKnown {
			lines = append(lines, fmt.Sprintf("%s %s (%s)", result.Status, result.ID, result.Surface))
		}
	}
	sort.Strings(lines)
	for _, line := range lines {
		fmt.Println("  " + line)
	}
}
