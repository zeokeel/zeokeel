package approvals

// Chat-reply parsing for approvals. The rule is deliberately narrow: an exact
// match against a small vocabulary (or that vocabulary plus an explicit item
// index). Anything else returns false so the caller asks for a number instead
// of guessing on the user's behalf.

import (
	"strconv"
	"strings"
)

// DecisionKind is what the user said.
type DecisionKind int

const (
	// DecisionNone is the zero value: nothing was understood.
	DecisionNone DecisionKind = iota
	// DecisionApprove approves the current item once.
	DecisionApprove
	// DecisionApproveAlways approves this item and remembers the pattern.
	DecisionApproveAlways
	// DecisionReject declines the item; the draft is kept.
	DecisionReject
	// DecisionUndo takes back a decision made in the last window.
	DecisionUndo
)

// Decision is one parsed reply. Index is 1-based and 0 when the reply carried no
// index (only legal while a single approval waits).
type Decision struct {
	Kind  DecisionKind
	Index int
}

// replyVocabulary maps an exact reply to a decision. The map is intentionally a
// closed list: adding a synonym is a deliberate product decision, not a
// heuristic.
var replyVocabulary = map[string]DecisionKind{
	// approve
	"同意":  DecisionApprove,
	"可以":  DecisionApprove,
	"发吧":  DecisionApprove,
	"行":   DecisionApprove,
	"批准":  DecisionApprove,
	"允许":  DecisionApprove,
	"ok":  DecisionApprove,
	"yes": DecisionApprove,
	// approve always
	"以后都同意":  DecisionApproveAlways,
	"一直同意":   DecisionApproveAlways,
	"都同意":    DecisionApproveAlways,
	"always": DecisionApproveAlways,
	// reject
	"不同意": DecisionReject,
	"不行":  DecisionReject,
	"别发":  DecisionReject,
	"拒绝":  DecisionReject,
	"不要":  DecisionReject,
	"否":   DecisionReject,
	"no":  DecisionReject,
	// undo
	"改主意":  DecisionUndo,
	"撤回":   DecisionUndo,
	"撤销":   DecisionUndo,
	"undo": DecisionUndo,
}

// indexMarkers are stripped together with digits so "第 3 条同意", "同意 #2" and
// "3: 不同意" all reduce to the same shape.
var indexMarkers = []string{"第", "条", "#", ":", "：", "的", "个", "号"}

// ParseReply interprets one chat message. pending is how many approvals wait in
// that conversation: with more than one, an index is mandatory.
func ParseReply(text string, pending int) (Decision, bool) {
	raw := strings.TrimSpace(text)
	if raw == "" || strings.HasPrefix(raw, ">") {
		return Decision{}, false
	}
	// A question is not a decision ("同意吗？我再想想").
	if strings.ContainsAny(raw, "?？") {
		return Decision{}, false
	}

	normalized := strings.ToLower(raw)
	normalized = strings.TrimRight(normalized, "！!。.,，;；:： \t\r\n")
	normalized = strings.TrimLeft(normalized, " \t\r\n")

	index, hasIndex := extractIndex(normalized)
	body := stripIndex(normalized)

	kind, ok := replyVocabulary[body]
	if !ok {
		return Decision{}, false
	}

	if hasIndex {
		if index < 1 || index > pending {
			return Decision{}, false
		}
		return Decision{Kind: kind, Index: index}, true
	}
	// Without an index the reply is only unambiguous while one item waits.
	if pending > 1 {
		return Decision{}, false
	}
	return Decision{Kind: kind}, true
}

// extractIndex finds the first digit run in text ("" / non-numeric = no index).
func extractIndex(text string) (int, bool) {
	start := -1
	for i, r := range text {
		if r >= '0' && r <= '9' {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			value, err := strconv.Atoi(text[start:i])
			if err != nil {
				return 0, false
			}
			return value, true
		}
	}
	if start >= 0 {
		value, err := strconv.Atoi(text[start:])
		if err != nil {
			return 0, false
		}
		return value, true
	}
	return 0, false
}

// stripIndex removes the digits and the index markers, leaving the decision
// word behind ("同意 #2" -> "同意").
func stripIndex(text string) string {
	out := text
	for _, marker := range indexMarkers {
		out = strings.ReplaceAll(out, marker, " ")
	}
	var b strings.Builder
	for _, r := range out {
		if r >= '0' && r <= '9' {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), "")
}
