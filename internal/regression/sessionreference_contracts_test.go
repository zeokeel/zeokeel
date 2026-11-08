package regression

// Adversarial audit: the session-reference contract details. Candidate ranking
// (exact ID > ID prefix > title, empty-query recency, cap 8), the
// sessionref: URI codec round trips / invalid inputs, typed budget errors, and
// the session_refs event's budget field when a reference block is truncated.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
	"github.com/zeokeel/zeokeel/internal/modules/agent"
)

// TestSessionReferenceCandidateRanking covers the @menu ranking contract.
func TestSessionReferenceCandidateRanking(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	candidates := []contracts.SessionReferenceCandidate{
		{SessionID: "sess-alpha", Title: "Alpha planning", UpdatedAt: base.Add(3 * time.Minute)},
		{SessionID: "sess-alphabet", Title: "Beta review", UpdatedAt: base.Add(2 * time.Minute)},
		{SessionID: "other", Title: "alpha notes", UpdatedAt: base.Add(time.Minute)},
		{SessionID: "unrelated", Title: "Gamma", UpdatedAt: base},
		{SessionID: "sess-alpha", Title: "duplicate", Score: 5, UpdatedAt: base.Add(4 * time.Minute)},
	}
	ranked := contracts.RankSessionReferenceCandidates(candidates, "SESS-ALPHA", 8)
	if len(ranked) < 2 || ranked[0].SessionID != "sess-alpha" || ranked[0].Title != "duplicate" {
		t.Fatalf("exact-ID match did not pick the duplicate's best entry: %+v", ranked)
	}
	ranked = contracts.RankSessionReferenceCandidates(candidates, "sess-alp", 0)
	if len(ranked) != 2 || ranked[0].SessionID != "sess-alpha" || ranked[1].SessionID != "sess-alphabet" {
		t.Fatalf("ID-prefix ranking = %+v", ranked)
	}
	titleCandidates := []contracts.SessionReferenceCandidate{
		{SessionID: "sess-one", Title: "Alpha planning", UpdatedAt: base},
		{SessionID: "other", Title: "alpha notes", UpdatedAt: base.Add(time.Minute)},
	}
	ranked = contracts.RankSessionReferenceCandidates(titleCandidates, "alpha", 0)
	if len(ranked) != 2 || ranked[0].SessionID != "other" || ranked[1].SessionID != "sess-one" {
		t.Fatalf("title-contains ranking (newest tie-break) = %+v", ranked)
	}
	ranked = contracts.RankSessionReferenceCandidates(candidates, "review", 0)
	if len(ranked) != 1 || ranked[0].SessionID != "sess-alphabet" {
		t.Fatalf("title-contains single match = %+v", ranked)
	}
	if ranked = contracts.RankSessionReferenceCandidates(candidates, "no-such", 0); len(ranked) != 0 {
		t.Fatalf("non-matching query returned %+v", ranked)
	}
	// Empty query: every candidate, newest first, capped at the default limit 8.
	many := make([]contracts.SessionReferenceCandidate, 0, 12)
	for index := 0; index < 12; index++ {
		many = append(many, contracts.SessionReferenceCandidate{
			SessionID: "s" + itoa(int64(index)), UpdatedAt: base.Add(time.Duration(index) * time.Minute),
		})
	}
	ranked = contracts.RankSessionReferenceCandidates(many, "  ", 0)
	if len(ranked) != contracts.DefaultSessionReferenceLimit {
		t.Fatalf("default cap = %d, want %d", len(ranked), contracts.DefaultSessionReferenceLimit)
	}
	if ranked[0].SessionID != "s11" || ranked[len(ranked)-1].SessionID != "s4" {
		t.Fatalf("empty-query order = %+v", ranked)
	}
	if ranked = contracts.RankSessionReferenceCandidates(many, "", 2); len(ranked) != 2 || ranked[0].SessionID != "s11" {
		t.Fatalf("explicit cap = %+v", ranked)
	}
	// Blank/duplicate IDs are dropped or collapsed.
	ranked = contracts.RankSessionReferenceCandidates([]contracts.SessionReferenceCandidate{
		{SessionID: "   "}, {SessionID: "keep"},
	}, "", 8)
	if len(ranked) != 1 || ranked[0].SessionID != "keep" {
		t.Fatalf("blank candidate handling = %+v", ranked)
	}
}

// TestSessionReferenceURIAndBudget covers the URI codec round trip, invalid IDs
// and the typed budget error classification.
func TestSessionReferenceURIAndBudget(t *testing.T) {
	roundTrip := []string{"sess-1", "sess/one", "会话-一", "a.b_c-d", "with%percent"}
	for _, sessionID := range roundTrip {
		uri, err := contracts.EncodeSessionReferenceURI(sessionID)
		if err != nil {
			t.Fatalf("Encode(%q): %v", sessionID, err)
		}
		if !strings.HasPrefix(uri, contracts.SessionReferenceURIScheme) {
			t.Fatalf("URI %q lacks the scheme", uri)
		}
		decoded, err := contracts.DecodeSessionReferenceURI(uri)
		if err != nil || decoded != sessionID {
			t.Fatalf("round trip %q -> %q -> %q (err %v)", sessionID, uri, decoded, err)
		}
	}
	// A bare ID is accepted for older callers.
	if decoded, err := contracts.DecodeSessionReferenceURI("plain-id"); err != nil || decoded != "plain-id" {
		t.Fatalf("bare decode = %q, %v", decoded, err)
	}
	for _, invalid := range []string{"", "  ", "has space", "bad\x01char"} {
		if _, err := contracts.EncodeSessionReferenceURI(invalid); err == nil {
			t.Fatalf("Encode(%q) succeeded", invalid)
		}
	}
	if _, err := contracts.DecodeSessionReferenceURI(""); err == nil {
		t.Fatal("Decode(empty) succeeded")
	}
	if _, err := contracts.DecodeSessionReferenceURI("sessionref:%zz"); err == nil {
		t.Fatal("Decode(malformed percent) succeeded")
	}

	// Budget: messages checked before runes; errors.Is works; used reports the
	// overrun amount.
	err := contracts.CheckSessionReferenceBudget(-2, -5)
	if !errors.Is(err, contracts.ErrSessionReferenceBudget) {
		t.Fatalf("budget error is not typed: %v", err)
	}
	var typed *contracts.SessionReferenceBudgetError
	if !errors.As(err, &typed) || typed.Kind != "messages" || typed.Used != 2 {
		t.Fatalf("messages budget error = %+v", err)
	}
	err = contracts.CheckSessionReferenceBudget(0, -7)
	if !errors.As(err, &typed) || typed.Kind != "runes" || typed.Used != 7 {
		t.Fatalf("runes budget error = %+v", err)
	}
	if err := contracts.CheckSessionReferenceBudget(0, 0); err != nil {
		t.Fatalf("zero budget reported an error: %v", err)
	}
	if err := contracts.CheckSessionReferenceBudget(3, 100); err != nil {
		t.Fatalf("positive budget reported an error: %v", err)
	}
}

// TestSessionReferenceBudgetEvent covers the session_refs event: a referenced
// session with more than 20 messages truncates the block and records the typed
// budget fields; a small reference records no budget.
func TestSessionReferenceBudgetEvent(t *testing.T) {
	harness := startAgent(t, agent.Config{}, nil)
	ctx := context.Background()
	referenceID := harness.newChatSession(t, "Referenced")
	for index := 0; index < 11; index++ {
		// Each run adds a user + assistant message: 22 messages total (> the
		// 20-message injection cap).
		harness.sendAndWait(t, referenceID, "ref-"+itoa(int64(index)))
	}
	mainID := harness.newChatSession(t, "Main")
	harness.sendAndWait(t, mainID, contracts.FormatSessionReference(referenceID)+" summarize")

	store := requireService(t, harness.h.ctx, contracts.KeyStore)
	events, err := store.Events(ctx, mainID, 0, 0)
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	var refsPayload []byte
	for _, event := range events {
		if event.Type == "session_refs" {
			refsPayload = event.Payload
		}
	}
	if len(refsPayload) == 0 {
		t.Fatalf("session_refs event missing; events = %+v", events)
	}
	var payload struct {
		IDs    []string `json:"ids"`
		Budget *struct {
			Kind  string `json:"kind"`
			Limit int    `json:"limit"`
			Used  int    `json:"used"`
		} `json:"budget"`
	}
	if err := json.Unmarshal(refsPayload, &payload); err != nil {
		t.Fatalf("decode session_refs payload: %v (%s)", err, refsPayload)
	}
	if len(payload.IDs) != 1 || payload.IDs[0] != referenceID {
		t.Fatalf("session_refs ids = %v", payload.IDs)
	}
	if payload.Budget == nil || payload.Budget.Kind != "messages" || payload.Budget.Used != 2 {
		t.Fatalf("session_refs budget = %+v, want messages/used 2", payload.Budget)
	}
	// The injected transcript keeps the tail (last 20 messages): ref-0 is out,
	// ref-10 is in.
	body := string(harness.model.lastBody())
	if !strings.Contains(body, "ref-10") {
		t.Fatalf("injected context lacks the newest reference message")
	}
	if strings.Contains(body, "ref-0\n") || strings.Contains(body, `ref-0"`) {
		t.Fatalf("injected context kept a message beyond the 20-message cap")
	}

	// A small referenced session records no budget field.
	smallID := harness.newChatSession(t, "Small ref")
	harness.sendAndWait(t, smallID, "small message")
	main2 := harness.newChatSession(t, "Main2")
	harness.sendAndWait(t, main2, contracts.FormatSessionReference(smallID)+" summarize")
	events, err = store.Events(ctx, main2, 0, 0)
	if err != nil {
		t.Fatalf("read small events: %v", err)
	}
	for _, event := range events {
		if event.Type != "session_refs" {
			continue
		}
		if strings.Contains(string(event.Payload), `"budget"`) {
			t.Fatalf("small reference recorded a budget: %s", event.Payload)
		}
	}
}
