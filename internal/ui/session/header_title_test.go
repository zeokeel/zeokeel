package session_test

// Frame test for header title truncation: a long conversation title must not
// eat the band; it ellipsizes at a bounded width so the agent entries and host
// utilities keep their room. The cap grew from 7 to 14 * 32 DIP in the v0.2.19
// subtraction pass: with zero-value agent entries gone, an ordinary sentence
// no longer needs the 220 px DSH crumb cap, while a pathological title stays
// bounded.

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
	// 14 * 32 DIP bounds a long title while leaving room for the entries; a
	// 1 DIP slack keeps sub-pixel layout honest.
	if max := design.Space8 * 14; titleRect.W > max+1 {
		t.Fatalf("title width = %.1f, want <= %.1f (header title cap)", titleRect.W, max)
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
