package host

import "testing"

// TestFrameworkLocale pins the product-language → mygo-locale mapping the
// desktop host applies at startup and on every runtime language switch
// (fix round SH-06): Chinese is mygo's zh-Hans catalog, everything else the
// framework's English strings.
func TestFrameworkLocale(t *testing.T) {
	cases := map[string]string{
		"zh":      "zh-Hans",
		"ZH":      "zh-Hans",
		" zh ":    "zh-Hans",
		"zh-Hans": "zh-Hans",
		"zh-CN":   "zh-Hans",
		"zh_TW":   "zh-Hans",
		"en":      "en",
		"EN":      "en",
		"en-US":   "en",
		"":        "en",
		"ja":      "en",
	}
	for in, want := range cases {
		if got := frameworkLocale(in); got != want {
			t.Errorf("frameworkLocale(%q) = %q, want %q", in, got, want)
		}
	}
}
