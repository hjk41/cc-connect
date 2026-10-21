package codex

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestAppServerSession_MCPElicitationRequiresExplicitAnswer(t *testing.T) {
	for _, tc := range []struct {
		name, mode, behavior, answer, want string
	}{
		{"URL allow", "url", "allow", "", "accept"},
		{"URL deny", "url", "deny", "", "decline"},
		{"form deny", "form", "deny", `{"name":"Ada"}`, "decline"},
		{"form parse failure", "form", "allow", "not JSON", "decline"},
		{"form array", "form", "allow", "[]", "decline"},
		{"form null", "form", "allow", "null", "decline"},
		{"form missing answer", "form", "allow", "", "decline"},
		{"form allow", "form", "allow", `{"name":"Ada"}`, "accept"},
		{"openai form allow", "openai/form", "allow", `{"name":"Ada"}`, "accept"},
		{"openaiForm allow", "openaiForm", "allow", `{"name":"Ada"}`, "accept"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, responses := newElicitationTestSession(t)
			s.handleServerRequest(serverRequestProbe(t, `"elicitation-1"`, "mcpServer/elicitation/request", map[string]any{
				"serverName": "example-server", "threadId": "thread-1", "turnId": "turn-1",
				"mode": tc.mode, "message": "Please provide details", "url": "https://example.test/authorize",
				"title": "Authorization", "description": "Requested by the MCP server",
				"requestedSchema": map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}},
			}))
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
