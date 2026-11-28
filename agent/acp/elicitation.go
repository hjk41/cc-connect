package acp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

// elicitationOption is one selectable choice parsed from an elicitation
// form schema. Value is the enum `const` that must round-trip back to the
// agent verbatim; Label is the human-facing title the engine renders and
// echoes back in answers. For kimi acp the two are identical, but the ACP
// schema allows them to differ, so they are tracked separately.
type elicitationOption struct {
	Value       string
	Label       string
	Description string
}

// elicitationQuestion pairs a parsed question with its property key in the
// elicitation form schema (e.g. "q0"), which the response content map is
// keyed by.
type elicitationQuestion struct {
	PropertyKey string
	// OtherPropertyKey is Factory Droid's companion free-text field
	// (<question key>_other) used when the enum value "other" is selected.
	OtherPropertyKey string
	Question         core.UserQuestion
	Options          []elicitationOption
}

// elicitationState is cached per pending elicitation/create request so
// RespondPermission can translate engine answers back into the
// CreateElicitationResponse shape the agent expects.
type elicitationState struct {
	Questions []elicitationQuestion
}

// elicitationEnumOption matches the ACP EnumOption shape used by oneOf /
// items.anyOf entries in a form-mode requestedSchema.
type elicitationEnumOption struct {
	Const       string `json:"const"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type elicitationProperty struct {
	Type        string                  `json:"type"`
	Title       string                  `json:"title"`
	Description string                  `json:"description"`
	OneOf       []elicitationEnumOption `json:"oneOf"`
	Items       *struct {
		AnyOf []elicitationEnumOption `json:"anyOf"`
	} `json:"items"`
}

// parseElicitationCreateParams parses elicitation/create form-mode params
// into ordered questions. Single-select questions arrive as
// type:"string"+oneOf; multi-select as type:"array"+items.anyOf (kimi acp
// also sets minItems:1, making every question required). Property order is
// taken from requestedSchema.required (kimi emits q0, q1, ... in question
// order); without it, keys are sorted for determinism.
//
// Properties that carry no enum options (free-text string/number/boolean
// fields) are skipped: cc-connect's ask-question wizard only renders
// option lists.
func parseElicitationCreateParams(params json.RawMessage) (*elicitationState, error) {
	var p struct {
		Mode    string `json:"mode"`
		Message string `json:"message"`
		Schema  struct {
			Properties map[string]elicitationProperty `json:"properties"`
			Required   []string                       `json:"required"`
		} `json:"requestedSchema"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("acp: parse elicitation/create params: %w", err)
	}
	if p.Mode != "" && p.Mode != "form" {
		return nil, fmt.Errorf("acp: unsupported elicitation mode %q", p.Mode)
	}
	if len(p.Schema.Properties) == 0 {
		return nil, fmt.Errorf("acp: elicitation/create with empty requestedSchema.properties")
	}

	order := elicitationPropertyOrder(p.Schema.Properties, p.Schema.Required)
	st := &elicitationState{}
	for _, key := range order {
		prop := p.Schema.Properties[key]
		q, ok := elicitationPropertyToQuestion(prop)
		if !ok {
			continue
		}
		otherKey := ""
		if otherProp, ok := p.Schema.Properties[key+"_other"]; ok && otherProp.Type == "string" {
			otherKey = key + "_other"
		}
		st.Questions = append(st.Questions, elicitationQuestion{
			PropertyKey:      key,
			OtherPropertyKey: otherKey,
			Question:         q,
			Options:          elicitationEnumOptions(prop),
		})
	}
	if len(st.Questions) == 0 {
		return nil, fmt.Errorf("acp: elicitation/create schema has no enumerable option fields")
	}
	return st, nil
}

// elicitationPropertyOrder returns property keys in `required` order,
// appending any non-required keys (sorted) afterwards.
func elicitationPropertyOrder(props map[string]elicitationProperty, required []string) []string {
	seen := make(map[string]bool, len(required))
	order := make([]string, 0, len(props))
	for _, key := range required {
		if _, ok := props[key]; ok && !seen[key] {
			seen[key] = true
			order = append(order, key)
		}
	}
	var rest []string
	for key := range props {
		if !seen[key] {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	return append(order, rest...)
}

func elicitationPropertyToQuestion(prop elicitationProperty) (core.UserQuestion, bool) {
	q := core.UserQuestion{Question: prop.Title, Header: prop.Title}
	// Devin maps ask_user_question's short header to `title` and the full
	// question text to `description`; kimi puts the question in `title` and
	// leaves `description` empty. Prefer the longer form when present.
	if prop.Description != "" {
		q.Question = prop.Description
	}
	switch prop.Type {
	case "string":
		if len(prop.OneOf) == 0 {
			return core.UserQuestion{}, false
		}
	case "array":
		if prop.Items == nil || len(prop.Items.AnyOf) == 0 {
			return core.UserQuestion{}, false
		}
		q.MultiSelect = true
	default:
		return core.UserQuestion{}, false
	}
	for _, opt := range elicitationEnumOptions(prop) {
		q.Options = append(q.Options, core.UserQuestionOption{
			Label:       opt.Label,
			Description: opt.Description,
		})
	}
	if q.Question == "" || len(q.Options) == 0 {
		return core.UserQuestion{}, false
	}
	return q, true
}

func elicitationEnumOptions(prop elicitationProperty) []elicitationOption {
	raw := prop.OneOf
	if prop.Type == "array" && prop.Items != nil {
		raw = prop.Items.AnyOf
	}
	// Option shape differs by agent: kimi uses const=value/title=label/
	// description=hint; Devin uses const=label/title=description with no
	// `description` field; Factory Droid uses machine consts (option_0,
	// option_1, other), human labels in title, and hints in description.
	factoryStyle := machineValuedElicitationOptions(raw)
	devinStyle := !factoryStyle && prop.Description != ""
	opts := make([]elicitationOption, 0, len(raw))
	for _, o := range raw {
		opt := elicitationOption{Value: o.Const}
		switch {
		case factoryStyle:
			opt.Label = o.Title
			if opt.Label == "" {
				opt.Label = o.Const
			}
			opt.Description = o.Description
		case devinStyle:
			opt.Label = o.Const
			opt.Description = o.Title
		default:
			opt.Label = o.Title
			if opt.Label == "" {
				opt.Label = o.Const
			}
			opt.Description = o.Description
		}
		opts = append(opts, opt)
	}
	return opts
}

// machineValuedElicitationOptions recognizes Factory Droid's generated enum
// values. Every non-other option is option_<number>; meaningful user-defined
// labels (including a literal "option_0" in another agent's schema) therefore
// do not take this path unless the whole generated pattern is present.
func machineValuedElicitationOptions(raw []elicitationEnumOption) bool {
	seenNumbered := false
	for _, o := range raw {
		if o.Const == "other" {
			continue
		}
		suffix, ok := strings.CutPrefix(o.Const, "option_")
		if !ok {
			return false
		}
		if _, err := strconv.Atoi(suffix); err != nil {
			return false
		}
		seenNumbered = true
	}
	return seenNumbered
}

func matchElicitationOption(options []elicitationOption, answer string) (elicitationOption, bool) {
	for _, opt := range options {
		if answer == opt.Label || answer == opt.Value {
			return opt, true
		}
	}
	return elicitationOption{}, false
}

func elicitationOtherOption(q elicitationQuestion) (elicitationOption, bool) {
	if q.OtherPropertyKey == "" {
		return elicitationOption{}, false
	}
	for _, opt := range q.Options {
		if opt.Value == "other" {
			return opt, true
		}
	}
	return elicitationOption{}, false
}

// buildElicitationMultiSelectValues maps the engine's comma-joined answer
// back to enum consts in declared order. Unknown parts are custom text; when
// the schema has Factory Droid's "other" plus <key>_other companion field,
// they are returned through that channel instead of being dropped.
func buildElicitationMultiSelectValues(q elicitationQuestion, ans string) ([]string, string) {
	parts := strings.Split(ans, ", ")
	matched := make(map[string]bool, len(parts))
	customParts := make([]string, 0, 1)
	other, hasOther := elicitationOtherOption(q)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if opt, ok := matchElicitationOption(q.Options, part); ok {
			matched[opt.Value] = true
			continue
		}
		customParts = append(customParts, part)
	}

	wantOther := len(customParts) > 0 && hasOther
	picked := make([]string, 0, len(matched)+1)
	for _, opt := range q.Options {
		if matched[opt.Value] || (wantOther && opt.Value == other.Value) {
			picked = append(picked, opt.Value)
		}
	}
	if wantOther {
		return picked, strings.Join(customParts, ", ")
	}
	return picked, ""
}

// buildElicitationResponse translates an engine PermissionResult into an
// ACP CreateElicitationResponse. The engine reports answers as
// UpdatedInput["answers"], a map keyed by question text with string values
// (multi-select labels joined with ", " in declared order). Here they are
// re-keyed to the form's property keys and re-split into arrays for
// multi-select questions, with each label mapped back to its enum const.
//
// Anything other than an allow with at least one matched answer resolves
// to {"action":"decline"}, which kimi acp maps to the tool's canonical
// "user dismissed" branch (decline and cancel are equivalent there).
func buildElicitationResponse(st *elicitationState, result core.PermissionResult) map[string]any {
	decline := map[string]any{"action": "decline"}
	if !strings.EqualFold(result.Behavior, "allow") || st == nil {
		return decline
	}
	answersRaw, ok := result.UpdatedInput["answers"]
	if !ok {
		return decline
	}
	answers, ok := answersRaw.(map[string]any)
	if !ok || len(answers) == 0 {
		return decline
	}

	content := make(map[string]any, len(st.Questions))
	for _, q := range st.Questions {
		ans, _ := answers[q.Question.Question].(string)
		ans = strings.TrimSpace(ans)
		if ans == "" {
			continue
		}
		if q.Question.MultiSelect {
			picked, otherText := buildElicitationMultiSelectValues(q, ans)
			if len(picked) > 0 {
				content[q.PropertyKey] = picked
			}
			if otherText != "" && q.OtherPropertyKey != "" {
				content[q.OtherPropertyKey] = otherText
			}
			continue
		}
		if opt, ok := matchElicitationOption(q.Options, ans); ok {
			content[q.PropertyKey] = opt.Value
			continue
		}
		if other, ok := elicitationOtherOption(q); ok {
			content[q.PropertyKey] = other.Value
			content[q.OtherPropertyKey] = ans
			continue
		}
		content[q.PropertyKey] = ans
	}
	if len(content) == 0 {
		return decline
	}
	return map[string]any{
		"action":  "accept",
		"content": content,
	}
}
