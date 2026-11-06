package importer

import (
	"fmt"
	"os"
	"strings"
)

// CredentialRef is one credential reference found in DSH's credentials file.
// It deliberately carries only the environment/key NAME: the value is parsed
// away and never stored, returned or logged, so an import report can be shared
// safely.
type CredentialRef struct {
	// Name is the credential key, e.g. "DEEPSEEK_API_KEY".
	Name string `json:"name"`
	// Section says where the name was found (currently always "refs").
	Section string `json:"section"`
}

// InspectCredentials reads the credential NAMES from a DSH .credentials.yaml.
// A missing file returns (nil, nil): no credentials is a normal state. The
// parser is intentionally minimal (top-level `refs:` map of KEY: value) so the
// importer does not need a YAML dependency; anything it cannot classify is
// ignored rather than guessed at.
//
// Credential values are never read out of the file beyond the line scan: the
// function splits on the first ':' and keeps only the key part.
func InspectCredentials(path string) ([]CredentialRef, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	if err := inspectCredentialFileMode(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("importer: read credentials file: %w", err)
	}
	var refs []CredentialRef
	seen := make(map[string]bool)
	inRefs := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if indent == 0 {
			// Top-level key: only the refs section carries importable names.
			inRefs = strings.HasPrefix(trimmed, "refs:")
			continue
		}
		if !inRefs {
			continue
		}
		key, _, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		name := strings.Trim(strings.TrimSpace(key), `"'`)
		if name == "" || seen[name] || strings.ContainsAny(name, " \t") {
			continue
		}
		seen[name] = true
		refs = append(refs, CredentialRef{Name: name, Section: "refs"})
	}
	return refs, nil
}

// inspectCredentialFileMode reports a loose permission on the DSH credentials
// file. It is a warning-level observation, not a hard failure: the importer
// only ever reflects it in the report.
func inspectCredentialFileMode(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("credentials file %s is group/world accessible (%o); DSH should store it 0600", path, info.Mode().Perm())
	}
	return nil
}

// CredentialNames returns just the names, for callers that only need a list.
func CredentialNames(refs []CredentialRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ref.Name)
	}
	return out
}
