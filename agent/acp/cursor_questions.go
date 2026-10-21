package acp

import (
	"encoding/json"
	"log/slog"
	"strings"
	"unicode"

	"github.com/chenhg5/cc-connect/core"
)

type cursorAskQuestionRequest struct {
	ToolCallID string           `json:"toolCallId"`
	Title      string           `json:"title,omitempty"`
	Questions  []cursorQuestion `json:"questions"`
}

type cursorQuestion struct {
	ID            string                 `json:"id"`
	Prompt        string                 `json:"prompt"`
	Options       []cursorQuestionOption `json:"options"`
	AllowMultiple bool                   `json:"allowMultiple,omitempty"`
}

type cursorQuestionOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type cursorQuestionAnswer struct {
	QuestionID        string   `json:"questionId"`
	SelectedOptionIDs []string `json:"selectedOptionIds"`
}

type cursorAskQuestionResponse struct {
	Outcome cursorAskQuestionOutcome `json:"outcome"`
}

type cursorAskQuestionOutcome struct {
	Outcome string                 `json:"outcome"`
	Answers []cursorQuestionAnswer `json:"answers,omitempty"`
	Reason  string                 `json:"reason,omitempty"`
}

type cursorAskQuestionState struct {
	RPCID     json.RawMessage
	Questions []cursorQuestion
}

type cursorCreatePlanRequest struct {
	ToolCallID string          `json:"toolCallId"`
	Name       string          `json:"name,omitempty"`
	Overview   string          `json:"overview,omitempty"`
	Plan       string          `json:"plan"`
	Todos      json.RawMessage `json:"todos,omitempty"`
	IsProject  bool            `json:"isProject,omitempty"`
	Phases     json.RawMessage `json:"phases,omitempty"`
}

type cursorCreatePlanResponse struct {
	Outcome cursorCreatePlanOutcome `json:"outcome"`
}

type cursorCreatePlanOutcome struct {
	Outcome string `json:"outcome"`
	PlanURI string `json:"planUri,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func (p cursorAskQuestionRequest) userQuestions() []core.UserQuestion {
	questions := make([]core.UserQuestion, 0, len(p.Questions))
	for _, q := range p.Questions {
		options := make([]core.UserQuestionOption, 0, len(q.Options))
		for _, option := range q.Options {
			options = append(options, core.UserQuestionOption{Label: option.Label})
		}
		questions = append(questions, core.UserQuestion{
			Question: q.Prompt, Header: p.Title, Options: options, MultiSelect: q.AllowMultiple,
		})
	}
	return questions
}

// mapCursorAnswers translates the engine's prompt-keyed answers back to the
// original option IDs. Unknown/free-text answers never become selections.
func mapCursorAnswers(questions []cursorQuestion, updatedInput map[string]any) []cursorQuestionAnswer {
	answers, _ := updatedInput["answers"].(map[string]any)
	var mapped []cursorQuestionAnswer
	for _, q := range questions {
		var values []string
		switch answer := answers[q.Prompt].(type) {
		case string:
			values = []string{answer}
		case []string:
			values = answer
		}
		var ids []string
		seen := make(map[string]bool)
		for _, value := range values {
			for _, id := range cursorAnswerOptionIDs(q, value) {
				if !seen[id] && (q.AllowMultiple || len(ids) == 0) {
					ids = append(ids, id)
					seen[id] = true
				}
			}
		}
		if len(ids) > 0 {
			mapped = append(mapped, cursorQuestionAnswer{QuestionID: q.ID, SelectedOptionIDs: ids})
		}
	}
	return mapped
}

func cursorAnswerOptionIDs(q cursorQuestion, value string) []string {
	match := func(value string) string {
		value = strings.TrimSpace(value)
		for _, option := range q.Options {
			if strings.EqualFold(value, option.Label) || strings.EqualFold(value, option.ID) {
				return option.ID
			}
		}
		return ""
	}
	// Try the full label first so options containing spaces or commas work.
	if id := match(value); id != "" {
		return []string{id}
	}
	if !q.AllowMultiple {
		return nil
	}
	var ids []string
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '，' }) {
		if id := match(part); id != "" {
			ids = append(ids, id)
			continue
		}
		for _, token := range strings.FieldsFunc(part, unicode.IsSpace) {
			if id := match(token); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func (s *acpSession) handleCursorAskQuestion(id, params json.RawMessage) {
	var p cursorAskQuestionRequest
	if err := json.Unmarshal(params, &p); err != nil {
		logCursorResponseError(id, s.tr.respondError(id, -32602, "invalid params"))
		return
	}
	if len(p.Questions) == 0 {
		logCursorResponseError(id, s.tr.respondSuccess(id, cursorAskQuestionResponse{Outcome: cursorAskQuestionOutcome{Outcome: "skipped"}}))
		return
	}
	key := jsonIDKey(id)
	s.askMu.Lock()
	if s.askByID == nil {
		s.askByID = make(map[string]cursorAskQuestionState)
	}
	s.askByID[key] = cursorAskQuestionState{RPCID: id, Questions: p.Questions}
	s.askMu.Unlock()
	questions := p.userQuestions()
	input := []string{p.Title}
	for _, q := range p.Questions {
		input = append(input, q.Prompt)
	}
	s.emit(core.Event{
		Type: core.EventPermissionRequest, RequestID: key, ToolName: "AskUserQuestion",
		Questions: questions, ToolInput: strings.TrimSpace(strings.Join(input, "\n")),
		ToolInputRaw: map[string]any{"questions": questions},
	})
}

func (s *acpSession) handleCursorCreatePlan(id, params json.RawMessage) {
	var p cursorCreatePlanRequest
	if err := json.Unmarshal(params, &p); err != nil {
		logCursorResponseError(id, s.tr.respondError(id, -32602, "invalid params"))
		return
	}
	key := jsonIDKey(id)
	s.planMu.Lock()
	if s.planByID == nil {
		s.planByID = make(map[string]json.RawMessage)
	}
	s.planByID[key] = id
	s.planMu.Unlock()
	var input []string
	for _, text := range []string{p.Name, p.Overview, p.Plan} {
		if strings.TrimSpace(text) != "" {
			input = append(input, text)
		}
	}
	s.emit(core.Event{
		Type: core.EventPermissionRequest, RequestID: key, ToolName: "Plan",
		ToolInput: strings.Join(input, "\n\n"),
	})
}

func logCursorResponseError(id json.RawMessage, err error) {
	if err != nil {
		slog.Warn("acp: cursor extension response failed", "request_id", jsonIDKey(id), "error", err)
	}
}
