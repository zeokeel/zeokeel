package design

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The registry is the machine-readable form of the design contract in
// docs/DESIGN.md: the entry components, the Agent scenarios, the rejected
// anti-patterns and the registered self-drawn areas (allowlist). It is the
// single source the lint tooling (internal/design/conform, designlint) and the
// conformance tests read; docs/DESIGN.md is its human-readable mirror.
//
// It is JSON rather than the YAML sketch in docs/DESIGN.md appendix A because
// the offline module cache has no YAML parser and the design contract must not
// grow a dependency just to be readable. Encoding/json is in the standard
// library, so the registry stays pure data.
//
// DESIGN.md §7.2 / §8.3: adding a self-drawn widget means adding an allowlist
// entry here (with owner, reason and until) in the same change.
//
//go:embed registry.json
var registryJSON []byte

// Registry is the parsed design/registry.json.
type Registry struct {
	Version      int           `json:"version"`
	Source       Source        `json:"source"`
	Components   []Component   `json:"components"`
	Scenarios    []Scenario    `json:"scenarios"`
	AntiPatterns []AntiPattern `json:"anti_patterns"`
	Allowlist    []AllowEntry  `json:"allowlist"`
}

// Source pins the registry to the framework version and the packages it maps.
type Source struct {
	Mygo      string `json:"mygo"`
	Entries   string `json:"entries"`
	DesignDoc string `json:"design_doc"`
	LintDoc   string `json:"lint_doc"`
}

// Component is one entry component: the only way a scenario may render its
// content. Entry and Package name the target API; the current implementation
// may still live somewhere else (see Status).
type Component struct {
	ID        string   `json:"id"`
	Entry     string   `json:"entry"`
	Package   string   `json:"package"`
	States    []string `json:"states,omitempty"`
	Allowed   []string `json:"allowed,omitempty"`
	Forbidden []string `json:"forbidden,omitempty"`
	Rules     []string `json:"rules,omitempty"`
	Status    string   `json:"status,omitempty"`
	Doc       string   `json:"doc,omitempty"`
}

// Scenario maps an Agent task stage to its single entry component and to what
// that entry must (or must not) use underneath.
type Scenario struct {
	ID       string   `json:"id"`
	Entry    string   `json:"entry"`
	Forbid   []string `json:"forbid,omitempty"`
	Requires []string `json:"requires,omitempty"`
	Doc      string   `json:"doc,omitempty"`
}

// AntiPattern is one AP-xx from docs/DESIGN.md §3.1, linked to the lint rule
// that detects it (empty when it can only be reviewed by a human or a frame
// test).
type AntiPattern struct {
	ID      string `json:"id"`
	Rule    string `json:"rule"`
	Check   string `json:"check,omitempty"`
	Message string `json:"message"`
	Doc     string `json:"doc,omitempty"`
}

// AllowEntry registers one legal self-drawn area (DESIGN.md §7.1). Missing
// owner/reason/until, or an expired until, makes the entry invalid (DS-06b).
type AllowEntry struct {
	ID     string   `json:"id"`
	Paths  []string `json:"paths"`
	Rules  []string `json:"rules,omitempty"`
	What   string   `json:"what"`
	Owner  string   `json:"owner"`
	Reason string   `json:"reason"`
	Until  string   `json:"until"`
}

// KnownRuleIDs are the design-contract rules the lint layer implements or
// plans (docs/DESIGN-LINT.md §2). DS-06b is the malformed-exemption error.
var KnownRuleIDs = []string{
	"DS-01", "DS-02", "DS-03", "DS-04", "DS-05",
	"DS-06", "DS-06b", "DS-07", "DS-08", "DS-09", "DS-10",
	"DS-11", "DS-12", "DS-13", "DS-14", "DS-15", "DS-16", "DS-17",
}

// today returns the reference day for allowlist expiry checks. It is a
// variable so tests can pin it.
var today = func() time.Time { return time.Now().UTC() }

// LoadRegistry parses the embedded registry and validates it.
func LoadRegistry() (*Registry, error) {
	r, err := ParseRegistry(registryJSON)
	if err != nil {
		return nil, fmt.Errorf("design: registry.json: %w", err)
	}
	if err := r.Validate(); err != nil {
		return nil, fmt.Errorf("design: registry.json: %w", err)
	}
	return r, nil
}

// ParseRegistry decodes a registry document. It does not validate it; call
// Validate for that.
func ParseRegistry(data []byte) (*Registry, error) {
	var r Registry
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if dec.More() {
		return nil, errors.New("decode: trailing JSON after the registry object")
	}
	return &r, nil
}

// Validate checks the registry's schema invariants and the allowlist's
// owner/until contract (DS-08). It returns every problem it finds, not just
// the first one.
func (r *Registry) Validate() error { return r.ValidateAt(today()) }

// ValidateAt validates against a fixed day, so tests can age the allowlist.
func (r *Registry) ValidateAt(now time.Time) error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if r.Version != 1 {
		add("version = %d, want 1", r.Version)
	}
	if r.Source.Mygo == "" {
		add("source.mygo is empty")
	}
	if r.Source.Entries == "" {
		add("source.entries is empty")
	}
	if len(r.Components) == 0 {
		add("components is empty")
	}
	if len(r.Scenarios) == 0 {
		add("scenarios is empty")
	}
	if len(r.AntiPatterns) == 0 {
		add("anti_patterns is empty")
	}
	if len(r.Allowlist) == 0 {
		add("allowlist is empty")
	}

	componentIDs := make(map[string]bool, len(r.Components))
	for i, c := range r.Components {
		switch {
		case c.ID == "":
			add("components[%d].id is empty", i)
		case componentIDs[c.ID]:
			add("duplicate component id %q", c.ID)
		}
		componentIDs[c.ID] = true
		if c.Entry == "" {
			add("component %q: entry is empty", c.ID)
		}
		if c.Package == "" {
			add("component %q: package is empty", c.ID)
		}
	}

	scenarioIDs := make(map[string]bool, len(r.Scenarios))
	for i, s := range r.Scenarios {
		switch {
		case s.ID == "":
			add("scenarios[%d].id is empty", i)
		case scenarioIDs[s.ID]:
			add("duplicate scenario id %q", s.ID)
		}
		scenarioIDs[s.ID] = true
		if s.Entry == "" {
			add("scenario %q: entry is empty", s.ID)
		}
		if len(s.Forbid) == 0 && len(s.Requires) == 0 {
			add("scenario %q: needs forbid or requires", s.ID)
		}
	}

	apIDs := make(map[string]bool, len(r.AntiPatterns))
	for i, ap := range r.AntiPatterns {
		switch {
		case ap.ID == "":
			add("anti_patterns[%d].id is empty", i)
		case apIDs[ap.ID]:
			add("duplicate anti-pattern id %q", ap.ID)
		}
		apIDs[ap.ID] = true
		if !strings.HasPrefix(ap.ID, "AP-") {
			add("anti-pattern %q: id must look like AP-xx", ap.ID)
		}
		if ap.Message == "" {
			add("anti-pattern %q: message is empty", ap.ID)
		}
		for _, rule := range splitRules(ap.Rule) {
			if !knownRule(rule) {
				add("anti-pattern %q: unknown rule %q", ap.ID, rule)
			}
		}
	}

	allowIDs := make(map[string]bool, len(r.Allowlist))
	for i, a := range r.Allowlist {
		switch {
		case a.ID == "":
			add("allowlist[%d].id is empty", i)
		case allowIDs[a.ID]:
			add("duplicate allowlist id %q", a.ID)
		}
		allowIDs[a.ID] = true
		if len(a.Paths) == 0 {
			add("allowlist %q: paths is empty", a.ID)
		}
		for _, p := range a.Paths {
			if strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.Contains(p, "..") {
				add("allowlist %q: path %q must be repo-relative with forward slashes", a.ID, p)
			}
		}
		if a.What == "" {
			add("allowlist %q: what is empty", a.ID)
		}
		if a.Owner == "" {
			add("allowlist %q: owner is empty (DS-06b)", a.ID)
		}
		if a.Reason == "" {
			add("allowlist %q: reason is empty (DS-06b)", a.ID)
		}
		if a.Until == "" {
			add("allowlist %q: until is empty (DS-06b)", a.ID)
			continue
		}
		until, err := time.Parse("2006-01-02", a.Until)
		if err != nil {
			add("allowlist %q: until %q is not yyyy-mm-dd", a.ID, a.Until)
			continue
		}
		if until.Before(day(now)) {
			add("allowlist %q expired on %s (DS-06b)", a.ID, a.Until)
		}
		for _, rule := range splitRules(strings.Join(a.Rules, ",")) {
			if !knownRule(rule) {
				add("allowlist %q: unknown rule %q", a.ID, rule)
			}
		}
	}

	return errors.Join(errs...)
}

// AllowEntries returns the allowlist entries with their rules defaulted to
// DS-06, as the lint layer expects them.
func (r *Registry) AllowEntries() []AllowEntry {
	out := make([]AllowEntry, 0, len(r.Allowlist))
	for _, a := range r.Allowlist {
		if len(a.Rules) == 0 {
			a.Rules = []string{"DS-06"}
		}
		out = append(out, a)
	}
	return out
}

// ComponentByID returns the registry entry for a component id.
func (r *Registry) ComponentByID(id string) (Component, bool) {
	for _, c := range r.Components {
		if c.ID == id {
			return c, true
		}
	}
	return Component{}, false
}

// ScenarioByID returns the registry entry for a scenario id.
func (r *Registry) ScenarioByID(id string) (Scenario, bool) {
	for _, s := range r.Scenarios {
		if s.ID == id {
			return s, true
		}
	}
	return Scenario{}, false
}

// AntiPatternByID returns the registry entry for an AP-xx id.
func (r *Registry) AntiPatternByID(id string) (AntiPattern, bool) {
	for _, ap := range r.AntiPatterns {
		if ap.ID == id {
			return ap, true
		}
	}
	return AntiPattern{}, false
}

func splitRules(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func knownRule(id string) bool {
	for _, known := range KnownRuleIDs {
		if id == known {
			return true
		}
	}
	return false
}

func day(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
