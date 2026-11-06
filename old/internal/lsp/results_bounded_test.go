package lsp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// locationJSON renders one valid Location entry.
func locationJSON(uri string) string {
	return fmt.Sprintf(`{"uri":%q,"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}}`, uri)
}

// TestParseLocationsIsBounded: a hostile/abnormal server can return an
// arbitrarily long location array; the parser must stop at the render bound
// instead of materializing every element.
func TestParseLocationsIsBounded(t *testing.T) {
	const want = 50 // LSPMaxLocations (internal/tools/lsp.go); kept literal to avoid a package cycle
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < 5000; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(locationJSON(fmt.Sprintf("file:///workspace/f%d.go", i)))
	}
	b.WriteByte(']')
	locations, err := parseLocations(json.RawMessage(b.String()))
	if err != nil {
		t.Fatalf("parseLocations: %v", err)
	}
	if len(locations) != want {
		t.Fatalf("parseLocations returned %d locations, want the %d-entry parse bound", len(locations), want)
	}
	for i, location := range locations {
		if got, expected := location.URI, fmt.Sprintf("file:///workspace/f%d.go", i); got != expected {
			t.Fatalf("location %d = %q, want %q", i, got, expected)
		}
	}
}

// TestParseLocationsDoesNotDecodePastBound: entries beyond the parse bound are
// not decoded, so a malformed trailing element cannot turn a bounded read into
// an error (the JSON-RPC frame itself was already validated as JSON).
func TestParseLocationsDoesNotDecodePastBound(t *testing.T) {
	items := make([]string, 0, 52)
	for i := 0; i < 50; i++ {
		items = append(items, locationJSON(fmt.Sprintf("file:///f%d.go", i)))
	}
	items = append(items, `{"unexpected":true}`, `42`)
	locations, err := parseLocations(json.RawMessage("[" + strings.Join(items, ",") + "]"))
	if err != nil {
		t.Fatalf("bounded parse decoded past its limit: %v", err)
	}
	if len(locations) != 50 {
		t.Fatalf("locations = %d, want 50", len(locations))
	}
}

// TestParseLocationsStillRejectsMalformedWithinBound keeps the error contract
// for the entries that are actually parsed.
func TestParseLocationsStillRejectsMalformedWithinBound(t *testing.T) {
	if _, err := parseLocations(json.RawMessage(`[{"unexpected":true}]`)); err == nil {
		t.Fatal("parseLocations accepted a malformed location inside the bound")
	}
}

// TestParseLocationsSingleAndNull preserves the non-array result shapes.
func TestParseLocationsSingleAndNull(t *testing.T) {
	locations, err := parseLocations(json.RawMessage(`null`))
	if err != nil || locations != nil {
		t.Fatalf("null result = (%v, %v), want (nil, nil)", locations, err)
	}
	locations, err = parseLocations(json.RawMessage(locationJSON("file:///only.go")))
	if err != nil {
		t.Fatalf("single result: %v", err)
	}
	if len(locations) != 1 || locations[0].URI != "file:///only.go" {
		t.Fatalf("single result = %+v", locations)
	}
}
