package cursor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestAgent_AllowedToolsWorkspaceAndSessionSnapshots(t *testing.T) {
	for _, input := range []any{[]string{"Bash", "bash"}, []any{"Bash", "bash", 7}} {
		raw, err := New(map[string]any{"cmd": "go", "allowed_tools": input})
		if err != nil {
			t.Fatal(err)
		}
		a := raw.(*Agent)
		var auth core.ToolAuthorizer = a
		if err := auth.AddAllowedTools("BASH", "WebFetch", "WebFetch"); err != nil {
			t.Fatal(err)
		}
		want := []string{"Bash", "WebFetch"}
		if !reflect.DeepEqual(auth.GetAllowedTools(), want) {
			t.Fatalf("allowed tools = %v, want %v", auth.GetAllowedTools(), want)
		}
		got := auth.GetAllowedTools()
		got[0] = "changed"
		opts := a.WorkspaceAgentOptions()
		childRaw, err := New(opts)
		if err != nil {
			t.Fatal(err)
		}
		child := childRaw.(*Agent)
		opts["allowed_tools"].([]string)[0] = "changed"
		if err := child.AddAllowedTools("Patch"); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a.GetAllowedTools(), want) || !reflect.DeepEqual(child.GetAllowedTools(), []string{"Bash", "WebFetch", "Patch"}) {
			t.Fatal("get/options/clone aliased the parent allowlist")
		}

		first, err := a.StartSession(context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		defer first.Close()
		if err := a.AddAllowedTools("Patch"); err != nil {
			t.Fatal(err)
		}
		second, err := a.StartSession(context.Background(), "")
		if err != nil {
			t.Fatal(err)
		}
		defer second.Close()
		if !reflect.DeepEqual(first.(*cursorSession).allowedTools, want) || !reflect.DeepEqual(second.(*cursorSession).allowedTools, []string{"Bash", "WebFetch", "Patch"}) {
			t.Fatal("allowlist changes were not isolated to the next session")
		}
		first.(*cursorSession).allowedTools[0] = "changed"
		if a.GetAllowedTools()[0] != "Bash" {
			t.Fatal("session aliases agent allowlist")
		}
	}
}

type interactionBuffer struct{ bytes.Buffer }

func (*interactionBuffer) Close() error { return nil }

func TestHandleInteractionQuery_AllowedToolsAutoApprove(t *testing.T) {
	for _, mode := range []string{"default", "plan", "ask", "force"} {
		for _, tc := range []struct {
			name, allowed, queryType string
			request                  map[string]any
		}{
			{"shell", "bash", "shellRequestQuery", shellInteractionRequest(7, "pwd", false)},
			{"webFetch", "WEBFETCH", "webFetchRequestQuery", webFetchInteractionRequest(7, "https://example.com", false)},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				cs := newTestSession(mode)
				defer cs.cancel()
				cs.allowedTools = []string{tc.allowed}
				out := &interactionBuffer{}
				cs.stdin = out
				cs.handleInteractionQuery(tc.request)
				if len(cs.events) != 0 || cs.pending != nil {
					t.Fatal("allowed query emitted an event or left a pending request")
				}
				if out.Len() == 0 {
					t.Fatal("allowed query did not write approved")
				}
				reply := readInteractionResponse(t, out)
				response := reply["response"].(map[string]any)
				key := strings.TrimSuffix(tc.queryType, "Query") + "Response"
				result := response[key].(map[string]any)
				if _, ok := result["approved"]; !ok || response["id"] != float64(7) || reply["query_type"] != tc.queryType {
					t.Fatalf("response = %#v", reply)
				}
			})
		}
	}
}

func TestHandleInteractionQuery_AllowedToolsPreserveManualRequests(t *testing.T) {
	for _, tc := range []struct {
		name, allowed, tool string
		request             map[string]any
	}{
		{"nonmatching", "Bash", "WebFetch", webFetchInteractionRequest(1, "https://example.com", false)},
		{"exact-only", "Bas*", "Bash", shellInteractionRequest(1, "pwd", false)},
		{"unknown", "mystery", "mystery", map[string]any{"subtype": "request", "query_type": "mysteryRequestQuery", "query": map[string]any{"id": float64(1)}}},
		{"missing-args", "shell", "shell", map[string]any{"subtype": "request", "query_type": "shellRequestQuery", "query": map[string]any{"id": float64(1)}}},
		{"question", "AskUserQuestion", "AskUserQuestion", map[string]any{"subtype": "request", "query_type": "AskUserQuestionQuery", "query": map[string]any{"id": float64(1)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := newTestSession("default")
			defer cs.cancel()
			cs.allowedTools = []string{tc.allowed}
			out := &interactionBuffer{}
			cs.stdin = out
			cs.handleInteractionQuery(tc.request)
			select {
			case ev := <-cs.events:
				if ev.Type != core.EventPermissionRequest || ev.ToolName != tc.tool {
					t.Fatalf("event = %+v", ev)
				}
			default:
				t.Fatal("expected manual permission request")
			}
			if out.Len() != 0 {
				t.Fatalf("manual request wrote an automatic response: %s", out.String())
			}
		})
	}
}

func TestHandleInteractionQuery_AllowedToolsIgnoreNonRequests(t *testing.T) {
	cs := newTestSession("default")
	defer cs.cancel()
	cs.allowedTools = []string{"Bash"}
	out := &interactionBuffer{}
	cs.stdin = out
	query := shellInteractionRequest(1, "pwd", false)
	query["subtype"] = "response"
	cs.handleInteractionQuery(query)
	if out.Len() != 0 || len(cs.events) != 0 {
		t.Fatal("non-request query was intercepted")
	}
}

// newTestSession creates a cursorSession suitable for unit tests (no real CLI process).
func newTestSession(mode string) *cursorSession {
	ctx, cancel := context.WithCancel(context.Background())
	cs := &cursorSession{
		cmd:     "agent",
		workDir: "/tmp",
		mode:    mode,
		events:  make(chan core.Event, 16),
		ctx:     ctx,
		cancel:  cancel,
	}
	cs.alive.Store(true)
	return cs
}

// connectStdinPipe wires an in-memory pipe as the session's stdin and returns
// the reader end so tests can inspect what was written.
func connectStdinPipe(cs *cursorSession) io.Reader {
	r, w := io.Pipe()
	cs.stdinMu.Lock()
	cs.stdin = w
	cs.stdinMu.Unlock()
	return r
}

// readInteractionResponse reads one JSON line from the reader.
func readInteractionResponse(t *testing.T, r io.Reader) map[string]any {
	t.Helper()
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		t.Fatal("expected a line from stdin pipe, got none")
	}
	var out map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &out); err != nil {
		t.Fatalf("parse stdin JSON: %v (line: %s)", err, scanner.Text())
	}
	return out
}

// webFetchInteractionRequest builds a raw map matching Cursor's interaction_query/request.
func webFetchInteractionRequest(id int, url string, skipApproval bool) map[string]any {
	return map[string]any{
		"type":       "interaction_query",
		"subtype":    "request",
		"query_type": "webFetchRequestQuery",
		"query": map[string]any{
			"id": float64(id),
			"webFetchRequestQuery": map[string]any{
				"args":         map[string]any{"url": url},
				"skipApproval": skipApproval,
			},
		},
	}
}

// shellInteractionRequest builds a raw map matching Cursor's shellRequestQuery.
func shellInteractionRequest(id int, cmd string, skipApproval bool) map[string]any {
	return map[string]any{
		"type":       "interaction_query",
		"subtype":    "request",
		"query_type": "shellRequestQuery",
		"query": map[string]any{
			"id": float64(id),
			"shellRequestQuery": map[string]any{
				"args":         map[string]any{"command": cmd},
				"skipApproval": skipApproval,
			},
		},
	}
}

// TestHandleInteractionQuery_DefaultMode_EmitsPermissionRequest checks that in
// default mode an interaction_query/request causes an EventPermissionRequest and
// no automatic stdin response (user must approve/deny via RespondPermission).
func TestHandleInteractionQuery_DefaultMode_EmitsPermissionRequest(t *testing.T) {
	cs := newTestSession("default")
	defer cs.cancel()
	_ = connectStdinPipe(cs)

	cs.handleInteractionQuery(webFetchInteractionRequest(0, "https://example.com", false))

	select {
	case evt := <-cs.events:
		if evt.Type != core.EventPermissionRequest {
			t.Fatalf("event type = %q, want EventPermissionRequest", evt.Type)
		}
		if evt.ToolName != "WebFetch" {
			t.Errorf("tool name = %q, want WebFetch", evt.ToolName)
		}
		if evt.ToolInput != "https://example.com" {
			t.Errorf("tool input = %q, want https://example.com", evt.ToolInput)
		}
		if evt.RequestID == "" {
			t.Error("request ID should be non-empty")
		}
	default:
		t.Fatal("expected EventPermissionRequest, got nothing")
	}
}

// TestHandleInteractionQuery_DefaultMode_ShellEmitsPermissionRequest checks the
// same for shellRequestQuery (the originally reported Windows bug scenario).
func TestHandleInteractionQuery_DefaultMode_ShellEmitsPermissionRequest(t *testing.T) {
	cs := newTestSession("default")
	defer cs.cancel()
	_ = connectStdinPipe(cs)

	cs.handleInteractionQuery(shellInteractionRequest(1, "git clone https://example.com", false))

	select {
	case evt := <-cs.events:
		if evt.Type != core.EventPermissionRequest {
			t.Fatalf("event type = %q, want EventPermissionRequest", evt.Type)
		}
		if evt.ToolName != "Bash" {
			t.Errorf("tool name = %q, want Bash", evt.ToolName)
		}
	default:
		t.Fatal("expected EventPermissionRequest for shell query, got nothing")
	}
}

// TestHandleInteractionQuery_SkipApproval_AutoApproves checks that when Cursor
// marks a request as skipApproval=true, we immediately write an approval response.
func TestHandleInteractionQuery_SkipApproval_AutoApproves(t *testing.T) {
	cs := newTestSession("default")
	defer cs.cancel()
	r := connectStdinPipe(cs)

	done := make(chan map[string]any, 1)
	go func() {
		done <- readInteractionResponse(t, r)
	}()

	cs.handleInteractionQuery(webFetchInteractionRequest(3, "https://example.com", true))

	resp := <-done
	if resp["subtype"] != "response" {
		t.Errorf("subtype = %q, want response", resp["subtype"])
	}
	response, _ := resp["response"].(map[string]any)
	inner, _ := response["webFetchRequestResponse"].(map[string]any)
	if _, ok := inner["approved"]; !ok {
		t.Errorf("expected approved in response, got %v", inner)
	}
}

// TestHandleInteractionQuery_NonDefaultMode_AutoDenies verifies that in plan/ask
// mode interaction queries are denied immediately without user intervention.
func TestHandleInteractionQuery_NonDefaultMode_AutoDenies(t *testing.T) {
	for _, mode := range []string{"plan", "ask"} {
		t.Run(mode, func(t *testing.T) {
			cs := newTestSession(mode)
			defer cs.cancel()
			r := connectStdinPipe(cs)

			done := make(chan map[string]any, 1)
			go func() {
				done <- readInteractionResponse(t, r)
			}()

			cs.handleInteractionQuery(webFetchInteractionRequest(0, "https://example.com", false))

			resp := <-done
			response, _ := resp["response"].(map[string]any)
			inner, _ := response["webFetchRequestResponse"].(map[string]any)
			if _, ok := inner["rejected"]; !ok {
				t.Errorf("mode=%s: expected rejected in response, got %v", mode, inner)
			}

			// No EventPermissionRequest should be emitted.
			select {
			case evt := <-cs.events:
				t.Errorf("mode=%s: unexpected event %q", mode, evt.Type)
			default:
			}
		})
	}
}

// TestRespondPermission_Approve writes an approval for a pending query and checks
// that the correct JSON is sent to stdin.
func TestRespondPermission_Approve(t *testing.T) {
	cs := newTestSession("default")
	defer cs.cancel()
	r := connectStdinPipe(cs)

	// Simulate a pending interaction query (as would be set by handleInteractionQuery).
	cs.pendingMu.Lock()
	cs.pending = &pendingInteractionQuery{id: 7, queryType: "webFetchRequestQuery"}
	cs.pendingMu.Unlock()

	done := make(chan map[string]any, 1)
	go func() {
		done <- readInteractionResponse(t, r)
	}()

	if err := cs.RespondPermission("webFetchRequestQuery:7", core.PermissionResult{Behavior: "allow"}); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}

	resp := <-done
	if resp["type"] != "interaction_query" {
		t.Errorf("type = %q, want interaction_query", resp["type"])
	}
	if resp["subtype"] != "response" {
		t.Errorf("subtype = %q, want response", resp["subtype"])
	}
	if resp["query_type"] != "webFetchRequestQuery" {
		t.Errorf("query_type = %q, want webFetchRequestQuery", resp["query_type"])
	}
	response, _ := resp["response"].(map[string]any)
	if idVal, _ := response["id"].(float64); int(idVal) != 7 {
		t.Errorf("response.id = %v, want 7", response["id"])
	}
	inner, _ := response["webFetchRequestResponse"].(map[string]any)
	if _, ok := inner["approved"]; !ok {
		t.Errorf("expected approved in response body, got %v", inner)
	}

	// Pending should be cleared.
	cs.pendingMu.Lock()
	pending := cs.pending
	cs.pendingMu.Unlock()
	if pending != nil {
		t.Error("pending query should be nil after RespondPermission")
	}
}

// TestRespondPermission_Deny verifies denial responses.
func TestRespondPermission_Deny(t *testing.T) {
	cs := newTestSession("default")
	defer cs.cancel()
	r := connectStdinPipe(cs)

	cs.pendingMu.Lock()
	cs.pending = &pendingInteractionQuery{id: 2, queryType: "shellRequestQuery"}
	cs.pendingMu.Unlock()

	done := make(chan map[string]any, 1)
	go func() {
		done <- readInteractionResponse(t, r)
	}()

	if err := cs.RespondPermission("shellRequestQuery:2", core.PermissionResult{
		Behavior: "deny",
		Message:  "user rejected",
	}); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}

	resp := <-done
	response, _ := resp["response"].(map[string]any)
	inner, _ := response["shellRequestResponse"].(map[string]any)
	rejected, _ := inner["rejected"].(map[string]any)
	if rejected["reason"] != "user rejected" {
		t.Errorf("reject reason = %q, want \"user rejected\"", rejected["reason"])
	}
}

// TestRespondPermission_NoPending is a no-op regression guard: if no query is
// pending (e.g. force mode or stale call), RespondPermission must not panic.
func TestRespondPermission_NoPending(t *testing.T) {
	cs := newTestSession("default")
	defer cs.cancel()

	if err := cs.RespondPermission("some-id", core.PermissionResult{Behavior: "allow"}); err != nil {
		t.Fatalf("RespondPermission with no pending: %v", err)
	}
}

// TestWriteInteractionResponse_ResponseFormat exercises the JSON shape emitted
// for both approval and denial paths.
func TestWriteInteractionResponse_ResponseFormat(t *testing.T) {
	cases := []struct {
		name      string
		queryType string
		approved  bool
		wantKey   string
	}{
		{"web-approve", "webFetchRequestQuery", true, "approved"},
		{"web-deny", "webFetchRequestQuery", false, "rejected"},
		{"shell-approve", "shellRequestQuery", true, "approved"},
		{"shell-deny", "shellRequestQuery", false, "rejected"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := newTestSession("default")
			defer cs.cancel()
			r := connectStdinPipe(cs)

			done := make(chan string, 1)
			go func() {
				scanner := bufio.NewScanner(r)
				if scanner.Scan() {
					done <- scanner.Text()
				}
			}()

			cs.writeInteractionResponse(0, tc.queryType, tc.approved, "test reason")

			line := <-done
			if !strings.Contains(line, tc.wantKey) {
				t.Errorf("response JSON does not contain %q: %s", tc.wantKey, line)
			}
		})
	}
}
