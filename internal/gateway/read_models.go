package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Read models for the three screens that used to call endpoints the gateway never
// served: capabilities (R23), insights (R24) and the session Memory tab.
//
// The web client has had these screens, and their fetch layer, since the migration;
// every call answered 404 "unknown method". The backend services all existed
// (skills, MCP plugins, insights, memoryv2) — only the transport was missing, so
// these handlers are thin shapers over the services, not new logic.

// capabilitySkillDTO is one skill row of the capabilities screen.
type capabilitySkillDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

// capabilityConnectorDTO is one MCP server row.
type capabilityConnectorDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Transport string `json:"transport"`
	Tools     int    `json:"tools"`
	Status    string `json:"status"`
}

type capabilitiesDTO struct {
	Skills     []capabilitySkillDTO     `json:"skills"`
	Connectors []capabilityConnectorDTO `json:"connectors"`
}

// handleCapabilities serves GET /api/capabilities: the skills this build can load
// and the MCP connectors it knows about.
func (g *Gateway) handleCapabilities(ctx context.Context) (any, *protocolError) {
	payload := capabilitiesDTO{Skills: []capabilitySkillDTO{}, Connectors: []capabilityConnectorDTO{}}

	if skills, ok := g.root.Get(contracts.KeySkills); ok && skills != nil {
		if list, err := skills.List(ctx); err == nil {
			for _, skill := range list {
				payload.Skills = append(payload.Skills, capabilitySkillDTO{
					ID:          skill.Name,
					Name:        skill.Name,
					Description: skill.Description,
					Source:      skillDirectory(skill.Path),
				})
			}
		}
	}

	if plugins, ok := g.root.Get(contracts.KeyPlugins); ok && plugins != nil {
		for _, plugin := range plugins.MCP() {
			transport := "http"
			if plugin.URL == "" {
				transport = "stdio"
			}
			payload.Connectors = append(payload.Connectors, capabilityConnectorDTO{
				ID:        plugin.Name,
				Name:      plugin.Name,
				Transport: transport,
				Tools:     plugin.Tools,
				Status:    plugin.Status,
			})
		}
	}

	return payload, nil
}

// skillDirectory is the folder one skill file lives in — the row's "source".
func skillDirectory(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Dir(path)
}

// serveReadModel authenticates one GET and writes the handler's JSON answer. Every
// read model goes through here so the token check cannot be forgotten per endpoint.
func (g *Gateway) serveReadModel(w http.ResponseWriter, r *http.Request,
	load func(context.Context) (any, *protocolError)) {
	if err := g.authorizeRequest(r, true); err != nil {
		status := http.StatusUnauthorized
		if err.Code == codeBadOrigin || err.Data["name"] == "bad_host" {
			status = http.StatusForbidden
		}
		writeProtocolError(w, status, err)
		return
	}
	payload, protocolErr := load(r.Context())
	if protocolErr != nil {
		status := http.StatusInternalServerError
		if protocolErr.Code == codeCapabilityUnsupported {
			status = http.StatusServiceUnavailable
		}
		writeProtocolError(w, status, protocolErr)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// insightsTotalsDTO is the window-wide part of the insights screen.
type insightsTotalsDTO struct {
	Sessions     int `json:"sessions"`
	Turns        int `json:"turns"`
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	ToolCalls    int `json:"toolCalls"`
}

// insightsDayDTO is one day of the by-day chart.
type insightsDayDTO struct {
	Day       string `json:"day"`
	Turns     int    `json:"turns"`
	ToolCalls int    `json:"toolCalls"`
}

// insightsModelDTO is one model's share of the window. The backend aggregates by
// model (and by workspace); it has no per-tool breakdown, so the screen shows the
// model split rather than inventing a tool one.
type insightsModelDTO struct {
	Model       string `json:"model"`
	Calls       int    `json:"calls"`
	TotalTokens int    `json:"totalTokens"`
}

type insightsDTO struct {
	Range   int                `json:"range"`
	Totals  insightsTotalsDTO  `json:"totals"`
	ByDay   []insightsDayDTO   `json:"byDay"`
	ByModel []insightsModelDTO `json:"byModel"`
}

// handleInsights serves GET /api/insights?range=7|30.
func (g *Gateway) handleInsights(ctx context.Context, days int) (any, *protocolError) {
	service, ok := g.root.Get(contracts.KeyInsights)
	if !ok || service == nil {
		return insightsDTO{Range: days, ByDay: []insightsDayDTO{}, ByModel: []insightsModelDTO{}}, nil
	}
	result, err := service.Insights(ctx, contracts.InsightsQuery{Days: days})
	if err != nil {
		return nil, internalError(err)
	}
	payload := insightsDTO{
		Range: result.Days,
		Totals: insightsTotalsDTO{
			Sessions:     result.Totals.Sessions,
			Turns:        result.Totals.Turns,
			InputTokens:  result.Totals.PromptTokens,
			OutputTokens: result.Totals.CompletionTokens,
			ToolCalls:    result.Totals.ToolCalls,
		},
		ByDay:   make([]insightsDayDTO, 0, len(result.Daily)),
		ByModel: make([]insightsModelDTO, 0, len(result.Models)),
	}
	for _, day := range result.Daily {
		payload.ByDay = append(payload.ByDay, insightsDayDTO{
			Day: day.Day.Format("2006-01-02"), Turns: day.Turns, ToolCalls: day.ToolCalls,
		})
	}
	for _, model := range result.Models {
		payload.ByModel = append(payload.ByModel, insightsModelDTO{
			Model: model.Model, Calls: model.Calls, TotalTokens: model.TotalTokens,
		})
	}
	return payload, nil
}

// insightsDays reads the range query parameter (7 or 30 days; anything else 7).
func insightsDays(raw string) int {
	if raw == "30" {
		return 30
	}
	return 7
}

// handleInsightsHTTP serves GET /api/insights?range=7|30.
func (g *Gateway) handleInsightsHTTP(w http.ResponseWriter, r *http.Request) {
	days := insightsDays(r.URL.Query().Get("range"))
	g.serveReadModel(w, r, func(ctx context.Context) (any, *protocolError) {
		return g.handleInsights(ctx, days)
	})
}

// memoryEntryDTO is one row of the session Memory tab.
type memoryEntryDTO struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Summary   string `json:"summary,omitempty"`
	Type      string `json:"type,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type memoryListDTO struct {
	Entries []memoryEntryDTO `json:"entries"`
}

// handleSessionMemory serves GET /api/sessions/{id}/memory.
//
// The memory store is workspace-scoped, and this build has exactly one workspace
// (workspaces cannot be added), so the tab shows that workspace's memory as a whole:
// the session id in the URL is the screen's address, not a filter.
func (g *Gateway) handleSessionMemory(ctx context.Context) (any, *protocolError) {
	payload := memoryListDTO{Entries: []memoryEntryDTO{}}
	service, ok := g.root.Get(contracts.KeyMemoryService)
	if !ok || service == nil {
		return payload, nil
	}
	root, ok := g.currentWorkspaceRoot(ctx)
	if !ok {
		return payload, nil
	}
	items, err := service.List(ctx, root, "", nil, readModelMemoryLimit)
	if err != nil {
		return nil, internalError(err)
	}
	for _, item := range items {
		entry := memoryEntryDTO{
			ID:      item.URI,
			Title:   item.Title,
			Summary: item.Summary,
			Type:    item.Type,
		}
		if !item.Updated.IsZero() {
			entry.UpdatedAt = item.Updated.Format(time.RFC3339)
		}
		payload.Entries = append(payload.Entries, entry)
	}
	return payload, nil
}

// readModelMemoryLimit bounds the memory rows one screen load reads.
const readModelMemoryLimit = 200

// currentWorkspaceRoot resolves the open workspace's root directory.
func (g *Gateway) currentWorkspaceRoot(ctx context.Context) (string, bool) {
	service, ok := g.root.Get(contracts.KeyWorkspace)
	if !ok || service == nil {
		return "", false
	}
	current, ok, err := service.Current(ctx)
	if err != nil || !ok {
		return "", false
	}
	return current.Root, true
}

// handleSessionReadModel dispatches the per-session read models.
func (g *Gateway) handleSessionReadModel(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasSuffix(r.URL.Path, "/memory"):
		g.serveReadModel(w, r, g.handleSessionMemory)
	default:
		writeProtocolError(w, http.StatusNotFound, errMethodNotFound(r.URL.Path))
	}
}

// automationRunDTO is one run record of a task's history.
type automationRunDTO struct {
	StartedAt   string `json:"startedAt,omitempty"`
	CompletedAt string `json:"completedAt,omitempty"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	Summary     string `json:"summary,omitempty"`
	SessionID   string `json:"sessionId,omitempty"`
}

// automationTaskDTO is the automation screen's row.
type automationTaskDTO struct {
	ID         string             `json:"id"`
	Title      string             `json:"title"`
	Prompt     string             `json:"prompt"`
	Schedule   string             `json:"schedule"`
	Enabled    bool               `json:"enabled"`
	LastRun    string             `json:"lastRun,omitempty"`
	NextRun    string             `json:"nextRun,omitempty"`
	LastError  string             `json:"lastError,omitempty"`
	RunHistory []automationRunDTO `json:"runHistory"`
}

type automationListDTO struct {
	Tasks []automationTaskDTO `json:"tasks"`
}

// automationRequest is the create/update body.
type automationRequest struct {
	Title    string `json:"title"`
	Prompt   string `json:"prompt"`
	Schedule string `json:"schedule"`
	Enabled  *bool  `json:"enabled"`
}

// handleAutomation dispatches the /api/automation routes.
func (g *Gateway) handleAutomation(w http.ResponseWriter, r *http.Request) {
	if err := g.authorizeRequest(r, true); err != nil {
		status := http.StatusUnauthorized
		if err.Code == codeBadOrigin || err.Data["name"] == "bad_host" {
			status = http.StatusForbidden
		}
		writeProtocolError(w, status, err)
		return
	}
	service, ok := g.root.Get(contracts.KeyAutomation)
	if !ok || service == nil {
		writeProtocolError(w, http.StatusServiceUnavailable,
			protocolErrorWithMessage(codeCapabilityUnsupported, "automation unavailable"))
		return
	}
	ctx := r.Context()
	rest := strings.TrimPrefix(r.URL.Path, "/api/automation")
	rest = strings.TrimPrefix(rest, "/")

	// /api/automation
	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			tasks, err := service.List(ctx)
			if err != nil {
				writeProtocolError(w, http.StatusInternalServerError, internalError(err))
				return
			}
			payload := automationListDTO{Tasks: make([]automationTaskDTO, 0, len(tasks))}
			for _, task := range tasks {
				payload.Tasks = append(payload.Tasks, automationTaskDTOOf(task))
			}
			writeJSON(w, http.StatusOK, payload)
		case http.MethodPost:
			var body automationRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeProtocolError(w, http.StatusBadRequest, errInvalidParams("body", "is not JSON"))
				return
			}
			if strings.TrimSpace(body.Title) == "" || strings.TrimSpace(body.Prompt) == "" ||
				strings.TrimSpace(body.Schedule) == "" {
				writeProtocolError(w, http.StatusBadRequest,
					errInvalidParams("title", "title, prompt and schedule are required"))
				return
			}
			enabled := true
			if body.Enabled != nil {
				enabled = *body.Enabled
			}
			created, err := service.Create(ctx, contracts.AutomationTask{
				Name: body.Title, Prompt: body.Prompt, Schedule: body.Schedule, Enabled: enabled,
			})
			if err != nil {
				writeProtocolError(w, http.StatusBadRequest, internalError(err))
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"task": automationTaskDTOOf(created)})
		default:
			w.Header().Set("Allow", "GET, POST")
			writeProtocolError(w, http.StatusMethodNotAllowed,
				protocolErrorWithMessage(codeMethodNotFound, "method not allowed"))
		}
		return
	}

	// /api/automation/{id}/run
	if strings.HasSuffix(rest, "/run") && r.Method == http.MethodPost {
		id := strings.TrimSuffix(rest, "/run")
		if strings.TrimSpace(id) == "" {
			writeProtocolError(w, http.StatusBadRequest, errInvalidParams("id", "is required"))
			return
		}
		if err := service.RunNow(ctx, id); err != nil {
			writeProtocolError(w, http.StatusBadRequest, internalError(err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	// /api/automation/{id}
	id := rest
	if strings.Contains(id, "/") || strings.TrimSpace(id) == "" {
		writeProtocolError(w, http.StatusNotFound, errMethodNotFound(r.URL.Path))
		return
	}
	switch r.Method {
	case http.MethodPatch:
		existing, found := findAutomationTask(ctx, service, id)
		if !found {
			writeProtocolError(w, http.StatusNotFound, errInvalidParams("id", "unknown task"))
			return
		}
		var body automationRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeProtocolError(w, http.StatusBadRequest, errInvalidParams("body", "is not JSON"))
			return
		}
		if body.Title != "" {
			existing.Name = body.Title
		}
		if body.Prompt != "" {
			existing.Prompt = body.Prompt
		}
		if body.Schedule != "" {
			existing.Schedule = body.Schedule
		}
		if body.Enabled != nil {
			existing.Enabled = *body.Enabled
		}
		updated, err := service.Update(ctx, existing)
		if err != nil {
			writeProtocolError(w, http.StatusBadRequest, internalError(err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"task": automationTaskDTOOf(updated)})
	case http.MethodDelete:
		if err := service.Delete(ctx, id); err != nil {
			writeProtocolError(w, http.StatusBadRequest, internalError(err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		w.Header().Set("Allow", "PATCH, DELETE")
		writeProtocolError(w, http.StatusMethodNotAllowed,
			protocolErrorWithMessage(codeMethodNotFound, "method not allowed"))
	}
}

// findAutomationTask reads one task by id (the service exposes no Get).
func findAutomationTask(ctx context.Context, service contracts.AutomationService,
	id string) (contracts.AutomationTask, bool) {
	tasks, err := service.List(ctx)
	if err != nil {
		return contracts.AutomationTask{}, false
	}
	for _, task := range tasks {
		if task.ID == id {
			return task, true
		}
	}
	return contracts.AutomationTask{}, false
}

// automationTaskDTOOf maps one backend task onto the screen's row.
func automationTaskDTOOf(task contracts.AutomationTask) automationTaskDTO {
	dto := automationTaskDTO{
		ID:         task.ID,
		Title:      task.Name,
		Prompt:     task.Prompt,
		Schedule:   task.Schedule,
		Enabled:    task.Enabled,
		LastError:  task.LastError,
		RunHistory: make([]automationRunDTO, 0, len(task.RunHistory)),
	}
	if !task.LastRun.IsZero() {
		dto.LastRun = task.LastRun.Format(time.RFC3339)
	}
	if !task.NextRun.IsZero() {
		dto.NextRun = task.NextRun.Format(time.RFC3339)
	}
	for _, run := range task.RunHistory {
		record := automationRunDTO{
			Status: run.Status, Error: run.Error, Summary: run.Summary, SessionID: run.SessionID,
		}
		if !run.StartedAt.IsZero() {
			record.StartedAt = run.StartedAt.Format(time.RFC3339)
		}
		if !run.CompletedAt.IsZero() {
			record.CompletedAt = run.CompletedAt.Format(time.RFC3339)
		}
		dto.RunHistory = append(dto.RunHistory, record)
	}
	return dto
}

// --- team (T2) ---------------------------------------------------------------
//
// The browser's team screen: spawn a teammate, send it work, interrupt or close it,
// read the activity feed, and open one member's detail. The backend service existed
// (contracts.KeyTeam) with no transport at all — the screen could only show its empty
// state. These methods are the missing half; the client refreshes its snapshot after
// every action and polls while the screen is open (there is no team change hook in
// the service to subscribe to).

// teamMemberDTO is one teammate row.
type teamMemberDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	RunState string `json:"runState"`
	Model    string `json:"model,omitempty"`
	Created  string `json:"created,omitempty"`
	Last     string `json:"last,omitempty"`
	Error    string `json:"error,omitempty"`
}

// teamActivityDTO is one activity feed row.
type teamActivityDTO struct {
	ID       string `json:"id"`
	MemberID string `json:"memberId,omitempty"`
	Kind     string `json:"kind"`
	Text     string `json:"text,omitempty"`
	At       string `json:"at,omitempty"`
}

type teamSnapshotDTO struct {
	Members    []teamMemberDTO   `json:"members"`
	Activities []teamActivityDTO `json:"activities"`
}

// teamSpawnParams is team.spawn's parameter contract.
type teamSpawnParams struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
	Model  string `json:"model,omitempty"`
}

// teamSendParams is team.send's parameter contract.
type teamSendParams struct {
	ID      string `json:"id"`
	Message string `json:"message"`
	Async   bool   `json:"async,omitempty"`
}

// teamIDParams is the parameter contract of the methods that take only an id.
type teamIDParams struct {
	ID string `json:"id"`
}

// teamActivityParams is team.activity's parameter contract.
type teamActivityParams struct {
	Limit int `json:"limit,omitempty"`
}

// teamService returns the team backend or a capability error.
func (g *Gateway) teamService() (contracts.TeamService, *protocolError) {
	service, ok := g.root.Get(contracts.KeyTeam)
	if !ok || service == nil {
		return nil, protocolErrorWithMessage(codeCapabilityUnsupported, "team unavailable")
	}
	return service, nil
}

// teamSnapshot serves team.snapshot: members plus the activity feed.
func (g *Gateway) teamSnapshot() (any, *protocolError) {
	service, problem := g.teamService()
	if problem != nil {
		return nil, problem
	}
	payload := teamSnapshotDTO{Members: []teamMemberDTO{}, Activities: []teamActivityDTO{}}
	for _, member := range service.Members() {
		row := teamMemberDTO{
			ID: member.ID, Name: member.Name, Status: member.Status,
			RunState: member.RunState, Model: member.Model, Last: member.Last, Error: member.Error,
		}
		if !member.Created.IsZero() {
			row.Created = member.Created.UTC().Format(time.RFC3339)
		}
		payload.Members = append(payload.Members, row)
	}
	if lister, ok := service.(contracts.TeamActivityLister); ok {
		for _, activity := range lister.TeamActivities() {
			row := teamActivityDTO{ID: activity.ID, MemberID: activity.MemberID, Kind: activity.Kind, Text: activity.Text}
			if !activity.Time.IsZero() {
				row.At = activity.Time.UTC().Format(time.RFC3339)
			}
			payload.Activities = append(payload.Activities, row)
		}
	}
	return payload, nil
}

// teamSpawn serves team.spawn.
func (g *Gateway) teamSpawn(ctx context.Context, params json.RawMessage) (any, *protocolError) {
	service, problem := g.teamService()
	if problem != nil {
		return nil, problem
	}
	var body teamSpawnParams
	if err := decodeParams(params, &body); err != nil {
		return nil, err
	}
	if strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Prompt) == "" {
		return nil, errInvalidParams("name", "name and prompt are required")
	}
	member, err := service.Spawn(ctx, body.Name, body.Prompt, body.Model)
	if err != nil {
		return nil, internalError(err)
	}
	return map[string]any{"member": teamMemberDTOOf(member)}, nil
}

// teamSend serves team.send (async queues the message, sync waits for the answer).
func (g *Gateway) teamSend(ctx context.Context, params json.RawMessage) (any, *protocolError) {
	service, problem := g.teamService()
	if problem != nil {
		return nil, problem
	}
	var body teamSendParams
	if err := decodeParams(params, &body); err != nil {
		return nil, err
	}
	if strings.TrimSpace(body.ID) == "" || strings.TrimSpace(body.Message) == "" {
		return nil, errInvalidParams("id", "id and message are required")
	}
	if body.Async {
		if err := service.SendAsync(body.ID, body.Message); err != nil {
			return nil, internalError(err)
		}
		return map[string]any{"ok": true}, nil
	}
	answer, err := service.Send(ctx, body.ID, body.Message)
	if err != nil {
		return nil, internalError(err)
	}
	return map[string]any{"answer": answer}, nil
}

// teamWait serves team.wait: block until the member's current run finishes.
func (g *Gateway) teamWait(ctx context.Context, params json.RawMessage) (any, *protocolError) {
	service, problem := g.teamService()
	if problem != nil {
		return nil, problem
	}
	var body teamIDParams
	if err := decodeParams(params, &body); err != nil {
		return nil, err
	}
	if strings.TrimSpace(body.ID) == "" {
		return nil, errInvalidParams("id", "is required")
	}
	answer, err := service.Wait(ctx, body.ID)
	if err != nil {
		return nil, internalError(err)
	}
	return map[string]any{"answer": answer}, nil
}

// teamInterrupt serves team.interrupt.
func (g *Gateway) teamInterrupt(params json.RawMessage) (any, *protocolError) {
	service, problem := g.teamService()
	if problem != nil {
		return nil, problem
	}
	var body teamIDParams
	if err := decodeParams(params, &body); err != nil {
		return nil, err
	}
	if strings.TrimSpace(body.ID) == "" {
		return nil, errInvalidParams("id", "is required")
	}
	if err := service.Interrupt(body.ID); err != nil {
		return nil, internalError(err)
	}
	return map[string]any{"ok": true}, nil
}

// teamClose serves team.close.
func (g *Gateway) teamClose(params json.RawMessage) (any, *protocolError) {
	service, problem := g.teamService()
	if problem != nil {
		return nil, problem
	}
	var body teamIDParams
	if err := decodeParams(params, &body); err != nil {
		return nil, err
	}
	if strings.TrimSpace(body.ID) == "" {
		return nil, errInvalidParams("id", "is required")
	}
	if err := service.Close(body.ID); err != nil {
		return nil, internalError(err)
	}
	return map[string]any{"ok": true}, nil
}

// teamActivity serves team.activity: the newest feed rows, oldest first.
func (g *Gateway) teamActivity(params json.RawMessage) (any, *protocolError) {
	service, problem := g.teamService()
	if problem != nil {
		return nil, problem
	}
	var body teamActivityParams
	if err := decodeParams(params, &body); err != nil {
		return nil, err
	}
	payload := map[string]any{"items": []teamActivityDTO{}}
	lister, ok := service.(contracts.TeamActivityLister)
	if !ok {
		return payload, nil
	}
	rows := []teamActivityDTO{}
	for _, activity := range lister.TeamActivities() {
		row := teamActivityDTO{ID: activity.ID, MemberID: activity.MemberID, Kind: activity.Kind, Text: activity.Text}
		if !activity.Time.IsZero() {
			row.At = activity.Time.UTC().Format(time.RFC3339)
		}
		rows = append(rows, row)
	}
	if body.Limit > 0 && len(rows) > body.Limit {
		rows = rows[len(rows)-body.Limit:]
	}
	payload["items"] = rows
	return payload, nil
}

// teamMemberDTOOf maps one member.
func teamMemberDTOOf(member contracts.TeamMember) teamMemberDTO {
	row := teamMemberDTO{
		ID: member.ID, Name: member.Name, Status: member.Status,
		RunState: member.RunState, Model: member.Model, Last: member.Last, Error: member.Error,
	}
	if !member.Created.IsZero() {
		row.Created = member.Created.UTC().Format(time.RFC3339)
	}
	return row
}
