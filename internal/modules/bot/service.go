package bot

// The bot store: bot templates (reusable岗位 definitions) and bot instances
// (persistent, owned entities). Plan G1 — "Bot = 模板 + 记忆作用域 + 工具授予 +
// 默认模型".
//
// The rules that shape this file:
//
//   - A bot is an entity, not a session. The id is stable for the life of the
//     record, and the record outlives every conversation it takes part in.
//   - Inheritance happens once, at create time, and the instance stores the
//     resolved policy (model, tool whitelist, memory scope). Editing a template
//     later must not reach back into bots that already exist: a tool grant is a
//     permission surface, and permissions do not grow behind the user's back.
//   - Creation never overwrites. A duplicate id is ErrBotDuplicate, because
//     replacing a bot would replace its grants with the new ones.
//   - Identity is immutable. Update may retune a bot's name, owner, model,
//     grants, scope and status; it may not change the id, the template it came
//     from, or when it was created.
//   - A retired bot is retired. Retiring is terminal (ErrBotInvalid on any
//     attempt to move it back), so "this bot is gone" cannot quietly undo
//     itself.
//   - Everything is validated on the way in AND on the way out of the file: a
//     document this build cannot fully honour is reported as
//     contracts.ErrBotStoreCorrupt with an empty, usable store, rather than
//     partially loaded. Half a bot list is worse than none: a silently dropped
//     instance changes who may act.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Validation budgets. They are deliberately small: a bot is a small record, not
// a place to store a prompt library.
const (
	// MinIDChars and MaxIDChars bound the [a-z0-9-]{2,64} id rule.
	MinIDChars = 2
	// MaxIDChars is the id cap; it also keeps an id short enough to sit in a
	// task card without truncation.
	MaxIDChars = 64
	// MaxNameRunes bounds a template or bot display name.
	MaxNameRunes = 80
	// MaxRoleRunes bounds the岗位 label.
	MaxRoleRunes = 80
	// MaxModelRunes bounds the model reference ("" = follow the session).
	MaxModelRunes = 120
	// MaxTools bounds how many tools one role may hold.
	MaxTools = 32
	// MaxOwnerRunes bounds the owner field. Not a G1 rule, just store hygiene:
	// the field is free-form until team sharing defines its shape.
	MaxOwnerRunes = 120
)

// idPattern is the id rule for bots and templates, stated once so creation,
// update and the loader cannot drift: lower-case ASCII, digits and dashes,
// 2..64 of them. It also keeps an id safe as a path/diagnostic fragment.
var idPattern = regexp.MustCompile(`^[a-z0-9-]{2,64}$`)

// toolPattern is the tool-name alphabet the tool surface already uses
// (`browser_open`, `mcp__server__tool`, `fs.read`). Lower case only: names are
// compared by exact match downstream, so accepting mixed case would make "the
// same tool" ambiguous.
var toolPattern = regexp.MustCompile(`^[a-z0-9_.:-]{1,120}$`)

// LeadTemplateID is the built-in template every product has on first start, so
// there is always something to instantiate before anyone designs a team.
const LeadTemplateID = "lead"

// Service is the template + instance store. It is safe for concurrent use; the
// maps are the running source of truth and every mutation rewrites the document.
type Service struct {
	mu        sync.Mutex
	path      string // <dir>/bots.json; empty means in-memory only
	templates map[string]contracts.BotTemplate
	instances map[string]contracts.BotInstance
	now       func() time.Time
}

var _ contracts.BotService = (*Service)(nil)

// NewService builds an in-memory store already carrying the built-in templates.
// Open is the durable constructor; this one is for tests and for a caller that
// only wants the validation rules.
func NewService() *Service {
	service := &Service{
		templates: make(map[string]contracts.BotTemplate),
		instances: make(map[string]contracts.BotInstance),
		now:       time.Now,
	}
	for _, template := range builtinTemplates() {
		service.templates[template.ID] = template
	}
	return service
}

// builtinTemplates are the templates a fresh product starts with. Seeding is
// idempotent and never overwrites: a user who retunes "lead" keeps their version.
func builtinTemplates() []contracts.BotTemplate {
	return []contracts.BotTemplate{{
		ID:          LeadTemplateID,
		Name:        "Lead",
		Role:        "Lead",
		Description: "编排者：把收件箱里的活变成任务，派给队友，验收结果；队友只在自己被授予的范围内动手。",
		MemoryScope: contracts.BotMemoryScopeTeam,
	}}
}

// seedLocked installs any built-in template that is missing. The caller holds the
// lock. It runs on every open (and every construction), which is what makes the
// built-in template present even when the document was corrupt or predates it.
func (s *Service) seedLocked() {
	for _, template := range builtinTemplates() {
		if _, exists := s.templates[template.ID]; !exists {
			s.templates[template.ID] = template
		}
	}
}

// Templates implements contracts.BotService.
func (s *Service) Templates(ctx context.Context) ([]contracts.BotTemplate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contracts.BotTemplate, 0, len(s.templates))
	for _, template := range s.templates {
		out = append(out, cloneTemplate(template))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// PutTemplate implements contracts.BotService: it upserts one template.
//
// Two properties the settings screen depends on, stated here because they are
// not visible in the signature:
//
//   - The write replaces the whole record. A field the caller omits is cleared
//     (an empty MemoryScope becomes the agent default), never inherited from the
//     previous version: a "patch" that kept an old tool list would make the
//     grant depend on what the caller happened not to send.
//   - The id is the identity. There is no rename, so editing a template cannot
//     leave its instances pointing at a name that no longer exists.
//
// What an edit does NOT do is equally deliberate: it does not touch instances.
// Each instance stored the model, tool whitelist and memory scope resolved from
// the template at creation time (see resolve), so a retuned template applies to
// bots created afterwards only. A tool grant is a permission surface, and
// permissions must not grow behind the user's back just because a template was
// edited.
func (s *Service) PutTemplate(ctx context.Context, template contracts.BotTemplate) (contracts.BotTemplate, error) {
	if err := ctx.Err(); err != nil {
		return contracts.BotTemplate{}, err
	}
	normalized, err := normalizeTemplate(template)
	if err != nil {
		return contracts.BotTemplate{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	previous, hadPrevious := s.templates[normalized.ID]
	// Only the template map is written. Instances keep the policy they resolved
	// at creation, which is what makes a template edit non-retroactive.
	s.templates[normalized.ID] = normalized
	if err := s.saveLocked(); err != nil {
		if hadPrevious {
			s.templates[normalized.ID] = previous
		} else {
			delete(s.templates, normalized.ID)
		}
		return contracts.BotTemplate{}, err
	}
	return cloneTemplate(normalized), nil
}

// Template implements contracts.BotService: one template by id.
//
// The boolean is "found", not an error. A get backs a form that may still be
// empty, so "no such template" is an answer the caller renders rather than a
// fault it reports; ErrBotTemplateUnknown stays reserved for the write path,
// where naming a missing template is a real mistake (Create).
func (s *Service) Template(ctx context.Context, id string) (contracts.BotTemplate, bool, error) {
	if err := ctx.Err(); err != nil {
		return contracts.BotTemplate{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	template, ok := s.templates[strings.TrimSpace(id)]
	if !ok {
		return contracts.BotTemplate{}, false, nil
	}
	return cloneTemplate(template), true, nil
}

// Instances implements contracts.BotService.
func (s *Service) Instances(ctx context.Context) ([]contracts.BotInstance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contracts.BotInstance, 0, len(s.instances))
	for _, instance := range s.instances {
		out = append(out, cloneInstance(instance))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Create implements contracts.BotService. Inheritance is resolved here, once.
func (s *Service) Create(ctx context.Context, instance contracts.BotInstance) (contracts.BotInstance, error) {
	if err := ctx.Err(); err != nil {
		return contracts.BotInstance{}, err
	}
	instance = trimInstance(instance)
	if err := validateID("bot id", instance.ID); err != nil {
		return contracts.BotInstance{}, err
	}
	if instance.TemplateID == "" {
		return contracts.BotInstance{}, fmt.Errorf("%w: bot %q names no template", contracts.ErrBotInvalid, instance.ID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	template, ok := s.templates[instance.TemplateID]
	if !ok {
		return contracts.BotInstance{}, fmt.Errorf("%w: %q", contracts.ErrBotTemplateUnknown, instance.TemplateID)
	}
	if _, exists := s.instances[instance.ID]; exists {
		return contracts.BotInstance{}, fmt.Errorf("%w: %q", contracts.ErrBotDuplicate, instance.ID)
	}

	resolved := resolve(template, instance)
	if resolved.CreatedAt.IsZero() {
		resolved.CreatedAt = s.now()
	}
	resolved.CreatedAt = resolved.CreatedAt.UTC().Truncate(time.Second)
	if !resolved.LastSeenAt.IsZero() {
		resolved.LastSeenAt = resolved.LastSeenAt.UTC().Truncate(time.Second)
	}
	if resolved.Status == "" {
		resolved.Status = contracts.BotStatusActive
	}
	if err := validateInstance(resolved); err != nil {
		return contracts.BotInstance{}, err
	}

	s.instances[resolved.ID] = resolved
	if err := s.saveLocked(); err != nil {
		// The map already holds the bot; drop it again so a failed write does not
		// leave the process believing in something a restart will not have.
		delete(s.instances, resolved.ID)
		return contracts.BotInstance{}, err
	}
	return cloneInstance(resolved), nil
}

// Update implements contracts.BotService.
//
// Which fields an empty value means "leave alone" for, and why: a field whose
// empty value is a real value is written verbatim (DefaultModel "" = follow the
// session, Owner "" = unowned), and every other field treats empty as "keep
// what is stored". That is the only reading under which a caller can both patch
// one field and deliberately clear a grant (a non-nil empty AllowedTools).
func (s *Service) Update(ctx context.Context, instance contracts.BotInstance) (contracts.BotInstance, error) {
	if err := ctx.Err(); err != nil {
		return contracts.BotInstance{}, err
	}
	instance = trimInstance(instance)
	if err := validateID("bot id", instance.ID); err != nil {
		return contracts.BotInstance{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.instances[instance.ID]
	if !ok {
		return contracts.BotInstance{}, fmt.Errorf("%w: %q", contracts.ErrBotUnknown, instance.ID)
	}
	// Identity is immutable: the template a bot came from and when it was created
	// are facts about its history, not settings.
	if instance.TemplateID != "" && instance.TemplateID != stored.TemplateID {
		return contracts.BotInstance{}, fmt.Errorf("%w: bot %q cannot change template from %q to %q", contracts.ErrBotInvalid, stored.ID, stored.TemplateID, instance.TemplateID)
	}
	if !instance.CreatedAt.IsZero() && !instance.CreatedAt.UTC().Truncate(time.Second).Equal(stored.CreatedAt) {
		return contracts.BotInstance{}, fmt.Errorf("%w: bot %q cannot change its creation time", contracts.ErrBotInvalid, stored.ID)
	}

	next := instance
	next.TemplateID = stored.TemplateID
	next.CreatedAt = stored.CreatedAt
	if next.Name == "" {
		next.Name = stored.Name
	}
	if next.MemoryScope == "" {
		next.MemoryScope = stored.MemoryScope
	}
	if next.Status == "" {
		next.Status = stored.Status
	}
	if next.AllowedTools == nil {
		next.AllowedTools = cloneTools(stored.AllowedTools)
	}
	if next.LastSeenAt.IsZero() {
		next.LastSeenAt = stored.LastSeenAt
	} else {
		next.LastSeenAt = next.LastSeenAt.UTC().Truncate(time.Second)
	}
	// Retirement is terminal: a bot that was retired must not be reactivated, or
	// "retired" would just be a slower kind of active.
	if stored.Status == contracts.BotStatusRetired && next.Status != contracts.BotStatusRetired {
		return contracts.BotInstance{}, fmt.Errorf("%w: bot %q is retired and cannot become %q", contracts.ErrBotInvalid, stored.ID, next.Status)
	}
	if err := validateInstance(next); err != nil {
		return contracts.BotInstance{}, err
	}

	s.instances[next.ID] = next
	if err := s.saveLocked(); err != nil {
		s.instances[next.ID] = stored
		return contracts.BotInstance{}, err
	}
	return cloneInstance(next), nil
}

// Get implements contracts.BotService.
func (s *Service) Get(ctx context.Context, id string) (contracts.BotInstance, error) {
	if err := ctx.Err(); err != nil {
		return contracts.BotInstance{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	instance, ok := s.instances[strings.TrimSpace(id)]
	if !ok {
		return contracts.BotInstance{}, fmt.Errorf("%w: %q", contracts.ErrBotUnknown, id)
	}
	return cloneInstance(instance), nil
}

// resolve fills the policy an instance did not set from its template.
func resolve(template contracts.BotTemplate, instance contracts.BotInstance) contracts.BotInstance {
	if instance.Name == "" {
		instance.Name = template.Name
	}
	if instance.DefaultModel == "" {
		instance.DefaultModel = template.DefaultModel
	}
	if len(instance.AllowedTools) == 0 {
		instance.AllowedTools = cloneTools(template.AllowedTools)
	}
	if instance.MemoryScope == "" {
		instance.MemoryScope = template.MemoryScope
	}
	if instance.MemoryScope == "" {
		instance.MemoryScope = contracts.BotMemoryScopeAgent
	}
	return instance
}

// normalizeTemplate validates and canonicalizes one template.
func normalizeTemplate(template contracts.BotTemplate) (contracts.BotTemplate, error) {
	template.ID = strings.TrimSpace(template.ID)
	template.Name = strings.TrimSpace(template.Name)
	template.Role = strings.TrimSpace(template.Role)
	template.Description = strings.TrimSpace(template.Description)
	template.DefaultModel = strings.TrimSpace(template.DefaultModel)
	template.SystemPrompt = strings.TrimSpace(template.SystemPrompt)
	template.AllowedTools = trimTools(template.AllowedTools)
	template.MemoryScope = strings.TrimSpace(template.MemoryScope)
	if template.MemoryScope == "" {
		template.MemoryScope = contracts.BotMemoryScopeAgent
	}
	if err := validateTemplate(template); err != nil {
		return contracts.BotTemplate{}, err
	}
	return template, nil
}

func validateTemplate(template contracts.BotTemplate) error {
	if err := validateID("template id", template.ID); err != nil {
		return err
	}
	if template.Name == "" {
		return fmt.Errorf("%w: template %q has no name", contracts.ErrBotInvalid, template.ID)
	}
	if err := bounded("template name", template.Name, MaxNameRunes); err != nil {
		return err
	}
	if err := bounded("template role", template.Role, MaxRoleRunes); err != nil {
		return err
	}
	if err := bounded("template default model", template.DefaultModel, MaxModelRunes); err != nil {
		return err
	}
	if err := validateTools(template.AllowedTools); err != nil {
		return err
	}
	return validateMemoryScope(template.MemoryScope)
}

func validateInstance(instance contracts.BotInstance) error {
	if err := validateID("bot id", instance.ID); err != nil {
		return err
	}
	if err := validateID("template id", instance.TemplateID); err != nil {
		return err
	}
	if instance.Name == "" {
		return fmt.Errorf("%w: bot %q has no name", contracts.ErrBotInvalid, instance.ID)
	}
	if err := bounded("bot name", instance.Name, MaxNameRunes); err != nil {
		return err
	}
	if err := bounded("bot owner", instance.Owner, MaxOwnerRunes); err != nil {
		return err
	}
	if err := bounded("bot default model", instance.DefaultModel, MaxModelRunes); err != nil {
		return err
	}
	if err := validateTools(instance.AllowedTools); err != nil {
		return err
	}
	if err := validateMemoryScope(instance.MemoryScope); err != nil {
		return err
	}
	switch instance.Status {
	case contracts.BotStatusActive, contracts.BotStatusPaused, contracts.BotStatusRetired:
	default:
		return fmt.Errorf("%w: bot %q has unknown status %q", contracts.ErrBotInvalid, instance.ID, instance.Status)
	}
	if instance.CreatedAt.IsZero() {
		return fmt.Errorf("%w: bot %q has no creation time", contracts.ErrBotInvalid, instance.ID)
	}
	return nil
}

func validateID(field, value string) error {
	if !idPattern.MatchString(value) {
		// The value is echoed: an id is not a secret, and "which id was rejected"
		// is the whole point of the message.
		return fmt.Errorf("%w: %s %q must match [a-z0-9-]{2,64}", contracts.ErrBotInvalid, field, value)
	}
	return nil
}

func validateMemoryScope(scope string) error {
	switch scope {
	case contracts.BotMemoryScopeAgent, contracts.BotMemoryScopeTeam:
		return nil
	}
	return fmt.Errorf("%w: memory scope %q must be %q or %q", contracts.ErrBotInvalid, scope, contracts.BotMemoryScopeAgent, contracts.BotMemoryScopeTeam)
}

func validateTools(tools []string) error {
	if len(tools) > MaxTools {
		return fmt.Errorf("%w: %d tools, at most %d allowed", contracts.ErrBotInvalid, len(tools), MaxTools)
	}
	seen := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if !toolPattern.MatchString(tool) {
			return fmt.Errorf("%w: tool %q must match [a-z0-9_.:-]{1,120}", contracts.ErrBotInvalid, tool)
		}
		if seen[tool] {
			return fmt.Errorf("%w: duplicate tool %q", contracts.ErrBotInvalid, tool)
		}
		seen[tool] = true
	}
	return nil
}

func bounded(field, value string, limit int) error {
	if utf8.RuneCountInString(value) > limit {
		return fmt.Errorf("%w: %s is %d runes, at most %d allowed", contracts.ErrBotInvalid, field, utf8.RuneCountInString(value), limit)
	}
	return nil
}

// trimInstance canonicalizes the free text of one instance. Slices are trimmed in
// place-safe fashion (a new slice), so a caller's list is never mutated.
func trimInstance(instance contracts.BotInstance) contracts.BotInstance {
	instance.ID = strings.TrimSpace(instance.ID)
	instance.TemplateID = strings.TrimSpace(instance.TemplateID)
	instance.Name = strings.TrimSpace(instance.Name)
	instance.Owner = strings.TrimSpace(instance.Owner)
	instance.MemoryScope = strings.TrimSpace(instance.MemoryScope)
	instance.DefaultModel = strings.TrimSpace(instance.DefaultModel)
	instance.Status = strings.TrimSpace(instance.Status)
	if instance.AllowedTools != nil {
		instance.AllowedTools = trimTools(instance.AllowedTools)
	}
	return instance
}

func trimTools(tools []string) []string {
	if tools == nil {
		return nil
	}
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, strings.TrimSpace(tool))
	}
	return out
}

func cloneTemplate(template contracts.BotTemplate) contracts.BotTemplate {
	template.AllowedTools = cloneTools(template.AllowedTools)
	return template
}

func cloneInstance(instance contracts.BotInstance) contracts.BotInstance {
	instance.AllowedTools = cloneTools(instance.AllowedTools)
	return instance
}

func cloneTools(tools []string) []string {
	if tools == nil {
		return nil
	}
	out := make([]string, len(tools))
	copy(out, tools)
	return out
}
