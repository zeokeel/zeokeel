package gateway

// M6 permission table at the entry points: a collaborator may dispatch but not
// approve; a manager may approve but not change the roster.

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

type fakeUsers struct {
	mu   sync.Mutex
	list []contracts.User
}

func (f *fakeUsers) Users(context.Context) ([]contracts.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]contracts.User(nil), f.list...), nil
}

func (f *fakeUsers) Put(_ context.Context, user contracts.User) (contracts.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.list {
		if f.list[i].ID == user.ID {
			f.list[i] = user
			return user, nil
		}
	}
	user.CreatedAt = time.Unix(0, 0).UTC()
	f.list = append(f.list, user)
	return user, nil
}

func (f *fakeUsers) Get(_ context.Context, id string) (contracts.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, user := range f.list {
		if user.ID == id {
			return user, nil
		}
	}
	return contracts.User{}, contracts.ErrUserUnknown
}

// memorySettings is the settings row store the acting-user switch writes to.
type memorySettings struct {
	mu     sync.Mutex
	values map[string]string
}

func (m *memorySettings) Get(key, def string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if value, ok := m.values[key]; ok {
		return value
	}
	return def
}

func (m *memorySettings) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.values == nil {
		m.values = map[string]string{}
	}
	m.values[key] = value
	return nil
}

func (m *memorySettings) All() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for key, value := range m.values {
		out[key] = value
	}
	return out
}

func newUsersGateway(t *testing.T, roster ...contracts.User) *Gateway {
	t.Helper()
	root := testRoot(t, newFakeChat())
	if _, err := root.Provide(contracts.KeyUsers, contracts.UserService(&fakeUsers{list: roster})); err != nil {
		t.Fatalf("provide users: %v", err)
	}
	if _, err := root.Provide(contracts.KeySettings, contracts.SettingsService(&memorySettings{})); err != nil {
		t.Fatalf("provide settings: %v", err)
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return gateway
}

func TestCollaboratorMayNotApprove(t *testing.T) {
	gateway := newUsersGateway(t,
		contracts.User{ID: "owner", Name: "Owner", Role: contracts.UserRoleOwner},
		contracts.User{ID: "helper", Name: "Helper", Role: contracts.UserRoleCollaborator},
	)
	raw, _ := json.Marshal(map[string]any{"id": "helper"})
	if _, problem := gateway.usersAct(context.Background(), raw); problem != nil {
		t.Fatalf("act: %+v", problem)
	}
	review, _ := json.Marshal(map[string]any{"id": "task-1", "decision": "accept", "reviewer": "helper"})
	_, problem := gateway.taskReview(context.Background(), review)
	if problem == nil {
		t.Fatal("a collaborator approved a task")
	}
	if problem.Code != codeInvalidRequest {
		t.Fatalf("code = %d, want %d", problem.Code, codeInvalidRequest)
	}
}

func TestManagerMayNotChangeTheRoster(t *testing.T) {
	gateway := newUsersGateway(t,
		contracts.User{ID: "owner", Name: "Owner", Role: contracts.UserRoleOwner},
		contracts.User{ID: "mgr", Name: "Manager", Role: contracts.UserRoleManager},
	)
	raw, _ := json.Marshal(map[string]any{"id": "mgr"})
	if _, problem := gateway.usersAct(context.Background(), raw); problem != nil {
		t.Fatalf("act: %+v", problem)
	}
	roster, _ := json.Marshal(map[string]any{"id": "sneaky", "name": "Sneaky", "role": "owner"})
	if _, problem := gateway.usersUpsert(context.Background(), roster); problem == nil {
		t.Fatal("a manager promoted someone to owner")
	}
}

func TestOwnerMayDoEverything(t *testing.T) {
	gateway := newUsersGateway(t,
		contracts.User{ID: "owner", Name: "Owner", Role: contracts.UserRoleOwner},
	)
	roster, _ := json.Marshal(map[string]any{"id": "mgr", "name": "Manager", "role": "manager"})
	if _, problem := gateway.usersUpsert(context.Background(), roster); problem != nil {
		t.Fatalf("owner upsert: %+v", problem)
	}
}

func TestNoRosterLeavesTheGateInert(t *testing.T) {
	// A product built without the users module must behave exactly as before.
	root := testRoot(t, newFakeChat())
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if problem := gateway.requireAction(context.Background(), contracts.ActionManageBots); problem != nil {
		t.Fatalf("requireAction without a roster = %+v, want nil", problem)
	}
}

func TestActingUserSnapshotNamesTheActor(t *testing.T) {
	gateway := newUsersGateway(t,
		contracts.User{ID: "owner", Name: "Owner", Role: contracts.UserRoleOwner},
		contracts.User{ID: "mgr", Name: "Manager", Role: contracts.UserRoleManager},
	)
	raw, _ := json.Marshal(map[string]any{"id": "mgr"})
	if _, problem := gateway.usersAct(context.Background(), raw); problem != nil {
		t.Fatalf("act: %+v", problem)
	}
	result, problem := gateway.usersSnapshot(context.Background(), nil)
	if problem != nil {
		t.Fatalf("snapshot: %+v", problem)
	}
	payload, _ := result.(usersSnapshotResult)
	if payload.ActingUserID != "mgr" || payload.ActingRole != contracts.UserRoleManager {
		t.Fatalf("snapshot = %+v, want the acting manager named", payload)
	}
}
