package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func newCursorRequestTestSession(t *testing.T) (*acpSession, *bytes.Buffer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := &acpSession{
		ctx: ctx, cancel: cancel, events: make(chan core.Event, 8),
		permByID:      make(map[string]permState),
		toolInputByID: make(map[string]string), toolNameByID: make(map[toolCallKey]string),
	}
	s.alive.Store(true)
	var out bytes.Buffer
	s.tr = newTransport(strings.NewReader(""), &out, s.onNotification, s.onServerRequest)
	return s, &out
}

func assertCursorRPCResponse(t *testing.T, out *bytes.Buffer, id, result string) {
	t.Helper()
	want := `{"jsonrpc":"2.0","id":` + id + `,"result":` + result + "}\n"
	if out.String() != want {
		t.Fatalf("response = %s, want %s", out.String(), want)
	}
}

func TestCursorAskQuestion_ForwardsAndMapsAnswers(t *testing.T) {
	for _, tc := range []struct {
		name, id, behavior string
		multiple           bool
		answer             any
		want               string
	}{
		{"single-label", `"ask-1"`, "allow", false, "a", `{"outcome":{"outcome":"answered","answers":[{"questionId":"q1","selectedOptionIds":["opt-a"]}]}}`},
		{"single-id", `42`, "ALLOW", false, "OPT-B", `{"outcome":{"outcome":"answered","answers":[{"questionId":"q1","selectedOptionIds":["opt-b"]}]}}`},
		{"deny", `"deny-1"`, "deny", false, "A", `{"outcome":{"outcome":"skipped","reason":"user declined"}}`},
		{"other-behavior", `43`, "cancel", false, "A", `{"outcome":{"outcome":"skipped","reason":"user declined"}}`},
		{"multi-comma", `44`, "allow", true, "A, B", `{"outcome":{"outcome":"answered","answers":[{"questionId":"q1","selectedOptionIds":["opt-a","opt-b"]}]}}`},
		{"multi-chinese-comma", `45`, "allow", true, "a，b", `{"outcome":{"outcome":"answered","answers":[{"questionId":"q1","selectedOptionIds":["opt-a","opt-b"]}]}}`},
		{"multi-whitespace", `46`, "allow", true, "A\t B", `{"outcome":{"outcome":"answered","answers":[{"questionId":"q1","selectedOptionIds":["opt-a","opt-b"]}]}}`},
		{"multi-slice", `47`, "allow", true, []string{"A", "unknown", "OPT-B", "a"}, `{"outcome":{"outcome":"answered","answers":[{"questionId":"q1","selectedOptionIds":["opt-a","opt-b"]}]}}`},
		{"free-text", `"free"`, "allow", false, "something else", `{"outcome":{"outcome":"skipped"}}`},
		{"missing-answer", `48`, "allow", false, nil, `{"outcome":{"outcome":"skipped"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, out := newCursorRequestTestSession(t)
			params := `{"toolCallId":"tool-1","title":"Choose","questions":[{"id":"q1","prompt":"Which?","options":[{"id":"opt-a","label":"A"},{"id":"opt-b","label":"B"}],"allowMultiple":` + strconv.FormatBool(tc.multiple) + `}]}`
			s.tr.dispatchLine([]byte(`{"jsonrpc":"2.0","id":` + tc.id + `,"method":"cursor/ask_question","params":` + params + `}`))
			if out.Len() != 0 {
				t.Fatalf("blocking question acknowledged before user answer: %s", out.String())
			}
			ev := readEvent(t, s)
			wantQuestions := []core.UserQuestion{{Question: "Which?", Header: "Choose", Options: []core.UserQuestionOption{{Label: "A"}, {Label: "B"}}, MultiSelect: tc.multiple}}
			if ev.Type != core.EventPermissionRequest || ev.ToolName != "AskUserQuestion" || ev.RequestID != jsonIDKey(json.RawMessage(tc.id)) || !reflect.DeepEqual(ev.Questions, wantQuestions) || !strings.Contains(ev.ToolInput, "Which?") {
				t.Fatalf("question event = %+v", ev)
			}
			if len(s.events) != 0 {
				t.Fatal("more than one question event")
			}
			result := core.PermissionResult{Behavior: tc.behavior, UpdatedInput: map[string]any{"answers": map[string]any{"Which?": tc.answer}}}
			if err := s.RespondPermission(ev.RequestID, result); err != nil {
				t.Fatal(err)
			}
			assertCursorRPCResponse(t, out, tc.id, tc.want)
			if err := s.RespondPermission(ev.RequestID, result); err == nil || !strings.Contains(err.Error(), "unknown permission request") {
				t.Fatalf("late response error = %v", err)
			}
		})
	}
}

func TestCursorAskQuestion_EmptyQuestionsSkipped(t *testing.T) {
	s, out := newCursorRequestTestSession(t)
	s.onServerRequest("cursor/ask_question", json.RawMessage(`"empty"`), json.RawMessage(`{"toolCallId":"tool-empty","questions":[]}`))
	assertCursorRPCResponse(t, out, `"empty"`, `{"outcome":{"outcome":"skipped"}}`)
	if len(s.events) != 0 {
		t.Fatal("empty questions emitted an event")
	}
	if err := s.RespondPermission("empty", core.PermissionResult{Behavior: "allow"}); err == nil || !strings.Contains(err.Error(), "unknown permission request") {
		t.Fatalf("empty questions left pending request: %v", err)
	}
}

func TestCursorCreatePlan_ForwardsApproval(t *testing.T) {
	for _, tc := range []struct{ behavior, outcome string }{{"allow", "accepted"}, {"deny", "rejected"}} {
		t.Run(tc.behavior, func(t *testing.T) {
			s, out := newCursorRequestTestSession(t)
			s.onServerRequest("cursor/create_plan", json.RawMessage(`"plan-1"`), json.RawMessage(`{"toolCallId":"tool-plan","name":"Migration","overview":"Upgrade storage","plan":"1. Back up\n2. Migrate","todos":[],"isProject":true,"phases":[]}`))
			if out.Len() != 0 {
				t.Fatalf("blocking plan acknowledged before user decision: %s", out.String())
			}
			ev := readEvent(t, s)
			if ev.Type != core.EventPermissionRequest || ev.ToolName != "Plan" || ev.RequestID != "plan-1" || len(ev.Questions) != 0 {
				t.Fatalf("plan event = %+v", ev)
			}
			for _, text := range []string{"Migration", "Upgrade storage", "1. Back up\n2. Migrate"} {
				if !strings.Contains(ev.ToolInput, text) {
					t.Fatalf("plan input %q missing %q", ev.ToolInput, text)
				}
			}
			if len(s.events) != 0 {
				t.Fatal("more than one plan event")
			}
			if err := s.RespondPermission(ev.RequestID, core.PermissionResult{Behavior: tc.behavior}); err != nil {
				t.Fatal(err)
			}
			assertCursorRPCResponse(t, out, `"plan-1"`, `{"outcome":{"outcome":"`+tc.outcome+`"}}`)
			if err := s.RespondPermission(ev.RequestID, core.PermissionResult{Behavior: tc.behavior}); err == nil || !strings.Contains(err.Error(), "unknown permission request") {
				t.Fatalf("late response error = %v", err)
			}
		})
	}
}

func TestCursorExtensions_PreserveAcknowledgements(t *testing.T) {
	for _, method := range []string{"cursor/update_todos", "cursor/task", "cursor/generate_image", "cursor/unknown_extension"} {
		t.Run(method, func(t *testing.T) {
			s, out := newCursorRequestTestSession(t)
			s.onNotification(method, json.RawMessage(`{}`))
			if len(s.events) != 0 || out.Len() != 0 {
				t.Fatal("notification emitted an event or response")
			}
			s.onServerRequest(method, json.RawMessage(`7`), json.RawMessage(`{}`))
			assertCursorRPCResponse(t, out, `7`, `{}`)
		})
	}
}

func TestCursorExtensions_PreserveStandardPermission(t *testing.T) {
	for _, tc := range []struct{ behavior, want string }{
		{"allow", `{"outcome":{"optionId":"yes","outcome":"selected"}}`},
		{"deny", `{"outcome":{"optionId":"no","outcome":"selected"}}`},
	} {
		t.Run(tc.behavior, func(t *testing.T) {
			s, out := newCursorRequestTestSession(t)
			s.toolInputByID["shell"] = "pwd"
			s.onServerRequest("session/request_permission", json.RawMessage(`9`), json.RawMessage(`{"toolCall":{"toolCallId":"shell","title":"Shell","kind":"execute"},"options":[{"optionId":"yes","kind":"allow_once"},{"optionId":"no","kind":"reject_once"}]}`))
			ev := readEvent(t, s)
			s.wg.Wait()
			if ev.Type != core.EventPermissionRequest || ev.ToolName != "Shell" || ev.RequestID != "9" {
				t.Fatalf("standard permission event = %+v", ev)
			}
			if err := s.RespondPermission(ev.RequestID, core.PermissionResult{Behavior: tc.behavior}); err != nil {
				t.Fatal(err)
			}
			assertCursorRPCResponse(t, out, `9`, tc.want)
		})
	}
}

func TestMapCursorAnswers_PreservesQuestionIDsAndMultiwordLabels(t *testing.T) {
	questions := []cursorQuestion{
		{ID: "database", Prompt: "Database?", AllowMultiple: true, Options: []cursorQuestionOption{{ID: "pg", Label: "PostgreSQL database"}, {ID: "sqlite", Label: "SQLite database"}}},
		{ID: "missing", Prompt: "Unanswered?", Options: []cursorQuestionOption{{ID: "x", Label: "X"}}},
		{ID: "framework", Prompt: "Framework?", Options: []cursorQuestionOption{{ID: "gin", Label: "Gin"}, {ID: "echo", Label: "Echo"}}},
	}
	input := map[string]any{"answers": map[string]any{
		"Framework?": []string{"ECHO", "Gin"},
		"Database?":  "postgresql database, SQLite database",
	}}
	want := []cursorQuestionAnswer{
		{QuestionID: "database", SelectedOptionIDs: []string{"pg", "sqlite"}},
		{QuestionID: "framework", SelectedOptionIDs: []string{"echo"}},
	}
	if got := mapCursorAnswers(questions, input); !reflect.DeepEqual(got, want) {
		t.Fatalf("mapped answers = %+v, want %+v", got, want)
	}
	if got := mapCursorAnswers(questions, nil); len(got) != 0 {
		t.Fatalf("nil input fabricated answers: %+v", got)
	}
}

func TestCursorBlockingRequests_InvalidParams(t *testing.T) {
	for _, method := range []string{"cursor/ask_question", "cursor/create_plan"} {
		t.Run(method, func(t *testing.T) {
			s, out := newCursorRequestTestSession(t)
			s.onServerRequest(method, json.RawMessage(`12`), json.RawMessage(`{"plan":[],"questions":false}`))
			want := "{\"jsonrpc\":\"2.0\",\"id\":12,\"error\":{\"code\":-32602,\"message\":\"invalid params\"}}\n"
			if out.String() != want || len(s.events) != 0 {
				t.Fatalf("invalid request response = %s, events = %d", out.String(), len(s.events))
			}
		})
	}
}
