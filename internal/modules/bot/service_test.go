package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// fixedNow keeps CreatedAt assertions exact instead of "some time around now".
var fixedNow = time.Date(2026, 11, 25, 9, 30, 0, 0, time.UTC)

func newTestService(t *testing.T) *Service {
	t.Helper()
	service := NewService()
	service.now = func() time.Time { return fixedNow }
	return service
}

// TestBuiltinTemplateIsSeeded pins the one thing a fresh product must be able to
// do before anyone designs a team: create a bot from the built-in "lead" role.
func TestBuiltinTemplateIsSeeded(t *testing.T) {
	service := newTestService(t)
	templates, err := service.Templates(context.Background())
	if err != nil {
		t.Fatalf("Templates: %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("Templates = %+v, want exactly the built-in template", templates)
	}
	lead := templates[0]
	if lead.ID != LeadTemplateID || lead.Name != "Lead" || lead.Role != "Lead" {
		t.Fatalf("built-in template = %+v, want id/name/role lead/Lead/Lead", lead)
	}
	if lead.MemoryScope != contracts.BotMemoryScopeTeam {
		t.Fatalf("built-in memory scope = %q, want %q", lead.MemoryScope, contracts.BotMemoryScopeTeam)
	}
	// A bot can be created from it right away.
	created, err := service.Create(context.Background(), contracts.BotInstance{ID: "bot-1", TemplateID: LeadTemplateID})
	if err != nil {
		t.Fatalf("Create from the built-in template: %v", err)
	}
	if created.Name != "Lead" {
		t.Fatalf("created name = %q, want the template name fallback", created.Name)
	}
}

// TestSeedingIsIdempotent: opening twice must not duplicate or overwrite the
// built-in, and a user who retuned it keeps their version.
func TestSeedingIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	retuned := contracts.BotTemplate{
		ID:          LeadTemplateID,
		Name:        "Chief",
		Role:        "Lead",
		Description: "retuned",
		MemoryScope: contracts.BotMemoryScopeAgent,
	}
	if _, err := first.PutTemplate(context.Background(), retuned); err != nil {
		t.Fatalf("PutTemplate(lead): %v", err)
	}
	second, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	templates, err := second.Templates(context.Background())
	if err != nil {
		t.Fatalf("Templates: %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("Templates after reopen = %+v, want one (no duplicate seed)", templates)
	}
	if templates[0].Name != "Chief" || templates[0].MemoryScope != contracts.BotMemoryScopeAgent {
		t.Fatalf("reopen overwrote the retuned built-in: %+v", templates[0])
	}
}

// TestPutTemplateAndTemplates: put is an upsert, the list is sorted, and the
// returned value normalizes what the caller left at its zero value.
func TestPutTemplateAndTemplates(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()

	stored, err := service.PutTemplate(ctx, contracts.BotTemplate{
		ID:           "researcher",
		Name:         "  研究 Bot  ",
		Role:         "Researcher",
		Description:  "查资料",
		SystemPrompt: "when to use ...",
		DefaultModel: "deepseek-v4",
		AllowedTools: []string{" fs.read ", "web_search"},
	})
	if err != nil {
		t.Fatalf("PutTemplate: %v", err)
	}
	if stored.Name != "研究 Bot" {
		t.Fatalf("stored name = %q, want the trimmed name", stored.Name)
	}
	if stored.MemoryScope != contracts.BotMemoryScopeAgent {
		t.Fatalf("stored memory scope = %q, want the agent default", stored.MemoryScope)
	}
	if got, want := strings.Join(stored.AllowedTools, ","), "fs.read,web_search"; got != want {
		t.Fatalf("stored tools = %q, want %q", got, want)
	}

	// Second put with the same id replaces, it does not append.
	replaced, err := service.PutTemplate(ctx, contracts.BotTemplate{ID: "researcher", Name: "研究 Bot v2"})
	if err != nil {
		t.Fatalf("PutTemplate(replace): %v", err)
	}
	if replaced.Name != "研究 Bot v2" {
		t.Fatalf("replaced name = %q", replaced.Name)
	}
	if len(replaced.AllowedTools) != 0 {
		t.Fatalf("replaced tools = %v, want cleared", replaced.AllowedTools)
	}

	templates, err := service.Templates(ctx)
	if err != nil {
		t.Fatalf("Templates: %v", err)
	}
	ids := make([]string, 0, len(templates))
	for _, template := range templates {
		ids = append(ids, template.ID)
	}
	if got, want := strings.Join(ids, ","), "lead,researcher"; got != want {
		t.Fatalf("template ids = %q, want %q (sorted)", got, want)
	}
}

// TestCreateInheritsTemplatePolicy is the G1 rule: 模板 = 默认模型 + 工具授予 +
// 记忆作用域, and an instance that sets none of them gets all three.
func TestCreateInheritsTemplatePolicy(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if _, err := service.PutTemplate(ctx, contracts.BotTemplate{
		ID:           "researcher",
		Name:         "研究 Bot",
		Role:         "Researcher",
		DefaultModel: "deepseek-v4",
		AllowedTools: []string{"fs.read", "web_search"},
		MemoryScope:  contracts.BotMemoryScopeTeam,
	}); err != nil {
		t.Fatalf("PutTemplate: %v", err)
	}

	created, err := service.Create(ctx, contracts.BotInstance{ID: "bot-1", TemplateID: "researcher", Owner: " alice "})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Name != "研究 Bot" {
		t.Fatalf("name = %q, want inherited template name", created.Name)
	}
	if created.DefaultModel != "deepseek-v4" {
		t.Fatalf("model = %q, want inherited model", created.DefaultModel)
	}
	if got, want := strings.Join(created.AllowedTools, ","), "fs.read,web_search"; got != want {
		t.Fatalf("tools = %q, want inherited %q", got, want)
	}
	if created.MemoryScope != contracts.BotMemoryScopeTeam {
		t.Fatalf("scope = %q, want inherited team", created.MemoryScope)
	}
	if created.Owner != "alice" {
		t.Fatalf("owner = %q, want trimmed alice", created.Owner)
	}
	if created.Status != contracts.BotStatusActive {
		t.Fatalf("status = %q, want active by default", created.Status)
	}
	if !created.CreatedAt.Equal(fixedNow) {
		t.Fatalf("created at = %v, want %v", created.CreatedAt, fixedNow)
	}
	if !created.LastSeenAt.IsZero() {
		t.Fatalf("last seen = %v, want zero (never seen)", created.LastSeenAt)
	}

	// Inheritance copies: mutating the returned list must not touch the store.
	created.AllowedTools[0] = "shell"
	stored, err := service.Get(ctx, "bot-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.AllowedTools[0] != "fs.read" {
		t.Fatalf("store leaked its tool list through a copy: %v", stored.AllowedTools)
	}
}

// TestCreateOverridesWin: an instance that sets a field keeps it, field by
// field (setting one must not drag the others back to the template).
func TestCreateOverridesWin(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if _, err := service.PutTemplate(ctx, contracts.BotTemplate{
		ID:           "researcher",
		Name:         "研究 Bot",
		Role:         "Researcher",
		DefaultModel: "deepseek-v4",
		AllowedTools: []string{"fs.read", "web_search"},
		MemoryScope:  contracts.BotMemoryScopeTeam,
	}); err != nil {
		t.Fatalf("PutTemplate: %v", err)
	}

	created, err := service.Create(ctx, contracts.BotInstance{
		ID:           "bot-2",
		TemplateID:   "researcher",
		Name:         "数据 Bot",
		DefaultModel: "gpt-mini",
		AllowedTools: []string{"sql_query"},
		MemoryScope:  contracts.BotMemoryScopeAgent,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Name != "数据 Bot" || created.DefaultModel != "gpt-mini" {
		t.Fatalf("overrides lost: %+v", created)
	}
	if got, want := strings.Join(created.AllowedTools, ","), "sql_query"; got != want {
		t.Fatalf("tools = %q, want the override %q (not merged with the template)", got, want)
	}
	if created.MemoryScope != contracts.BotMemoryScopeAgent {
		t.Fatalf("scope = %q, want the override agent", created.MemoryScope)
	}

	// A later template edit must not reach back into the instance: grants are
	// permissions, and permissions do not grow behind the user's back.
	if _, err := service.PutTemplate(ctx, contracts.BotTemplate{
		ID:           "researcher",
		Name:         "研究 Bot",
		Role:         "Researcher",
		DefaultModel: "deepseek-v5",
		AllowedTools: []string{"fs.read", "web_search", "shell"},
		MemoryScope:  contracts.BotMemoryScopeTeam,
	}); err != nil {
		t.Fatalf("PutTemplate(update): %v", err)
	}
	stored, err := service.Get(ctx, "bot-2")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.DefaultModel != "gpt-mini" || len(stored.AllowedTools) != 1 {
		t.Fatalf("template edit reached the existing bot: %+v", stored)
	}
}

// TestCreateDuplicateID: creation never overwrites a bot, because that would
// replace its grants too.
func TestCreateDuplicateID(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if _, err := service.Create(ctx, contracts.BotInstance{ID: "bot-1", TemplateID: LeadTemplateID}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := service.Create(ctx, contracts.BotInstance{ID: "bot-1", TemplateID: LeadTemplateID, Name: "steal"})
	if !errors.Is(err, contracts.ErrBotDuplicate) {
		t.Fatalf("second Create error = %v, want ErrBotDuplicate", err)
	}
	stored, err := service.Get(ctx, "bot-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Name == "steal" {
		t.Fatal("a duplicate Create overwrote the existing bot")
	}
}

// TestCreateUnknownTemplate: naming a template that does not exist (or none at
// all) must not fall back to the built-in one.
func TestCreateUnknownTemplate(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if _, err := service.Create(ctx, contracts.BotInstance{ID: "bot-1", TemplateID: "ghost"}); !errors.Is(err, contracts.ErrBotTemplateUnknown) {
		t.Fatalf("unknown template error = %v, want ErrBotTemplateUnknown", err)
	}
	if _, err := service.Create(ctx, contracts.BotInstance{ID: "bot-1"}); !errors.Is(err, contracts.ErrBotInvalid) {
		t.Fatalf("missing template error = %v, want ErrBotInvalid", err)
	}
	if instances, err := service.Instances(ctx); err != nil || len(instances) != 0 {
		t.Fatalf("Instances = %v, %v; want none", instances, err)
	}
}

// TestValidationTable drives every budget through both halves of the store, so a
// new field cannot be added to one path and forgotten in the other.
func TestValidationTable(t *testing.T) {
	ctx := context.Background()
	manyTools := make([]string, MaxTools+1)
	for i := range manyTools {
		manyTools[i] = fmt.Sprintf("tool_%d", i)
	}
	longName := strings.Repeat("名", MaxNameRunes+1)
	longRole := strings.Repeat("r", MaxRoleRunes+1)
	longModel := strings.Repeat("m", MaxModelRunes+1)

	templateCases := []struct {
		name     string
		template contracts.BotTemplate
	}{
		{"id too short", contracts.BotTemplate{ID: "a", Name: "n"}},
		{"id outside alphabet", contracts.BotTemplate{ID: "Bot_1", Name: "n"}},
		{"id too long", contracts.BotTemplate{ID: strings.Repeat("a", MaxIDChars+1), Name: "n"}},
		{"no name", contracts.BotTemplate{ID: "ok-id"}},
		{"name too long", contracts.BotTemplate{ID: "ok-id", Name: longName}},
		{"role too long", contracts.BotTemplate{ID: "ok-id", Name: "n", Role: longRole}},
		{"model too long", contracts.BotTemplate{ID: "ok-id", Name: "n", DefaultModel: longModel}},
		{"tool outside alphabet", contracts.BotTemplate{ID: "ok-id", Name: "n", AllowedTools: []string{"Bash"}}},
		{"tool empty", contracts.BotTemplate{ID: "ok-id", Name: "n", AllowedTools: []string{""}}},
		{"duplicate tool", contracts.BotTemplate{ID: "ok-id", Name: "n", AllowedTools: []string{"fs.read", "fs.read"}}},
		{"too many tools", contracts.BotTemplate{ID: "ok-id", Name: "n", AllowedTools: manyTools}},
		{"unknown memory scope", contracts.BotTemplate{ID: "ok-id", Name: "n", MemoryScope: "global"}},
	}
	for _, testCase := range templateCases {
		t.Run("template/"+testCase.name, func(t *testing.T) {
			service := newTestService(t)
			if _, err := service.PutTemplate(ctx, testCase.template); !errors.Is(err, contracts.ErrBotInvalid) {
				t.Fatalf("PutTemplate error = %v, want ErrBotInvalid", err)
			}
		})
	}

	instanceCases := []struct {
		name     string
		instance contracts.BotInstance
	}{
		{"id outside alphabet", contracts.BotInstance{ID: "Bot-1", TemplateID: LeadTemplateID}},
		{"id too long", contracts.BotInstance{ID: strings.Repeat("b", MaxIDChars+1), TemplateID: LeadTemplateID}},
		{"name too long", contracts.BotInstance{ID: "bot-1", TemplateID: LeadTemplateID, Name: longName}},
		{"owner too long", contracts.BotInstance{ID: "bot-1", TemplateID: LeadTemplateID, Name: "n", Owner: strings.Repeat("o", MaxOwnerRunes+1)}},
		{"model too long", contracts.BotInstance{ID: "bot-1", TemplateID: LeadTemplateID, Name: "n", DefaultModel: longModel}},
		{"tool outside alphabet", contracts.BotInstance{ID: "bot-1", TemplateID: LeadTemplateID, Name: "n", AllowedTools: []string{"Bash"}}},
		{"duplicate tool", contracts.BotInstance{ID: "bot-1", TemplateID: LeadTemplateID, Name: "n", AllowedTools: []string{"a", "a"}}},
		{"too many tools", contracts.BotInstance{ID: "bot-1", TemplateID: LeadTemplateID, Name: "n", AllowedTools: manyTools}},
		{"unknown memory scope", contracts.BotInstance{ID: "bot-1", TemplateID: LeadTemplateID, Name: "n", MemoryScope: "global"}},
		{"unknown status", contracts.BotInstance{ID: "bot-1", TemplateID: LeadTemplateID, Name: "n", Status: "sleeping"}},
	}
	for _, testCase := range instanceCases {
		t.Run("instance/"+testCase.name, func(t *testing.T) {
			service := newTestService(t)
			if _, err := service.Create(ctx, testCase.instance); !errors.Is(err, contracts.ErrBotInvalid) {
				t.Fatalf("Create error = %v, want ErrBotInvalid", err)
			}
			if instances, err := service.Instances(ctx); err != nil || len(instances) != 0 {
				t.Fatalf("a rejected Create left %v (%v)", instances, err)
			}
		})
	}
}

// TestUpdateRules pins what Update may and may not do: the mutable fields change,
// identity fields are rejected, empty means "keep" for everything whose empty
// value is not a real value, and retirement is terminal.
func TestUpdateRules(t *testing.T) {
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
	created, err := service.Create(ctx, contracts.BotInstance{ID: "bot-1", TemplateID: "researcher", Owner: "alice"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// A minimal patch: only the fields with a meaning at their zero value change.
	seenAt := fixedNow.Add(time.Hour)
	updated, err := service.Update(ctx, contracts.BotInstance{
		ID:           "bot-1",
		Name:         created.Name,
		Owner:        "",
		AllowedTools: []string{"fs.read", "web_search"},
		DefaultModel: "",
		Status:       contracts.BotStatusPaused,
		LastSeenAt:   seenAt,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != created.Name {
		t.Fatalf("name = %q, want the stored %q", updated.Name, created.Name)
	}
	if updated.TemplateID != "researcher" || !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("identity changed: %+v", updated)
	}
	if updated.MemoryScope != contracts.BotMemoryScopeTeam {
		t.Fatalf("scope = %q, want the stored team", updated.MemoryScope)
	}
	if got, want := strings.Join(updated.AllowedTools, ","), "fs.read,web_search"; got != want {
		t.Fatalf("tools = %q, want %q", got, want)
	}
	if updated.DefaultModel != "" {
		t.Fatalf("model = %q, want the explicit empty (follow the session)", updated.DefaultModel)
	}
	if updated.Owner != "" {
		t.Fatalf("owner = %q, want the explicit empty (unowned)", updated.Owner)
	}
	if updated.Status != contracts.BotStatusPaused {
		t.Fatalf("status = %q, want paused", updated.Status)
	}
	if !updated.LastSeenAt.Equal(seenAt) {
		t.Fatalf("last seen = %v, want %v", updated.LastSeenAt, seenAt)
	}

	// The template can never change, and neither can the creation time.
	if _, err := service.Update(ctx, contracts.BotInstance{ID: "bot-1", TemplateID: "lead"}); !errors.Is(err, contracts.ErrBotInvalid) {
		t.Fatalf("template change error = %v, want ErrBotInvalid", err)
	}
	if _, err := service.Update(ctx, contracts.BotInstance{ID: "bot-1", CreatedAt: fixedNow.Add(-time.Hour)}); !errors.Is(err, contracts.ErrBotInvalid) {
		t.Fatalf("created-at change error = %v, want ErrBotInvalid", err)
	}
	// Unknown bot.
	if _, err := service.Update(ctx, contracts.BotInstance{ID: "bot-9", Name: "ghost"}); !errors.Is(err, contracts.ErrBotUnknown) {
		t.Fatalf("unknown update error = %v, want ErrBotUnknown", err)
	}
	// A nil tool list keeps the stored grant; an empty non-nil one clears it.
	kept, err := service.Update(ctx, contracts.BotInstance{ID: "bot-1", Name: created.Name, Status: contracts.BotStatusPaused})
	if err != nil {
		t.Fatalf("Update(nil tools): %v", err)
	}
	if len(kept.AllowedTools) != 2 {
		t.Fatalf("nil tool list cleared the grant: %v", kept.AllowedTools)
	}
	cleared, err := service.Update(ctx, contracts.BotInstance{ID: "bot-1", Name: created.Name, AllowedTools: []string{}})
	if err != nil {
		t.Fatalf("Update(empty tools): %v", err)
	}
	if len(cleared.AllowedTools) != 0 {
		t.Fatalf("empty tool list did not clear the grant: %v", cleared.AllowedTools)
	}

	// Retiring is terminal: it can be set once and never undone.
	retired, err := service.Update(ctx, contracts.BotInstance{ID: "bot-1", Name: created.Name, Status: contracts.BotStatusRetired})
	if err != nil {
		t.Fatalf("Update(retire): %v", err)
	}
	if retired.Status != contracts.BotStatusRetired {
		t.Fatalf("status = %q, want retired", retired.Status)
	}
	if _, err := service.Update(ctx, contracts.BotInstance{ID: "bot-1", Name: created.Name, Status: contracts.BotStatusActive}); !errors.Is(err, contracts.ErrBotInvalid) {
		t.Fatalf("reactivation error = %v, want ErrBotInvalid", err)
	}
}

// TestGetAndUnknownIDs: a typo must be "unknown", not "empty record".
func TestGetAndUnknownIDs(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	if _, err := service.Get(ctx, "bot-1"); !errors.Is(err, contracts.ErrBotUnknown) {
		t.Fatalf("Get error = %v, want ErrBotUnknown", err)
	}
	if _, err := service.Update(ctx, contracts.BotInstance{ID: "nope-1", Name: "x"}); !errors.Is(err, contracts.ErrBotUnknown) {
		t.Fatalf("Update error = %v, want ErrBotUnknown", err)
	}
}

// TestListsAreSortedAndComplete: paused and retired bots stay in the list (the
// history needs them) and the order is stable.
func TestListsAreSortedAndComplete(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	for _, id := range []string{"bot-b", "bot-a", "bot-c"} {
		if _, err := service.Create(ctx, contracts.BotInstance{ID: id, TemplateID: LeadTemplateID}); err != nil {
			t.Fatalf("Create(%s): %v", id, err)
		}
	}
	if _, err := service.Update(ctx, contracts.BotInstance{ID: "bot-c", Status: contracts.BotStatusRetired}); err != nil {
		t.Fatalf("Update(retire): %v", err)
	}
	instances, err := service.Instances(ctx)
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	if len(instances) != 3 {
		t.Fatalf("Instances = %d, want 3 (retired included)", len(instances))
	}
	for i, want := range []string{"bot-a", "bot-b", "bot-c"} {
		if instances[i].ID != want {
			t.Fatalf("Instances[%d] = %q, want %q (sorted)", i, instances[i].ID, want)
		}
	}
}

// TestCanceledContextIsHonoured: every entry point reports a dead context
// instead of doing work the caller already gave up on.
func TestCanceledContextIsHonoured(t *testing.T) {
	service := newTestService(t)
	if _, err := service.Create(context.Background(), contracts.BotInstance{ID: "bot-1", TemplateID: LeadTemplateID}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Templates(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Templates error = %v, want context.Canceled", err)
	}
	if _, err := service.Instances(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Instances error = %v, want context.Canceled", err)
	}
	if _, err := service.Create(ctx, contracts.BotInstance{ID: "bot-2", TemplateID: LeadTemplateID}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create error = %v, want context.Canceled", err)
	}
	if _, err := service.Update(ctx, contracts.BotInstance{ID: "bot-1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Update error = %v, want context.Canceled", err)
	}
	if _, err := service.PutTemplate(ctx, contracts.BotTemplate{ID: "ok-id", Name: "n"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("PutTemplate error = %v, want context.Canceled", err)
	}
	if _, err := service.Get(ctx, "bot-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get error = %v, want context.Canceled", err)
	}
}
