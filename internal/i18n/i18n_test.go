package i18n

import (
	"strings"
	"testing"
)

// TestCatalogComplete pins the extracted catalog: every key has both texts and
// the surface is large enough to be the real UI catalog, not a stub.
func TestCatalogComplete(t *testing.T) {
	catalog := Catalog()
	if len(catalog) < 300 {
		t.Fatalf("catalog has %d keys, want the full UI surface (>=300)", len(catalog))
	}
	for key, entry := range catalog {
		if strings.TrimSpace(entry.EN) == "" {
			t.Errorf("%s: empty English text", key)
		}
		if strings.TrimSpace(entry.ZH) == "" {
			t.Errorf("%s: empty Chinese text", key)
		}
	}
	for _, key := range []string{
		"settings.settings", "shell.new_session", "session.chat",
		"automation.automation_tasks", "composer.approval_required",
	} {
		if !Has(key) {
			t.Errorf("Has(%q) = false, want true", key)
		}
		if _, ok := catalog[key]; !ok {
			t.Errorf("Catalog missing fixed key %q", key)
		}
	}
}

// TestTFormatting pins language selection, formatting and the key fallback.
func TestTFormatting(t *testing.T) {
	if got := T(LangEN, "home.tagline"); got != "What will you build today?" {
		t.Fatalf("T(en, home.tagline) = %q", got)
	}
	if got := T(LangZH, "home.tagline"); got != "今天想构建什么？" {
		t.Fatalf("T(zh, home.tagline) = %q", got)
	}
	if got := T(LangEN, "automation.every_n_minutes", 3); !strings.Contains(got, "3") {
		t.Fatalf("T with args = %q", got)
	}
	if got := T(LangZH, "automation.every_n_minutes", 3); !strings.Contains(got, "3") {
		t.Fatalf("T(zh) with args = %q", got)
	}
	// Unknown keys fall back to the key itself, as the shell always did.
	if got := T(LangZH, "no.such.key"); got != "no.such.key" {
		t.Fatalf("T(unknown) = %q", got)
	}
	if Has("no.such.key") {
		t.Fatal("Has(unknown) = true")
	}
}

// TestNormalize pins the accepted language codes.
func TestNormalize(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Lang
	}{
		{"en", LangEN},
		{"zh", LangZH},
		{"EN", ""},
		{"zh-CN", ""},
		{"", ""},
		{"fr", ""},
	} {
		if got := Normalize(tc.in); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestDefaultLanguage pins the product default (Chinese) and the test hook.
func TestDefaultLanguage(t *testing.T) {
	if got := DefaultLive(); got != LangZH {
		t.Fatalf("DefaultLive() = %q, want zh", got)
	}
	SetDefaultLanguageForTest(LangEN)
	if got := DefaultLive(); got != LangEN {
		t.Fatalf("DefaultLive() after hook = %q, want en", got)
	}
	SetDefaultLanguageForTest("")
	if got := DefaultLive(); got != LangZH {
		t.Fatalf("DefaultLive() after reset = %q, want zh", got)
	}
}
