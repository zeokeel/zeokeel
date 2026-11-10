package session_test

// Frame test for DSH-style header title truncation: a long conversation title
// must not eat the band; it ellipsizes at the DSH crumb width so the agent
// entries and host utilities keep their room.

import (
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/design"
)

func TestHeaderLongTitleIsTruncatedLikeDSH(t *testing.T) {
	title := strings.Repeat("超长会话标题", 24)
	h := newAgentSurfaceHarness(t, headerChat(title), agentServices{
		team: &fakeTeam{members: []contracts.TeamMember{
			{ID: "member-1", Name: "文档审查员", RunState: contracts.TeamRunRunning},
		}},
	})
	tester := h.tester()
	tester.Frame()

	titleRect, ok := tester.Find(title)
	if !ok {
		t.Fatalf("header title missing; texts = %v", tester.Texts())
	}
	// DSH caps one breadcrumb at 220 CSS px; the token expression below is the
	// same 7 * 32 DIP cap (a 1 DIP slack keeps sub-pixel layout honest).
	if max := design.Space8 * 7; titleRect.W > max+1 {
		t.Fatalf("title width = %.1f, want <= %.1f (DSH crumb cap)", titleRect.W, max)
	}
	// The agent entries stay present and to the right of the truncated title.
	teamRect, ok := tester.Find("智能体团队")
	if !ok {
		t.Fatalf("team entry disappeared after the title truncation; texts = %v", tester.Texts())
	}
	if teamRect.X <= titleRect.X {
		t.Fatalf("team entry x=%.1f is not right of the title x=%.1f", teamRect.X, titleRect.X)
	}
}
