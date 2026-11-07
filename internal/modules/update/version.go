package update

// Pragmatic semver comparison ported from the old shell's updater:
// MAJOR[.MINOR[.PATCH]][-prerelease][+build]. Missing components are 0, a
// prerelease sorts before its release and numeric prerelease identifiers sort
// before alphanumeric ones.

import (
	"fmt"
	"strings"
)

// version is one parsed semver-ish version.
type version struct {
	major, minor, patch int
	pre                 []string
}

// CompareVersions compares two semver-ish versions.
func CompareVersions(a, b string) (int, error) {
	left, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	right, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	return compareVersion(left, right), nil
}

// parseVersion parses MAJOR[.MINOR[.PATCH]][-prerelease][+build].
func parseVersion(raw string) (version, error) {
	var parsed version
	text := strings.TrimSpace(raw)
	if text == "" {
		return parsed, fmt.Errorf("empty version")
	}
	if index := strings.IndexByte(text, '+'); index >= 0 {
		text = text[:index]
	}
	core := text
	if index := strings.IndexByte(text, '-'); index >= 0 {
		core = text[:index]
		prerelease := text[index+1:]
		if prerelease == "" {
			return parsed, fmt.Errorf("empty prerelease in %q", raw)
		}
		parsed.pre = strings.Split(prerelease, ".")
		for _, identifier := range parsed.pre {
			if identifier == "" {
				return parsed, fmt.Errorf("empty prerelease identifier in %q", raw)
			}
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) > 3 {
		return parsed, fmt.Errorf("too many version components in %q", raw)
	}
	numbers := [3]int{}
	for index, part := range parts {
		if part == "" {
			return parsed, fmt.Errorf("empty version component in %q", raw)
		}
		value := 0
		for _, character := range part {
			if character < '0' || character > '9' {
				return parsed, fmt.Errorf("non-numeric version component %q in %q", part, raw)
			}
			value = value*10 + int(character-'0')
		}
		numbers[index] = value
	}
	parsed.major, parsed.minor, parsed.patch = numbers[0], numbers[1], numbers[2]
	return parsed, nil
}

func compareVersion(left, right version) int {
	if comparison := compareInt(left.major, right.major); comparison != 0 {
		return comparison
	}
	if comparison := compareInt(left.minor, right.minor); comparison != 0 {
		return comparison
	}
	if comparison := compareInt(left.patch, right.patch); comparison != 0 {
		return comparison
	}
	switch {
	case len(left.pre) == 0 && len(right.pre) == 0:
		return 0
	case len(left.pre) == 0:
		return 1
	case len(right.pre) == 0:
		return -1
	}
	for index := 0; index < len(left.pre) && index < len(right.pre); index++ {
		if comparison := comparePrereleaseID(left.pre[index], right.pre[index]); comparison != 0 {
			return comparison
		}
	}
	return compareInt(len(left.pre), len(right.pre))
}

func comparePrereleaseID(left, right string) int {
	leftNumber, leftNumeric := numericID(left)
	rightNumber, rightNumeric := numericID(right)
	switch {
	case leftNumeric && rightNumeric:
		return compareInt(leftNumber, rightNumber)
	case leftNumeric:
		return -1
	case rightNumeric:
		return 1
	default:
		return strings.Compare(left, right)
	}
}

func numericID(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	number := 0
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, false
		}
		number = number*10 + int(character-'0')
	}
	return number, true
}

func compareInt(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
