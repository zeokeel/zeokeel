package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The automation screen (R22) fetched /api/automation and PATCHed /api/automation/{id}
// — both 404. The backend service already had List/Create/Update/Delete/RunNow (and a
// run history inside each task), so the screen can be the native one: create, edit,
// enable/disable, run now, delete.

type fakeAutomation struct {
	tasks    []contracts.AutomationTask
	ran      []string
	deleted  []string
	created  int
	nextID   int
	failNext error
}

func (f *fakeAutomation) List(context.Context) ([]contracts.AutomationTask, error) {
	if f.failNext != nil {
		return nil, f.failNext
	}
	return append([]contracts.AutomationTask(nil), f.tasks...), nil
}

func (f *fakeAutomation) Create(_ context.Context, task contracts.AutomationTask) (contracts.AutomationTask, error) {
	f.created++
	f.nextID++
	task.ID = fmt.Sprintf("auto-%d", f.nextID)
	f.tasks = append(f.tasks, task)
	return task, nil
}

func (f *fakeAutomation) Update(_ context.Context, task contracts.AutomationTask) (contracts.AutomationTask, error) {
	for i, existing := range f.tasks {
		if existing.ID == task.ID {
			f.tasks[i] = task
			return task, nil
		}
	}
	return contracts.AutomationTask{}, fmt.Errorf("unknown task %s", task.ID)
}

func (f *fakeAutomation) Delete(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	for i, task := range f.tasks {
		if task.ID == id {
			f.tasks = append(f.tasks[:i], f.tasks[i+1:]...)
			return nil
		}
	}
	return nil
}

func (f *fakeAutomation) RunNow(_ context.Context, id string) error {
	f.ran = append(f.ran, id)
	return nil
}

func (f *fakeAutomation) Subscribe(func()) (func(), error) { return func() {}, nil }

func newAutomationGateway(t *testing.T, service contracts.AutomationService) *runningGateway {
	t.Helper()
	chat := newFakeChat()
	chat.addSession("s1", "First", "hello")
	root := testRoot(t, chat)
	if service != nil {
		if _, err := root.Provide(contracts.KeyAutomation, contracts.AutomationService(service)); err != nil {
			t.Fatalf("provide automation: %v", err)
		}
	}
	gateway, err := New(root, Options{DataDir: t.TempDir(), Port: -1, Logger: testLogger{t: t}, Out: discardWriter{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := gateway.Provide(root); err != nil {
		t.Fatalf("Provide: %v", err)
	}
	return serveGateway(t, gateway)
}

func automationRequest2(t *testing.T, gw *runningGateway, method, path, body string) (int, string) {
	t.Helper()
	request, err := http.NewRequest(method, "http://"+gw.address+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+gw.info.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()
	var decoded strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := response.Body.Read(buf)
		if n > 0 {
			decoded.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return response.StatusCode, decoded.String()
}

func TestAutomationListsTasksWithScheduleAndHistory(t *testing.T) {
	last := time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)
	next := time.Date(2026, 11, 25, 8, 0, 0, 0, time.UTC)
	service := &fakeAutomation{tasks: []contracts.AutomationTask{{
		ID: "auto-1", Name: "每日构建检查", Prompt: "跑构建", Schedule: "*/30 * * * *",
		Enabled: true, LastRun: last, NextRun: next,
		RunHistory: []contracts.AutomationRunRecord{{
			StartedAt: last, CompletedAt: last.Add(time.Minute), Status: "ok",
			Summary: "ran in chat session sess-1", SessionID: "sess-1",
		}},
	}}}
	gw := newAutomationGateway(t, service)
	defer gw.stop(t)

	status, body := filesGet(t, gw, "/api/automation", true)
	if status != http.StatusOK {
		t.Fatalf("GET = %d %q", status, body)
	}
	var payload automationListDTO
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode: %v (%q)", err, body)
	}
	if len(payload.Tasks) != 1 {
		t.Fatalf("tasks = %+v", payload.Tasks)
	}
	task := payload.Tasks[0]
	if task.Title != "每日构建检查" || task.Schedule != "*/30 * * * *" || !task.Enabled {
		t.Fatalf("task = %+v", task)
	}
	if task.LastRun != "2026-11-24T08:00:00Z" || task.NextRun != "2026-11-25T08:00:00Z" {
		t.Fatalf("times = %q / %q", task.LastRun, task.NextRun)
	}
	if len(task.RunHistory) != 1 || task.RunHistory[0].SessionID != "sess-1" {
		t.Fatalf("history = %+v", task.RunHistory)
	}
}

func TestAutomationCreatesEditsRunsAndDeletes(t *testing.T) {
	service := &fakeAutomation{}
	gw := newAutomationGateway(t, service)
	defer gw.stop(t)

	status, body := automationRequest2(t, gw, http.MethodPost, "/api/automation",
		`{"title":"每日检查","prompt":"跑一遍构建","schedule":"0 8 * * *"}`)
	if status != http.StatusOK {
		t.Fatalf("POST = %d %q", status, body)
	}
	if service.created != 1 || len(service.tasks) != 1 {
		t.Fatalf("create did not reach the service: %+v", service)
	}

	if status, body := automationRequest2(t, gw, http.MethodPatch, "/api/automation/auto-1",
		`{"enabled":false}`); status != http.StatusOK {
		t.Fatalf("PATCH = %d %q", status, body)
	}
	if service.tasks[0].Enabled {
		t.Fatalf("the toggle did not reach the service")
	}

	if status, body := automationRequest2(t, gw, http.MethodPost, "/api/automation/auto-1/run", ""); status != http.StatusOK {
		t.Fatalf("run = %d %q", status, body)
	}
	if len(service.ran) != 1 || service.ran[0] != "auto-1" {
		t.Fatalf("RunNow calls = %v", service.ran)
	}

	if status, body := automationRequest2(t, gw, http.MethodDelete, "/api/automation/auto-1", ""); status != http.StatusOK {
		t.Fatalf("DELETE = %d %q", status, body)
	}
	if len(service.deleted) != 1 || service.deleted[0] != "auto-1" {
		t.Fatalf("Delete calls = %v", service.deleted)
	}
}

func TestAutomationRejectsIncompleteCreates(t *testing.T) {
	gw := newAutomationGateway(t, &fakeAutomation{})
	defer gw.stop(t)

	for _, body := range []string{
		`{"title":"只有标题"}`,
		`{"title":"标题","prompt":"提示词"}`,
		`{"prompt":"提示词","schedule":"0 8 * * *"}`,
	} {
		if status, _ := automationRequest2(t, gw, http.MethodPost, "/api/automation", body); status != http.StatusBadRequest {
			t.Fatalf("incomplete create %q = %d, want 400", body, status)
		}
	}
	// A patch for a task that does not exist is a 404, not a silent create.
	if status, _ := automationRequest2(t, gw, http.MethodPatch, "/api/automation/nope",
		`{"enabled":true}`); status != http.StatusNotFound {
		t.Fatalf("unknown task patch = %d, want 404", status)
	}
}

func TestAutomationNeedsTheToken(t *testing.T) {
	gw := newAutomationGateway(t, &fakeAutomation{})
	defer gw.stop(t)
	if status, _ := filesGet(t, gw, "/api/automation", false); status != http.StatusUnauthorized {
		t.Fatalf("without a token = %d, want 401", status)
	}
	if status, _ := automationRequest2NoAuth(t, gw); status != http.StatusUnauthorized {
		t.Fatalf("POST without a token = %d, want 401", status)
	}
}

// automationRequest2NoAuth posts without the bearer header.
func automationRequest2NoAuth(t *testing.T, gw *runningGateway) (int, string) {
	t.Helper()
	response, err := http.Post("http://"+gw.address+"/api/automation", "application/json",
		strings.NewReader(`{"title":"x","prompt":"y","schedule":"0 8 * * *"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	return response.StatusCode, ""
}
