package gateway

// session.preset.get / session.preset.set: presence on the wire, the answer
// shape, parameter validation and the typed-error mapping.
//
// The enforcement half (the role's allowlist vetoing a tool call) lives in
// internal/modules/agent/preset_role_test.go; this file protects the seam — the
// methods are really dispatched, the contract classifies them, and "cleared"
// (no role) is distinguishable from "the call failed".

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakePresetChat is a chat backend plus the optional SessionPresetSetter half.
type fakePresetChat struct {
	*fakeChat
	mu     sync.Mutex
	preset string
	err    error
}

func (f *fakePresetChat) SetSessionPreset(sessionID, presetID string) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.preset = strings.TrimSpace(presetID)
	return nil
}

func (f *fakePresetChat) SessionPreset(sessionID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.preset
}

func newPresetGateway(t *testing.T, chat contracts.ChatService) *Gateway {
	t.Helper()
	root := testRoot(t, chat)
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return gateway
}

// TestSessionPresetIsDispatchedAndDeclared: both halves are in the dispatch
// table, both are classified in contractParams (the generator derives
// contract.json from those), and the wire contract keeps sessionId required
// while presetId stays optional (omitting it is how a client clears the role).
func TestSessionPresetIsDispatchedAndDeclared(t *testing.T) {
	for _, method := range []string{"session.preset.get", "session.preset.set"} {
		if !slices.Contains(SupportedMethods(), method) {
			t.Fatalf("%s is not dispatched", method)
		}
		if _, ok := contractParams[method]; !ok {
			t.Fatalf("%s is dispatched but not classified in contractParams", method)
		}
	}
	spec, err := paramSpecOf(sessionPresetParams{})
	if err != nil {
		t.Fatalf("paramSpecOf: %v", err)
	}
	if len(spec.Required) != 1 || spec.Required[0] != "sessionId" {
		t.Fatalf("required = %v, want [sessionId]", spec.Required)
	}
	if len(spec.Optional) != 1 || spec.Optional[0] != "presetId" {
		t.Fatalf("optional = %v, want [presetId]", spec.Optional)
	}
}

func TestSessionPresetRoundTrip(t *testing.T) {
	chat := &fakePresetChat{fakeChat: newFakeChat()}
	chat.addSession("s1", "role", "")
	gw := newPresetGateway(t, chat)
	ctx := context.Background()

	out, problem := gw.setSessionPreset(ctx, bindTaskParams(t, map[string]any{
		"sessionId": " s1 ", "presetId": " lead ",
	}))
	if problem != nil {
		t.Fatalf("set: %+v", problem)
	}
	if encoded, err := json.Marshal(out); err != nil || string(encoded) != `{"presetId":"lead"}` {
		t.Fatalf("set answer = %s (%v), want {\"presetId\":\"lead\"}", encoded, err)
	}

	// The read answers with the selection in effect, not with the request.
	out, problem = gw.getSessionPreset(ctx, bindTaskParams(t, map[string]any{"sessionId": "s1"}))
	if problem != nil {
		t.Fatalf("get: %+v", problem)
	}
	if encoded, err := json.Marshal(out); err != nil || string(encoded) != `{"presetId":"lead"}` {
		t.Fatalf("get answer = %s (%v), want {\"presetId\":\"lead\"}", encoded, err)
	}

	// Omitting presetId clears the role, and the answer says so explicitly.
	out, problem = gw.setSessionPreset(ctx, bindTaskParams(t, map[string]any{"sessionId": "s1"}))
	if problem != nil {
		t.Fatalf("clear: %+v", problem)
	}
	if encoded, err := json.Marshal(out); err != nil || string(encoded) != `{"presetId":""}` {
		t.Fatalf("clear answer = %s (%v), want {\"presetId\":\"\"}", encoded, err)
	}
}

func TestSessionPresetValidatesSessionID(t *testing.T) {
	chat := &fakePresetChat{fakeChat: newFakeChat()}
	gw := newPresetGateway(t, chat)
	ctx := context.Background()

	for _, params := range []map[string]any{
		{"presetId": "lead"}, // sessionId missing
		{"sessionId": "   "}, // sessionId blank
	} {
		for name, call := range map[string]func(context.Context, json.RawMessage) (any, *protocolError){
			"get": gw.getSessionPreset,
			"set": gw.setSessionPreset,
		} {
			_, problem := call(ctx, bindTaskParams(t, params))
			if problem == nil || problem.Code != codeInvalidParams {
				t.Fatalf("%s params %v problem = %+v, want codeInvalidParams", name, params, problem)
			}
			if problem.Data["field"] != "sessionId" {
				t.Fatalf("%s params %v field = %v, want sessionId", name, params, problem.Data["field"])
			}
		}
	}
	if chat.preset != "" {
		t.Fatalf("a rejected request wrote a preset: %q", chat.preset)
	}
}

func TestSessionPresetMapsTypedErrors(t *testing.T) {
	chat := &fakePresetChat{fakeChat: newFakeChat(), err: contracts.ErrSessionPresetInvalid}
	gw := newPresetGateway(t, chat)

	_, problem := gw.setSessionPreset(context.Background(), bindTaskParams(t, map[string]any{
		"sessionId": "s1", "presetId": "nope",
	}))
	if problem == nil || problem.Code != codeInvalidRequest ||
		!strings.Contains(problem.Message, "session preset") {
		t.Fatalf("problem = %+v, want codeInvalidRequest with the typed text", problem)
	}
}

// TestSessionPresetWithoutSetter: a product whose chat backend has no preset
// half reports the method as unavailable instead of silently dropping the call.
func TestSessionPresetWithoutSetter(t *testing.T) {
	gw := newPresetGateway(t, newFakeChat())
	for name, call := range map[string]func(context.Context, json.RawMessage) (any, *protocolError){
		"get": gw.getSessionPreset,
		"set": gw.setSessionPreset,
	} {
		_, problem := call(context.Background(), bindTaskParams(t, map[string]any{"sessionId": "s1"}))
		if problem == nil || problem.Code != codeInternalError {
			t.Fatalf("%s problem = %+v, want codeInternalError", name, problem)
		}
		if problem.Data["name"] != "preset_unavailable" {
			t.Fatalf("%s problem name = %v, want preset_unavailable", name, problem.Data["name"])
		}
	}
}
