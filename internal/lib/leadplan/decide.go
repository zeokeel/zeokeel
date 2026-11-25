package leadplan

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/zeokeel/zeokeel/internal/contracts"
)

// Field caps. A model that exceeds them is rejected, never silently truncated:
// a subject cut in half is a worse outcome than asking again on the next pass.
const (
	maxSubjectRunes  = 200
	maxDetailRunes   = 4000
	maxAssigneeRunes = 64
)

// fencePattern matches a markdown code block and captures its body. The
// language tag is optional, and models sometimes put the JSON on the same line
// as the fence.
var fencePattern = regexp.MustCompile("(?s)```[A-Za-z0-9_+-]*[ \t]*\r?\n?(.*?)```")

// decisionPayload is the wire shape. Unrelated keys are ignored on purpose:
// models add "confidence" or "reasoning" and that is not a reason to fail.
type decisionPayload struct {
	Action   string `json:"action"`
	Subject  string `json:"subject"`
	Detail   string `json:"detail"`
	Assignee string `json:"assignee"`
	Note     string `json:"note"`
}

// ParseDecision turns a model answer into a validated decision. It accepts a
// bare object, a fenced code block, or an object surrounded by prose (the first
// balanced object wins), then enforces the action vocabulary and the field caps.
//
// Errors are typed: contracts.ErrLeadPlanParse when no usable JSON object is
// present, contracts.ErrLeadPlanInvalid when the JSON is well formed but the
// decision is not.
func ParseDecision(raw string) (contracts.PlanDecision, error) {
	text := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	if text == "" {
		return contracts.PlanDecision{}, fmt.Errorf("%w: empty answer", contracts.ErrLeadPlanParse)
	}

	// 1. The whole answer is JSON (the normal case).
	if json.Valid([]byte(text)) {
		return decodeObject(text)
	}
	// 2. The answer is a fenced code block, possibly with prose around it.
	for _, block := range fencedBlocks(text) {
		if json.Valid([]byte(block)) {
			return decodeObject(block)
		}
	}
	// 3. The answer is prose with an object somewhere inside.
	for _, object := range balancedObjects(text) {
		if json.Valid([]byte(object)) {
			return decodeObject(object)
		}
	}
	return contracts.PlanDecision{}, fmt.Errorf("%w: no JSON object found", contracts.ErrLeadPlanParse)
}

// decodeObject decodes one JSON value as a decision. Valid JSON that is not an
// object (an array, a string, a bare number) is a wrong answer, not prose, so
// it fails as a parse error instead of being searched for objects.
func decodeObject(text string) (contracts.PlanDecision, error) {
	var payload decisionPayload
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return contracts.PlanDecision{}, fmt.Errorf("%w: %v", contracts.ErrLeadPlanParse, err)
	}
	return normalizeAndValidate(payload)
}

// fencedBlocks returns the body of every markdown code block, trimmed and in
// order.
func fencedBlocks(text string) []string {
	var out []string
	for _, match := range fencePattern.FindAllStringSubmatch(text, -1) {
		if body := strings.TrimSpace(match[1]); body != "" {
			out = append(out, body)
		}
	}
	return out
}

// balancedObjects returns every top-level brace-balanced substring, in order.
// Braces inside JSON strings do not count, which is what makes an item body
// like "use {these} braces" harmless.
func balancedObjects(text string) []string {
	var out []string
	offset := 0
	for {
		start := strings.IndexByte(text[offset:], '{')
		if start < 0 {
			return out
		}
		start += offset
		object, end, ok := scanObject(text, start)
		if ok {
			out = append(out, object)
			offset = end
			continue
		}
		offset = start + 1
	}
}

// scanObject returns the balanced object starting at start, and the index just
// past it.
func scanObject(text string, start int) (string, int, bool) {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1], i + 1, true
			}
		}
	}
	return "", 0, false
}

// normalizeAndValidate trims the fields, then checks the action vocabulary and
// the caps.
func normalizeAndValidate(payload decisionPayload) (contracts.PlanDecision, error) {
	decision := contracts.PlanDecision{
		Action:   strings.ToLower(strings.TrimSpace(payload.Action)),
		Subject:  strings.TrimSpace(payload.Subject),
		Detail:   strings.TrimSpace(payload.Detail),
		Assignee: strings.TrimSpace(payload.Assignee),
		Note:     strings.TrimSpace(payload.Note),
	}
	if err := validateDecision(decision); err != nil {
		return contracts.PlanDecision{}, err
	}
	return decision, nil
}

// validateDecision reports the first rule the decision breaks.
func validateDecision(d contracts.PlanDecision) error {
	switch d.Action {
	case contracts.LeadActionTask:
		if d.Subject == "" {
			return fmt.Errorf("%w: action %q requires a non-empty subject", contracts.ErrLeadPlanInvalid, d.Action)
		}
	case contracts.LeadActionIgnore, contracts.LeadActionSkip:
	case "":
		return fmt.Errorf("%w: missing action", contracts.ErrLeadPlanInvalid)
	default:
		return fmt.Errorf("%w: unknown action %q (want %q, %q or %q)", contracts.ErrLeadPlanInvalid,
			d.Action, contracts.LeadActionTask, contracts.LeadActionIgnore, contracts.LeadActionSkip)
	}
	for _, field := range []struct {
		name string
		text string
		max  int
	}{
		{"subject", d.Subject, maxSubjectRunes},
		{"detail", d.Detail, maxDetailRunes},
		{"assignee", d.Assignee, maxAssigneeRunes},
	} {
		if n := utf8.RuneCountInString(field.text); n > field.max {
			return fmt.Errorf("%w: %s is %d characters (max %d)", contracts.ErrLeadPlanInvalid, field.name, n, field.max)
		}
	}
	return nil
}

// Decide runs one planning round: build the prompt, ask the model, parse the
// answer.
//
// ok is false when the model chose "skip": nothing should be applied and the
// item stays for a human. The decision is still returned in that case so the
// caller can record why. A model failure is wrapped so errors.Is can tell it
// from a bad answer.
func Decide(ctx context.Context, model contracts.PlannerModel, item contracts.InboxItem, rules []contracts.LeadRule, openTasks int) (contracts.PlanDecision, bool, error) {
	if err := ctx.Err(); err != nil {
		return contracts.PlanDecision{}, false, err
	}
	if model == nil {
		return contracts.PlanDecision{}, false, fmt.Errorf("%w: no planner model configured", contracts.ErrLeadPlanModel)
	}

	prompt := BuildPrompt(item, rules, openTasks)
	answer, err := model.Decide(ctx, prompt)
	if err != nil {
		return contracts.PlanDecision{}, false, fmt.Errorf("%w: %w", contracts.ErrLeadPlanModel, err)
	}

	decision, err := ParseDecision(answer)
	if err != nil {
		return contracts.PlanDecision{}, false, err
	}
	return decision, decision.Action != contracts.LeadActionSkip, nil
}
