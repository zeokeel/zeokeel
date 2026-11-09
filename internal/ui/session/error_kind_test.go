package session_test

// Tests for localized assistant error rows by ErrorKind.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func errorKindChat(kind, text string) *fakeChat {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "错误分类",
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "问题"},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "回答", Error: text, ErrorKind: kind},
		},
	}, true)
	return chat
}

func TestAssistantErrorKindHeadings(t *testing.T) {
	tests := []struct {
		kind     string
		original string
		want     string
	}{
		{"auth", "HTTP 401 Unauthorized", "鉴权失败：请检查 API Key"},
		{"rate_limit", "429 rate limit exceeded", "请求过于频繁：请稍后重试"},
		{"network", "dial tcp: connection refused", "网络连接失败"},
		{"timeout", "context deadline exceeded", "请求超时"},
		{"context", "maximum context length exceeded", "上下文超限：请压缩或新建会话"},
		{"cancelled", "context canceled", "已取消"},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			h := newHarness(t, errorKindChat(test.kind, test.original))
			tester := h.tester()
			h.assertTexts(tester, test.want)
			if test.kind == "cancelled" {
				// A user stop is not an error: the raw English cancellation text
				// must not be repeated under the neutral 已取消 row.
				h.assertNoTexts(tester, test.original)
				return
			}
			h.assertTexts(tester, test.original)
		})
	}
}

func TestAssistantErrorUnknownKindKeepsOriginal(t *testing.T) {
	for _, kind := range []string{"", "other", "unknown"} {
		h := newHarness(t, errorKindChat(kind, "raw diagnostic error"))
		tester := h.tester()
		h.assertTexts(tester, "raw diagnostic error")
		h.assertNoTexts(tester, "鉴权失败", "请求过于频繁", "网络连接失败", "请求超时", "上下文超限", "已取消")
	}
}

func TestAssistantErrorKindEnglish(t *testing.T) {
	h := newHarness(t, errorKindChat("auth", "HTTP 401 Unauthorized"))
	h.ui.Locale().SetLanguage("en")
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "Authentication failed: check the API key", "HTTP 401 Unauthorized")
}
