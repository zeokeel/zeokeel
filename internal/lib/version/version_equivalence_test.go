package version

// Equivalence tests for the 2026-11 swap of the hand-written semver parser for
// golang.org/x/mod/semver. The implementation is a pure replacement, so the
// evidence has to be a differential test: legacyCompare below is a frozen copy
// of the code that shipped before the swap, and every pair in the corpus must
// produce the same ordering from both implementations. Cases where x/mod/semver
// is deliberately stricter than the old parser (semver 2.0.0 grammar
// violations) are pinned separately in TestStricterThanLegacyParser.

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/mod/semver"
)

// TestSemverPrecedenceRules covers the comparison rules the product depends on:
// zero-padded numeric segments, prerelease ordering, numeric-before-alphanumeric
// prerelease identifiers, the shorter-identifier-set rule, an optional "v"
// prefix and ignored build metadata.
func TestSemverPrecedenceRules(t *testing.T) {
	cases := []struct {
		name, left, right string
		want              int
	}{
		{"numeric segments are numbers, not strings", "0.4.1", "0.4.10", -1},
		{"numeric segments reversed", "0.4.10", "0.4.1", 1},
		{"prerelease sorts before its release", "1.0.0", "1.0.1-rc.1", -1},
		{"prerelease sorts before its release reversed", "1.0.1-rc.1", "1.0.0", 1},
		{"numeric prerelease identifier before alphanumeric", "1.0.0-1", "1.0.0-alpha", -1},
		{"numeric prerelease identifier before alphanumeric reversed", "1.0.0-alpha", "1.0.0-1", 1},
		{"numeric prerelease identifiers compare by value", "1.0.0-rc.2", "1.0.0-rc.10", -1},
		{"fewer identifiers sort first when prefixes match", "1.0.0", "1.0.0-alpha", 1},
		{"prerelease vs release needs no leading zero", "1.0.0-alpha", "1.0.0", -1},
		{"v prefix is ignored", "v0.4.1", "0.4.1", 0},
		{"v prefix ignored on both sides", "v0.4.1", "v0.4.10", -1},
		{"uppercase V prefix is accepted", "V0.4.1", "v0.4.1", 0},
		{"surrounding space is ignored", " 0.4.1 ", "0.4.1", 0},
		{"build metadata is ignored", "1.0.0+build", "1.0.0", 0},
		{"build metadata ignored with prerelease", "1.0.0-rc.1+build.5", "1.0.0-rc.1", 0},
		{"build metadata ignored while comparing", "1.0.0+build", "1.0.1", -1},
		{"missing segments are zero", "1.2", "1.2.0", 0},
		{"missing patch is zero", "1", "1.0.0", 0},
		{"REAL prod case: VERSION 0.4.1 vs next 0.4.2", "0.4.1", "0.4.2", -1},
	}
	for _, testCase := range cases {
		got, err := CompareVersions(testCase.left, testCase.right)
		if err != nil {
			t.Fatalf("%s: CompareVersions(%q, %q): %v", testCase.name, testCase.left, testCase.right, err)
		}
		if got != testCase.want {
			t.Fatalf("%s: CompareVersions(%q, %q) = %d, want %d", testCase.name, testCase.left, testCase.right, got, testCase.want)
		}
	}
}

// TestSideBySideOldVsNew prints both implementations' verdict for every
// required case and fails on the first disagreement. Run with -v for the table;
// it is the readable evidence that the swap did not change any ordering.
func TestSideBySideOldVsNew(t *testing.T) {
	required := [][2]string{
		{"0.4.1", "0.4.10"},
		{"1.0.0", "1.0.1-rc.1"},
		{"1.0.0-1", "1.0.0-alpha"},        // numeric prerelease identifier vs alphanumeric
		{"1.0.0-beta.2", "1.0.0-beta.11"}, // numeric prerelease identifiers by value
		{"1.0.0", "1.0.0-alpha"},          // fewer identifiers / prerelease vs release
		{"v0.4.1", "0.4.10"},              // "v" prefix on one side only
		{"V0.4.1", "v0.4.10"},             // uppercase "V" prefix
		{"1.0.0+build", "1.0.0"},          // build metadata ignored
		{"1.0.0-rc.1+build.5", "1.0.0-rc.1"},
		{"1.2", "1.2.0"}, // missing segments
		{"1", "1.0.0"},
	}
	for _, pair := range required {
		oldValue, oldErr := legacyCompareVersions(pair[0], pair[1])
		newValue, newErr := CompareVersions(pair[0], pair[1])
		t.Logf("old %-22q vs %-18q -> %2d (err=%v) | new -> %2d (err=%v)", pair[0], pair[1], oldValue, oldErr, newValue, newErr)
		if oldErr != nil || newErr != nil {
			t.Fatalf("%q vs %q: old err=%v, new err=%v", pair[0], pair[1], oldErr, newErr)
		}
		if sign(oldValue) != sign(newValue) {
			t.Fatalf("%q vs %q: old says %d, new says %d", pair[0], pair[1], oldValue, newValue)
		}
	}
}

// TestSemverSpecChain walks the precedence chain from the semver 2.0.0 spec in
// order; every neighbouring pair must compare -1.
func TestSemverSpecChain(t *testing.T) {
	chain := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
	}
	for index := 0; index < len(chain)-1; index++ {
		got, err := CompareVersions(chain[index], chain[index+1])
		if err != nil {
			t.Fatalf("CompareVersions(%q, %q): %v", chain[index], chain[index+1], err)
		}
		if got != -1 {
			t.Fatalf("CompareVersions(%q, %q) = %d, want -1", chain[index], chain[index+1], got)
		}
	}
}

// TestCompareVersionsRejectsInvalidInsteadOfComparingEqual pins requirement 4:
// an invalid version must be an error, never the 0 that semver.Compare returns
// for invalid input ("all invalid semantic versions compare equal").
func TestCompareVersionsRejectsInvalidInsteadOfComparingEqual(t *testing.T) {
	invalid := []string{
		"",               // empty
		"   ",            // whitespace only
		"x",              // not a version
		"dev",            // the plain-build appVersion the host passes in
		"1.x",            // non-numeric component
		"1.2.3.4",        // too many components
		"1..2",           // empty component
		"1.2.0-",         // empty prerelease
		"-1.0.0",         // signed component
		"1.2.0-alpha..1", // empty prerelease identifier
		"v",              // prefix only
		"V",
		"1.2.3-x_y",      // "_" is not an identifier character
		"1.2.3-01",       // numeric prerelease identifier with a leading zero
		"01.2.3",         // leading zero in a numeric component
		"1.2.3+build..1", // empty build identifier
	}
	for _, value := range invalid {
		pairs := [][2]string{
			{value, "1.0.0"}, // invalid on the left
			{"1.0.0", value}, // invalid on the right
			{value, value},   // the silent-equality trap: semver.Compare would say 0
		}
		for _, pair := range pairs {
			got, err := CompareVersions(pair[0], pair[1])
			if err == nil {
				t.Fatalf("CompareVersions(%q, %q) = %d, nil error: an invalid version must not be compared", pair[0], pair[1], got)
			}
			if got != 0 {
				t.Fatalf("CompareVersions(%q, %q) = %d on error, want 0", pair[0], pair[1], got)
			}
			if _, err := Parse(value); err == nil {
				t.Fatalf("Parse(%q) accepted an invalid version", value)
			}
			t.Logf("rejected %-16q -> %v", pair[0]+" vs "+pair[1], err)
		}
	}
}

// TestSemverCompareWouldSilentlyEqualInvalidInput proves the IsValid guard is
// load-bearing rather than defensive noise: the upstream call really does report
// two malformed versions as equal.
func TestSemverCompareWouldSilentlyEqualInvalidInput(t *testing.T) {
	for _, value := range []string{"v1.x", "vdev", "v1.2.3-01", "v01.2.3"} {
		if got := semver.Compare(value, value); got != 0 {
			t.Fatalf("semver.Compare(%q, %q) = %d; the premise of the guard changed", value, value, got)
		}
		if semver.IsValid(value) {
			t.Fatalf("semver.IsValid(%q) = true; want the value to be invalid", value)
		}
	}
}

// TestStricterThanLegacyParser documents the only behavioural difference from
// the hand-written parser: x/mod/semver enforces the semver 2.0.0 grammar, so
// strings the old parser read leniently (leading zeros, "_" in identifiers) are
// now rejected. No release tag uses them, and the host treats an unparsable
// version conservatively (it keeps the pending plan), so an error is the safer
// answer than an invented ordering.
func TestStricterThanLegacyParser(t *testing.T) {
	specViolations := []string{"1.02.3", "1.2.03", "01.2.3", "1.0.0-01", "1.0.0-x_y"}
	for _, value := range specViolations {
		if _, err := legacyParse(value); err != nil {
			t.Fatalf("legacyParse(%q) = %v; the frozen reference no longer accepts it, so the divergence is mislabelled", value, err)
		}
		if _, err := Parse(value); err == nil {
			t.Fatalf("Parse(%q) accepted a semver 2.0.0 grammar violation", value)
		}
	}
}

// TestLegacyEquivalence compares the adapter against the frozen copy of the
// pre-swap implementation over a corpus that crosses every ordering rule:
// numeric segments, missing segments, prefixes, prerelease shapes and build
// metadata. It also checks antisymmetry, which the old implementation satisfied
// and callers depend on.
func TestLegacyEquivalence(t *testing.T) {
	corpus := []string{
		"0.4.0", "0.4.1", "0.4.2", "0.4.9", "0.4.10", "0.4.11",
		"1", "1.0", "1.0.0", "1.0.1", "1.2", "1.2.0", "1.2.1", "1.3.0", "2.0.0", "10.0.0",
		"v0.4.1", "V0.4.1", "v1.0.0", " 1.2.3 ",
		"1.0.0-0", "1.0.0-1", "1.0.0-2", "1.0.0-11",
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-alpha-1",
		"1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0-rc.10",
		"1.0.0-x", "1.0.0-zzz", "1.0.0+build", "1.0.0+build.5",
		"1.0.0-rc.1+build.5", "0.4.1+build.9", "1.0.1-rc.1",
	}
	for _, left := range corpus {
		for _, right := range corpus {
			got, err := CompareVersions(left, right)
			if err != nil {
				t.Fatalf("CompareVersions(%q, %q): %v", left, right, err)
			}
			want, wantErr := legacyCompareVersions(left, right)
			if wantErr != nil {
				t.Fatalf("legacyCompareVersions(%q, %q): %v", left, right, wantErr)
			}
			if sign(got) != sign(want) {
				t.Fatalf("CompareVersions(%q, %q) = %d, legacy implementation says %d", left, right, got, want)
			}
			// Compare must stay antisymmetric: callers negate it.
			reverse, err := CompareVersions(right, left)
			if err != nil {
				t.Fatalf("CompareVersions(%q, %q): %v", right, left, err)
			}
			if reverse != -got {
				t.Fatalf("CompareVersions(%q, %q) = %d but CompareVersions(%q, %q) = %d", left, right, got, right, left, reverse)
			}
		}
	}
}

func sign(value int) int {
	switch {
	case value < 0:
		return -1
	case value > 0:
		return 1
	default:
		return 0
	}
}

// --- Frozen reference: the hand-written implementation removed by the swap. ---
//
// This is a verbatim copy of the pre-swap internal/lib/version logic (Parse,
// compareVersion, comparePrereleaseID, numericID, compareInt) with a legacy
// prefix. It exists only as the oracle for TestLegacyEquivalence and
// TestStricterThanLegacyParser; do not modernise it.

type legacyVersion struct {
	major, minor, patch int
	pre                 []string
}

func legacyCompareVersions(a, b string) (int, error) {
	left, err := legacyParse(a)
	if err != nil {
		return 0, err
	}
	right, err := legacyParse(b)
	if err != nil {
		return 0, err
	}
	return legacyCompareVersion(left, right), nil
}

func legacyParse(raw string) (legacyVersion, error) {
	var parsed legacyVersion
	text := strings.TrimSpace(raw)
	if text == "" {
		return parsed, fmt.Errorf("empty version")
	}
	if len(text) > 1 && (text[0] == 'v' || text[0] == 'V') {
		text = text[1:]
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

func legacyCompareVersion(left, right legacyVersion) int {
	if comparison := legacyCompareInt(left.major, right.major); comparison != 0 {
		return comparison
	}
	if comparison := legacyCompareInt(left.minor, right.minor); comparison != 0 {
		return comparison
	}
	if comparison := legacyCompareInt(left.patch, right.patch); comparison != 0 {
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
		if comparison := legacyComparePrereleaseID(left.pre[index], right.pre[index]); comparison != 0 {
			return comparison
		}
	}
	return legacyCompareInt(len(left.pre), len(right.pre))
}

func legacyComparePrereleaseID(left, right string) int {
	leftNumber, leftNumeric := legacyNumericID(left)
	rightNumber, rightNumeric := legacyNumericID(right)
	switch {
	case leftNumeric && rightNumeric:
		return legacyCompareInt(leftNumber, rightNumber)
	case leftNumeric:
		return -1
	case rightNumeric:
		return 1
	default:
		return strings.Compare(left, right)
	}
}

func legacyNumericID(value string) (int, bool) {
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

func legacyCompareInt(left, right int) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
