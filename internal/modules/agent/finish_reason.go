package agent

// RequireFinishReason resolves the product's stream-completeness policy.
//
// The OpenAI-compatible provider (github.com/zeokeel/zeokeel-rig) implements the
// detection itself behind openai.Config.RequireCompleteStream: a stream that
// closes before a finish_reason or the [DONE] terminator returns
// openai.ErrIncompleteStream instead of a clean io.EOF. The product always
// requires a clean terminator (A2 fixed behavior): the old chat.requireFinishReason
// override was a Settings control that no longer exists, so neither the stored
// row nor Config.RequireFinishReason can turn the guard off. The config field is
// kept because shipped assemblies still set it.

// requireFinishReason reports whether streams must terminate cleanly. It is
// always true: truncated answers must never pass as a successful turn.
func (s *service) requireFinishReason() bool {
	return true
}
