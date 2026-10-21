// Package toolallowlist handles bridge-side tool allowlists. Agents own the
// mutable list; sessions receive a copy and only consult it for approvals.
package toolallowlist

import "strings"

// Parse accepts both programmatic options and decoded configuration arrays.
func Parse(value any) []string {
	var tools []string
	switch value := value.(type) {
	case []string:
		tools = value
	case []any:
		for _, entry := range value {
			if tool, ok := entry.(string); ok {
				tools = append(tools, tool)
			}
		}
	}
	return Add(nil, tools...)
}

// Add preserves spelling and order while deduplicating case-insensitively.
func Add(list []string, tools ...string) []string {
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		found := false
		for _, existing := range list {
			if strings.EqualFold(existing, tool) {
				found = true
				break
			}
		}
		if !found {
			list = append(list, tool)
		}
	}
	return list
}

// Allows matches an exact reported tool name. Questions and unknown-tool
// placeholders are never eligible, even if explicitly present in the list.
// Callers must also check that the event is a recognized permission request.
func Allows(list []string, tool string) bool {
	for _, reserved := range []string{"", "AskUserQuestion", "unknown", "permission", "other"} {
		if strings.EqualFold(tool, reserved) {
			return false
		}
	}
	for _, allowed := range list {
		if strings.EqualFold(allowed, tool) {
			return true
		}
	}
	return false
}
