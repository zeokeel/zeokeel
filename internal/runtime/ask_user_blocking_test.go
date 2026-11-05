//go:build tdd_red

package runtime_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/runtime"
	"github.com/zeokeel/zeokeel/internal/tools"
)

// FP-16 TDD (RED): ask_user must block inside the turn.
//
// Today tools.AskUserTool.Invoke returns immediately with
// {"status":"pending"}: the question only becomes a card from the tool_end
// result, the turn ends, and the user's answer arrives as the NEXT user turn.
// The next round adds a runtime QuestionGate seam: the turn wrapper publishes the
// question card first, then the tool blocks until the answer arrives (or the
// question times out and degrades to pending), and the answer is returned as the
// SAME tool call's result so the model continues in the same turn.
//
// This file is red today: the seam does not exist and the tool returns at once.
// It must go green (or be rewritten against the real seam) when FP-16 lands.

// TestAskUserBlockingSeamExists pins the planned seams on both sides: the runtime
// Config must carry a QuestionGate (implemented by the app/controller to publish
// the card and wait for the answer), and the ask_user tool must accept a
// question channel delivered through its invocation context.
func TestAskUserBlockingSeamExists(t *testing.T) {
	if _, ok := reflect.TypeOf(runtime.Config{}).FieldByName("QuestionGate"); !ok {
		t.Fatalf("RED (FP-16): runtime.Config has no QuestionGate seam; ask_user is fire-and-forget and cannot block inside a turn")
	}
	toolType := reflect.TypeOf(tools.NewAskUserTool())
	_, hasSetter := toolType.MethodByName("SetQuestionChannel")
	_, hasInvoke := toolType.MethodByName("Invoke")
	if !hasSetter || !hasInvoke {
		t.Fatalf("RED (FP-16): AskUserTool has no question-channel seam (SetQuestionChannel=%v Invoke=%v); the tool cannot publish a card and block for an answer", hasSetter, hasInvoke)
	}
}

// TestAskUserBlocksWithinTurn drives one real agent turn whose model calls
// ask_user, then delivers an answer through the planned question channel and
// expects the answer to come back as the same tool call's result.
//
// Without the seam the tool returns "pending" immediately, so the turn cannot
// observe the answer; the test fails with the RED message below.
func TestAskUserBlocksWithinTurn(t *testing.T) {
	// The tool invocation is the observable unit of the missing behavior: with
	// the seam, running it must not complete before the user answers.
	tool := tools.NewAskUserTool()
	args, err := json.Marshal(map[string]string{"question": "Which database should I target?"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	out, invokeErr := tool.Invoke(context.Background(), args)
	elapsed := time.Since(start)
	if invokeErr != nil {
		t.Fatalf("ask_user: %v", invokeErr)
	}
	if elapsed < 100*time.Millisecond {
		t.Fatalf("RED (FP-16): ask_user returned after %v with %s; it must block for the user's answer (published card + answer channel) instead of ending the turn with a pending question", elapsed, strings.TrimSpace(out))
	}
	if !strings.Contains(out, `"status":"answered"`) {
		t.Fatalf("RED (FP-16): ask_user result does not carry the answer: %s", strings.TrimSpace(out))
	}
}
