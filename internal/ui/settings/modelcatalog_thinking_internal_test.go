package settings

// Unit test for US-07 (2026-11 wave D, model catalog editor side): the
// default-thinking select offers a SINGLE default expression (跟随系列默认, the
// empty override) — the redundant "默认" mode option that meant the same thing
// is dropped, so the user is not faced with two near-synonymous defaults.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestCatalogDefaultThinkingOptionsSingleDefault pins the dedup for a
// DeepSeek-family model (default / low / medium / high modes).
func TestCatalogDefaultThinkingOptionsSingleDefault(t *testing.T) {
	modes := contracts.BuiltinThinkingModes(contracts.ModelFamilyDeepSeek)
	labels, ids := catalogDefaultThinkingOptions(sandboxLocaleStub{lang: "zh"}, modes)
	if len(labels) == 0 || labels[0] != "跟随系列默认" || ids[0] != "" {
		t.Fatalf("first option = %q/%q, want the single 跟随系列默认 empty override", labels[0], ids[0])
	}
	for index, id := range ids {
		if id == contracts.ThinkingModeDefault {
			t.Fatalf("US-07: the redundant near-synonymous 默认 option %q is still offered", labels[index])
		}
	}
	// The strength levels stay reachable.
	for _, want := range []string{"低强度", "中强度", "高强度"} {
		found := false
		for _, label := range labels {
			if label == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("strength level %q missing from options %v", want, labels)
		}
	}
}
