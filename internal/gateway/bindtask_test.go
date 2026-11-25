package gateway

// session.bindtask: presence on the wire, the answer shape, parameter validation
// and the typed-error mapping. The enforcement itself (the agent's tool gate) is
// covered in internal/modules/agent/taskbinding_test.go; what this file protects
// is the seam — the method is really dispatched, the contract classifies it, and
// a client cannot mistake "cleared" for "the call failed".

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fakeBinderChat is a chat backend plus the optional SessionTaskBinder half: one
// binding string and one injectable failure.
type fakeBinderChat struct {
	*fakeChat
	mu     sync.Mutex
	taskID string
	err    error
}

func (f *fakeBinderChat) BindSessionTask(_ context.Context, sessionID, taskID string) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.taskID = strings.TrimSpace(taskID)
	return nil
}

func (f *fakeBinderChat) SessionTask(sessionID string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.taskID, nil
}

func newBindTaskGateway(t *testing.T, chat contracts.ChatService) *Gateway {
	t.Helper()
	root := testRoot(t, chat)
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return gateway
}

func bindTaskParams(t *testing.T, params map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal %v: %v", params, err)
	}
	return raw
}

// TestSessionBindTaskIsDispatchedAndDeclared is the presence half: the method is
// in the dispatch table (SupportedMethods feeds the client), classified in
// contractParams (the generator derives contract.json from it), and its wire
// contract keeps sessionId required while taskId stays optional (omitting it is
// how a client clears the binding).
func TestSessionBindTaskIsDispatchedAndDeclared(t *testing.T) {
	if !slices.Contains(SupportedMethods(), "session.bindtask") {
		t.Fatal("session.bindtask is not dispatched")
	}
	if _, ok := contractParams["session.bindtask"]; !ok {
		t.Fatal("session.bindtask is dispatched but not classified in contractParams")
	}
	spec, err := paramSpecOf(sessionBindTaskParams{})
	if err != nil {
		t.Fatalf("paramSpecOf: %v", err)
	}
	if len(spec.Required) != 1 || spec.Required[0] != "sessionId" {
		t.Fatalf("required = %v, want [sessionId]", spec.Required)
	}
	if len(spec.Optional) != 1 || spec.Optional[0] != "taskId" {
		t.Fatalf("optional = %v, want [taskId]", spec.Optional)
	}
}

func TestSessionBindTaskRoundTrip(t *testing.T) {
	chat := &fakeBinderChat{fakeChat: newFakeChat()}
	chat.addSession("s1", "bound", "")
	gw := newBindTaskGateway(t, chat)
	ctx := context.Background()

	out, problem := gw.bindSessionTask(ctx, bindTaskParams(t, map[string]any{
		"sessionId": " s1 ", "taskId": " task-1 ",
	}))
	if problem != nil {
		t.Fatalf("bind: %+v", problem)
	}
	bound, ok := out.(sessionBindTaskResult)
	if !ok || bound.TaskID != "task-1" {
		t.Fatalf("bind result = (%T) %+v, want task-1", out, out)
	}
	if encoded, err := json.Marshal(out); err != nil || string(encoded) != `{"taskId":"task-1"}` {
		t.Fatalf("bind answer = %s (%v), want {\"taskId\":\"task-1\"}", encoded, err)
	}

	// Omitting taskId clears the binding, and the answer says so explicitly
	// instead of dropping the field ("cleared" must be distinguishable).
	out, problem = gw.bindSessionTask(ctx, bindTaskParams(t, map[string]any{"sessionId": "s1"}))
	if problem != nil {
		t.Fatalf("clear: %+v", problem)
	}
	if encoded, err := json.Marshal(out); err != nil || string(encoded) != `{"taskId":""}` {
		t.Fatalf("clear answer = %s (%v), want {\"taskId\":\"\"}", encoded, err)
	}
}

func TestSessionBindTaskValidatesSessionID(t *testing.T) {
	chat := &fakeBinderChat{fakeChat: newFakeChat()}
	gw := newBindTaskGateway(t, chat)
	ctx := context.Background()

	for _, params := range []map[string]any{
		{"taskId": "task-1"}, // sessionId missing
		{"sessionId": "   "}, // sessionId blank
		{"sessionId": "", "taskId": "task-1"},
	} {
		_, problem := gw.bindSessionTask(ctx, bindTaskParams(t, params))
		if problem == nil || problem.Code != codeInvalidParams {
			t.Fatalf("params %v problem = %+v, want codeInvalidParams", params, problem)
		}
		if problem.Data["field"] != "sessionId" {
			t.Fatalf("params %v field = %v, want sessionId", params, problem.Data["field"])
		}
	}
	if chat.taskID != "" {
		t.Fatalf("a rejected request wrote a binding: %q", chat.taskID)
	}
}

func TestSessionBindTaskMapsTypedErrors(t *testing.T) {
	chat := &fakeBinderChat{fakeChat: newFakeChat(), err: contracts.ErrSessionTaskBindingInvalid}
	gw := newBindTaskGateway(t, chat)

	_, problem := gw.bindSessionTask(context.Background(), bindTaskParams(t, map[string]any{
		"sessionId": "s1", "taskId": "bad task",
	}))
	if problem == nil || problem.Code != codeInvalidRequest ||
		!strings.Contains(problem.Message, "session task binding") {
		t.Fatalf("problem = %+v, want codeInvalidRequest with the typed text", problem)
	}
}

// TestSessionBindTaskWithoutBinder: a product whose chat backend has no binding
// half reports the method as unavailable instead of silently dropping the call.
func TestSessionBindTaskWithoutBinder(t *testing.T) {
	gw := newBindTaskGateway(t, newFakeChat())

	_, problem := gw.bindSessionTask(context.Background(), bindTaskParams(t, map[string]any{
		"sessionId": "s1", "taskId": "task-1",
	}))
	if problem == nil || problem.Code != codeInternalError {
		t.Fatalf("problem = %+v, want codeInternalError", problem)
	}
	if problem.Data["name"] != "taskbinding_unavailable" {
		t.Fatalf("problem name = %v, want taskbinding_unavailable", problem.Data["name"])
	}
}
