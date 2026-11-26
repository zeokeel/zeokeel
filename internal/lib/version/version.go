// Package version compares semver-ish product versions. It lives in the shared
// library layer because both the update module and the process host need it
// (2026-11 audit AR-22: the host must not import a backend module for a pure
// function).
//
// Ordering is delegated to golang.org/x/mod/semver (semver 2.0.0 precedence:
// MAJOR.MINOR.PATCH, then prerelease identifiers — numeric ones by value,
// alphanumeric ones lexically, numeric before alphanumeric, a shorter set
// before a longer one that shares its prefix — with build metadata ignored and
// a prerelease sorting before its release). This package is only the adapter
// between that implementation and the project's conventions:
//
//   - x/mod/semver requires a "v" prefix. Release tags carry one ("v0.4.1")
//     while the VERSION file does not ("0.4.1"), so Parse accepts both, plus
//     the uppercase "V" the previous hand-written parser tolerated.
//   - A missing minor/patch is read as 0 (x/mod/semver already accepts vMAJOR
//     and vMAJOR.MINOR as shorthands), so "1.2" == "1.2.0".
//   - x/mod/semver compares an invalid string as *less than* a valid one and
//     returns 0 when both are invalid, so feeding it unvalidated input would
//     silently turn "the version is garbage" into "the versions are equal".
//     Every entry point here validates with semver.IsValid first and returns an
//     error instead, so a caller that ignores the error cannot mistake a
//     malformed version for an equal one.
package version

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// Version is one parsed semver-ish version.
//
// The fields the hand-written parser exposed (major/minor/patch int, pre
// []string) were unexported and unreadable outside this package, so nothing
// outside loses access by their removal: x/mod/semver is now the single source
// of truth for both parsing and ordering, and the mapping is
// major/minor/patch -> the padded "vMAJOR.MINOR.PATCH" core of form,
// pre -> its "-PRERELEASE" suffix, build metadata -> dropped (it never affects
// precedence).
type Version struct {
	// form is the x/mod/semver canonical form: "v" prefixed, minor and patch
	// padded with zeros when absent, build metadata stripped.
	form string
}

// CompareVersions compares two semver-ish versions: -1 when a < b, 0 when
// equal, +1 when a > b. Invalid input returns an error (never a silent 0).
func CompareVersions(a, b string) (int, error) {
	left, err := Parse(a)
	if err != nil {
		return 0, err
	}
	right, err := Parse(b)
	if err != nil {
		return 0, err
	}
	return semver.Compare(left.form, right.form), nil
}

// Parse parses MAJOR[.MINOR[.PATCH]][-prerelease][+build] and returns an error
// for anything x/mod/semver rejects (the semver 2.0.0 grammar: numeric
// components without leading zeros, non-empty dot-separated prerelease
// identifiers). A leading "v" (the tag convention of the release pipeline) is
// accepted and ignored, so the running binary's -ldflags version (v1.2.3)
// compares like 1.2.3. Callers that only validate a version string can discard
// the returned Version.
func Parse(raw string) (Version, error) {
	form, err := canonicalForm(raw)
	if err != nil {
		return Version{}, err
	}
	return Version{form: form}, nil
}

// canonicalForm normalises a project version string into the shape
// golang.org/x/mod/semver accepts, or explains why it cannot.
func canonicalForm(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", errors.New("empty version")
	}
	// The "v" tag prefix is optional in project input and mandatory for
	// x/mod/semver; the previous parser also accepted an uppercase "V".
	if text[0] == 'v' || text[0] == 'V' {
		text = text[1:]
	}
	tagged := "v" + text
	// IsValid is the guard against semver.Compare's "invalid compares as 0"
	// behaviour: without it a malformed version would look equal to any other
	// malformed version and lower than every valid one.
	if !semver.IsValid(tagged) {
		return "", fmt.Errorf("invalid version %q", raw)
	}
	// Canonical pads a missing minor/patch and drops build metadata, so the
	// stored form is comparable as well as printable.
	return semver.Canonical(tagged), nil
}
