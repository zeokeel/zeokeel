package regression

// Adversarial audit: the ACP session/list and session/load JSON-RPC methods,
// driven through the real stdio frame loop (newline-framed JSON-RPC 2.0) with a
// scripted chat service. Negative/boundary cases: newest-updated ordering, empty
// lists, bad params, unknown session ids, the initialize loadSession capability,
// message filtering and "load never changes the active session".

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/acp"
)

// acpWireFrame is one decoded JSON-RPC response frame.
type acpWireFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *acpWireError   `json:"error"`
}

// acpWireError is one JSON-RPC error object.
type acpWireError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// acpFrameLine renders one request frame.
func acpFrameLine(id string, method string, params string) string {
	if params == "" {
		params = "{}"
	}
	return `{"jsonrpc":"2.0","id":` + id + `,"method":"` + method + `","params":` + params + `}`
}

// serveACPLines runs the real ACP Serve loop over the given raw frames.
func serveACPLines(t *testing.T, chat contracts.ChatService, lines ...string) []acpWireFrame {
	t.Helper()
	h := newBareHarness(t)
	provide(t, h.ctx, contracts.KeyChat, chat)
	activate(t, h.ctx, acp.Module(), acp.Config{})
	service := requireService(t, h.ctx, contracts.KeyACP)
	input := strings.Join(lines, "\n") + "\n"
	var out bytes.Buffer
	if err := service.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	decoder := json.NewDecoder(&out)
	frames := make([]acpWireFrame, 0, len(lines))
	for decoder.More() {
		var frame acpWireFrame
		if err := decoder.Decode(&frame); err != nil {
			t.Fatalf("decode response frame: %v; out = %s", err, out.String())
		}
		frames = append(frames, frame)
	}
	return frames
}

// acpInitializeLine is the handshake frame used before the session methods.
func acpInitializeLine(id string) string {
	return acpFrameLine(id, acp.MethodInitialize, `{}`)
}

// acpSeedChat builds the fake chat with deterministic session metadata.
func acpSeedChat(t *testing.T) *fakeChat {
	t.Helper()
	base := time.Unix(1_700_000_000, 0)
	chat := newFakeChat()
	chat.addSession(contracts.ChatSession{ID: "s-old", Title: "Old", Model: "model-old",
		UpdatedAt: base.Add(-2 * time.Hour), CreatedAt: base.Add(-2 * time.Hour)}, false)
	chat.addSession(contracts.ChatSession{ID: "s-new", Title: "Newest", Model: "model-new",
		UpdatedAt: base, CreatedAt: base}, false)
	chat.addSession(contracts.ChatSession{ID: "s-mid", Title: "Middle",
		UpdatedAt: base.Add(-time.Hour), CreatedAt: base.Add(-time.Hour)}, false)
	// addSession prepends, so insert the load target last (order is irrelevant to
	// the server's own sort, and active is set separately).
	chat.addSession(contracts.ChatSession{
		ID: "s-load", Title: "Load target", Model: "model-load", UpdatedAt: base.Add(-time.Minute),
		Messages: []contracts.ChatMessage{
			{ID: "m1", Role: contracts.ChatRoleUser, Text: "  hello  "},
			{ID: "m2", Role: contracts.ChatRoleAssistant, Text: "hi there"},
			{ID: "m3", Role: contracts.ChatRoleTool, Text: "tool output"},
			{ID: "m4", Role: contracts.ChatRoleAssistant, Text: "   "},
			{ID: "m5", Role: contracts.ChatRoleUser, Text: "follow up"},
		},
	}, false)
	if err := chat.SetActiveSession("s-new"); err != nil {
		t.Fatalf("SetActiveSession: %v", err)
	}
	return chat
}

// TestACPSessionListRealFrames covers session/list over real frames: newest
// updated first, empty lists, malformed params and no-id notifications.
func TestACPSessionListRealFrames(t *testing.T) {
	chat := acpSeedChat(t)
	frames := serveACPLines(t, chat,
		acpInitializeLine("1"),
		acpFrameLine("2", acp.MethodSessionList, `{}`),
	)
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want 2 (initialize + list)", len(frames))
	}
	if frames[1].Error != nil {
		t.Fatalf("session/list error: %+v", frames[1].Error)
	}
	var result struct {
		Sessions []struct {
			SessionID string `json:"sessionId"`
			Title     string `json:"title"`
			UpdatedAt string `json:"updatedAt"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(frames[1].Result, &result); err != nil {
		t.Fatalf("decode list result: %v", err)
	}
	order := make([]string, 0, len(result.Sessions))
	for _, session := range result.Sessions {
		order = append(order, session.SessionID)
	}
	if len(order) < 3 || order[0] != "s-new" {
		t.Fatalf("session/list order = %v, want newest updated first (s-new)", order)
	}
	for index := 1; index < len(order); index++ {
		if order[index] == "s-new" {
			t.Fatalf("session/list duplicated or misplaced s-new: %v", order)
		}
	}
	// Titles come from the chat service, and the active session is untouched.
	if result.Sessions[0].Title == "" {
		t.Fatalf("list item has no title: %+v", result.Sessions[0])
	}
	if got := chat.ActiveSession(); got != "s-new" {
		t.Fatalf("session/list changed the active session to %q", got)
	}

	// Empty list: no sessions, no error.
	empty := serveACPLines(t, newFakeChat(),
		acpInitializeLine("1"),
		acpFrameLine("2", acp.MethodSessionList, `{}`),
	)
	if len(empty) != 2 || empty[1].Error != nil {
		t.Fatalf("empty session/list frames = %+v", empty)
	}
	var emptyResult struct {
		Sessions []any `json:"sessions"`
	}
	if err := json.Unmarshal(empty[1].Result, &emptyResult); err != nil {
		t.Fatalf("decode empty list: %v", err)
	}
	if emptyResult.Sessions == nil || len(emptyResult.Sessions) != 0 {
		t.Fatalf("empty session/list = %+v, want an empty array", emptyResult)
	}

	// Malformed params: an array instead of an object is invalid params.
	bad := serveACPLines(t, chat,
		acpInitializeLine("1"),
		acpFrameLine("2", acp.MethodSessionList, `[]`),
	)
	if len(bad) != 2 || bad[1].Error == nil || bad[1].Error.Code != acp.CodeInvalidParams {
		t.Fatalf("session/list malformed params frame = %+v, want -32602", bad)
	}

	// A request without an id is a notification: no response frame.
	notification := `{"jsonrpc":"2.0","method":"session/list","params":{}}`
	silent := serveACPLines(t, chat, acpInitializeLine("1"), notification)
	if len(silent) != 1 {
		t.Fatalf("notification produced %d frames, want 1 (initialize only)", len(silent))
	}
}

// TestACPSessionLoadRealFrames covers session/load: replay filtering/order, the
// initialize loadSession capability, unknown/missing session ids and malformed
// params, and the guarantee that load never changes the active session.
func TestACPSessionLoadRealFrames(t *testing.T) {
	chat := acpSeedChat(t)
	frames := serveACPLines(t, chat,
		acpInitializeLine("1"),
		acpFrameLine("2", acp.MethodSessionLoad, `{"sessionId":"s-load"}`),
	)
	if len(frames) != 2 || frames[1].Error != nil {
		t.Fatalf("session/load frames = %+v", frames)
	}

	var initResult struct {
		AgentCapabilities struct {
			LoadSession bool `json:"loadSession"`
		} `json:"agentCapabilities"`
	}
	if err := json.Unmarshal(frames[0].Result, &initResult); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if !initResult.AgentCapabilities.LoadSession {
		t.Fatalf("initialize does not advertise agentCapabilities.loadSession: %s", frames[0].Result)
	}

	var result struct {
		SessionID string `json:"sessionId"`
		Title     string `json:"title"`
		Model     string `json:"model"`
		Messages  []struct {
			Role string `json:"role"`
			Text string `json:"text"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(frames[1].Result, &result); err != nil {
		t.Fatalf("decode load result: %v", err)
	}
	if result.SessionID != "s-load" || result.Title != "Load target" || result.Model != "model-load" {
		t.Fatalf("load result header = %+v", result)
	}
	roles := make([]string, 0, len(result.Messages))
	for _, message := range result.Messages {
		roles = append(roles, message.Role+":"+message.Text)
	}
	want := []string{"user:hello", "assistant:hi there", "user:follow up"}
	if len(roles) != len(want) {
		t.Fatalf("load messages = %v, want %v (tool/empty skipped, text trimmed)", roles, want)
	}
	for index := range want {
		if roles[index] != want[index] {
			t.Fatalf("load messages = %v, want %v", roles, want)
		}
	}
	if got := chat.ActiveSession(); got != "s-new" {
		t.Fatalf("session/load changed the active session to %q, want s-new", got)
	}

	// Unknown id and missing/blank session ids are invalid params.
	for name, params := range map[string]string{
		"unknown":   `{"sessionId":"nope"}`,
		"missing":   `{}`,
		"blank":     `{"sessionId":"   "}`,
		"malformed": `"not-an-object"`,
	} {
		frames := serveACPLines(t, chat,
			acpInitializeLine("1"),
			acpFrameLine("2", acp.MethodSessionLoad, params),
		)
		if len(frames) != 2 || frames[1].Error == nil || frames[1].Error.Code != acp.CodeInvalidParams {
			t.Fatalf("session/load %s params frame = %+v, want -32602", name, frames)
		}
	}
	if got := chat.ActiveSession(); got != "s-new" {
		t.Fatalf("failed session/load changed the active session to %q", got)
	}
}
