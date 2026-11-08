package hosttools

// End-to-end bridge tests: the product host services (uiroot's artifact and
// plan implementations) must drive the ask_user/present/plan tools.

import (
	"errors"
	"strings"
	"testing"

	cordis "github.com/zeokeel/zeokeel-kernel"
	"github.com/zeokeel/zeokeel-kernel/module"
	rig "github.com/zeokeel/zeokeel-rig"
	rigtool "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/uiroot"
)

// loadBridgeProduct loads hosttools with the host bridge services provided.
func loadBridgeProduct(t *testing.T, uiRoot *uiroot.Root, interactive bool) *product {
	t.Helper()
	root := cordis.Root()
	t.Cleanup(func() { _ = root.DisposeAll() })
	if _, err := root.Provide(contracts.KeyUserInteraction, contracts.UserInteraction(uiRoot.UserInteraction())); err != nil {
		t.Fatalf("provide interaction: %v", err)
	}
	if _, err := root.Provide(contracts.KeyArtifactPresenter, contracts.ArtifactPresenter(uiRoot.ArtifactPresenter())); err != nil {
		t.Fatalf("provide artifact presenter: %v", err)
	}
	if _, err := root.Provide(contracts.KeyPlanReviewer, contracts.PlanReviewer(uiRoot.PlanReviewer())); err != nil {
		t.Fatalf("provide plan reviewer: %v", err)
	}
	if _, err := root.Provide(contracts.KeyGoalTracker, contracts.GoalTracker(uiRoot.GoalTracker())); err != nil {
		t.Fatalf("provide goal tracker: %v", err)
	}
	rigRegistry := rigtool.NewRegistry()
	registry := newKernelRegistry(t, rigRegistry, nil)
	loader, err := module.Load(root, registry, []module.Entry{
		{ID: "toolkit", Module: "test/fake-toolkit"},
		{ID: "hosttools", Module: ModuleID, Config: map[string]any{"interactive": interactive}},
	})
	if err != nil {
		t.Fatalf("load hosttools product: %v", err)
	}
	t.Cleanup(func() { _ = loader.Close() })
	return &product{root: root, loader: loader, registry: rigRegistry}
}

func TestPresentUsesArtifactBridge(t *testing.T) {
	uiRoot := uiroot.New(uiroot.Config{AppName: "test"})
	artifacts := make(chan contracts.Artifact, 2)
	uiRoot.ArtifactPresenter().SetArtifactHandler(func(artifact contracts.Artifact) { artifacts <- artifact })
	product := loadBridgeProduct(t, uiRoot, true)

	content := product.callOK(t, ToolPresent, `{"title":"Report","content":"# all green"}`)
	if !strings.Contains(content, "presented artifact artifact-1") || !strings.Contains(content, "Report") {
		t.Fatalf("present content = %q", content)
	}
	artifact := <-artifacts
	if artifact.ID != "artifact-1" || artifact.Title != "Report" || artifact.Body != "# all green" {
		t.Fatalf("artifact = %+v", artifact)
	}
	if content := product.callOK(t, ToolPresent, `{"title":"Second","content":"body"}`); !strings.Contains(content, "artifact-2") {
		t.Fatalf("second present content = %q", content)
	}
}

func TestPlanToolsUsePlanBridge(t *testing.T) {
	uiRoot := uiroot.New(uiroot.Config{AppName: "test"})
	reviews := make(chan contracts.PlanReview, 4)
	uiRoot.PlanReviewer().SetPlanHandler(func(review contracts.PlanReview) { reviews <- review })
	product := loadBridgeProduct(t, uiRoot, true)

	content := product.callOK(t, ToolPlanReview, `{"plan":"# Release plan\n1. test"}`)
	if !strings.Contains(content, "reviewing") {
		t.Fatalf("plan_review content = %q", content)
	}
	review := <-reviews
	if review.State != "reviewing" || review.Content != "# Release plan\n1. test" {
		t.Fatalf("review snapshot = %+v", review)
	}
	if state := uiRoot.PlanReviewer().State(); state != "reviewing" {
		t.Fatalf("plan state after review = %q", state)
	}

	if content := product.callOK(t, ToolExitPlanMode, `{"action":"status"}`); content != "plan mode: reviewing" {
		t.Fatalf("status content = %q", content)
	}
	if content := product.callOK(t, ToolExitPlanMode, `{"action":"approve"}`); content != "plan mode: approved" {
		t.Fatalf("approve content = %q", content)
	}
	review = <-reviews
	if review.State != "approved" || review.Content != "# Release plan\n1. test" {
		t.Fatalf("approved snapshot = %+v", review)
	}
	if state := uiRoot.PlanReviewer().State(); state != "approved" {
		t.Fatalf("plan state after approve = %q", state)
	}
	if content := product.callError(t, ToolExitPlanMode, `{"action":"approve"}`); !strings.Contains(content, "no plan is under review") {
		t.Fatalf("second approve = %q", content)
	}
	if content := product.callError(t, ToolExitPlanMode, `{"action":"bogus"}`); !strings.Contains(content, "unknown plan action") {
		t.Fatalf("bogus content = %q", content)
	}
}

// TestInteractiveFalseKeepsArtifactBridgeHidesPlanTools pins the headless
// product slice: interactive=false keeps the artifact bridge and the other host
// tools, but the plan-review tools are not registered — no user could ever
// approve a plan there (ZK-07).
func TestInteractiveFalseKeepsArtifactBridgeHidesPlanTools(t *testing.T) {
	uiRoot := uiroot.New(uiroot.Config{AppName: "test"})
	artifacts := make(chan contracts.Artifact, 1)
	uiRoot.ArtifactPresenter().SetArtifactHandler(func(artifact contracts.Artifact) { artifacts <- artifact })
	product := loadBridgeProduct(t, uiRoot, false)

	if content := product.callOK(t, ToolPresent, `{"title":"T","content":"B"}`); !strings.Contains(content, "artifact-1") {
		t.Fatalf("present with interactive=false = %q", content)
	}
	<-artifacts
	if content := product.callError(t, ToolAskUser, `{"question":"q"}`); !strings.Contains(content, "no interactive UI") {
		t.Fatalf("ask_user with interactive=false = %q", content)
	}
	for _, name := range []string{ToolPlanReview, ToolExitPlanMode} {
		if _, err := product.call(t, name, `{}`); !errors.Is(err, rig.ErrToolNotFound) {
			t.Fatalf("%s with interactive=false = %v, want rig.ErrToolNotFound", name, err)
		}
	}
}

func TestGoalToolUsesGoalTrackerBridge(t *testing.T) {
	uiRoot := uiroot.New(uiroot.Config{AppName: "test"})
	events := make(chan struct {
		goal contracts.Goal
		ok   bool
	}, 8)
	uiRoot.GoalTracker().SetHandler(func(goal contracts.Goal, ok bool) {
		events <- struct {
			goal contracts.Goal
			ok   bool
		}{goal: goal, ok: ok}
	})
	product := loadBridgeProduct(t, uiRoot, true)

	content := product.callOK(t, ToolGoal, `{"action":"set","text":"Ship v0.5"}`)
	if !strings.Contains(content, "goal-1") || !strings.Contains(content, "Ship v0.5") {
		t.Fatalf("goal set content = %q", content)
	}
	id, text, status, ok := uiRoot.GoalTracker().Get()
	if !ok || id != "goal-1" || text != "Ship v0.5" || status != "active" {
		t.Fatalf("goal tracker after set = %q/%q/%q/%v", id, text, status, ok)
	}
	event := <-events
	if !event.ok || event.goal.Text != "Ship v0.5" {
		t.Fatalf("goal set event = %+v", event)
	}

	if content := product.callOK(t, ToolGoal, `{"action":"get"}`); content != "goal goal-1 [active]: Ship v0.5" {
		t.Fatalf("goal get content = %q", content)
	}
	if content := product.callOK(t, ToolGoal, `{"action":"status"}`); !strings.Contains(content, "Ship v0.5") {
		t.Fatalf("goal status content = %q", content)
	}
	if content := product.callOK(t, ToolGoal, `{"action":"clear"}`); content != "goal cleared" {
		t.Fatalf("goal clear content = %q", content)
	}
	if _, _, _, ok := uiRoot.GoalTracker().Get(); ok {
		t.Fatal("goal survived clear")
	}
	event = <-events
	if event.ok {
		t.Fatalf("goal clear event = %+v, want ok=false", event)
	}
	if content := product.callOK(t, ToolGoal, `{"action":"get"}`); content != "no goal is set" {
		t.Fatalf("goal get after clear = %q", content)
	}
}
