package gateway

// `bots.template.put` / `bots.template.get`: the write half of the settings
// screen, which until now could only look at the seeded templates.
//
// The seam these tests protect:
//   - the DTO the screen edits is the same one bots.snapshot already ships, so
//     an edit round-trips (snapshot → modify → put → snapshot);
//   - a rejected form is `-32600` with the validation text, not `-32603`;
//   - an unknown id answers `{template:null}`, because the form stays open;
//   - editing a template does NOT reshape a bot that already exists. That last
//     one is a permission rule, not a nicety: a bot's tool grant is what it may
//     touch, and it must not grow because someone retuned the template.
//
// These run against the real bot store (not a fake) on purpose: the rule being
// tested lives in the module's create-time resolution, so a fake would only ever
// agree with itself.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/bot"
)

// newTemplateGateway wires the real bot store into a gateway so the handlers run
// against the module's validation, resolution and persistence.
func newTemplateGateway(t *testing.T) (*Gateway, contracts.BotService) {
	t.Helper()
	root := testRoot(t, newFakeChat())
	service := bot.NewService()
	if _, err := root.Provide(contracts.KeyBots, contracts.BotService(service)); err != nil {
		t.Fatalf("provide bots: %v", err)
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return gateway, service
}

// templateParams marshals one request body. It rides on the shared mustJSON
// helper so a marshal failure reports the same way everywhere.
func templateParams(t *testing.T, value any) json.RawMessage {
	t.Helper()
	return json.RawMessage(mustJSON(t, value))
}

// TestBotsTemplatePutCreatesThenReplaces pins create-or-replace: the first put
// stores the whole record, the second one replaces it (an omitted field is
// cleared, not inherited).
func TestBotsTemplatePutCreatesThenReplaces(t *testing.T) {
	gateway, service := newTemplateGateway(t)
	ctx := context.Background()

	created, problem := gateway.botsTemplatePut(ctx, templateParams(t, map[string]any{
		"id":           "researcher",
		"name":         "研究 Bot",
		"role":         "Researcher",
		"description":  "查资料",
		"defaultModel": "deepseek-v4",
		"systemPrompt": "when to use ...",
		"allowedTools": []string{"fs.read", "web_search"},
		"memoryScope":  "team",
	}))
	if problem != nil {
		t.Fatalf("bots.template.put(create): %+v", problem)
	}
	dto, ok := created.(botTemplateDTO)
	if !ok {
		t.Fatalf("put returned %T, want botTemplateDTO", created)
	}
	if dto.ID != "researcher" || dto.Name != "研究 Bot" || dto.Role != "Researcher" || dto.Description != "查资料" {
		t.Fatalf("dto identity = %+v", dto)
	}
	if dto.Model != "deepseek-v4" || dto.Prompt != "when to use ..." || dto.MemoryScope != "team" {
		t.Fatalf("dto policy = %+v", dto)
	}
	if got, want := strings.Join(dto.Tools, ","), "fs.read,web_search"; got != want {
		t.Fatalf("dto tools = %q, want %q", got, want)
	}
	// The write really landed in the store, not just in the answer.
	stored, found, err := service.Template(ctx, "researcher")
	if err != nil || !found {
		t.Fatalf("Template(researcher) = %v, %v; want the stored template", found, err)
	}
	if stored.SystemPrompt != "when to use ..." || len(stored.AllowedTools) != 2 {
		t.Fatalf("stored template = %+v, want the request's fields", stored)
	}

	// Replace: only the two required fields, so the rest is cleared.
	replaced, problem := gateway.botsTemplatePut(ctx, templateParams(t, map[string]any{
		"id": "researcher", "name": "研究 Bot v2",
	}))
	if problem != nil {
		t.Fatalf("bots.template.put(replace): %+v", problem)
	}
	replacedDTO, ok := replaced.(botTemplateDTO)
	if !ok {
		t.Fatalf("replace returned %T, want botTemplateDTO", replaced)
	}
	if replacedDTO.Name != "研究 Bot v2" {
		t.Fatalf("replaced name = %q", replacedDTO.Name)
	}
	if replacedDTO.Prompt != "" || replacedDTO.Model != "" || replacedDTO.Description != "" || len(replacedDTO.Tools) != 0 {
		t.Fatalf("replace kept fields the caller omitted: %+v", replacedDTO)
	}
	if replacedDTO.MemoryScope != contracts.BotMemoryScopeAgent {
		t.Fatalf("replace scope = %q, want the agent default", replacedDTO.MemoryScope)
	}
}

// TestBotsTemplatePutValidationIsAClientError: a malformed form must come back as
// -32600 with the reason, never as "internal error". The table mirrors the
// module's rules, so a budget that moves in the module cannot leave the wire
// saying "something broke".
func TestBotsTemplatePutValidationIsAClientError(t *testing.T) {
	gateway, service := newTemplateGateway(t)
	ctx := context.Background()
	manyTools := make([]string, 33)
	for i := range manyTools {
		manyTools[i] = fmt.Sprintf("tool_%d", i)
	}

	cases := []struct {
		name   string
		params map[string]any
	}{
		{"no id", map[string]any{"name": "n"}},
		{"id outside alphabet", map[string]any{"id": "Bot_1", "name": "n"}},
		{"id too short", map[string]any{"id": "a", "name": "n"}},
		{"no name", map[string]any{"id": "ok-id"}},
		{"name too long", map[string]any{"id": "ok-id", "name": strings.Repeat("名", 81)}},
		{"unknown memory scope", map[string]any{"id": "ok-id", "name": "n", "memoryScope": "global"}},
		{"tool outside alphabet", map[string]any{"id": "ok-id", "name": "n", "allowedTools": []string{"Bash"}}},
		{"too many tools", map[string]any{"id": "ok-id", "name": "n", "allowedTools": manyTools}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, problem := gateway.botsTemplatePut(ctx, templateParams(t, testCase.params))
			if problem == nil {
				t.Fatal("a malformed template was accepted")
			}
			if problem.Code != codeInvalidRequest {
				t.Fatalf("problem = %+v, want -32600 (a form error, not a gateway fault)", problem)
			}
			if !strings.Contains(problem.Message, "bot:") {
				t.Fatalf("problem message = %q, want the typed bot error text", problem.Message)
			}
			// A rejected form must not leave a partial record behind.
			if _, found, _ := service.Template(ctx, "ok-id"); found {
				t.Fatal("a rejected put stored a template")
			}
		})
	}

	// A frame the struct cannot decode is invalid params, one step earlier.
	_, problem := gateway.botsTemplatePut(ctx, json.RawMessage(`{"id":5}`))
	if problem == nil || problem.Code != codeInvalidParams {
		t.Fatalf("undecodable params problem = %+v, want -32602", problem)
	}
}

// TestBotsTemplateGetReturnsAndUnknownIsNull pins both halves of the read: the
// happy path ships the same DTO the screen edits, and an unknown id is
// `{template:null}` (an empty form) rather than an error.
func TestBotsTemplateGetReturnsAndUnknownIsNull(t *testing.T) {
	gateway, _ := newTemplateGateway(t)
	ctx := context.Background()

	if _, problem := gateway.botsTemplatePut(ctx, templateParams(t, map[string]any{
		"id":           "researcher",
		"name":         "研究 Bot",
		"role":         "Researcher",
		"description":  "查资料",
		"defaultModel": "deepseek-v4",
		"systemPrompt": "when to use ...",
		"allowedTools": []string{"fs.read"},
		"memoryScope":  "team",
	})); problem != nil {
		t.Fatalf("put: %+v", problem)
	}

	answer, problem := gateway.botsTemplateGet(ctx, templateParams(t, map[string]any{"id": "researcher"}))
	if problem != nil {
		t.Fatalf("bots.template.get: %+v", problem)
	}
	result, ok := answer.(botTemplateGetResult)
	if !ok || result.Template == nil {
		t.Fatalf("get returned %#v, want a non-nil template", answer)
	}
	if result.Template.ID != "researcher" || result.Template.Prompt != "when to use ..." {
		t.Fatalf("template = %+v, want the stored record", result.Template)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if !strings.Contains(string(encoded), `"template":{`) {
		t.Fatalf("get wire shape = %s, want a `template` object", encoded)
	}

	unknown, problem := gateway.botsTemplateGet(ctx, templateParams(t, map[string]any{"id": "ghost"}))
	if problem != nil {
		t.Fatalf("bots.template.get(unknown) = %+v, want an answer (an empty form), not an error", problem)
	}
	unknownResult, ok := unknown.(botTemplateGetResult)
	if !ok || unknownResult.Template != nil {
		t.Fatalf("unknown get = %#v, want {template:null}", unknown)
	}
	encoded, err = json.Marshal(unknown)
	if err != nil {
		t.Fatalf("marshal unknown result: %v", err)
	}
	if got, want := string(encoded), `{"template":null}`; got != want {
		t.Fatalf("unknown wire shape = %s, want %s", got, want)
	}
}

// TestBotsTemplateEditDoesNotReshapeExistingInstances is the honesty requirement
// at the wire seam: retuning a template through the settings screen leaves
// already-created bots on the policy they were created with, and only bots
// created afterwards see the new one.
func TestBotsTemplateEditDoesNotReshapeExistingInstances(t *testing.T) {
	gateway, service := newTemplateGateway(t)
	ctx := context.Background()

	if _, problem := gateway.botsTemplatePut(ctx, templateParams(t, map[string]any{
		"id": "researcher", "name": "研究 Bot",
		"defaultModel": "deepseek-v4", "allowedTools": []string{"fs.read"}, "memoryScope": "team",
	})); problem != nil {
		t.Fatalf("put template: %+v", problem)
	}
	if _, problem := gateway.botsUpsert(ctx, templateParams(t, map[string]any{
		"id": "bot-1", "templateId": "researcher",
	})); problem != nil {
		t.Fatalf("bots.upsert: %+v", problem)
	}

	// The edit a user makes on the settings screen: a wider grant, a new model.
	if _, problem := gateway.botsTemplatePut(ctx, templateParams(t, map[string]any{
		"id": "researcher", "name": "研究 Bot v2", "systemPrompt": "new prompt",
		"defaultModel": "deepseek-v5", "allowedTools": []string{"fs.read", "fs.write", "shell"},
		"memoryScope": "agent",
	})); problem != nil {
		t.Fatalf("put template(edit): %+v", problem)
	}

	answer, problem := gateway.botsSnapshot(ctx, nil)
	if problem != nil {
		t.Fatalf("bots.snapshot: %+v", problem)
	}
	snapshot, ok := answer.(botsSnapshotResult)
	if !ok {
		t.Fatalf("snapshot returned %T", answer)
	}
	if len(snapshot.Instances) != 1 {
		t.Fatalf("instances = %+v, want the one bot", snapshot.Instances)
	}
	existing := snapshot.Instances[0]
	if existing.Model != "deepseek-v4" {
		t.Fatalf("instance model = %q, want the snapshotted deepseek-v4 (a template edit does not retro-apply)", existing.Model)
	}
	if got, want := strings.Join(existing.Tools, ","), "fs.read"; got != want {
		t.Fatalf("instance tools = %q, want %q (no grant growth behind the user's back)", got, want)
	}
	if existing.MemoryScope != contracts.BotMemoryScopeTeam {
		t.Fatalf("instance scope = %q, want the snapshotted team", existing.MemoryScope)
	}

	// A bot created after the edit does see the new policy.
	if _, problem := gateway.botsUpsert(ctx, templateParams(t, map[string]any{
		"id": "bot-2", "templateId": "researcher",
	})); problem != nil {
		t.Fatalf("bots.upsert(after edit): %+v", problem)
	}
	second, err := service.Get(ctx, "bot-2")
	if err != nil {
		t.Fatalf("Get(bot-2): %v", err)
	}
	if second.DefaultModel != "deepseek-v5" || second.MemoryScope != contracts.BotMemoryScopeAgent {
		t.Fatalf("new bot = %+v, want the retuned policy", second)
	}
	if got, want := strings.Join(second.AllowedTools, ","), "fs.read,fs.write,shell"; got != want {
		t.Fatalf("new bot tools = %q, want %q", got, want)
	}
}

// TestBotsTemplatePutAnswerIsTheTemplateDTO pins the answer shape the web client
// will read: the put returns the template itself (not a wrapper), with the same
// field names bots.snapshot uses.
func TestBotsTemplatePutAnswerIsTheTemplateDTO(t *testing.T) {
	gateway, _ := newTemplateGateway(t)
	ctx := context.Background()

	answer, problem := gateway.botsTemplatePut(ctx, templateParams(t, map[string]any{
		"id": "researcher", "name": "研究 Bot", "allowedTools": []string{"fs.read"},
	}))
	if problem != nil {
		t.Fatalf("put: %+v", problem)
	}
	encoded, err := json.Marshal(answer)
	if err != nil {
		t.Fatalf("marshal answer: %v", err)
	}
	// `defaultModel` is omitted when empty (empty means "follow the session"),
	// which is why the whole shape is pinned here rather than field by field.
	want := `{"id":"researcher","name":"研究 Bot","allowedTools":["fs.read"],"memoryScope":"agent"}`
	if string(encoded) != want {
		t.Fatalf("put answer = %s\nwant       = %s", encoded, want)
	}
}
