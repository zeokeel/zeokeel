package version

// Semver-ish comparison tests, migrated from internal/modules/update when the
// helper moved to the shared library layer (2026-11 audit AR-22): the process
// host and the update module both compare versions, and the host must not
// import a backend module for a pure function.

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		left, right string
		want        int
	}{
		{"1.2.0", "1.2.0", 0},
		{"1.2", "1.2.0", 0},
		{"1", "1.0.0", 0},
		{"1.2.1", "1.2.0", 1},
		{"1.2.0", "1.2.1", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.2.0-rc.1", "1.2.0", -1},
		{"1.2.0", "1.2.0-rc.1", 1},
		{"1.2.0-rc.10", "1.2.0-rc.2", 1},
		{"1.2.0-alpha", "1.2.0-beta", -1},
		{"1.2.0-alpha.1", "1.2.0-alpha", 1},
		{"1.2.0+build.5", "1.2.0", 0},
		{"v1.2.0", "1.2.0", 0},
		{"V1.2.0", "v1.2.0", 0},
	}
	for _, testCase := range cases {
		got, err := CompareVersions(testCase.left, testCase.right)
		if err != nil {
			t.Fatalf("CompareVersions(%q, %q): %v", testCase.left, testCase.right, err)
		}
		if got != testCase.want {
			t.Fatalf("CompareVersions(%q, %q) = %d, want %d", testCase.left, testCase.right, got, testCase.want)
		}
	}
	invalid := []string{"", "x", "1.x", "1.2.3.4", "1.2.0-", "1.2.0-alpha..1", "-1.0.0"}
	for _, value := range invalid {
		if _, err := CompareVersions(value, "1.0.0"); err == nil {
			t.Fatalf("CompareVersions(%q) accepted an invalid version", value)
		}
	}
}

func TestParse(t *testing.T) {
	valid := []string{"1", "1.2", "1.2.3", "v1.2.3", "V1.2.3", "1.2.3-rc.1", "1.2.3+build.5", " 1.2.3 "}
	for _, value := range valid {
		if _, err := Parse(value); err != nil {
			t.Fatalf("Parse(%q): %v", value, err)
		}
	}
	invalid := []string{"", "x", "1.x", "1.2.3.4", "1.2.0-", "1.2.0-alpha..1", "-1.0.0", "1..2"}
	for _, value := range invalid {
		if _, err := Parse(value); err == nil {
			t.Fatalf("Parse(%q) accepted an invalid version", value)
		}
	}
}
