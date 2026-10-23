package codex

import (
	"encoding/json"
	"log/slog"
	"sort"
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
	Meta            map[string]any  `json:"_meta"`
}

func (s *appServerSession) handleElicitationRequest(rawID, paramsRaw json.RawMessage) {
	var params appServerElicitationParams
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		slog.Warn("codex app-server: declining malformed elicitation", "error", err)
		s.replyElicitation(rawID, map[string]any{"action": "decline"})
		return
	}
	if params.Meta["codex_approval_kind"] == "mcp_tool_call" {
		s.promptMCPToolApproval(rawID, params)
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

func (s *appServerSession) promptMCPToolApproval(rawID json.RawMessage, params appServerElicitationParams) {
	toolName := mcpApprovalToolName(params)
	var names []string
	for _, name := range []string{params.ServerName, toolName} {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	displayName := "MCP"
	if len(names) > 0 {
		displayName += " " + strings.Join(names, "/")
	}
	event := core.Event{
		Type: core.EventPermissionRequest, RequestID: string(rawID), SessionID: params.ThreadID,
		ToolName: displayName, ToolInput: mcpToolApprovalInput(params, toolName),
		ToolInputRaw: map[string]any{
			"serverName": params.ServerName, "threadId": params.ThreadID, "turnId": params.TurnID, "mode": params.Mode,
			"toolName": toolName, "toolParams": params.Meta["tool_params"], "_meta": params.Meta,
		},
	}
	s.promptElicitationApproval(rawID, event, func(result core.PermissionResult) map[string]any {
		action := "decline"
		if strings.EqualFold(result.Behavior, "allow") {
			action = "accept"
		}
		return map[string]any{"action": action}
	})
}

// mcpApprovalToolName resolves the MCP tool name for the permission card.
// Codex 0.163 does not put tool_name in _meta for MCP tool approvals; the
// raw name is embedded in the approval message. Prefer explicit metadata,
// then parse the quoted name from the message, then fall back to tool_title.
func mcpApprovalToolName(params appServerElicitationParams) string {
	if name, _ := params.Meta["tool_name"].(string); strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	for _, marker := range []string{`tool "`, `tool '`} {
		i := strings.Index(params.Message, marker)
		if i < 0 {
			continue
		}
		quote := marker[len(marker)-1:]
		rest := params.Message[i+len(marker):]
		if j := strings.Index(rest, quote); j > 0 {
			return strings.TrimSpace(rest[:j])
		}
	}
	if title, _ := params.Meta["tool_title"].(string); strings.TrimSpace(title) != "" {
		return strings.TrimSpace(title)
	}
	return ""
}

func mcpToolApprovalInput(params appServerElicitationParams, toolName string) string {
	var lines []string
	if params.Message != "" {
		lines = append(lines, params.Message)
	}
	if params.ServerName != "" {
		lines = append(lines, "Server: "+params.ServerName)
	}
	if toolName != "" {
		lines = append(lines, "Tool: "+toolName)
	}
	for _, key := range []string{"tool_title", "tool_description"} {
		if text, _ := params.Meta[key].(string); text != "" {
			lines = append(lines, text)
		}
	}
	if display, ok := params.Meta["tool_params_display"].([]any); ok {
		for _, entry := range display {
			param, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			name, _ := param["display_name"].(string)
			if name == "" {
				name, _ = param["name"].(string)
			}
			lines = append(lines, name+": "+elicitationParamValue(param["value"]))
		}
	} else if toolParams, ok := params.Meta["tool_params"].(map[string]any); ok {
		keys := make([]string, 0, len(toolParams))
		for key := range toolParams {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			lines = append(lines, key+": "+elicitationParamValue(toolParams[key]))
		}
	}
	return strings.Join(lines, "\n\n")
}

func elicitationParamValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	// Preserve empty objects, arrays and null, which may be meaningful arguments.
	data, err := json.Marshal(value)
	if err != nil {
		slog.Warn("codex app-server: format elicitation parameter failed", "error", err)
		return ""
	}
	return string(data)
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
	s.promptElicitationApproval(rawID, event, func(result core.PermissionResult) map[string]any {
		return elicitationResponse(params.Mode, question, result)
	})
}

func (s *appServerSession) promptElicitationApproval(rawID json.RawMessage, event core.Event, response func(core.PermissionResult) map[string]any) {
	requestID := event.RequestID
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
		s.replyElicitation(rawID, response(result))
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
