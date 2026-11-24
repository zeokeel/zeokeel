package agent

import (
	"strings"
	"testing"
)

// The identity line is the first thing every run tells the model about itself, so
// a stale product form there is a product bug, not a wording nit: asked "are you a
// desktop app or a browser?", a run of v0.3.2 answered "I am the native desktop
// application of ZeoKeel (not a browser environment)" — because this line still
// described the retired desktop UI (removed in the S4 migration; the product is now
// a gateway process plus the browser client).
func TestAgentIdentityDescribesTheBrowserProduct(t *testing.T) {
	identity := agentIdentity
	if strings.Contains(strings.ToLower(identity), "desktop") {
		t.Fatalf("identity still claims a desktop app: %q", identity)
	}
	if !strings.Contains(strings.ToLower(identity), "browser") {
		t.Fatalf("identity does not tell the model it runs behind a browser client: %q", identity)
	}
	// The name and the "coding agent" framing are still true and must survive.
	if !strings.Contains(identity, "ZeoKeel") {
		t.Fatalf("identity dropped the product name: %q", identity)
	}
	if !strings.Contains(strings.ToLower(identity), "coding") {
		t.Fatalf("identity dropped the coding-agent framing: %q", identity)
	}
}

// The guidance must not tell the model to look at a window, a device, or a UI it
// does not have: the browser owns the rendering.
func TestIdentityGuidanceNamesNoDesktopOnlySurface(t *testing.T) {
	for _, banned := range []string{"desktop app", "the app window", "native window"} {
		if strings.Contains(strings.ToLower(agentIdentity), banned) {
			t.Fatalf("identity names a desktop-only surface (%q): %q", banned, agentIdentity)
		}
	}
}
