package codex

import (
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

type appServerElicitationParams struct {
	ServerName      string          `json:"serverName"`
	ThreadID        string          `json:"threadId"`
	TurnID          string          `json:"turnId"`
	Message         string          `json:"message"`
	Mode            string          `json:"mode"`
	RequestedSchema json.RawMessage `json:"requestedSchema"`
	URL             string          `json:"url"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
}

func (s *appServerSession) handleElicitationRequest(rawID, paramsRaw json.RawMessage) {
	var params appServerElicitationParams
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		slog.Warn("codex app-server: declining malformed elicitation", "error", err)
		s.replyElicitation(rawID, map[string]any{"action": "decline"})
		return
	}
	form := false
	switch params.Mode {
	case "url":
		if strings.TrimSpace(params.URL) == "" {
			slog.Warn("codex app-server: declining URL elicitation without URL")
			s.replyElicitation(rawID, map[string]any{"action": "decline"})
			return
		}
	case "form", "openai/form", "openaiForm":
		form = true
	case "openai/userVerification":
		slog.Info("codex app-server: declining user verification; IM channel cannot provide device proof", "server", params.ServerName)
		s.replyElicitation(rawID, map[string]any{"action": "decline"})
		return
	default:
		slog.Info("codex app-server: declining unsupported elicitation mode", "mode", params.Mode, "server", params.ServerName)
		s.replyElicitation(rawID, map[string]any{"action": "decline"})
		return
	}
	s.promptElicitation(rawID, params, form)
}

func (s *appServerSession) promptElicitation(rawID json.RawMessage, params appServerElicitationParams, form bool) {
	requestID := string(rawID)
	text := strings.Join([]string{params.ServerName, params.Title, params.Description, params.Message}, "\n")
	event := core.Event{Type: core.EventPermissionRequest, RequestID: requestID, ToolName: "MCP", SessionID: params.ThreadID}
	rawInput := map[string]any{"serverName": params.ServerName, "threadId": params.ThreadID, "turnId": params.TurnID, "mode": params.Mode}
	question := ""
	if form {
		schema := "{}"
		if len(params.RequestedSchema) > 0 {
			var value any
			if json.Unmarshal(params.RequestedSchema, &value) == nil {
				compact, _ := json.Marshal(value)
				schema = string(compact)
			}
		}
		question = text + "\nJSON schema: " + schema + "\nReply with a JSON object for content."
		event.ToolName = "AskUserQuestion"
		event.Questions = []core.UserQuestion{{Question: question, Header: params.Title}}
		rawInput["questions"] = []any{map[string]any{"question": question, "header": params.Title, "options": []any{}}}
		text = question
	} else {
		text += "\n" + params.URL
		rawInput["message"] = text
		rawInput["url"] = params.URL
	}
	event.ToolInput, event.ToolInputRaw = text, rawInput
	ch := make(chan core.PermissionResult, 1)
	s.approvalsMu.Lock()
	if s.pendingApprovals == nil {
		s.pendingApprovals = make(map[string]chan core.PermissionResult)
	}
	s.pendingApprovals[requestID] = ch
	s.approvalsMu.Unlock()
	s.flushPendingAsThinking()
	s.emit(event)
	go func() {
		timer := time.NewTimer(5 * time.Minute)
		defer timer.Stop()
		result := core.PermissionResult{Behavior: "deny"}
		select {
		case result = <-ch:
		case <-s.contextDone():
		case <-timer.C:
		}
		s.approvalsMu.Lock()
		delete(s.pendingApprovals, requestID)
		s.approvalsMu.Unlock()
		response := elicitationResponse(params.Mode, question, result)
		s.replyElicitation(rawID, response)
	}()
}

func elicitationResponse(mode, question string, result core.PermissionResult) map[string]any {
	decline := map[string]any{"action": "decline"}
	if !strings.EqualFold(result.Behavior, "allow") {
		slog.Info("codex app-server: elicitation declined or unanswered", "mode", mode)
		return decline
	}
	if mode == "url" {
		return map[string]any{"action": "accept"}
	}
	answers, _ := result.UpdatedInput["answers"].(map[string]any)
	values := appServerRequestUserInputAnswerValues(answers[question])
	var content map[string]any
	if len(values) != 1 || json.Unmarshal([]byte(values[0]), &content) != nil || content == nil {
		slog.Warn("codex app-server: declining form elicitation; answer must be a JSON object")
		return decline
	}
	return map[string]any{"action": "accept", "content": content}
}

func (s *appServerSession) replyElicitation(rawID json.RawMessage, response map[string]any) {
	if err := s.writeJSON(map[string]any{"jsonrpc": "2.0", "id": rawID, "result": response}); err != nil {
		slog.Warn("codex app-server: reply to elicitation failed", "error", err)
	}
}
