package bot

// Template reading and the one honesty rule of the template half: a template is
// editable, and editing it must never reshape a bot that already exists.
//
// Why this needs its own test file: the templates are seeded and shareable, so
// "edit the template" is the natural thing a settings screen offers. If that
// edit reached back into instances, the tool grant (a permission surface) would
// grow behind the user's back — silently, at a distance, and only for bots that
// happened to be created from the retuned template.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestTemplateGetter pins the getter's contract: (template, found, error), with
// an unknown id as an ordinary answer rather than an error, because a get backs
// an "empty form" state.
func TestTemplateGetter(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()

	if _, found, err := service.Template(ctx, "ghost"); err != nil || found {
		t.Fatalf("Template(ghost) = found %v, err %v; want false, nil", found, err)
	}
	if _, found, err := service.Template(ctx, ""); err != nil || found {
		t.Fatalf("Template(\"\") = found %v, err %v; want false, nil", found, err)
	}

	if _, err := service.PutTemplate(ctx, contracts.BotTemplate{
		ID:           "researcher",
		Name:         "研究 Bot",
		Role:         "Researcher",
		Description:  "查资料",
		SystemPrompt: "when to use ...",
		DefaultModel: "deepseek-v4",
		AllowedTools: []string{"fs.read", "web_search"},
		MemoryScope:  contracts.BotMemoryScopeTeam,
	}); err != nil {
		t.Fatalf("PutTemplate: %v", err)
	}

	found, ok, err := service.Template(ctx, " researcher ")
	if err != nil || !ok {
		t.Fatalf("Template(researcher) = ok %v, err %v; want found", ok, err)
	}
	if found.ID != "researcher" || found.Name != "研究 Bot" || found.Role != "Researcher" {
		t.Fatalf("Template = %+v, want the stored identity", found)
	}
	if found.SystemPrompt != "when to use ..." || found.DefaultModel != "deepseek-v4" {
		t.Fatalf("Template = %+v, want the stored prompt and model", found)
	}
	if got, want := strings.Join(found.AllowedTools, ","), "fs.read,web_search"; got != want {
		t.Fatalf("Template tools = %q, want %q", got, want)
	}
	if found.MemoryScope != contracts.BotMemoryScopeTeam {
		t.Fatalf("Template scope = %q, want team", found.MemoryScope)
	}

	// A copy, not the store's slice: mutating the answer must not edit the store.
	found.AllowedTools[0] = "shell"
	again, _, err := service.Template(ctx, "researcher")
	if err != nil {
		t.Fatalf("Template again: %v", err)
	}
	if again.AllowedTools[0] != "fs.read" {
		t.Fatalf("store leaked its tool list through the getter: %v", again.AllowedTools)
	}

	// The built-in template is readable through the same door.
	if builtin, ok, err := service.Template(ctx, LeadTemplateID); err != nil || !ok || builtin.Name == "" {
		t.Fatalf("Template(lead) = %+v, %v, %v; want the seeded template", builtin, ok, err)
	}
}

// TestTemplateGetterHonoursACanceledContext keeps the new entry point in line
// with every other one: a dead context is reported, not ignored.
func TestTemplateGetterHonoursACanceledContext(t *testing.T) {
	service := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := service.Template(ctx, LeadTemplateID); !errors.Is(err, context.Canceled) {
		t.Fatalf("Template error = %v, want context.Canceled", err)
	}
}

// TestPutTemplateIsCreateOrReplace pins the write semantics the gateway's
// `bots.template.put` advertises: a put replaces the whole record, so a field
// the caller omits is cleared rather than kept. A patch-shaped put would let a
// caller change one field while silently keeping an old permission list.
func TestPutTemplateIsCreateOrReplace(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()

	if _, err := service.PutTemplate(ctx, contracts.BotTemplate{
		ID:           "researcher",
		Name:         "研究 Bot",
		Role:         "Researcher",
		Description:  "查资料",
		SystemPrompt: "when to use ...",
		DefaultModel: "deepseek-v4",
		AllowedTools: []string{"fs.read", "web_search"},
		MemoryScope:  contracts.BotMemoryScopeTeam,
	}); err != nil {
		t.Fatalf("PutTemplate(create): %v", err)
	}

	// Replace with the two fields a caller must always send.
	replaced, err := service.PutTemplate(ctx, contracts.BotTemplate{ID: "researcher", Name: "研究 Bot v2"})
	if err != nil {
		t.Fatalf("PutTemplate(replace): %v", err)
	}
	if replaced.Name != "研究 Bot v2" {
		t.Fatalf("replaced name = %q", replaced.Name)
	}
	// Omitted fields are cleared, not inherited from the previous version.
	if replaced.Role != "" || replaced.Description != "" || replaced.SystemPrompt != "" || replaced.DefaultModel != "" {
		t.Fatalf("replace kept fields the caller omitted: %+v", replaced)
	}
	if len(replaced.AllowedTools) != 0 {
		t.Fatalf("replace kept the old grant: %v", replaced.AllowedTools)
	}
	if replaced.MemoryScope != contracts.BotMemoryScopeAgent {
		t.Fatalf("replace scope = %q, want the agent default", replaced.MemoryScope)
	}
}

// TestTemplateEditDoesNotReshapeExistingInstances is the honesty rule of the
// whole feature, in its hardest form: a bot created from the template WITHOUT
// overrides still keeps the policy it was created with when the template is
// retuned. Only bots created after the edit get the new model, tools and scope.
func TestTemplateEditDoesNotReshapeExistingInstances(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()

	if _, err := service.PutTemplate(ctx, contracts.BotTemplate{
		ID:           "researcher",
		Name:         "研究 Bot",
		DefaultModel: "deepseek-v4",
		AllowedTools: []string{"fs.read"},
		MemoryScope:  contracts.BotMemoryScopeTeam,
	}); err != nil {
		t.Fatalf("PutTemplate: %v", err)
	}
	before, err := service.Create(ctx, contracts.BotInstance{ID: "bot-1", TemplateID: "researcher"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if before.DefaultModel != "deepseek-v4" || before.MemoryScope != contracts.BotMemoryScopeTeam || len(before.AllowedTools) != 1 {
		t.Fatalf("created bot = %+v, want the resolved template policy", before)
	}

	// Retune the template: a wider grant, another model, another scope.
	if _, err := service.PutTemplate(ctx, contracts.BotTemplate{
		ID:           "researcher",
		Name:         "研究 Bot v2",
		SystemPrompt: "new prompt",
		DefaultModel: "deepseek-v5",
		AllowedTools: []string{"fs.read", "fs.write", "shell"},
		MemoryScope:  contracts.BotMemoryScopeAgent,
	}); err != nil {
		t.Fatalf("PutTemplate(edit): %v", err)
	}

	stored, err := service.Get(ctx, "bot-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.DefaultModel != "deepseek-v4" {
		t.Fatalf("model = %q, want the snapshotted deepseek-v4 (template edits do not retro-apply)", stored.DefaultModel)
	}
	if got, want := strings.Join(stored.AllowedTools, ","), "fs.read"; got != want {
		t.Fatalf("tools = %q, want the snapshotted %q (no grant growth behind the user's back)", got, want)
	}
	if stored.MemoryScope != contracts.BotMemoryScopeTeam {
		t.Fatalf("scope = %q, want the snapshotted team", stored.MemoryScope)
	}

	// A bot created after the edit does get the new policy: the edit is not a
	// no-op, it only applies forward.
	after, err := service.Create(ctx, contracts.BotInstance{ID: "bot-2", TemplateID: "researcher"})
	if err != nil {
		t.Fatalf("Create(after edit): %v", err)
	}
	if after.DefaultModel != "deepseek-v5" || after.MemoryScope != contracts.BotMemoryScopeAgent {
		t.Fatalf("new bot = %+v, want the retuned policy", after)
	}
	if got, want := strings.Join(after.AllowedTools, ","), "fs.read,fs.write,shell"; got != want {
		t.Fatalf("new bot tools = %q, want %q", got, want)
	}
	// ... and the two bots are allowed to have drifted apart.
	if after.Name == stored.Name {
		t.Logf("both bots are named %q (the name is inherited at creation)", stored.Name)
	}
}

// TestTemplateEditSurvivesReopenWithoutReshapingInstances: the same rule across a
// restart. A store that re-resolved policy at load time would pass the previous
// test and still rewrite every grant on restart.
func TestTemplateEditSurvivesReopenWithoutReshapingInstances(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	if _, err := first.PutTemplate(ctx, contracts.BotTemplate{
		ID:           "researcher",
		Name:         "研究 Bot",
		DefaultModel: "deepseek-v4",
		AllowedTools: []string{"fs.read"},
		MemoryScope:  contracts.BotMemoryScopeTeam,
	}); err != nil {
		t.Fatalf("PutTemplate: %v", err)
	}
	if _, err := first.Create(ctx, contracts.BotInstance{ID: "bot-1", TemplateID: "researcher"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := first.PutTemplate(ctx, contracts.BotTemplate{
		ID:           "researcher",
		Name:         "研究 Bot v2",
		DefaultModel: "deepseek-v5",
		AllowedTools: []string{"fs.read", "shell"},
		MemoryScope:  contracts.BotMemoryScopeAgent,
	}); err != nil {
		t.Fatalf("PutTemplate(edit): %v", err)
	}

	second, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	template, ok, err := second.Template(ctx, "researcher")
	if err != nil || !ok {
		t.Fatalf("Template after reopen = %v, %v", ok, err)
	}
	if template.DefaultModel != "deepseek-v5" || len(template.AllowedTools) != 2 {
		t.Fatalf("template edit did not persist: %+v", template)
	}
	stored, err := second.Get(ctx, "bot-1")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if stored.DefaultModel != "deepseek-v4" || len(stored.AllowedTools) != 1 || stored.MemoryScope != contracts.BotMemoryScopeTeam {
		t.Fatalf("reopen re-resolved the instance against the edited template: %+v", stored)
	}
}

// TestTemplateIDIsItsIdentity: a "rename" is a second template. The old id keeps
// its record (and its bots), because instances name it and cannot be repointed.
func TestTemplateIDIsItsIdentity(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if _, err := service.PutTemplate(ctx, contracts.BotTemplate{
		ID: "researcher", Name: "研究 Bot", DefaultModel: "deepseek-v4", AllowedTools: []string{"fs.read"},
	}); err != nil {
		t.Fatalf("PutTemplate: %v", err)
	}
	if _, err := service.Create(ctx, contracts.BotInstance{ID: "bot-1", TemplateID: "researcher"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// The same content under a new id is a new template, not a rename.
	if _, err := service.PutTemplate(ctx, contracts.BotTemplate{
		ID: "researcher-v2", Name: "研究 Bot", DefaultModel: "deepseek-v4", AllowedTools: []string{"fs.read"},
	}); err != nil {
		t.Fatalf("PutTemplate(new id): %v", err)
	}
	old, ok, err := service.Template(ctx, "researcher")
	if err != nil || !ok {
		t.Fatalf("Template(researcher) = %v, %v; want the original still present", ok, err)
	}
	if old.ID != "researcher" {
		t.Fatalf("old template id = %q, want researcher", old.ID)
	}
	stored, err := service.Get(ctx, "bot-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.TemplateID != "researcher" {
		t.Fatalf("instance template = %q, want researcher (an instance cannot be repointed)", stored.TemplateID)
	}
	if _, err := service.Update(ctx, contracts.BotInstance{ID: "bot-1", TemplateID: "researcher-v2"}); !errors.Is(err, contracts.ErrBotInvalid) {
		t.Fatalf("repoint error = %v, want ErrBotInvalid", err)
	}
}

// TestTemplateInUseErrorIsReservedAndDistinct: ErrBotTemplateInUse exists for the
// delete this store does not have yet. Pinning it as distinct from the
// unknown-template error keeps a future delete from being wired to the wrong
// branch (and vice versa) without anyone noticing.
func TestTemplateInUseErrorIsReservedAndDistinct(t *testing.T) {
	wrapped := fmt.Errorf("bot: template %q: %w", "researcher", contracts.ErrBotTemplateInUse)
	if !errors.Is(wrapped, contracts.ErrBotTemplateInUse) {
		t.Fatal("ErrBotTemplateInUse is not discoverable through %w")
	}
	if errors.Is(wrapped, contracts.ErrBotTemplateUnknown) {
		t.Fatal("ErrBotTemplateInUse aliases ErrBotTemplateUnknown: a delete would be reported as a bad id")
	}
	if contracts.ErrBotTemplateInUse.Error() == contracts.ErrBotTemplateUnknown.Error() {
		t.Fatal("the two template errors share a message; a log cannot tell them apart")
	}
}
