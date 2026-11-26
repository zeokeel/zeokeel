package agent

// The prompt/gate agreement test (2026-11-26 audit, 高).
//
// The finding: under a session-level read-only override the model was told that
// "read-only and informational tools, MCP tools and shell commands run without
// approval", while gate() refused shell and MCP outright — no card, just a
// denial. The model then retried what the prompt had just promised.
//
// The fix is structural (permission_policy.go): the sentence is rendered from
// approvalPolicyClaims, and the session-override layer of the gate calls the same
// overridePermission. This test closes the loop behaviorally — for every tier and
// every claim, the REAL gate is asked the same question and must give the answer
// the sentence states. A future tightening of either side fails here.

import (
	"context"
	"strings"
	"sync"
	"testing"

	rig "github.com/zeokeel/zeokeel-rig"
	rigtools "github.com/zeokeel/zeokeel-rig/tools"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// policyApprovals is the approvals service double this test needs: it applies the
// SAME mode/set table the shipping service applies (auto-allow a preset's set,
// deny-all for "never", a card otherwise) and records which tools reached the
// card. Using a deny-always fake would make "the gate asked" and "the product
// shows a card" indistinguishable.
type policyApprovals struct {
	mode string
	mu   sync.Mutex
	// cards records the tools that reached the dialog.
	cards []string
}

func (a *policyApprovals) Mode() string {
	if a.mode == "" {
		return contracts.PermissionWorkspace
	}
	return a.mode
}

func (a *policyApprovals) Request(_ context.Context, request contracts.ApprovalRequest) (contracts.ApprovalDecision, error) {
	switch normalizeApprovalMode(a.Mode()) {
	case contracts.PermissionFull:
		return contracts.ApprovalAllow, nil
	case contracts.PermissionReadOnly:
		if contracts.ReadOnlyAutoAllowed(request.Tool) {
			return contracts.ApprovalAllow, nil
		}
	case contracts.PermissionWorkspace:
		if contracts.WorkspaceAutoAllowed(request.Tool) {
			return contracts.ApprovalAllow, nil
		}
	}
	a.mu.Lock()
	a.cards = append(a.cards, request.Tool)
	a.mu.Unlock()
	return contracts.ApprovalAllow, nil
}

func (a *policyApprovals) Pending() []contracts.ApprovalRequest { return nil }

func (a *policyApprovals) Resolve(string, contracts.ApprovalDecision) error { return nil }

func (a *policyApprovals) Subscribe(func()) (func(), error) { return func() {}, nil }

// cardFor reports whether tool reached the approval dialog.
func (a *policyApprovals) cardFor(tool string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, card := range a.cards {
		if card == tool {
			return true
		}
	}
	return false
}

// deniedTools is the headless "never" policy: a deny-all assembly.
type deniedApprovals struct{ mode string }

func (a deniedApprovals) Mode() string {
	if a.mode == "" {
		return "never"
	}
	return a.mode
}

func (a deniedApprovals) Request(context.Context, contracts.ApprovalRequest) (contracts.ApprovalDecision, error) {
	return contracts.ApprovalDeny, nil
}

func (a deniedApprovals) Pending() []contracts.ApprovalRequest             { return nil }
func (a deniedApprovals) Resolve(string, contracts.ApprovalDecision) error { return nil }
func (a deniedApprovals) Subscribe(func()) (func(), error)                 { return func() {}, nil }

// TestApprovalPolicyClaimsMatchTheGate is the audit's regression test: the
// sentence the model reads and the decision the gate makes agree for every
// (tier, tool) combination the sentence talks about — most importantly, a
// read-only session override REFUSES shell commands and MCP tools instead of
// calling them exempt.
func TestApprovalPolicyClaimsMatchTheGate(t *testing.T) {
	// Two assemblies: one with the sandbox module (commands exempt), one without
	// (commands keep the ordinary approval flow). The tier says which.
	sandbox, sandboxPolicy := healthySandbox(t)
	cases := []struct {
		name      string
		policy    approvalPolicy
		sandbox   contracts.SandboxService
		policySrc contracts.ToolPolicy
		approvals contracts.Approvals
		override  string
		wantCards bool // sanity: at least one claim reaches the dialog
	}{
		{
			name:      "global workspace",
			policy:    approvalPolicy{Mode: contracts.PermissionWorkspace, ApprovalsAvailable: true, SandboxAssembled: true},
			sandbox:   sandbox,
			policySrc: sandboxPolicy,
			approvals: &policyApprovals{mode: contracts.PermissionWorkspace},
			wantCards: true,
		},
		{
			name:      "global read-only",
			policy:    approvalPolicy{Mode: contracts.PermissionReadOnly, ApprovalsAvailable: true, SandboxAssembled: true},
			sandbox:   sandbox,
			policySrc: sandboxPolicy,
			approvals: &policyApprovals{mode: contracts.PermissionReadOnly},
			wantCards: true,
		},
		{
			name:      "global full",
			policy:    approvalPolicy{Mode: contracts.PermissionFull, ApprovalsAvailable: true, SandboxAssembled: true},
			sandbox:   sandbox,
			policySrc: sandboxPolicy,
			approvals: &policyApprovals{mode: contracts.PermissionFull},
		},
		{
			// The audited combination: a read-only session override under a
			// workspace global preset refuses the command and MCP families.
			name:      "session read-only override",
			policy:    approvalPolicy{Mode: contracts.PermissionWorkspace, SessionOverride: contracts.PermissionReadOnly, ApprovalsAvailable: true, SandboxAssembled: true},
			sandbox:   sandbox,
			policySrc: sandboxPolicy,
			approvals: &policyApprovals{mode: contracts.PermissionWorkspace},
			override:  contracts.PermissionReadOnly,
		},
		{
			name:      "session workspace override",
			policy:    approvalPolicy{Mode: contracts.PermissionWorkspace, SessionOverride: contracts.PermissionWorkspace, ApprovalsAvailable: true, SandboxAssembled: true},
			sandbox:   sandbox,
			policySrc: sandboxPolicy,
			approvals: &policyApprovals{mode: contracts.PermissionWorkspace},
			override:  contracts.PermissionWorkspace,
			wantCards: true,
		},
		{
			name:      "session full override",
			policy:    approvalPolicy{Mode: contracts.PermissionWorkspace, SessionOverride: contracts.PermissionFull, ApprovalsAvailable: true, SandboxAssembled: true},
			sandbox:   sandbox,
			policySrc: sandboxPolicy,
			approvals: &policyApprovals{mode: contracts.PermissionWorkspace},
			override:  contracts.PermissionFull,
		},
		{
			// A trimmed assembly with no approvals service: the gate fails closed,
			// so nothing is claimed as running without approval.
			name:      "no approvals service",
			policy:    approvalPolicy{Mode: contracts.PermissionWorkspace, ApprovalsAvailable: false, SandboxAssembled: true},
			sandbox:   sandbox,
			policySrc: sandboxPolicy,
		},
		{
			// A trimmed assembly with no sandbox module: commands are NOT exempt,
			// they reach the dialog like any other non-auto-allowed tool.
			name:      "no sandbox module",
			policy:    approvalPolicy{Mode: contracts.PermissionWorkspace, ApprovalsAvailable: true, SandboxAssembled: false},
			approvals: &policyApprovals{mode: contracts.PermissionWorkspace},
			wantCards: true,
		},
		{
			// A deny-all assembly reports the retired "never" mode, which
			// normalizes to the read-only preset, and DenyAll is derived from it.
			name:      "headless deny-all",
			policy:    approvalPolicy{Mode: contracts.PermissionReadOnly, ApprovalsAvailable: true, DenyAll: true, SandboxAssembled: true},
			sandbox:   sandbox,
			policySrc: sandboxPolicy,
			approvals: deniedApprovals{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newService(Config{}, rigtools.NewRegistry(),
				contracts.SettingsService(newFakeSettings(nil)), &fakeLogger{}, newFakeStore(), tc.approvals)
			svc.sandboxService = tc.sandbox
			svc.policySource = tc.policySrc
			sessionID, err := svc.NewSession("")
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			if tc.override != "" {
				if err := svc.SetSessionPermission(sessionID, tc.override); err != nil {
					t.Fatalf("SetSessionPermission(%q): %v", tc.override, err)
				}
			}

			// The tier the prompt would render from must be the tier this case
			// declares: if the service resolves something else, the comparison
			// below would be about a different policy than the sentence.
			resolved := svc.effectiveApprovalPolicy(sessionID).normalized()
			if resolved.Mode != tc.policy.normalized().Mode ||
				resolved.SessionOverride != tc.policy.normalized().SessionOverride ||
				resolved.ApprovalsAvailable != tc.policy.ApprovalsAvailable ||
				resolved.DenyAll != tc.policy.DenyAll ||
				resolved.SandboxAssembled != tc.policy.SandboxAssembled {
				t.Fatalf("resolved tier = %+v, want %+v", resolved, tc.policy.normalized())
			}

			claims := approvalPolicyClaims(resolved)
			if len(claims) == 0 {
				t.Fatal("the tier produced no claims")
			}
			cards := 0
			for _, claim := range claims {
				decision, _ := svc.gate(sessionID).Approve(context.Background(),
					&gateTestTool{name: claim.Tool}, rig.ToolCall{Name: claim.Tool})
				card := false
				if recorder, ok := tc.approvals.(*policyApprovals); ok {
					card = recorder.cardFor(claim.Tool)
				}
				if card {
					cards++
				}
				switch claim.Disposition {
				case dispositionAllowed:
					if decision != rig.DecisionAllow || card {
						t.Fatalf("%s (%s): claim=allowed but gate=%q card=%v", claim.Tool, claim.Family, decision, card)
					}
				case dispositionRefused:
					// The half the audit was about: a refusal must be a refusal,
					// never a dialog the user can approve. An empty decision is
					// the SDK's fail-closed form (funcGate maps "" to deny, and
					// gate errors abort with ""), so "not allow and no card" is
					// the honest predicate.
					if decision == rig.DecisionAllow || card {
						t.Fatalf("%s (%s): claim=refused but gate=%q card=%v", claim.Tool, claim.Family, decision, card)
					}
				case dispositionAsks:
					if !card {
						t.Fatalf("%s (%s): claim=asks but no approval card was requested (gate=%q)", claim.Tool, claim.Family, decision)
					}
				default:
					t.Fatalf("%s (%s): unknown disposition %q", claim.Tool, claim.Family, claim.Disposition)
				}
			}
			if tc.wantCards && cards == 0 {
				t.Fatal("no claim reached the approval dialog; the fake approvals service is not being consulted")
			}
		})
	}
}

// TestReadOnlyOverrideSentenceNamesTheRefusedFamilies pins the user-visible half
// of the fix: the sentence must SAY that shell commands and MCP tools are
// refused — naming them — instead of describing them as exempt.
func TestReadOnlyOverrideSentenceNamesTheRefusedFamilies(t *testing.T) {
	sentence := approvalPolicyLine(approvalPolicy{
		Mode: contracts.PermissionWorkspace, SessionOverride: contracts.PermissionReadOnly,
		ApprovalsAvailable: true, SandboxAssembled: true,
	})
	for _, want := range []string{
		"Approval policy: read-only —",
		"shell commands (bash/pwsh)",
		"MCP tools",
		"refused outright",
		"A refusal is not a prompt",
	} {
		if !strings.Contains(sentence, want) {
			t.Fatalf("read-only override sentence %q is missing %q", sentence, want)
		}
	}
	// The exemption wording that made the model retry must be gone for this tier.
	if strings.Contains(sentence, "MCP tools and shell commands run without approval") {
		t.Fatalf("read-only override sentence still calls them exempt: %q", sentence)
	}
}

// TestUnconfinedLineSilentWhenCommandsAreRefused: a tier that refuses the command
// family has no fence to disclose — warning about "unconfined commands" there
// would suggest an operation that cannot happen.
func TestUnconfinedLineSilentWhenCommandsAreRefused(t *testing.T) {
	policy := approvalPolicy{
		Mode: contracts.PermissionWorkspace, SessionOverride: contracts.PermissionReadOnly,
		ApprovalsAvailable: true, SandboxAssembled: true,
		Confinement: contracts.SandboxConfinementNone,
	}
	if line := unconfinedCommandsLine(policy); line != "" {
		t.Fatalf("refused commands produced an unconfined disclosure: %q", line)
	}
}

// sentenceAliases are the ways a claim family may be named in prose. The legacy
// spellings are here on purpose: this test must be able to read the OLD hand-
// written sentences too, so that it reports the lie it was written to catch
// instead of only noticing that the vocabulary changed.
var sentenceAliases = map[string][]string{
	"read-only and informational tools": {"read-only and informational tools", "read-only/informational tools"},
	"file writes":                       {"file writes", "file tools"},
	"shell commands (bash/pwsh)":        {"shell commands (bash/pwsh)", "shell commands"},
	"MCP tools":                         {"MCP tools"},
	"network tools":                     {"network tools", "network and settings writes", "network"},
	"settings writes":                   {"settings writes", "network and settings writes"},
	"interaction tools":                 {"interaction tools", "ask_user"},
}

// sentenceClaimIn reads the disposition one claim family is given BY THE SENTENCE
// TEXT. It deliberately parses prose instead of calling approvalPolicyClaims: a
// sentence hardcoded separately from the claim table must not be able to pass by
// agreeing with itself (2026-11-26 audit, 高: the literal sentence test was
// exactly why the false claims stayed green).
//
// ok is false when the sentence says nothing about the family at all.
func sentenceClaimIn(sentence, family string) (toolDisposition, bool) {
	at := -1
	for _, alias := range append([]string{family}, sentenceAliases[family]...) {
		if cut := strings.Index(sentence, alias); cut >= 0 && (at < 0 || cut < at) {
			at = cut
		}
	}
	if at < 0 {
		return "", false
	}
	start := 0
	for _, separator := range []string{"; ", " — "} {
		if cut := strings.LastIndex(sentence[:at], separator); cut >= 0 && cut+len(separator) > start {
			start = cut + len(separator)
		}
	}
	end := len(sentence)
	for _, separator := range []string{"; ", ". ", "."} {
		if cut := strings.Index(sentence[at:], separator); cut >= 0 && at+cut < end {
			end = at + cut
		}
	}
	clause := sentence[start:end]
	switch {
	case strings.Contains(clause, "run without approval"):
		return dispositionAllowed, true
	case strings.Contains(clause, "refused outright"):
		return dispositionRefused, true
	case strings.Contains(clause, "may ask for approval"):
		return dispositionAsks, true
	}
	return "", false
}

// TestApprovalSentenceAgreesWithTheGate is the audit's third fork closed
// behaviourally: for every tier the prompt can describe, every family the
// sentence talks about is put in front of the REAL gate, and what the sentence
// said must be what the gate then does.
//
// The rule for a family the sentence never mentions: silence is only honest when
// the gate has nothing to warn about (it allows the call and asks nobody). A
// refusal or a dialog the sentence kept quiet about is the failure mode the audit
// found.
//
// The three combinations the audit named are all here, because the session
// override was not the only place the prose was written by hand:
//   - no approvals service: the global sentence promised "shell/MCP run without
//     approval" while the gate fails closed on every family;
//   - a headless deny-all assembly: "never" normalizes to read-only, so the
//     sentence promised the read-only exemptions while every branch denies;
//   - the sandbox module assembled with no command policy: the claim said the
//     command family was exempt while shellCommandExempt needs the policy to
//     judge the call, so the call reached the approval dialog instead.
func TestApprovalSentenceAgreesWithTheGate(t *testing.T) {
	sandbox, sandboxPolicy := healthySandbox(t)
	cases := []struct {
		name      string
		policy    approvalPolicy
		sandbox   contracts.SandboxService
		policySrc contracts.ToolPolicy
		approvals contracts.Approvals
		override  string
		wantCards bool
	}{
		{
			name:      "shipped workspace",
			policy:    approvalPolicy{Mode: contracts.PermissionWorkspace, ApprovalsAvailable: true, SandboxAssembled: true},
			sandbox:   sandbox,
			policySrc: sandboxPolicy,
			approvals: &policyApprovals{mode: contracts.PermissionWorkspace},
			wantCards: true,
		},
		{
			name:      "shipped read-only",
			policy:    approvalPolicy{Mode: contracts.PermissionReadOnly, ApprovalsAvailable: true, SandboxAssembled: true},
			sandbox:   sandbox,
			policySrc: sandboxPolicy,
			approvals: &policyApprovals{mode: contracts.PermissionReadOnly},
			wantCards: true,
		},
		{
			name:      "shipped full",
			policy:    approvalPolicy{Mode: contracts.PermissionFull, ApprovalsAvailable: true, SandboxAssembled: true},
			sandbox:   sandbox,
			policySrc: sandboxPolicy,
			approvals: &policyApprovals{mode: contracts.PermissionFull},
		},
		{
			// Fork 1: no approvals service. The gate fails closed everywhere a
			// prompt would be needed; the sentence must not call those families
			// exempt.
			name:      "no approvals service",
			policy:    approvalPolicy{Mode: contracts.PermissionWorkspace, ApprovalsAvailable: false, SandboxAssembled: true},
			sandbox:   sandbox,
			policySrc: sandboxPolicy,
		},
		{
			// The same fork under the full preset: "no approval prompts are
			// shown" reads as "nothing to approve" while the gate refuses every
			// family it cannot ask about.
			name:      "no approvals service, full preset",
			policy:    approvalPolicy{Mode: contracts.PermissionFull, ApprovalsAvailable: false, SandboxAssembled: true},
			sandbox:   sandbox,
			policySrc: sandboxPolicy,
		},
		{
			// Fork 2: a headless deny-all assembly. "never" normalizes to the
			// read-only preset, and denyAllApprovalPolicy vetoes every branch.
			name:      "headless deny-all",
			policy:    approvalPolicy{Mode: contracts.PermissionReadOnly, ApprovalsAvailable: true, DenyAll: true, SandboxAssembled: true},
			sandbox:   sandbox,
			policySrc: sandboxPolicy,
			approvals: deniedApprovals{},
		},
		{
			// Fork 3: the sandbox module is assembled but the assembly has no
			// command policy, so shellCommandExempt cannot judge a call and the
			// command family reaches the approval dialog.
			name:      "sandbox assembled without a command policy",
			policy:    approvalPolicy{Mode: contracts.PermissionWorkspace, ApprovalsAvailable: true, SandboxAssembled: true},
			sandbox:   sandbox,
			approvals: &policyApprovals{mode: contracts.PermissionWorkspace},
			wantCards: true,
		},
		{
			// The command family is not exempt without the sandbox module.
			name:      "no sandbox module",
			policy:    approvalPolicy{Mode: contracts.PermissionWorkspace, ApprovalsAvailable: true, SandboxAssembled: false},
			approvals: &policyApprovals{mode: contracts.PermissionWorkspace},
			wantCards: true,
		},
		{
			name:      "session read-only override",
			policy:    approvalPolicy{Mode: contracts.PermissionWorkspace, SessionOverride: contracts.PermissionReadOnly, ApprovalsAvailable: true, SandboxAssembled: true},
			sandbox:   sandbox,
			policySrc: sandboxPolicy,
			approvals: &policyApprovals{mode: contracts.PermissionWorkspace},
			override:  contracts.PermissionReadOnly,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newService(Config{}, rigtools.NewRegistry(),
				contracts.SettingsService(newFakeSettings(nil)), &fakeLogger{}, newFakeStore(), tc.approvals)
			svc.sandboxService = tc.sandbox
			svc.policySource = tc.policySrc
			sessionID, err := svc.NewSession("")
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			if tc.override != "" {
				if err := svc.SetSessionPermission(sessionID, tc.override); err != nil {
					t.Fatalf("SetSessionPermission(%q): %v", tc.override, err)
				}
			}

			resolved := svc.effectiveApprovalPolicy(sessionID).normalized()
			if resolved.ApprovalsAvailable != tc.policy.ApprovalsAvailable ||
				resolved.SandboxAssembled != tc.policy.SandboxAssembled ||
				resolved.DenyAll != tc.policy.DenyAll {
				t.Fatalf("resolved tier = %+v, want %+v", resolved, tc.policy)
			}
			sentence := approvalPolicyLine(resolved)
			claims := approvalPolicyClaims(resolved)
			if len(claims) == 0 {
				t.Fatal("the tier produced no claims")
			}

			named := 0
			cards := 0
			for _, claim := range claims {
				decision, _ := svc.gate(sessionID).Approve(context.Background(),
					&gateTestTool{name: claim.Tool}, rig.ToolCall{Name: claim.Tool})
				card := false
				if recorder, ok := tc.approvals.(*policyApprovals); ok {
					card = recorder.cardFor(claim.Tool)
				}
				if card {
					cards++
				}
				stated, said := sentenceClaimIn(sentence, claim.Family)
				if !said {
					// Silence is only honest when the gate has nothing to warn
					// about: it allows the call and asks nobody.
					if decision != rig.DecisionAllow || card {
						t.Fatalf("%s (%s): the sentence does not mention this family, but the gate says %q (card=%v). Sentence: %q",
							claim.Tool, claim.Family, decision, card, sentence)
					}
					continue
				}
				named++
				switch stated {
				case dispositionAllowed:
					if decision != rig.DecisionAllow || card {
						t.Fatalf("%s (%s): the sentence says \"run without approval\", the gate says %q (card=%v). Sentence: %q",
							claim.Tool, claim.Family, decision, card, sentence)
					}
				case dispositionRefused:
					if decision == rig.DecisionAllow || card {
						t.Fatalf("%s (%s): the sentence says \"refused outright\", the gate says %q (card=%v). Sentence: %q",
							claim.Tool, claim.Family, decision, card, sentence)
					}
				case dispositionAsks:
					if !card {
						t.Fatalf("%s (%s): the sentence says a prompt may appear, no card was requested (gate %q). Sentence: %q",
							claim.Tool, claim.Family, decision, sentence)
					}
				default:
					t.Fatalf("%s (%s): the sentence states an unknown disposition %q", claim.Tool, claim.Family, stated)
				}
			}
			if named == 0 && !strings.Contains(sentence, "no approval prompts are shown") {
				t.Fatalf("the sentence names no family at all and is not the all-exempt form: %q", sentence)
			}
			if tc.wantCards && cards == 0 {
				t.Fatal("no family reached the approval dialog; the fake approvals service is not being consulted")
			}
		})
	}
}
