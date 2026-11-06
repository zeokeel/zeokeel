package memory

import (
	"strings"
	"testing"
)

func TestScrubSecretsDetectsAndRedacts(t *testing.T) {
	privateKey := "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA1234567890abcdef\nabcdefghijklmnopqrstuvwxyz\n-----END RSA PRIVATE KEY-----"
	cases := []struct {
		name       string
		in         string
		wantFound  bool
		wantKeep   []string
		wantAbsent []string
		wantExact  string
	}{
		{
			name:       "sk api key",
			in:         "use sk-abcdefghijklmnopqrstuvwxyz123456 for the API",
			wantFound:  true,
			wantKeep:   []string{"use ", " for the API"},
			wantAbsent: []string{"abcdefghijklmnopqrstuvwxyz123456"},
		},
		{
			name:       "aws access key",
			in:         "aws access key AKIAIOSFODNN7EXAMPLE is in the env",
			wantFound:  true,
			wantAbsent: []string{"AKIAIOSFODNN7EXAMPLE"},
		},
		{
			name:       "bearer token keeps scheme",
			in:         "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
			wantFound:  true,
			wantKeep:   []string{"Bearer"},
			wantAbsent: []string{"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"},
		},
		{
			name:       "api key assignment",
			in:         `api_key = "supersecretvalue123"`,
			wantFound:  true,
			wantKeep:   []string{"api_key"},
			wantAbsent: []string{"supersecretvalue123"},
		},
		{
			name:       "password assignment",
			in:         "password: hunter2hunter2",
			wantFound:  true,
			wantKeep:   []string{"password"},
			wantAbsent: []string{"hunter2hunter2"},
		},
		{
			name:       "token assignment",
			in:         "token=ghp_abcdefghijklmnopqrstuvwxyz",
			wantFound:  true,
			wantKeep:   []string{"token"},
			wantAbsent: []string{"ghp_abcdefghijklmnopqrstuvwxyz"},
		},
		{
			name:       "private key block",
			in:         "here is the key:\n" + privateKey + "\ndone",
			wantFound:  true,
			wantKeep:   []string{"here is the key:", "done"},
			wantAbsent: []string{"MIIEowIBAAKCAQEA", "BEGIN RSA PRIVATE KEY"},
		},
		{
			name:      "clean prose",
			in:        "The token budget is 10k per request.",
			wantFound: false,
			wantExact: "The token budget is 10k per request.",
		},
		{
			name:      "clean discussion",
			in:        "We discussed password policy and secret handling in ADR 0012.",
			wantFound: false,
			wantExact: "We discussed password policy and secret handling in ADR 0012.",
		},
		{
			name:      "ordinary hyphen words",
			in:        "task-manager and desk-lamp are ordinary words",
			wantFound: false,
			wantExact: "task-manager and desk-lamp are ordinary words",
		},
		{
			name:      "bearer without token",
			in:        "The Bearer scheme is documented.",
			wantFound: false,
			wantExact: "The Bearer scheme is documented.",
		},
		{
			name:      "empty comparison",
			in:        `if apiKey == "" { return }`,
			wantFound: false,
			wantExact: `if apiKey == "" { return }`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, found := ScrubSecrets(tc.in)
			if found != tc.wantFound {
				t.Fatalf("ScrubSecrets(%q) found = %v, want %v (out %q)", tc.in, found, tc.wantFound, got)
			}
			if tc.wantExact != "" && got != tc.wantExact {
				t.Fatalf("ScrubSecrets(%q) = %q, want unchanged %q", tc.in, got, tc.wantExact)
			}
			for _, keep := range tc.wantKeep {
				if !strings.Contains(got, keep) {
					t.Errorf("redacted text lost %q: %q", keep, got)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("redacted text still contains secret %q: %q", absent, got)
				}
			}
			// Redaction is idempotent: scrubbing the redacted text finds nothing.
			again, againFound := ScrubSecrets(got)
			if againFound {
				t.Errorf("second ScrubSecrets found a secret: %q -> %q", got, again)
			}
			if again != got {
				t.Errorf("ScrubSecrets is not idempotent: %q -> %q", got, again)
			}
		})
	}
}

func TestScrubSecretsMultipleSecrets(t *testing.T) {
	in := "key=sk-abcdefghijklmnopqrstuvwxyz123456 and password: hunter2hunter2"
	out, found := ScrubSecrets(in)
	if !found {
		t.Fatal("multiple secrets were not detected")
	}
	for _, secret := range []string{"sk-abcdefghijklmnopqrstuvwxyz123456", "hunter2hunter2"} {
		if strings.Contains(out, secret) {
			t.Errorf("redacted output still contains %q: %q", secret, out)
		}
	}
}
