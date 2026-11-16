package contracts

import "testing"

func TestNormalizeMCPURL(t *testing.T) {
	cases := map[string]string{
		"https://api.githubcopilot.com/mcp/": "https://api.githubcopilot.com/mcp",
		"https://api.githubcopilot.com/mcp":  "https://api.githubcopilot.com/mcp",
		"https://api.githubcopilot.com/":     "https://api.githubcopilot.com/",
		"":                                   "",
		"not a url":                          "not a url",
	}
	for input, want := range cases {
		if got := NormalizeMCPURL(input); got != want {
			t.Fatalf("NormalizeMCPURL(%q) = %q, want %q", input, got, want)
		}
	}
	row := NormalizeMCPAuthURL(MCPServerConfig{
		URL:  "https://mcp.example.com/mcp/",
		Auth: &MCPAuthConfig{Mode: MCPAuthOAuth2},
	})
	if row.URL != "https://mcp.example.com/mcp" {
		t.Fatalf("OAuth row URL = %q", row.URL)
	}
	bearer := NormalizeMCPAuthURL(MCPServerConfig{
		URL:  "https://mcp.example.com/mcp/",
		Auth: &MCPAuthConfig{Mode: MCPAuthBearer, Token: "t"},
	})
	if bearer.URL != "https://mcp.example.com/mcp/" {
		t.Fatalf("non-OAuth URL must be preserved, got %q", bearer.URL)
	}
}
