package gateway

// Migrated from internal/regression/acp_session_test.go (backend-only, 256
// lines) before S4 deletes internal/regression.
//
// Behaviour kept: one ACP round over the real stdio frame loop — newline-framed
// JSON-RPC 2.0, initialize, session/new, session/prompt, the streamed
// session/update notifications and the final response carrying a stop reason.

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

// acpFrame is one decoded JSON-RPC frame from the ACP loop.
type acpFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Params json.RawMessage `json:"params"`
}

// acpRequest renders one request line.
func acpRequest(id int, method, params string) string {
	if params == "" {
		params = "{}"
	}
	return `{"jsonrpc":"2.0","id":` + itoa(id) + `,"method":"` + method + `","params":` + params + `}`
}

// itoa renders a small non-negative id.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

// runACP feeds the given frames through the real ACP Serve loop.
func runACP(t *testing.T, rig *e2eRig, lines ...string) []acpFrame {
	t.Helper()
	service := requireValue(t, rig.root, contracts.KeyACP)
	input := strings.Join(lines, "\n") + "\n"
	var out bytes.Buffer
	if err := service.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	decoder := json.NewDecoder(&out)
	frames := make([]acpFrame, 0, len(lines)*2)
	for decoder.More() {
		var frame acpFrame
		if err := decoder.Decode(&frame); err != nil {
			t.Fatalf("decode frame: %v; out = %s", err, out.String())
		}
		frames = append(frames, frame)
	}
	return frames
}

// responseFor returns the response frame with the given id.
func responseFor(t *testing.T, frames []acpFrame, id string) acpFrame {
	t.Helper()
	for _, frame := range frames {
		if frame.Method != "" {
			continue
		}
		if strings.TrimSpace(string(frame.ID)) == id {
			if frame.Error != nil {
				t.Fatalf("request %s failed: %d %s", id, frame.Error.Code, frame.Error.Message)
			}
			return frame
		}
	}
	t.Fatalf("no response for id %s (frames: %+v)", id, frames)
	return acpFrame{}
}

// TestE2EACPSessionRound is the migrated ACP regression: one full conversation
// round through the stdio loop with the real agent backend.
func TestE2EACPSessionRound(t *testing.T) {
	rig := newE2ERig(t, func(body []byte, _ int) (int, string) {
		return 0, sseAnswer("ACP answered the prompt")
	}, e2eOptions{activateACP: true})

	frames := runACP(t, rig,
		acpRequest(1, acp.MethodInitialize, `{"protocolVersion":1,"clientCapabilities":{}}`),
		acpRequest(2, acp.MethodSessionNew, `{"cwd":"/tmp","mcpServers":[]}`),
	)

	// initialize advertises the loadSession capability the editor checks.
	var initResult map[string]any
	if err := json.Unmarshal(responseFor(t, frames, "1").Result, &initResult); err != nil {
		t.Fatalf("decode initialize: %v", err)
	}
	if initResult["agentCapabilities"] == nil {
		t.Fatalf("initialize result has no agentCapabilities: %v", initResult)
	}

	var newResult map[string]any
	if err := json.Unmarshal(responseFor(t, frames, "2").Result, &newResult); err != nil {
		t.Fatalf("decode session/new: %v", err)
	}
	sessionID, _ := newResult["sessionId"].(string)
	if sessionID == "" {
		t.Fatalf("session/new returned no sessionId: %v", newResult)
	}
	// The session really exists in the backend the browser also reads.
	if _, ok := rig.chat.Session(sessionID); !ok {
		t.Fatalf("session/new created %q but the backend does not know it", sessionID)
	}

	// One prompt round over the same loop: the streamed update plus the final
	// stop reason. The fake model answers immediately, so this terminates.
	round := runACP(t, rig,
		acpRequest(1, acp.MethodInitialize, `{"protocolVersion":1,"clientCapabilities":{}}`),
		acpRequest(3, acp.MethodSessionPrompt, `{"sessionId":"`+sessionID+`","prompt":[{"type":"text","text":"hello from the acp round"}]}`),
	)
	promptResponse := responseFor(t, round, "3")
	var promptResult map[string]any
	if err := json.Unmarshal(promptResponse.Result, &promptResult); err != nil {
		t.Fatalf("decode session/prompt: %v", err)
	}
	if reason, _ := promptResult["stopReason"].(string); reason == "" {
		t.Fatalf("session/prompt returned no stopReason: %v", promptResult)
	}
	if !containsUpdate(round) {
		t.Fatalf("no session/update notification was emitted (frames: %+v)", round)
	}

	// The turn landed in the durable conversation: the user prompt and the
	// assistant answer the scripted model produced.
	waitFor(t, "the ACP turn to be stored", func() bool {
		session, ok := rig.chat.Session(sessionID)
		if !ok {
			return false
		}
		user, assistant := false, false
		for _, message := range session.Messages {
			if message.Role == contracts.ChatRoleUser && strings.Contains(message.Text, "hello from the acp round") {
				user = true
			}
			if message.Role == contracts.ChatRoleAssistant && strings.Contains(message.Text, "ACP answered the prompt") {
				assistant = true
			}
		}
		return user && assistant
	})
	_ = time.Now()
}

// containsUpdate reports whether any frame is a session/update notification.
func containsUpdate(frames []acpFrame) bool {
	for _, frame := range frames {
		if frame.Method == acp.MethodSessionUpdate {
			return true
		}
	}
	return false
}

// TestE2EACPBadParamsStayStructured keeps the deleted negative case: a malformed
// session/prompt answers a JSON-RPC error instead of a panic or a silent hang.
func TestE2EACPBadParamsStayStructured(t *testing.T) {
	rig := newE2ERig(t, nil, e2eOptions{activateACP: true})
	frames := runACP(t, rig,
		acpRequest(1, acp.MethodInitialize, `{"protocolVersion":1,"clientCapabilities":{}}`),
		acpRequest(7, acp.MethodSessionPrompt, `{"sessionId":"does-not-exist","prompt":[{"type":"text","text":"hi"}]}`),
	)
	failed := false
	for _, frame := range frames {
		if frame.Method == "" && strings.TrimSpace(string(frame.ID)) == "7" && frame.Error != nil {
			failed = true
			if frame.Error.Code == 0 {
				t.Fatalf("the error frame has no code: %+v", frame.Error)
			}
		}
	}
	if !failed {
		t.Fatalf("an unknown session id did not produce an error frame: %+v", frames)
	}
}
