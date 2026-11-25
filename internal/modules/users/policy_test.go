package users

import (
	"testing"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// TestAllowedMatrix is the permission contract the gateway will enforce. The
// expected answers are spelled out one cell at a time on purpose: this table is
// the thing a reviewer should be able to check against the plan, so a test that
// derived the expectation from the implementation would prove nothing.
//
//	action            owner  manager  collaborator
//	dispatch          yes    yes      yes
//	approve           yes    yes      no
//	manage-bots       yes    no       no
//	review-memory     yes    yes      no
//	decide-budget     yes    yes      no
func TestAllowedMatrix(t *testing.T) {
	actions := []contracts.UserAction{
		contracts.ActionDispatch,
		contracts.ActionApprove,
		contracts.ActionManageBots,
		contracts.ActionReviewMemory,
		contracts.ActionDecideBudget,
	}
	want := map[string]map[contracts.UserAction]bool{
		contracts.UserRoleOwner: {
			contracts.ActionDispatch:     true,
			contracts.ActionApprove:      true,
			contracts.ActionManageBots:   true,
			contracts.ActionReviewMemory: true,
			contracts.ActionDecideBudget: true,
		},
		contracts.UserRoleManager: {
			contracts.ActionDispatch:     true,
			contracts.ActionApprove:      true,
			contracts.ActionManageBots:   false,
			contracts.ActionReviewMemory: true,
			contracts.ActionDecideBudget: true,
		},
		contracts.UserRoleCollaborator: {
			contracts.ActionDispatch:     true,
			contracts.ActionApprove:      false,
			contracts.ActionManageBots:   false,
			contracts.ActionReviewMemory: false,
			contracts.ActionDecideBudget: false,
		},
	}

	for role, row := range want {
		for _, action := range actions {
			expected, stated := row[action]
			if !stated {
				t.Fatalf("test table has no answer for role %q, action %q", role, action)
			}
			if got := contracts.Allowed(role, action); got != expected {
				t.Errorf("Allowed(%q, %q) = %v, want %v", role, action, got, expected)
			}
		}
	}
}

// TestAllowedDeniesByDefault: a permission surface fails closed. An unknown role
// and an unknown action must both be "no" for every role, because the incident
// that matters is an accidental grant.
func TestAllowedDeniesByDefault(t *testing.T) {
	roles := []string{contracts.UserRoleOwner, contracts.UserRoleManager, contracts.UserRoleCollaborator}
	unknownActions := []contracts.UserAction{"", "delete-user", "install-plugin", "ADMIN"}
	for _, role := range roles {
		for _, action := range unknownActions {
			if contracts.Allowed(role, action) {
				t.Errorf("Allowed(%q, %q) = true, want false (unknown action)", role, action)
			}
		}
	}
	for _, role := range []string{"", "admin", "Owner", " root "} {
		for _, action := range []contracts.UserAction{
			contracts.ActionDispatch,
			contracts.ActionApprove,
			contracts.ActionManageBots,
			contracts.ActionReviewMemory,
			contracts.ActionDecideBudget,
		} {
			if contracts.Allowed(role, action) {
				t.Errorf("Allowed(%q, %q) = true, want false (unknown role)", role, action)
			}
		}
	}
}
