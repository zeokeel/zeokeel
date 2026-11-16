package contracts

import (
	"reflect"
	"testing"
)

// fakeCatalog is a rig-free ToolCatalog implementation: the narrow interface
// must be satisfiable without importing any rig package (AR-06).
type fakeCatalog struct{ names []string }

func (c fakeCatalog) Names() []string { return append([]string(nil), c.names...) }
func (c fakeCatalog) Len() int        { return len(c.names) }

func TestToolCatalogNarrowSurface(t *testing.T) {
	var catalog ToolCatalog = fakeCatalog{names: []string{"bash", "read_file"}}
	if got := catalog.Names(); !reflect.DeepEqual(got, []string{"bash", "read_file"}) {
		t.Fatalf("Names() = %v, want the registered names", got)
	}
	if catalog.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", catalog.Len())
	}
	// The returned names are a copy: a consumer cannot mutate the catalog.
	got := catalog.Names()
	got[0] = "mutated"
	if catalog.Names()[0] != "bash" {
		t.Fatal("Names() shares its backing slice with the catalog")
	}
}

func TestKeyToolCatalogIdentity(t *testing.T) {
	// The new key is additive: the legacy KeyToolkit stays for the B/D-wave
	// consumer switch (E2 removes it last).
	if KeyToolCatalog.Name() != "zeokeel/toolcatalog" {
		t.Fatalf("KeyToolCatalog name = %q, want zeokeel/toolcatalog", KeyToolCatalog.Name())
	}
	if KeyToolkit.Name() != "zeokeel/toolkit" {
		t.Fatalf("KeyToolkit name = %q, want the legacy key preserved", KeyToolkit.Name())
	}
}

func TestNormalizeApprovalTimeoutSeconds(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"", 0},
		{"0", 0},
		{"30", 30},
		{"3600", 3600},
		{"-5", 0},
		{"abc", 0},
		{"99999999", 0},
	}
	for _, tc := range cases {
		if got := NormalizeApprovalTimeoutSeconds(tc.raw); got != tc.want {
			t.Fatalf("NormalizeApprovalTimeoutSeconds(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}
