package session_test

// 2026-11 UI review C: a restored attachment chip is isomorphic to the hot one —
// file name + size — instead of a bare "图片" placeholder. The render side reads
// contracts.Attachment.Size(), which prefers live Data and falls back to the
// persisted Bytes, so a restored image (decoded data) and a restored file (Bytes
// only) both show their size. The attachment metadata never renders as body.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

func TestRestoredAttachmentChipShowsNameAndSize(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "附件恢复",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "看看这张图",
				Attachments: []contracts.Attachment{
					// Restored image: bytes decoded from the persisted data URL.
					{ID: "a1", Name: "zeokeel.png", MIME: "image/png", Data: make([]byte, 70)},
					// Restored file: Data not loaded, size kept in Bytes.
					{ID: "a2", Name: "notes.txt", MIME: "text/plain", Bytes: 100},
				}},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	h.assertTexts(tester, "zeokeel.png", "70 B", "notes.txt", "100 B")
	// The prompt body renders; the "[附件] …" metadata never echoes into it.
	h.assertTexts(tester, "看看这张图")
	h.assertNoTexts(tester, "[附件]")
}

func TestRestoredOldImageChipFallsBackWithoutPanic(t *testing.T) {
	// An old record restored without a metadata part keeps the generic name but
	// still shows its decoded size; the chip builds without panic.
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "旧附件",
		Messages: []contracts.ChatMessage{
			{ID: "u1", Role: contracts.ChatRoleUser, Text: "旧图",
				Attachments: []contracts.Attachment{
					{ID: "a1", Name: "图片", MIME: "image/png", Data: make([]byte, 70)},
				}},
		},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()
	h.assertTexts(tester, "图片", "70 B")
}
