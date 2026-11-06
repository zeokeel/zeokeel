package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// maxParsedLocations bounds how many array entries one response decodes. The
// tool renders at most LSPMaxLocations (50) locations; decoding the rest of an
// abnormally long array would only spend memory and CPU on output that can
// never be shown, so the parser stops at the same bound.
const maxParsedLocations = 50

// parseLocations normalizes a textDocument/definition, /references or
// /implementation result. LSP servers answer with null, one Location, one
// LocationLink, or an array of either. Array results are decoded element by
// element and stop at maxParsedLocations: a hostile or buggy server cannot make
// one response allocate an unbounded slice.
func parseLocations(raw json.RawMessage) ([]Location, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if trimmed[0] == '[' {
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		if _, err := decoder.Token(); err != nil { // consume the opening '['
			return nil, fmt.Errorf("locations: %w", err)
		}
		locations := make([]Location, 0, maxParsedLocations)
		for decoder.More() && len(locations) < maxParsedLocations {
			var item json.RawMessage
			if err := decoder.Decode(&item); err != nil {
				return nil, fmt.Errorf("locations: %w", err)
			}
			location, err := parseLocation(item)
			if err != nil {
				return nil, err
			}
			locations = append(locations, location)
		}
		return locations, nil
	}
	location, err := parseLocation(trimmed)
	if err != nil {
		return nil, err
	}
	return []Location{location}, nil
}

// parseLocation accepts either a Location {uri, range} or a LocationLink
// {targetUri, targetSelectionRange/targetRange}.
func parseLocation(raw json.RawMessage) (Location, error) {
	var location struct {
		URI   string `json:"uri"`
		Range Range  `json:"range"`
	}
	if err := json.Unmarshal(raw, &location); err == nil && location.URI != "" {
		return Location{URI: location.URI, Range: location.Range}, nil
	}
	var link struct {
		TargetURI            string `json:"targetUri"`
		TargetRange          Range  `json:"targetRange"`
		TargetSelectionRange Range  `json:"targetSelectionRange"`
	}
	if err := json.Unmarshal(raw, &link); err != nil {
		return Location{}, fmt.Errorf("location: %w", err)
	}
	if link.TargetURI == "" {
		return Location{}, fmt.Errorf("location without a uri")
	}
	selected := link.TargetSelectionRange
	if selected == (Range{}) {
		selected = link.TargetRange
	}
	return Location{URI: link.TargetURI, Range: selected}, nil
}

// parseHoverResult extracts the contents union from a textDocument/hover
// result and normalizes it to plain text.
func parseHoverResult(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}
	var hover struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := json.Unmarshal(trimmed, &hover); err != nil {
		return "", err
	}
	return NormalizeHover(hover.Contents)
}
