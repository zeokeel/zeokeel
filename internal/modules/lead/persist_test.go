package lead_test

// TDD for durability: the rules are the operator's decisions about what happens
// to an inbox item, so a restart must not silently go back to "no rules" (every
// item would then be skipped and the operator would think the pass broke). The
// dangerous cases: the round trip of a rule set, a corrupt document (typed, and
// an empty set the caller can keep serving), and the file's privacy.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/inbox"
	"github.com/zeokeel/zeokeel/internal/modules/lead"
)

func openRuleStore(t *testing.T, dir string, inboxSvc contracts.InboxService, triage contracts.TriageService) *lead.Service {
	t.Helper()
	svc, err := lead.Open(dir, inboxSvc, triage, lead.Config{MaxPerRun: 10, Now: time.Now})
	if err != nil {
		t.Fatalf("Open(%s): %v", dir, err)
	}
	return svc
}

func newInbox(t *testing.T) contracts.InboxService {
	t.Helper()
	return inbox.NewService(inbox.Config{Now: time.Now, MaxPerMinute: 1000})
}

func TestRulesSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	inboxSvc := newInbox(t)
	triage := newFakeTriage()
	ctx := context.Background()

	want := []lead.Rule{
		{Source: "webhook", Assignee: "research-bot", SubjectPrefix: "事件：", DetailPrefix: "来源：webhook\n"},
		{Source: "email", Ignore: true},
	}
	first := openRuleStore(t, dir, inboxSvc, triage)
	if err := first.SetRules(ctx, want); err != nil {
		t.Fatalf("SetRules: %v", err)
	}

	second := openRuleStore(t, dir, inboxSvc, triage)
	got, err := second.Rules(ctx)
	if err != nil {
		t.Fatalf("Rules after reopen: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("rules after reopen = %+v, want %+v", got, want)
	}

	// The restored rules must actually drive a pass, not just be countable.
	arrive(t, inboxSvc, "webhook", "崩溃告警")
	arrive(t, inboxSvc, "wechat", "私人消息")
	report, err := second.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce after reopen: %v", err)
	}
	if report.Scanned != 2 || report.Dispatched != 1 || report.Skipped != 1 {
		t.Fatalf("report = %+v, want scanned 2 / dispatched 1 / skipped 1", report)
	}
	if len(triage.applied) != 1 || triage.applied[0].Task == nil || triage.applied[0].Task.Assignee != "research-bot" {
		t.Fatalf("applied = %+v, want the restored webhook rule", triage.applied)
	}
}

func TestRuleStoreStartsEmptyOnMissingFile(t *testing.T) {
	dir := t.TempDir()
	svc := openRuleStore(t, dir, newInbox(t), newFakeTriage())
	rules, err := svc.Rules(context.Background())
	if err != nil {
		t.Fatalf("Rules: %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("rules = %v, want none on a fresh data dir", rules)
	}
}

func TestCorruptRuleStoreIsTypedAndEmpty(t *testing.T) {
	dir := t.TempDir()
	inboxSvc := newInbox(t)
	triage := newFakeTriage()
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(dir, "rules.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}

	svc, err := lead.Open(dir, inboxSvc, triage, lead.Config{MaxPerRun: 10, Now: time.Now})
	if !errors.Is(err, lead.ErrLeadStoreCorrupt) {
		t.Fatalf("Open err = %v, want ErrLeadStoreCorrupt", err)
	}
	if svc == nil {
		t.Fatal("Open returned no service for a corrupt store; the caller cannot keep serving")
	}
	rules, rulesErr := svc.Rules(ctx)
	if rulesErr != nil {
		t.Fatalf("Rules after corrupt open: %v", rulesErr)
	}
	if len(rules) != 0 {
		t.Fatalf("rules after corrupt open = %v, want an empty set", rules)
	}
	// A pass with no rules skips everything instead of crashing.
	arrive(t, inboxSvc, "webhook", "later")
	report, err := svc.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce after corrupt open: %v", err)
	}
	if report.Scanned != 1 || report.Skipped != 1 {
		t.Fatalf("report = %+v, want the item skipped", report)
	}

	// Setting rules heals the document.
	if err := svc.SetRules(ctx, []lead.Rule{{Source: "webhook", Assignee: "bot"}}); err != nil {
		t.Fatalf("SetRules after corrupt open: %v", err)
	}
	reopened, err := lead.Open(dir, inboxSvc, triage, lead.Config{MaxPerRun: 10, Now: time.Now})
	if err != nil {
		t.Fatalf("Open after heal: %v", err)
	}
	healed, err := reopened.Rules(ctx)
	if err != nil || len(healed) != 1 || healed[0].Assignee != "bot" {
		t.Fatalf("rules after heal = (%+v, %v), want the written rule", healed, err)
	}
}

func TestRuleStoreIsAtomicAndPrivate(t *testing.T) {
	dir := t.TempDir()
	svc := openRuleStore(t, dir, newInbox(t), newFakeTriage())
	if err := svc.SetRules(context.Background(), []lead.Rule{{Source: "webhook", Assignee: "bot"}}); err != nil {
		t.Fatalf("SetRules: %v", err)
	}

	path := filepath.Join(dir, "rules.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("rule store mode = %o, want 600", perm)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".tmp" {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
}
