package gateway

// Installing a draft is the second act: accepting records a decision, installing
// writes a real skill file. A pending draft must not install, and an existing skill
// name must never be overwritten by a proposal.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeSkillEditor struct {
	mu      sync.Mutex
	created []contracts.SkillDraft
	err     error
}

func (f *fakeSkillEditor) CreateSkill(_ context.Context, draft contracts.SkillDraft) (contracts.Skill, error) {
	if f.err != nil {
		return contracts.Skill{}, f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, draft)
	return contracts.Skill{Name: draft.Name}, nil
}

func (f *fakeSkillEditor) UpdateSkill(context.Context, string, contracts.SkillDraft) (contracts.Skill, error) {
	return contracts.Skill{}, nil
}

func (f *fakeSkillEditor) DeleteSkill(context.Context, string) error { return nil }

func newInstallGateway(t *testing.T, drafts []contracts.TaskSkillDraft, editor contracts.SkillsEditor) *Gateway {
	t.Helper()
	root := testRoot(t, newFakeChat())
	store := &fakeSkillDrafts{drafts: drafts}
	if _, err := root.Provide(contracts.KeySkillDrafts, contracts.SkillDraftsService(store)); err != nil {
		t.Fatalf("provide drafts: %v", err)
	}
	if editor != nil {
		if _, err := root.Provide(contracts.KeySkillsEditor, editor); err != nil {
			t.Fatalf("provide editor: %v", err)
		}
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return gateway
}

func TestInstallWritesAcceptedDraftsOnly(t *testing.T) {
	editor := &fakeSkillEditor{}
	gw := newInstallGateway(t, []contracts.TaskSkillDraft{
		{ID: "draft-accepted", Name: "deploy-runbook", Summary: "use when", Body: "# deploy-runbook", Status: contracts.SkillDraftAccepted},
		{ID: "draft-pending", Name: "draft-pending", Status: contracts.SkillDraftPending},
	}, editor)

	if _, problem := gw.skillDraftInstall(context.Background(), json.RawMessage(`{"id":"draft-pending"}`)); problem == nil {
		t.Fatal("a pending draft was installed")
	}
	result, problem := gw.skillDraftInstall(context.Background(), json.RawMessage(`{"id":"draft-accepted"}`))
	if problem != nil {
		t.Fatalf("install accepted draft: %+v", problem)
	}
	payload, _ := result.(map[string]any)
	if payload["installed"] != true || payload["name"] != "deploy-runbook" {
		t.Fatalf("payload = %+v", payload)
	}
	editor.mu.Lock()
	defer editor.mu.Unlock()
	if len(editor.created) != 1 || editor.created[0].Description != "use when" {
		t.Fatalf("created = %+v, want one skill carrying the summary as its description", editor.created)
	}
}

func TestInstallSurfacesAnExistingSkillName(t *testing.T) {
	editor := &fakeSkillEditor{err: contracts.ErrSkillDraftInvalid}
	gw := newInstallGateway(t, []contracts.TaskSkillDraft{
		{ID: "draft-accepted", Name: "taken", Status: contracts.SkillDraftAccepted},
	}, editor)
	if _, problem := gw.skillDraftInstall(context.Background(), json.RawMessage(`{"id":"draft-accepted"}`)); problem == nil {
		t.Fatal("an install failure was reported as success")
	}
}

func TestInstallWithoutAnEditorIsRefused(t *testing.T) {
	gw := newInstallGateway(t, []contracts.TaskSkillDraft{
		{ID: "draft-accepted", Name: "x-skill", Status: contracts.SkillDraftAccepted},
	}, nil)
	if _, problem := gw.skillDraftInstall(context.Background(), json.RawMessage(`{"id":"draft-accepted"}`)); problem == nil {
		t.Fatal("a product without a skills editor claimed to install")
	}
}
