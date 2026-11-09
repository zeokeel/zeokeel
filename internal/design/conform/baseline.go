package conform

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// DefaultBaselineExpiry is the cleanup deadline written into new baseline
// entries (DESIGN-LINT §4.2: the stock ledger is time-boxed and only shrinks).
const DefaultBaselineExpiry = "2027-06-30"

// BaselineEntry is one grandfathered violation in .design-baseline.json.
// Matching ignores the line number: the identity is rule + file + normalized
// text, so gofmt/insertions do not invalidate the ledger (DESIGN-LINT §4.2).
type BaselineEntry struct {
	Rule    string `json:"rule"`
	File    string `json:"file"`
	Line    int    `json:"line,omitempty"`
	Text    string `json:"text"`
	Owner   string `json:"owner,omitempty"`
	Expires string `json:"expires,omitempty"`
}

// Baseline is the parsed .design-baseline.json.
type Baseline struct {
	Version   int             `json:"version"`
	Generated string          `json:"generated,omitempty"`
	Ignored   []BaselineEntry `json:"ignored"`
}

// LoadBaseline reads a baseline file. A missing file is an empty baseline
// (the first run writes one); a malformed file is an error.
func LoadBaseline(file string) (*Baseline, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return &Baseline{Version: 1}, nil
		}
		return nil, err
	}
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if b.Version != 1 {
		return nil, fmt.Errorf("%s: version = %d, want 1", file, b.Version)
	}
	return &b, nil
}

// Len returns the number of grandfathered violations.
func (b *Baseline) Len() int { return len(b.Ignored) }

// Mark sets Baseline=true on every violation covered by the ledger and
// returns the violations the baseline does not cover (the new ones). Each
// entry can cover one occurrence: two identical calls on one line need two
// entries.
func (b *Baseline) Mark(violations []Violation) []Violation {
	remaining := make(map[string]int, len(b.Ignored))
	for _, e := range b.Ignored {
		remaining[violationKey(e.Rule, e.File, e.Text)]++
	}
	fresh := make([]Violation, 0, len(violations))
	for i := range violations {
		key := violations[i].ID()
		if n := remaining[key]; n > 0 {
			remaining[key] = n - 1
			violations[i].Baseline = true
			continue
		}
		fresh = append(fresh, violations[i])
	}
	return fresh
}

// WriteBaseline writes the current violations as the new stock ledger.
func WriteBaseline(file string, violations []Violation) (int, error) {
	entries := make([]BaselineEntry, 0, len(violations))
	for _, v := range violations {
		owner := v.Owner
		if owner == "" {
			owner = moduleOwner(v.File)
		}
		expires := v.Expires
		if expires == "" {
			expires = DefaultBaselineExpiry
		}
		entries = append(entries, BaselineEntry{
			Rule:    v.Rule,
			File:    v.File,
			Line:    v.Line,
			Text:    normalizeSpace(v.Text),
			Owner:   owner,
			Expires: expires,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Rule < b.Rule
	})
	doc := Baseline{
		Version:   1,
		Generated: time.Now().UTC().Format("2006-01-02"),
		Ignored:   entries,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return 0, err
	}
	data = append(data, '\n')
	if err := os.WriteFile(file, data, 0o644); err != nil {
		return 0, err
	}
	return len(entries), nil
}

// moduleOwner derives a reasonable owner from a repo path for baseline
// entries that have no allowlist owner: internal/ui/<module> -> <module>,
// internal/uiroot -> uiroot, and so on.
func moduleOwner(file string) string {
	dir := path.Dir(file)
	switch {
	case dir == "internal/uiroot" || strings.HasPrefix(dir, "internal/uiroot/"):
		return "uiroot"
	case dir == "internal/uikit" || strings.HasPrefix(dir, "internal/uikit/"):
		return "uikit"
	case dir == "internal/design" || strings.HasPrefix(dir, "internal/design/"):
		return "design"
	case dir == "internal/ui":
		return "ui"
	case strings.HasPrefix(dir, "internal/ui/"):
		rest := strings.TrimPrefix(dir, "internal/ui/")
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			rest = rest[:i]
		}
		return rest
	}
	return "design-system"
}
