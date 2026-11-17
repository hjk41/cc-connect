package acp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func compactJSON(t *testing.T, s string) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(s)); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// kimi acp form-mode elicitation fixture: one single-select question (q0)
// and one multi-select question (q1), ordered via required.
const elicitationCreateFixture = `{
	"sessionId": "test-session-id",
	"toolCallId": "call_1",
	"mode": "form",
	"message": "Pick a color\nPick toppings",
	"requestedSchema": {
		"type": "object",
		"properties": {
			"q0": {
				"type": "string",
				"title": "Pick a color",
				"oneOf": [
					{"const": "red", "title": "red", "description": "warm"},
					{"const": "blue", "title": "blue"}
				]
			},
			"q1": {
				"type": "array",
				"title": "Pick toppings",
				"minItems": 1,
				"items": {"anyOf": [
					{"const": "cheese", "title": "cheese"},
					{"const": "bacon", "title": "bacon"},
					{"const": "onion", "title": "onion"}
				]}
			}
		},
		"required": ["q0", "q1"]
	}
}`

func TestParseElicitationCreateParams(t *testing.T) {
	st, err := parseElicitationCreateParams(json.RawMessage(elicitationCreateFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Questions) != 2 {
		t.Fatalf("got %d questions, want 2", len(st.Questions))
	}
	q0 := st.Questions[0]
	if q0.PropertyKey != "q0" || q0.Question.Question != "Pick a color" || q0.Question.MultiSelect {
		t.Fatalf("q0 = %+v", q0)
	}
	if len(q0.Question.Options) != 2 || q0.Question.Options[0].Label != "red" || q0.Question.Options[0].Description != "warm" {
		t.Fatalf("q0 options = %+v", q0.Question.Options)
	}
	q1 := st.Questions[1]
	if q1.PropertyKey != "q1" || !q1.Question.MultiSelect || len(q1.Options) != 3 {
		t.Fatalf("q1 = %+v", q1)
	}
}

// Devin (chisel) ask_user_question → elicitation/create fixture: the short
// header goes to `title`, the full question text to `description`, and each
// option's human label to `const` while `title` carries the option
// description. Bug: before the fix the parser used `title` as the question
// and option label, so Feishu cards rendered only the header ("部署形态")
// and long option descriptions — the actual question text was invisible.
const elicitationCreateDevinFixture = `{
	"sessionId": "test-session-id",
	"mode": "form",
	"message": "short_answer 服务放在哪？",
	"requestedSchema": {
		"type": "object",
		"properties": {
			"q0": {
				"type": "string",
				"title": "部署形态",
				"description": "short_answer 服务放在哪？",
				"oneOf": [
					{"const": "独立 Rust 服务", "title": "新 Fargate 服务，与高 QPS 搜索链路隔离"},
					{"const": "web-rust 内新模块", "title": "复用进程内 provider，开发最快"},
					{"const": "独立 Python 服务", "title": ""}
				]
			},
			"q1": {
				"type": "array",
				"title": "问题类型",
				"description": "第一期接受哪些问题类型？",
				"items": {"anyOf": [
					{"const": "单一事实值", "title": "一次检索即可回答"},
					{"const": "是非判断", "title": "回答是/否"}
				]}
			}
		},
		"required": ["q0", "q1"]
	}
}`

func TestParseElicitationCreateParams_DevinQuestionFromDescription(t *testing.T) {
	st, err := parseElicitationCreateParams(json.RawMessage(elicitationCreateDevinFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Questions) != 2 {
		t.Fatalf("got %d questions, want 2", len(st.Questions))
	}
	q0 := st.Questions[0]
	if q0.Question.Question != "short_answer 服务放在哪？" {
		t.Fatalf("q0 question = %q, want full question text, not header", q0.Question.Question)
	}
	if q0.Question.Header != "部署形态" {
		t.Fatalf("q0 header = %q, want 部署形态", q0.Question.Header)
	}
	// Devin puts the option label in const and the description in title;
	// labels must stay the short const text, descriptions the long title.
	if len(q0.Question.Options) != 3 ||
		q0.Question.Options[0].Label != "独立 Rust 服务" ||
		q0.Question.Options[0].Description != "新 Fargate 服务，与高 QPS 搜索链路隔离" {
		t.Fatalf("q0 options = %+v", q0.Question.Options)
	}
	if q0.Question.Options[2].Label != "独立 Python 服务" || q0.Question.Options[2].Description != "" {
		t.Fatalf("q0 option[2] = %+v, want label 独立 Python 服务 with empty description", q0.Question.Options[2])
	}
	q1 := st.Questions[1]
	if !q1.Question.MultiSelect || q1.Question.Question != "第一期接受哪些问题类型？" {
		t.Fatalf("q1 = %+v", q1.Question)
	}
	if len(q1.Options) != 2 || q1.Options[0].Value != "单一事实值" || q1.Options[0].Label != "单一事实值" {
		t.Fatalf("q1 wire options = %+v", q1.Options)
	}
}

func TestBuildElicitationResponse_devinAccept(t *testing.T) {
	st, err := parseElicitationCreateParams(json.RawMessage(elicitationCreateDevinFixture))
	if err != nil {
		t.Fatal(err)
	}
	res := buildElicitationResponse(st, core.PermissionResult{
		Behavior: "allow",
		UpdatedInput: map[string]any{
			"answers": map[string]any{
				"short_answer 服务放在哪？": "独立 Rust 服务",
				"第一期接受哪些问题类型？":        "是非判断, 单一事实值",
			},
		},
	})
	if res["action"] != "accept" {
		t.Fatalf("action = %v, want accept", res["action"])
	}
	content, ok := res["content"].(map[string]any)
	if !ok {
		t.Fatalf("content missing: %v", res)
	}
	if content["q0"] != "独立 Rust 服务" {
		t.Fatalf("q0 = %v, want 独立 Rust 服务", content["q0"])
	}
	picked, ok := content["q1"].([]string)
	if !ok || len(picked) != 2 || picked[0] != "单一事实值" || picked[1] != "是非判断" {
		t.Fatalf("q1 = %v, want declared-order [单一事实值 是非判断]", content["q1"])
	}
}

func TestParseElicitationCreateParams_rejectsNonFormMode(t *testing.T) {
	params := json.RawMessage(`{"mode": "url", "message": "m", "requestedSchema": {"properties": {"q0": {"type": "string"}}}}`)
	if _, err := parseElicitationCreateParams(params); err == nil {
		t.Fatal("want error for url-mode elicitation")
	}
}

func TestParseElicitationCreateParams_rejectsOptionlessSchema(t *testing.T) {
	// Free-text-only forms have no enum options to render in the IM wizard.
	params := json.RawMessage(`{"mode": "form", "message": "m", "requestedSchema": {"properties": {"q0": {"type": "string", "title": "Name?"}}}}`)
	if _, err := parseElicitationCreateParams(params); err == nil {
		t.Fatal("want error when no property carries enum options")
	}
}

func TestParseElicitationCreateParams_ordersByRequired(t *testing.T) {
	// Without `required`, property order falls back to sorted keys.
	params := json.RawMessage(`{
		"mode": "form",
		"requestedSchema": {"properties": {
			"qb": {"type": "string", "title": "B?", "oneOf": [{"const": "x", "title": "x"}]},
			"qa": {"type": "string", "title": "A?", "oneOf": [{"const": "y", "title": "y"}]}
		}}
	}`)
	st, err := parseElicitationCreateParams(params)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Questions) != 2 || st.Questions[0].PropertyKey != "qa" || st.Questions[1].PropertyKey != "qb" {
		t.Fatalf("order = %+v", st.Questions)
	}
}

func TestBuildElicitationResponse_accept(t *testing.T) {
	st, err := parseElicitationCreateParams(json.RawMessage(elicitationCreateFixture))
	if err != nil {
		t.Fatal(err)
	}
	// Engine answers: keyed by question text; multi-select joined with ", ".
	res := buildElicitationResponse(st, core.PermissionResult{
		Behavior: "allow",
		UpdatedInput: map[string]any{
			"answers": map[string]any{
				"Pick a color":  "blue",
				"Pick toppings": "onion, cheese",
			},
		},
	})
	if res["action"] != "accept" {
		t.Fatalf("action = %v, want accept", res["action"])
	}
	content, ok := res["content"].(map[string]any)
	if !ok {
		t.Fatalf("content missing: %v", res)
	}
	if content["q0"] != "blue" {
		t.Fatalf("q0 = %v, want blue", content["q0"])
	}
	// Multi-select must come back as an array in DECLARED option order
	// (cheese before onion), not in user-typed order.
	picked, ok := content["q1"].([]string)
	if !ok || len(picked) != 2 || picked[0] != "cheese" || picked[1] != "onion" {
		t.Fatalf("q1 = %v, want [cheese onion]", content["q1"])
	}
}

func TestBuildElicitationResponse_declines(t *testing.T) {
	st, err := parseElicitationCreateParams(json.RawMessage(elicitationCreateFixture))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]core.PermissionResult{
		"deny behavior":    {Behavior: "deny"},
		"allow no input":   {Behavior: "allow"},
		"allow no answers": {Behavior: "allow", UpdatedInput: map[string]any{}},
		"allow unknown answers": {Behavior: "allow", UpdatedInput: map[string]any{
			"answers": map[string]any{"unrelated question": "x"},
		}},
	}
	for name, result := range cases {
		res := buildElicitationResponse(st, result)
		if res["action"] != "decline" {
			t.Fatalf("%s: action = %v, want decline", name, res["action"])
		}
		if _, hasContent := res["content"]; hasContent {
			t.Fatalf("%s: decline must not carry content", name)
		}
	}
}

// TestSession_ElicitationRoundTrip drives the full server-request path over
// a pipe transport: elicitation/create in, EventPermissionRequest out,
// RespondPermission back, and the wire response asserted byte-level.
func TestSession_ElicitationRoundTrip(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)

	go func() {
		req := `{"jsonrpc":"2.0","id":42,"method":"elicitation/create","params":` +
			compactJSON(t, elicitationCreateFixture) + `}` + "\n"
		_, _ = wResp.Write([]byte(req))
	}()

	var ev core.Event
	select {
	case ev = <-s.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for EventPermissionRequest")
	}
	if ev.Type != core.EventPermissionRequest || ev.ToolName != "AskUserQuestion" {
		t.Fatalf("event = %+v", ev)
	}
	if len(ev.Questions) != 2 || ev.Questions[0].Question != "Pick a color" || !ev.Questions[1].MultiSelect {
		t.Fatalf("questions = %+v", ev.Questions)
	}

	// Read the eventual JSON-RPC response to id 42 from the client→server pipe.
	respCh := make(chan map[string]any, 1)
	go func() {
		sc := bufio.NewScanner(rReq)
		for sc.Scan() {
			var msg map[string]any
			if json.Unmarshal(sc.Bytes(), &msg) != nil {
				continue
			}
			if id, _ := msg["id"].(float64); id == 42 {
				respCh <- msg
				return
			}
		}
	}()

	err := s.RespondPermission(ev.RequestID, core.PermissionResult{
		Behavior: "allow",
		UpdatedInput: map[string]any{
			"answers": map[string]any{
				"Pick a color":  "red",
				"Pick toppings": "bacon, cheese",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	var msg map[string]any
	select {
	case msg = <-respCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for elicitation response")
	}
	result, _ := msg["result"].(map[string]any)
	if result["action"] != "accept" {
		t.Fatalf("result = %v", result)
	}
	content, _ := result["content"].(map[string]any)
	if content["q0"] != "red" {
		t.Fatalf("q0 = %v", content["q0"])
	}
	picked, _ := content["q1"].([]any)
	if len(picked) != 2 || picked[0] != "cheese" || picked[1] != "bacon" {
		t.Fatalf("q1 = %v, want [cheese bacon]", content["q1"])
	}
}

func TestSession_ElicitationDenyRespondsDecline(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)

	go func() {
		req := `{"jsonrpc":"2.0","id":"el-1","method":"elicitation/create","params":` +
			compactJSON(t, elicitationCreateFixture) + `}` + "\n"
		_, _ = wResp.Write([]byte(req))
	}()

	var ev core.Event
	select {
	case ev = <-s.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for EventPermissionRequest")
	}

	respCh := make(chan map[string]any, 1)
	go func() {
		sc := bufio.NewScanner(rReq)
		for sc.Scan() {
			var msg map[string]any
			if json.Unmarshal(sc.Bytes(), &msg) != nil {
				continue
			}
			if msg["id"] == "el-1" {
				respCh <- msg
				return
			}
		}
	}()

	if err := s.RespondPermission(ev.RequestID, core.PermissionResult{Behavior: "deny"}); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-respCh:
		result, _ := msg["result"].(map[string]any)
		if result["action"] != "decline" {
			t.Fatalf("result = %v, want decline", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for elicitation response")
	}
}

// A malformed elicitation/create must be answered with -32602 so the agent
// (kimi acp) can fall back to the request_permission bridge.
func TestSession_ElicitationInvalidParamsRespondsError(t *testing.T) {
	s, wResp, rReq := newTestSession(t, nil)

	go func() {
		_, _ = wResp.Write([]byte(`{"jsonrpc":"2.0","id":7,"method":"elicitation/create","params":{"mode":"url","message":"m","requestedSchema":{"properties":{}}}}` + "\n"))
	}()

	done := make(chan map[string]any, 1)
	go func() {
		sc := bufio.NewScanner(rReq)
		for sc.Scan() {
			var msg map[string]any
			if json.Unmarshal(sc.Bytes(), &msg) != nil {
				continue
			}
			if id, _ := msg["id"].(float64); id == 7 {
				done <- msg
				return
			}
		}
	}()

	select {
	case ev := <-s.Events():
		t.Fatalf("no event expected for rejected elicitation, got %+v", ev)
	case msg := <-done:
		errObj, _ := msg["error"].(map[string]any)
		if code, _ := errObj["code"].(float64); code != -32602 {
			t.Fatalf("msg = %v, want error -32602", msg)
		}
		if !strings.Contains(errObj["message"].(string), "url") {
			t.Fatalf("error message should name the rejected mode: %v", errObj)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for error response")
	}
}
