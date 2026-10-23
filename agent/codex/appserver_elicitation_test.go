package codex

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestAppServerSession_MCPElicitationRequiresExplicitAnswer(t *testing.T) {
	for _, tc := range []struct {
		name, mode, behavior, answer, want, approvalKind string
	}{
		{"URL allow", "url", "allow", "", "accept", ""},
		{"URL deny", "url", "deny", "", "decline", ""},
		{"form deny", "form", "deny", `{"name":"Ada"}`, "decline", ""},
		{"form parse failure", "form", "allow", "not JSON", "decline", ""},
		{"form array", "form", "allow", "[]", "decline", ""},
		{"form null", "form", "allow", "null", "decline", ""},
		{"form missing answer", "form", "allow", "", "decline", ""},
		{"form allow", "form", "allow", `{"name":"Ada"}`, "accept", ""},
		{"openai form allow", "openai/form", "allow", `{"name":"Ada"}`, "accept", ""},
		{"openaiForm allow", "openaiForm", "allow", `{"name":"Ada"}`, "accept", ""},
		{"other approval kind missing answer", "form", "allow", "", "decline", "browser_auth"},
		{"other approval kind form allow", "form", "allow", `{"name":"Ada"}`, "accept", "browser_auth"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, responses := newElicitationTestSession(t)
			params := map[string]any{
				"serverName": "example-server", "threadId": "thread-1", "turnId": "turn-1",
				"mode": tc.mode, "message": "Please provide details", "url": "https://example.test/authorize",
				"title": "Authorization", "description": "Requested by the MCP server",
				"requestedSchema": map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}},
			}
			if tc.approvalKind != "" {
				params["_meta"] = map[string]any{"codex_approval_kind": tc.approvalKind}
			}
			s.handleServerRequest(serverRequestProbe(t, `"elicitation-1"`, "mcpServer/elicitation/request", params))
			var event core.Event
			select {
			case event = <-s.events:
			case <-time.After(time.Second):
				t.Fatal("no elicitation permission event")
			}
			if event.Type != core.EventPermissionRequest || event.RequestID != `"elicitation-1"` || event.SessionID != "thread-1" {
				t.Fatalf("event = %#v", event)
			}
			if !strings.Contains(event.ToolInput, "Please provide details") || event.ToolInputRaw["serverName"] != "example-server" || event.ToolInputRaw["turnId"] != "turn-1" {
				t.Fatalf("missing request details: %#v", event)
			}
			result := core.PermissionResult{Behavior: tc.behavior}
			if tc.mode == "url" {
				if event.ToolName == "AskUserQuestion" || !strings.Contains(event.ToolInput, "https://example.test/authorize") {
					t.Fatalf("URL event = %#v", event)
				}
			} else {
				if event.ToolName != "AskUserQuestion" || len(event.Questions) != 1 || len(event.Questions[0].Options) != 0 {
					t.Fatalf("form event = %#v", event)
				}
				q := event.Questions[0].Question
				if !strings.Contains(q, `"type":"object"`) || !strings.Contains(q, "JSON object for content") {
					t.Fatalf("question = %q", q)
				}
				result.UpdatedInput = map[string]any{"answers": map[string]any{q: tc.answer}}
			}
			select {
			case reply := <-responses:
				t.Fatalf("elicitation auto-answered: %#v", reply)
			default:
			}
			if err := s.RespondPermission(event.RequestID, result); err != nil {
				t.Fatal(err)
			}
			reply := receiveElicitationResponse(t, responses)
			if reply.Action != tc.want {
				t.Fatalf("action = %q, want %q", reply.Action, tc.want)
			}
			if tc.mode != "url" && tc.want == "accept" {
				if reply.Content["name"] != "Ada" {
					t.Fatalf("content = %#v", reply.Content)
				}
			} else if reply.Content != nil {
				t.Fatalf("unexpected content: %#v", reply.Content)
			}
		})
	}
}

func TestAppServerSession_MCPToolApprovalUsesPermissionDecision(t *testing.T) {
	fullMeta := map[string]any{
		"codex_approval_kind": "mcp_tool_call", "tool_name": "ma3_report",
		"tool_title": "Save report", "tool_description": "Write a verified result",
		"tool_params": map[string]any{"problem": "raw problem", "outcome": "resolved"},
		"tool_params_display": []any{
			map[string]any{"name": "problem", "display_name": "Problem", "value": "approval bridge"},
			map[string]any{"name": "outcome", "value": "resolved"},
		},
	}
	for _, tc := range []struct {
		name, server, message, behavior, wantTool, wantAction string
		wantMetaTool                                          string
		meta                                                  map[string]any
		contains, absent                                      []string
	}{
		{
			name: "full metadata allow", server: "ma3", behavior: "allow", wantTool: "MCP ma3/ma3_report", wantAction: "accept", meta: fullMeta,
			contains: []string{"Save report", "Write a verified result", "Problem: approval bridge", "outcome: resolved"}, absent: []string{"raw problem"},
		},
		{
			name: "full metadata deny", server: "ma3", behavior: "deny", wantTool: "MCP ma3/ma3_report", wantAction: "decline", meta: fullMeta,
		},
		{
			name: "missing tool name", server: "ma3", behavior: "allow", wantTool: "MCP ma3", wantAction: "accept",
			meta:     map[string]any{"codex_approval_kind": "mcp_tool_call", "tool_params": map[string]any{"zeta": "last", "alpha": "first", "enabled": false}},
			contains: []string{"alpha: first", "enabled: false", "zeta: last"},
		},
		{
			name: "tool name parsed from Codex message", server: "ma3",
			message:  `Allow the ma3 MCP server to run tool "ma3_report"?`,
			behavior: "allow", wantTool: "MCP ma3/ma3_report", wantAction: "accept", wantMetaTool: "ma3_report",
			meta: map[string]any{"codex_approval_kind": "mcp_tool_call"},
		},
		{
			name: "structured params", server: "ma3", behavior: "allow", wantTool: "MCP ma3/ma3_report", wantAction: "accept",
			meta: map[string]any{
				"codex_approval_kind": "mcp_tool_call", "tool_name": "ma3_report",
				"tool_params": map[string]any{"array": []any{}, "object": map[string]any{}, "null": nil, "nested": map[string]any{"enabled": false}},
			},
			contains: []string{"array: []", "object: {}", "null: null", `nested: {"enabled":false}`},
		},
		{
			name: "missing server name", behavior: "allow", wantTool: "MCP ma3_report", wantAction: "accept", meta: fullMeta,
		},
		{
			name: "missing server and tool", behavior: "allow", wantTool: "MCP", wantAction: "accept",
			meta: map[string]any{"codex_approval_kind": "mcp_tool_call"},
		},
		{
			name: "context cancel", server: "ma3", behavior: "cancel", wantTool: "MCP ma3/ma3_report", wantAction: "decline", meta: fullMeta,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, responses := newElicitationTestSession(t)
			ctx, cancel := context.WithCancel(s.ctx)
			t.Cleanup(cancel)
			s.ctx = ctx
			message := tc.message
			if message == "" {
				message = "Approve this tool call?"
			}
			s.handleServerRequest(serverRequestProbe(t, `"elicitation-1"`, "mcpServer/elicitation/request", map[string]any{
				"serverName": tc.server, "threadId": "thread-1", "turnId": "turn-1", "mode": "form",
				"message": message, "_meta": tc.meta,
			}))
			var event core.Event
			select {
			case event = <-s.events:
			case <-time.After(time.Second):
				t.Fatal("no MCP tool permission event")
			}
			if event.Type != core.EventPermissionRequest || event.RequestID != `"elicitation-1"` || event.SessionID != "thread-1" || event.ToolName != tc.wantTool || len(event.Questions) != 0 {
				t.Fatalf("event = %#v", event)
			}
			toolName, _ := tc.meta["tool_name"].(string)
			if tc.wantMetaTool != "" {
				toolName = tc.wantMetaTool
			}
			for _, want := range append([]string{message, tc.server}, tc.contains...) {
				if !strings.Contains(event.ToolInput, want) {
					t.Errorf("ToolInput = %q, missing %q", event.ToolInput, want)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(event.ToolInput, absent) {
					t.Errorf("ToolInput = %q, unexpectedly contains %q", event.ToolInput, absent)
				}
			}
			for key, want := range map[string]any{
				"serverName": tc.server, "threadId": "thread-1", "turnId": "turn-1", "mode": "form",
				"toolName": toolName, "toolParams": tc.meta["tool_params"], "_meta": tc.meta,
			} {
				if got, ok := event.ToolInputRaw[key]; !ok || !reflect.DeepEqual(got, want) {
					t.Errorf("ToolInputRaw[%q] = %#v (present %v), want %#v", key, got, ok, want)
				}
			}
			select {
			case reply := <-responses:
				t.Fatalf("tool approval auto-answered: %s", reply)
			default:
			}
			if tc.behavior == "cancel" {
				cancel()
			} else if err := s.RespondPermission(event.RequestID, core.PermissionResult{Behavior: tc.behavior}); err != nil {
				t.Fatal(err)
			}
			reply := receiveElicitationResponse(t, responses)
			if reply.Action != tc.wantAction || reply.Content != nil {
				t.Fatalf("reply = %#v, want action %q without content", reply, tc.wantAction)
			}
			s.approvalsMu.Lock()
			pending := len(s.pendingApprovals)
			s.approvalsMu.Unlock()
			if pending != 0 {
				t.Fatalf("pending approvals after response = %d", pending)
			}
		})
	}
}

func TestAppServerSession_MCPToolApprovalTimeoutDeclines(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, responses := newElicitationTestSession(t)
		s.handleServerRequest(serverRequestProbe(t, `"elicitation-1"`, "mcpServer/elicitation/request", map[string]any{
			"serverName": "ma3", "mode": "form",
			"_meta": map[string]any{"codex_approval_kind": "mcp_tool_call", "tool_name": "ma3_report"},
		}))
		synctest.Wait()
		time.Sleep(5*time.Minute - time.Second)
		select {
		case reply := <-responses:
			t.Fatalf("approval timed out before five minutes: %s", reply)
		default:
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if reply := receiveElicitationResponse(t, responses); reply.Action != "decline" || reply.Content != nil {
			t.Fatalf("timeout reply = %#v", reply)
		}
		if err := s.RespondPermission(`"elicitation-1"`, core.PermissionResult{Behavior: "allow"}); err == nil {
			t.Fatal("timed out request still pending")
		}
	})
}

func TestAppServerSession_MCPElicitationUnsupportedModesDecline(t *testing.T) {
	for _, mode := range []string{"openai/userVerification", "unknown", ""} {
		t.Run(mode, func(t *testing.T) {
			s, responses := newElicitationTestSession(t)
			s.handleServerRequest(serverRequestProbe(t, `"elicitation-1"`, "mcpServer/elicitation/request", map[string]any{"mode": mode}))
			if reply := receiveElicitationResponse(t, responses); reply.Action != "decline" {
				t.Fatalf("reply = %#v", reply)
			}
			assertNoTurnEvents(t, s)
		})
	}
}

type elicitationTestReply struct {
	Action  string         `json:"action"`
	Content map[string]any `json:"content"`
}

func newElicitationTestSession(t *testing.T) (*appServerSession, chan []byte) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	responses := make(chan []byte, 1)
	s := &appServerSession{ctx: ctx, events: make(chan core.Event, 4)}
	s.stdin = &turnStartReplyWriter{write: func(p []byte) (int, error) {
		responses <- append([]byte(nil), p...)
		return len(p), nil
	}}
	return s, responses
}

func receiveElicitationResponse(t *testing.T, responses chan []byte) elicitationTestReply {
	t.Helper()
	var data []byte
	select {
	case data = <-responses:
	case <-time.After(time.Second):
		t.Fatal("no elicitation response")
	}
	var envelope struct {
		ID     string               `json:"id"`
		Result elicitationTestReply `json:"result"`
		Error  json.RawMessage      `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.ID != "elicitation-1" || len(envelope.Error) != 0 {
		t.Fatalf("envelope = %s", data)
	}
	return envelope.Result
}
