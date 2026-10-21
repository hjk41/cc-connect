package dsh

import "strings"

// canonicalEffort maps cc-connect effort names onto dsh thought_level
// values. dsh accepts off, low, high, and max.
func canonicalEffort(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return ""
	case "medium":
		return "high"
	case "xhigh":
		return "max"
	case "minimal", "none":
		return "off"
	default:
		return strings.TrimSpace(raw)
	}
}

// SetReasoningEffort stores the effort applied after the model.
// A running session takes it immediately; otherwise the next message
// of the current conversation does. Aliases are rewritten before the
// ACP thought_level check.
func (a *Agent) SetReasoningEffort(effort string) {
	if a == nil || a.Agent == nil {
		return
	}
	a.Agent.SetReasoningEffort(canonicalEffort(effort))
}
