package session_test

// UX-05 (2026-11 audit, DESIGN rule 9): editing a message back into the composer
// or clicking a hero example must not silently overwrite a non-empty draft —
// a ConfirmDanger asks first (Cancel is the Default), and cancelling keeps the
// half-written text.

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestEditMessageOverNonEmptyDraftAsksConfirmation is the decisive UX-05
// assertion: with a half-written draft, 编辑消息 opens the overwrite
// confirmation; Cancel keeps the draft; confirming replaces it.
func TestEditMessageOverNonEmptyDraftAsksConfirmation(t *testing.T) {
	const original = "原始问题：检查登录流程"
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "草稿保护",
		Messages: []contracts.ChatMessage{message("u1", contracts.ChatRoleUser, original)},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	// A half-written draft in the composer.
	focusComposer(t, tester)
	tester.Type("我写了一半的草稿")
	tester.Frame()

	h.click(tester, "用户消息操作")
	h.click(tester, "编辑消息")
	tester.Frame()

	// The confirmation asks before overwriting (the modal owns the frame, so
	// the draft itself is verified after the decision).
	h.assertTexts(tester, "覆盖当前草稿？", "取消", "覆盖草稿")

	// Cancel (the Default) keeps the draft and closes the dialog.
	h.click(tester, "取消")
	tester.Frame()
	h.assertNoTexts(tester, "覆盖当前草稿？")
	info, ok := tester.Access("输入消息…")
	if !ok || info.Value != "我写了一半的草稿" {
		t.Fatalf("cancel lost the draft: %+v found %v", info, ok)
	}

	// Confirming replaces the draft with the edited message.
	h.click(tester, "用户消息操作")
	h.click(tester, "编辑消息")
	tester.Frame()
	h.click(tester, "覆盖草稿")
	tester.Frame()
	info, ok = tester.Access("输入消息…")
	if !ok || info.Value != original {
		t.Fatalf("confirmed overwrite did not apply: %+v found %v", info, ok)
	}
	if sends := chat.sends(); len(sends) != 0 {
		t.Fatalf("edit auto-sent a message: %v", sends)
	}
}

// TestEditMessageOverEmptyDraftAppliesDirectly is the control: an empty draft
// needs no confirmation (the existing one-click edit stays frictionless).
func TestEditMessageOverEmptyDraftAppliesDirectly(t *testing.T) {
	const original = "原始问题"
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{
		ID: "sess-1", Title: "空草稿",
		Messages: []contracts.ChatMessage{message("u1", contracts.ChatRoleUser, original)},
	}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	h.click(tester, "用户消息操作")
	h.click(tester, "编辑消息")
	tester.Frame()
	h.assertNoTexts(tester, "覆盖当前草稿？")
	info, ok := tester.Access("输入消息…")
	if !ok || info.Value != original {
		t.Fatalf("empty-draft edit did not apply directly: %+v found %v", info, ok)
	}
}

// TestHeroExampleOverNonEmptyDraftAsksConfirmation covers the hero path: an
// example prompt must not silently replace a draft either.
func TestHeroExampleOverNonEmptyDraftAsksConfirmation(t *testing.T) {
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "sess-1", Title: "示例保护"}, true)
	h := newHarness(t, chat)
	tester := h.tester()
	tester.Frame()

	focusComposer(t, tester)
	tester.Type("我的草稿")
	tester.Frame()

	prompt := "检查当前工作区并给出摘要"
	h.click(tester, prompt)
	tester.Frame()
	h.assertTexts(tester, "覆盖当前草稿？")
	h.click(tester, "取消")
	tester.Frame()
	info, ok := tester.Access("输入消息…")
	if !ok || info.Value != "我的草稿" {
		t.Fatalf("cancel lost the draft: %+v found %v", info, ok)
	}
}
