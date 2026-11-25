package bot

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestOpenMissingFileIsAFreshStore: a product that has never saved a bot boots
// with the built-in templates and Open itself writes nothing (a corrupt or
// absent document must never be replaced just by starting up).
func TestOpenMissingFileIsAFreshStore(t *testing.T) {
	dir := t.TempDir()
	service, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if service.Path() != filepath.Join(dir, FileName) {
		t.Fatalf("Path = %q, want %q", service.Path(), filepath.Join(dir, FileName))
	}
	if _, statErr := os.Stat(service.Path()); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Open wrote %s (err %v); it must not", service.Path(), statErr)
	}
	templates, err := service.Templates(context.Background())
	if err != nil {
		t.Fatalf("Templates: %v", err)
	}
	if len(templates) != 1 || templates[0].ID != LeadTemplateID {
		t.Fatalf("Templates = %+v, want just the built-in lead", templates)
	}
}

// TestInMemoryStore: an empty dir is the same service without the durable half,
// which is what tests and validation-only callers use.
func TestInMemoryStore(t *testing.T) {
	service, err := Open("")
	if err != nil {
		t.Fatalf("Open(\"\"): %v", err)
	}
	if service.Path() != "" {
		t.Fatalf("Path = %q, want empty for an in-memory store", service.Path())
	}
	if _, err := service.PutTemplate(context.Background(), contracts.BotTemplate{ID: "researcher", Name: "研究 Bot"}); err != nil {
		t.Fatalf("PutTemplate: %v", err)
	}
	if _, err := service.Create(context.Background(), contracts.BotInstance{ID: "bot-1", TemplateID: "researcher"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

// TestPersistenceAcrossReopen is the "重启后仍在" half of G1: a bot is an entity,
// so its identity, owner, resolved grants and timestamps survive a restart.
func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	if _, err := first.PutTemplate(ctx, contracts.BotTemplate{
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
	created, err := first.Create(ctx, contracts.BotInstance{ID: "bot-1", TemplateID: "researcher", Owner: "alice"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	seenAt := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	// Update is a replacement of the mutable fields, so the caller passes the
	// record it read back (what the settings page does) with its changes applied.
	if _, err := first.Update(ctx, contracts.BotInstance{
		ID:           created.ID,
		Name:         created.Name,
		Owner:        created.Owner,
		DefaultModel: created.DefaultModel,
		AllowedTools: created.AllowedTools,
		MemoryScope:  created.MemoryScope,
		Status:       contracts.BotStatusPaused,
		LastSeenAt:   seenAt,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	second, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	templates, err := second.Templates(ctx)
	if err != nil {
		t.Fatalf("Templates: %v", err)
	}
	if len(templates) != 2 {
		t.Fatalf("Templates after reopen = %+v, want lead + researcher", templates)
	}
	var researcher contracts.BotTemplate
	for _, template := range templates {
		if template.ID == "researcher" {
			researcher = template
		}
	}
	if researcher.Name != "研究 Bot" || researcher.DefaultModel != "deepseek-v4" || researcher.MemoryScope != contracts.BotMemoryScopeTeam {
		t.Fatalf("template did not survive: %+v", researcher)
	}
	if got, want := strings.Join(researcher.AllowedTools, ","), "fs.read,web_search"; got != want {
		t.Fatalf("template tools after reopen = %q, want %q", got, want)
	}
	if researcher.SystemPrompt != "when to use ..." {
		t.Fatalf("template prompt did not survive: %q", researcher.SystemPrompt)
	}

	stored, err := second.Get(ctx, "bot-1")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if stored.Owner != "alice" || stored.TemplateID != "researcher" {
		t.Fatalf("identity did not survive: %+v", stored)
	}
	if !stored.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("created at = %v, want %v", stored.CreatedAt, created.CreatedAt)
	}
	if stored.Status != contracts.BotStatusPaused {
		t.Fatalf("status = %q, want paused", stored.Status)
	}
	if !stored.LastSeenAt.Equal(seenAt) {
		t.Fatalf("last seen = %v, want %v", stored.LastSeenAt, seenAt)
	}
	if stored.DefaultModel != "deepseek-v4" || len(stored.AllowedTools) != 2 || stored.MemoryScope != contracts.BotMemoryScopeTeam {
		t.Fatalf("resolved policy did not survive: %+v", stored)
	}
}

// TestStoreFileIsPrivate: the document holds who may act with which tools, so it
// is 0600 in a 0700 directory.
func TestStoreFileIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bots")
	service, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := service.Create(context.Background(), contracts.BotInstance{ID: "bot-1", TemplateID: LeadTemplateID}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	info, err := os.Stat(service.Path())
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("%s mode = %o, want 600", service.Path(), got)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat(dir): %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir mode = %o, want 700", got)
	}
	// And the document is the versioned shape the rest of the product expects.
	raw, err := os.ReadFile(service.Path())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var document struct {
		Version   int               `json:"version"`
		Templates []any             `json:"templates"`
		Instances []json.RawMessage `json:"instances"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("stored document is not JSON: %v", err)
	}
	if document.Version != storeVersion {
		t.Fatalf("version = %d, want %d", document.Version, storeVersion)
	}
	if len(document.Templates) != 1 || len(document.Instances) != 1 {
		t.Fatalf("document = %s, want one template and one bot", raw)
	}
}

// TestCorruptStore: a document this build cannot honour is a typed error plus an
// empty, usable store — the bytes stay on disk, and the next write replaces them.
func TestCorruptStore(t *testing.T) {
	valid := func(version int, templates, instances string) string {
		return `{"version":` + strconv.Itoa(version) + `,"templates":[` + templates + `],"instances":[` + instances + `]}`
	}
	cases := []struct {
		name string
		raw  string
	}{
		{"not json", "{not json"},
		{"no version", `{"templates":[],"instances":[]}`},
		{"future version", valid(storeVersion+1, "", "")},
		{"bad template id", valid(storeVersion, `{"ID":"BAD","Name":"n"}`, "")},
		{"duplicate template", valid(storeVersion, `{"ID":"ok-id","Name":"n"},{"ID":"ok-id","Name":"m"}`, "")},
		{"bad bot status", valid(storeVersion, `{"ID":"ok-id","Name":"n"}`, `{"ID":"bot-1","TemplateID":"ok-id","Name":"n","Status":"sleeping","CreatedAt":"2026-11-25T09:30:00Z","MemoryScope":"agent"}`)},
		{"bot without creation time", valid(storeVersion, `{"ID":"ok-id","Name":"n"}`, `{"ID":"bot-1","TemplateID":"ok-id","Name":"n","Status":"active","MemoryScope":"agent"}`)},
		{"bot names unknown template", valid(storeVersion, `{"ID":"ok-id","Name":"n"}`, `{"ID":"bot-1","TemplateID":"ghost","Name":"n","Status":"active","MemoryScope":"agent","CreatedAt":"2026-11-25T09:30:00Z"}`)},
		{"duplicate bot", valid(storeVersion, `{"ID":"ok-id","Name":"n"}`, `{"ID":"bot-1","TemplateID":"ok-id","Name":"n","Status":"active","MemoryScope":"agent","CreatedAt":"2026-11-25T09:30:00Z"},{"ID":"bot-1","TemplateID":"ok-id","Name":"m","Status":"active","MemoryScope":"agent","CreatedAt":"2026-11-25T09:30:00Z"}`)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, FileName)
			if err := os.WriteFile(path, []byte(testCase.raw), 0o600); err != nil {
				t.Fatalf("seed corrupt file: %v", err)
			}
			service, err := Open(dir)
			if !errors.Is(err, contracts.ErrBotStoreCorrupt) {
				t.Fatalf("Open error = %v, want ErrBotStoreCorrupt", err)
			}
			if service == nil {
				t.Fatal("Open returned no store alongside the corrupt error")
			}
			ctx := context.Background()
			if instances, listErr := service.Instances(ctx); listErr != nil || len(instances) != 0 {
				t.Fatalf("Instances = %v, %v; want an empty store", instances, listErr)
			}
			templates, listErr := service.Templates(ctx)
			if listErr != nil {
				t.Fatalf("Templates: %v", listErr)
			}
			if len(templates) != 1 || templates[0].ID != LeadTemplateID {
				t.Fatalf("Templates = %+v, want the built-in lead to survive a corrupt document", templates)
			}
			// The evidence is untouched: Open never writes.
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("ReadFile: %v", readErr)
			}
			if string(raw) != testCase.raw {
				t.Fatalf("Open rewrote the corrupt file: %q", raw)
			}
			// And the store is usable: the next write replaces the unreadable file.
			if _, createErr := service.Create(ctx, contracts.BotInstance{ID: "bot-1", TemplateID: LeadTemplateID}); createErr != nil {
				t.Fatalf("Create on a corrupt-but-usable store: %v", createErr)
			}
			reopened, reopenErr := Open(dir)
			if reopenErr != nil {
				t.Fatalf("reopen after rewrite: %v", reopenErr)
			}
			if _, getErr := reopened.Get(ctx, "bot-1"); getErr != nil {
				t.Fatalf("Get after reopen: %v", getErr)
			}
		})
	}
}

// TestLoadNormalizesRows: what a hand-edited document may not do is smuggle in an
// invalid row; what it may do is omit the optional fields, which normalize the
// same way they do on create.
func TestLoadNormalizesRows(t *testing.T) {
	dir := t.TempDir()
	raw := `{
	  "version": 1,
	  "templates": [{"ID":" ok-id ","Name":" Name "}],
	  "instances": [{"ID":"bot-1","TemplateID":"ok-id","Name":" Bot ","Status":"active","CreatedAt":"2026-11-25T09:30:00.123456789Z"}]
	}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(raw), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	service, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	stored, err := service.Get(ctx, "bot-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Name != "Bot" {
		t.Fatalf("name = %q, want trimmed", stored.Name)
	}
	if stored.MemoryScope != contracts.BotMemoryScopeAgent {
		t.Fatalf("scope = %q, want the agent default (a hand-edited row may omit it)", stored.MemoryScope)
	}
	if !stored.CreatedAt.Equal(time.Date(2026, 11, 25, 9, 30, 0, 0, time.UTC)) {
		t.Fatalf("created at = %v, want second precision", stored.CreatedAt)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
