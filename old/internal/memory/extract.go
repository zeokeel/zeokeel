package memory

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParseExtraction parses the model's extraction response into validated
// candidates.
//
// The response is normally one JSON object:
//
//	{"memories":[{"scope":"global","kind":"user","content":"...","confidence":0.8}]}
//
// A ```json fence, surrounding prose, or a bare JSON array are tolerated.
// Individual malformed or invalid entries are dropped (not fatal); the function
// returns the valid subset, deduplicated by content hash and capped at
// MaxCandidates. An empty or non-JSON response, or an object without a
// "memories" array, is an error.
func ParseExtraction(text string) ([]Candidate, error) {
	payload, err := extractionPayload(text)
	if err != nil {
		return nil, err
	}
	entries, err := extractionEntries(payload)
	if err != nil {
		return nil, err
	}
	candidates := make([]Candidate, 0, MaxCandidates)
	seen := make(map[string]bool, len(entries))
	for _, raw := range entries {
		var candidate Candidate
		if err := json.Unmarshal(raw, &candidate); err != nil {
			continue
		}
		validated, err := ValidateCandidate(candidate)
		if err != nil {
			continue
		}
		hash := Hash(validated.Content)
		if seen[hash] {
			continue
		}
		seen[hash] = true
		candidates = append(candidates, validated)
		if len(candidates) >= MaxCandidates {
			break
		}
	}
	return candidates, nil
}

// extractionPayload isolates the JSON value in text: it tolerates markdown code
// fences and surrounding prose, then returns the substring from the first JSON
// object/array opener to its last matching closer.
func extractionPayload(text string) (string, error) {
	payload := strings.TrimSpace(text)
	if payload == "" {
		return "", fmt.Errorf("memory: extraction response is empty")
	}
	if fence := strings.Index(payload, "```"); fence >= 0 {
		rest := payload[fence+len("```"):]
		if newline := strings.IndexByte(rest, '\n'); newline >= 0 {
			rest = rest[newline+1:]
		}
		if close := strings.Index(rest, "```"); close >= 0 {
			rest = rest[:close]
		}
		payload = strings.TrimSpace(rest)
	}
	if json.Valid([]byte(payload)) {
		return payload, nil
	}
	start := strings.IndexAny(payload, "{[")
	if start < 0 {
		return "", fmt.Errorf("memory: extraction response does not contain JSON")
	}
	end := strings.LastIndexAny(payload, "}]")
	if end <= start {
		return "", fmt.Errorf("memory: extraction response contains malformed JSON")
	}
	payload = strings.TrimSpace(payload[start : end+1])
	if !json.Valid([]byte(payload)) {
		return "", fmt.Errorf("memory: extraction response contains malformed JSON")
	}
	return payload, nil
}

// extractionEntries returns the raw candidate objects from a payload that is
// either {"memories":[...]} or a bare array.
func extractionEntries(payload string) ([]json.RawMessage, error) {
	trimmed := strings.TrimSpace(payload)
	if strings.HasPrefix(trimmed, "[") {
		var entries []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &entries); err != nil {
			return nil, fmt.Errorf("memory: extraction response is invalid JSON: %w", err)
		}
		return entries, nil
	}
	var wrapper struct {
		Memories json.RawMessage `json:"memories"`
	}
	if err := json.Unmarshal([]byte(trimmed), &wrapper); err != nil {
		return nil, fmt.Errorf("memory: extraction response is invalid JSON: %w", err)
	}
	if len(wrapper.Memories) == 0 {
		return nil, fmt.Errorf(`memory: extraction response is missing the "memories" array`)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(wrapper.Memories, &entries); err != nil {
		return nil, fmt.Errorf(`memory: extraction response "memories" is not an array: %w`, err)
	}
	return entries, nil
}
