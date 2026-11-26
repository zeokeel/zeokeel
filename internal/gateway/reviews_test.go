package gateway

// tasks.reviews: the read half of the audit trail.
//
// The decision itself (`tasks.review`) deletes the lease it describes, so the
// audit trail is the only record that survives — the task screen reads it here.
// What this file protects is the seam: the method is dispatched and classified,
// the DTO carries exactly the fields the screen needs, "never reviewed" is an
// empty list rather than an error, and a build without the capability says so
// instead of answering "no reviews".

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// reviewsFakeLease is the lease service plus the optional review log. Every
// other method panics through the nil embedded interface: the read must only
// call Reviews.
type reviewsFakeLease struct {
	contracts.TaskLeaseService
	reviews []contracts.TaskReview
	err     error
}

func (f *reviewsFakeLease) Reviews(context.Context, string) ([]contracts.TaskReview, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.reviews, nil
}

func reviewParams(t *testing.T, params map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal %v: %v", params, err)
	}
	return raw
}

func TestTaskReviewsIsDispatchedAndDeclared(t *testing.T) {
	if !slices.Contains(SupportedMethods(), "tasks.reviews") {
		t.Fatal("tasks.reviews is not dispatched")
	}
	if _, ok := contractParams["tasks.reviews"]; !ok {
		t.Fatal("tasks.reviews is dispatched but not classified in contractParams")
	}
	spec, err := paramSpecOf(taskReviewsParams{})
	if err != nil {
		t.Fatalf("paramSpecOf: %v", err)
	}
	if len(spec.Required) != 1 || spec.Required[0] != "id" {
		t.Fatalf("required = %v, want [id]", spec.Required)
	}
}

func TestTaskReviewsAnswersTheRecordedDecisions(t *testing.T) {
	at := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	lease := &reviewsFakeLease{reviews: []contracts.TaskReview{
		{
			TaskID: "task-1", Reviewer: contracts.PresetAuditorID, Role: contracts.PresetAuditorID,
			Decision: contracts.TaskReviewReject, Verdict: contracts.TaskVerdictFail,
			Findings: "缺来源", At: at,
		},
		{
			TaskID: "task-1", Reviewer: contracts.PresetLeadID, Role: contracts.PresetLeadID,
			Decision: contracts.TaskReviewAccept, Verdict: contracts.TaskVerdictPass,
			Findings: "证据齐全", At: at.Add(time.Hour),
		},
	}}
	gw := newReviewGateway(t, lease, nil)

	out, problem := gw.taskReviews(context.Background(), reviewParams(t, map[string]any{"id": "task-1"}))
	if problem != nil {
		t.Fatalf("taskReviews: %+v", problem)
	}
	result, ok := out.(taskReviewsResult)
	if !ok {
		t.Fatalf("answer = (%T) %+v, want taskReviewsResult", out, out)
	}
	if len(result.Reviews) != 2 {
		t.Fatalf("reviews = %+v, want two", result.Reviews)
	}
	first := result.Reviews[0]
	if first.Decision != contracts.TaskReviewReject || first.Verdict != contracts.TaskVerdictFail ||
		first.Findings != "缺来源" || first.Role != contracts.PresetAuditorID ||
		first.Reviewer != contracts.PresetAuditorID || !strings.HasPrefix(first.At, "2026-11-26T10:00:00") {
		t.Fatalf("first review = %+v, want the rejection with its findings", first)
	}
	// The wire shape the screen reads: decision + verdict are always present.
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"decision":"reject"`, `"verdict":"fail"`, `"findings":"缺来源"`, `"role":"auditor"`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("wire %s is missing %s", encoded, want)
		}
	}
}

// TestTaskReviewsOfATaskNobodyReviewed: an empty answer is legal and is not an
// error — "never reviewed" is a statement the screen renders.
func TestTaskReviewsOfATaskNobodyReviewed(t *testing.T) {
	gw := newReviewGateway(t, &reviewsFakeLease{}, nil)

	out, problem := gw.taskReviews(context.Background(), reviewParams(t, map[string]any{"id": "task-1"}))
	if problem != nil {
		t.Fatalf("taskReviews: %+v", problem)
	}
	encoded, err := json.Marshal(out)
	if err != nil || string(encoded) != `{"reviews":null}` {
		t.Fatalf("answer = %s (%v), want {\"reviews\":null}", encoded, err)
	}
}

func TestTaskReviewsValidatesTheTaskID(t *testing.T) {
	gw := newReviewGateway(t, &reviewsFakeLease{}, nil)
	for _, params := range []map[string]any{{}, {"id": "   "}} {
		_, problem := gw.taskReviews(context.Background(), reviewParams(t, params))
		if problem == nil || problem.Code != codeInvalidParams {
			t.Fatalf("params %v problem = %+v, want codeInvalidParams", params, problem)
		}
		if problem.Data["field"] != "id" {
			t.Fatalf("params %v field = %v, want id", params, problem.Data["field"])
		}
	}
}

// TestTaskReviewRefusesSelfReview: the constraint's wire half. Reviewing as the
// executor is the request's problem (the client should act as the audit role),
// so it answers -32600 with the typed text instead of "something broke".
func TestTaskReviewRefusesSelfReview(t *testing.T) {
	lease := &selfReviewLease{}
	gw := newReviewGateway(t, lease, nil)

	problem := review(t, gw, map[string]any{
		"id": "task-1", "decision": "accept", "reviewer": contracts.PresetExecutorID,
	})
	if problem == nil || problem.Code != codeInvalidRequest ||
		!strings.Contains(problem.Message, "reviewer is the executor") {
		t.Fatalf("problem = %+v, want codeInvalidRequest with the typed text", problem)
	}
	if !lease.accepted {
		// The fake is the one refusing; this asserts the call really arrived at
		// the lease layer instead of being refused by the gateway on its own.
		t.Fatal("the review never reached the lease service")
	}
}

// selfReviewLease is a lease service that refuses the self-review the way the
// real one does (the gateway must map the typed error, not invent its own rule).
type selfReviewLease struct {
	contracts.TaskLeaseService
	accepted bool
}

func (f *selfReviewLease) Accept(_ context.Context, taskID, reviewer, note string) (contracts.TaskLease, error) {
	f.accepted = true
	return contracts.TaskLease{}, contracts.ErrLeaseReviewerIsOwner
}

func (f *selfReviewLease) Reject(_ context.Context, taskID, reviewer, reason string) (contracts.TaskLease, error) {
	return contracts.TaskLease{}, contracts.ErrLeaseReviewerIsOwner
}

// TestTaskReviewsWithoutTheCapability: a lease service that cannot report the
// trail must not answer "no reviews" — that would be a false statement.
func TestTaskReviewsWithoutTheCapability(t *testing.T) {
	gw := newReviewGateway(t, &fakeLease{}, nil)

	_, problem := gw.taskReviews(context.Background(), reviewParams(t, map[string]any{"id": "task-1"}))
	if problem == nil || problem.Code != codeInternalError {
		t.Fatalf("problem = %+v, want codeInternalError", problem)
	}
	if problem.Data["name"] != "review_log_unavailable" {
		t.Fatalf("problem name = %v, want review_log_unavailable", problem.Data["name"])
	}
}
