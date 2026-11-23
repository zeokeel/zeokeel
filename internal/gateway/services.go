package gateway

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// The five host services the headless assembly never provides.
//
// provideServices in internal/host returns early when uiRoot is nil (host.go
// §825-827), so a headless product has no UserInteraction, ArtifactPresenter,
// PlanReviewer, Notifier or ForegroundSession. That is the mechanical reason a
// browser run would never ask a question, present an artifact, review a plan or
// raise a notification — the host-tools module sees no capability and registers
// the tools without them.
//
// The gateway provides them itself (docs/TRANSPORT.md §7.2) without touching
// uiroot or mygo: blocking agent-side calls are answered by client RPCs, and
// fire-and-forget calls become broadcasts on the hub.
//
// Answers are the gateway's own methods (ask.answer / ask.cancel / plan.exit /
// notify.action); their richer surfaces (snapshots, subscriptions, inbox) are
// the client stages that follow, but the blocking path must exist now or a run
// that asks a question would park forever.

// pendingQuestion is one question parked on a browser answer.
type pendingQuestion struct {
	channel   chan questionAnswer
	text      string
	options   []string
	createdAt time.Time
}

// questionAnswer is one resolved question.
type questionAnswer struct {
	value string
	err   error
}

// interactionService implements contracts.UserInteraction.
type interactionService struct {
	gw *Gateway
}

// SetHandler installs a display handler. The gateway pushes questions through
// the hub instead, so the handler is only kept for contract completeness and
// for embedders that install one.
func (s *interactionService) SetHandler(fn func(contracts.UserQuestion)) {
	s.gw.mu.Lock()
	s.gw.questionHandler = fn
	s.gw.mu.Unlock()
}

// Ask publishes ask.opened and parks until the client answers, cancels, or the
// run's context ends.
func (s *interactionService) Ask(ctx context.Context, text string, options []string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	g := s.gw
	id := g.newID("ask")
	question := &pendingQuestion{
		channel:   make(chan questionAnswer, 1),
		text:      text,
		options:   append([]string(nil), options...),
		createdAt: time.Now(),
	}
	g.mu.Lock()
	g.questions[id] = question
	handler := g.questionHandler
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		delete(g.questions, id)
		g.mu.Unlock()
	}()
	if handler != nil {
		handler(contracts.UserQuestion{ID: id, Text: text, Options: options})
	}
	g.broadcastEvent("ask.opened", map[string]any{"id": id, "text": text, "options": options})
	// The ask domain mirrors the same state as a snapshot: the event tells a
	// connected client something arrived, the domain lets a reconnecting one
	// rebuild it (§5.1.4).
	g.hub.markDomainDirty(domainAsk)
	select {
	case answer := <-question.channel:
		return answer.value, answer.err
	case <-ctx.Done():
		g.broadcastEvent("ask.closed", map[string]any{"id": id})
		g.hub.markDomainDirty(domainAsk)
		return "", ctx.Err()
	}
}

// Answer resolves one pending question.
func (s *interactionService) Answer(id, answer string) {
	g := s.gw
	g.mu.Lock()
	question := g.questions[id]
	g.mu.Unlock()
	if question == nil {
		return
	}
	select {
	case question.channel <- questionAnswer{value: answer}:
	default:
	}
	s.gw.hub.markDomainDirty(domainAsk)
}

// Cancel dismisses one pending question with an error.
func (s *interactionService) Cancel(id string) {
	g := s.gw
	g.mu.Lock()
	question := g.questions[id]
	g.mu.Unlock()
	if question == nil {
		return
	}
	select {
	case question.channel <- questionAnswer{err: errQuestionCancelled}:
	default:
	}
	s.gw.hub.markDomainDirty(domainAsk)
}

// artifactService implements contracts.ArtifactPresenter and ArtifactLister.
type artifactService struct {
	gw *Gateway
}

// SetArtifactHandler installs a display handler (kept for embedders).
func (s *artifactService) SetArtifactHandler(fn func(contracts.Artifact)) {
	s.gw.mu.Lock()
	s.gw.artifactHandler = fn
	s.gw.mu.Unlock()
}

// Present records one artifact, broadcasts it and returns its id immediately:
// the contract requires the tool to continue without waiting for the browser.
func (s *artifactService) Present(ctx context.Context, title, body string) (string, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	g := s.gw
	artifact := contracts.Artifact{
		ID:        g.newID("art"),
		Title:     strings.TrimSpace(title),
		Body:      body,
		CreatedAt: time.Now(),
	}
	g.mu.Lock()
	g.artifactItems = append(g.artifactItems, artifact)
	if len(g.artifactItems) > maxRetainedArtifacts {
		g.artifactItems = g.artifactItems[len(g.artifactItems)-maxRetainedArtifacts:]
	}
	handler := g.artifactHandler
	g.mu.Unlock()
	if handler != nil {
		handler(artifact)
	}
	g.broadcastEvent("artifact.presented", map[string]any{
		"id":        artifact.ID,
		"title":     artifact.Title,
		"body":      artifact.Body,
		"createdAt": artifact.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
	g.hub.markDomainDirty(domainArtifacts)
	return artifact.ID, nil
}

// Artifacts returns the retained history, newest first.
func (s *artifactService) Artifacts() []contracts.Artifact {
	g := s.gw
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]contracts.Artifact, 0, len(g.artifactItems))
	for i := len(g.artifactItems) - 1; i >= 0; i-- {
		out = append(out, g.artifactItems[i])
	}
	return out
}

// maxRetainedArtifacts bounds the presenter's history.
const maxRetainedArtifacts = 200

// planService implements contracts.PlanReviewer.
type planService struct {
	gw *Gateway

	mu      sync.Mutex
	state   string
	content string
	handler func(contracts.PlanReview)
}

// Plan review states, the same vocabulary uiroot publishes.
const (
	planIdle      = "idle"
	planReviewing = "reviewing"
	planApproved  = "approved"
	planCancelled = "cancelled"
)

// SetPlanHandler installs the display handler (kept for embedders).
func (s *planService) SetPlanHandler(fn func(contracts.PlanReview)) {
	s.mu.Lock()
	s.handler = fn
	s.mu.Unlock()
}

// Review parks a plan for display and returns the reviewing state. Like the
// desktop bridge it returns immediately: applying the decision is Exit's job.
func (s *planService) Review(ctx context.Context, content string) (string, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", errPlanContentRequired
	}
	s.mu.Lock()
	s.state = planReviewing
	s.content = content
	review := contracts.PlanReview{Content: content, State: planReviewing}
	handler := s.handler
	s.mu.Unlock()
	if handler != nil {
		handler(review)
	}
	s.gw.broadcastEvent("plan.reviewing", map[string]any{"state": review.State, "content": review.Content})
	s.gw.hub.markDomainDirty(domainPlan)
	return planReviewing, nil
}

// Exit applies one decision: approve, cancel or status.
func (s *planService) Exit(action string) (string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	s.mu.Lock()
	state := s.state
	if action == "status" {
		s.mu.Unlock()
		return state, nil
	}
	var next string
	switch action {
	case "approve", "cancel":
		if state != planReviewing {
			s.mu.Unlock()
			return state, errNoPlanUnderReview
		}
		if action == "approve" {
			next = planApproved
		} else {
			next = planCancelled
		}
		s.state = next
	default:
		s.mu.Unlock()
		return state, errUnknownPlanAction
	}
	review := contracts.PlanReview{Content: s.content, State: next}
	handler := s.handler
	s.mu.Unlock()
	if handler != nil {
		handler(review)
	}
	s.gw.broadcastEvent("plan.reviewing", map[string]any{"state": review.State, "content": review.Content})
	s.gw.hub.markDomainDirty(domainPlan)
	return next, nil
}

// State reports the current plan review state.
func (s *planService) State() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// notificationAction is one retained toast action (the wire cannot carry the
// contract's func(), so it becomes an actionId the client posts back).
type notificationAction struct {
	run func()
}

// notifyService implements contracts.Notifier and its optional halves.
type notifyService struct {
	gw *Gateway
}

// SetHandler installs a local toast handler (kept for embedders).
func (s *notifyService) SetHandler(fn func(contracts.Notification)) {
	s.gw.mu.Lock()
	s.gw.toastHandler = fn
	s.gw.mu.Unlock()
}

// Notify posts one toast.
func (s *notifyService) Notify(level, text string) contracts.Notification {
	return s.post("", level, text, "", "", false)
}

// NotifyAction posts one toast with an action button.
func (s *notifyService) NotifyAction(level, text, actionLabel string, action func()) contracts.Notification {
	return s.post("", level, text, actionLabel, s.retainAction(action), false)
}

// NotifySystem posts one toast and asks the tray shell to mirror it to the OS
// notification center. The gateway process owns no notification sink (§7.2).
func (s *notifyService) NotifySystem(level, text string) contracts.Notification {
	return s.post("", level, text, "", "", true)
}

// NotifySystemFor is NotifySystem tagged with the originating conversation.
func (s *notifyService) NotifySystemFor(level, text, sessionID string) contracts.Notification {
	return s.post(sessionID, level, text, "", "", true)
}

// SystemNotificationsSupported reports that the gateway holds no OS sink: the
// tray shell does. Settings surfaces read this to explain the switch.
func (s *notifyService) SystemNotificationsSupported() bool { return false }

// Dismiss removes one toast.
func (s *notifyService) Dismiss(id string) {
	g := s.gw
	g.mu.Lock()
	delete(g.toastActions, id)
	g.mu.Unlock()
}

// post builds, records and broadcasts one notification.
func (s *notifyService) post(sessionID, level, text, actionLabel, actionID string, wantSystem bool) contracts.Notification {
	g := s.gw
	notification := contracts.Notification{
		ID:          g.newID("toast"),
		Level:       level,
		SessionID:   sessionID,
		Text:        text,
		ActionLabel: actionLabel,
	}
	g.mu.Lock()
	if actionID != "" {
		g.toastActions[notification.ID] = actionID
	}
	handler := g.toastHandler
	g.mu.Unlock()
	if handler != nil {
		handler(notification)
	}
	payload := map[string]any{
		"id":    notification.ID,
		"level": notification.Level,
		"text":  notification.Text,
	}
	if sessionID != "" {
		payload["sessionId"] = sessionID
	}
	if actionLabel != "" {
		payload["actionLabel"] = actionLabel
		payload["actionId"] = actionID
	}
	if wantSystem {
		payload["wantSystem"] = true
	}
	g.broadcastEvent("toast.posted", payload)
	return notification
}

// retainAction registers one action closure under a fresh id.
func (s *notifyService) retainAction(action func()) string {
	if action == nil {
		return ""
	}
	g := s.gw
	id := g.newID("act")
	g.mu.Lock()
	g.actions[id] = &notificationAction{run: action}
	g.mu.Unlock()
	return id
}

// pendingQuestionSnapshot returns the question a run is currently parked on, if
// any (the ask domain's payload).
func (g *Gateway) pendingQuestionSnapshot() *askDTO {
	g.mu.Lock()
	defer g.mu.Unlock()
	for id, question := range g.questions {
		return &askDTO{
			ID:        id,
			Text:      question.text,
			Options:   append([]string(nil), question.options...),
			CreatedAt: timePointer(question.createdAt),
		}
	}
	return nil
}

// snapshot returns the plan review state and content.
func (s *planService) snapshot() (string, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, s.content
}

// artifactSnapshot returns the retained artifacts, newest first, as domain DTOs.
func (g *Gateway) artifactSnapshot() []artifactDTO {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]artifactDTO, 0, len(g.artifactItems))
	for i := len(g.artifactItems) - 1; i >= 0; i-- {
		item := g.artifactItems[i]
		out = append(out, artifactDTO{
			ID:        item.ID,
			Title:     item.Title,
			Body:      item.Body,
			CreatedAt: timePointer(item.CreatedAt),
		})
	}
	return out
}

// foregroundService implements contracts.ForegroundSession.
type foregroundService struct {
	gw *Gateway
}

// ForegroundSession returns the conversation the most recently reporting
// client is watching.
func (s *foregroundService) ForegroundSession() (string, bool) {
	g := s.gw
	g.mu.Lock()
	defer g.mu.Unlock()
	var best foregroundEntry
	found := false
	for _, entry := range g.foregroundByClient {
		if !found || entry.order > best.order {
			best, found = entry, true
		}
	}
	if !found || strings.TrimSpace(best.sessionID) == "" {
		return "", false
	}
	return best.sessionID, true
}

// foregroundEntry is one client's reported foreground conversation.
type foregroundEntry struct {
	sessionID string
	order     uint64
}

// setForegroundSession records one client's report.
func (g *Gateway) setForegroundSession(clientID, sessionID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.foregroundTick++
	if strings.TrimSpace(sessionID) == "" {
		delete(g.foregroundByClient, clientID)
		return
	}
	g.foregroundByClient[clientID] = foregroundEntry{sessionID: sessionID, order: g.foregroundTick}
}

// clearForegroundSession forgets a disconnected client's report.
func (g *Gateway) clearForegroundSession(clientID string) {
	g.mu.Lock()
	delete(g.foregroundByClient, clientID)
	g.mu.Unlock()
}

// answerQuestion resolves one pending ask (the ask.answer method).
func (g *Gateway) answerQuestion(raw []byte) (any, *protocolError) {
	var params askAnswerParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.ID) == "" {
		return nil, errInvalidParams("id", "is required")
	}
	g.interaction.Answer(params.ID, params.Answer)
	return struct{}{}, nil
}

// cancelQuestion dismisses one pending ask (the ask.cancel method).
func (g *Gateway) cancelQuestion(raw []byte) (any, *protocolError) {
	var params askAnswerParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.ID) == "" {
		return nil, errInvalidParams("id", "is required")
	}
	g.interaction.Cancel(params.ID)
	return struct{}{}, nil
}

// exitPlan applies one plan decision (the plan.exit method).
func (g *Gateway) exitPlan(raw []byte) (any, *protocolError) {
	var params planExitParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	state, err := g.plan.Exit(params.Action)
	if err != nil {
		return nil, internalError(err)
	}
	return planExitResult{State: state}, nil
}

// runNotificationAction runs one retained toast action (the notify.action
// method).
func (g *Gateway) runNotificationAction(raw []byte) (any, *protocolError) {
	var params notifyActionParams
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	g.mu.Lock()
	actionID, ok := g.toastActions[params.ID]
	var action *notificationAction
	if ok && (params.ActionID == "" || params.ActionID == actionID) {
		action = g.actions[actionID]
		delete(g.actions, actionID)
		delete(g.toastActions, params.ID)
	}
	g.mu.Unlock()
	if action == nil {
		return nil, appError(codeCapabilityUnsupported, "action_unavailable", false)
	}
	// The action was written for the desktop UI thread; the gateway has no such
	// thread, and the module that registered it never touches Go UI state.
	if action.run != nil {
		go action.run()
	}
	return struct{}{}, nil
}
