package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestAgent_AllowedToolsWorkspaceSnapshot(t *testing.T) {
	for _, input := range []any{[]string{"shell", "SHELL"}, []any{"shell", "SHELL", 7}} {
		raw, err := New(map[string]any{"cmd": os.Args[0], "allowed_tools": input})
		if err != nil {
			t.Fatal(err)
		}
		a := raw.(*Agent)
		var auth core.ToolAuthorizer = a
		if err := auth.AddAllowedTools("SHELL", "edit", "edit"); err != nil {
			t.Fatal(err)
		}
		want := []string{"shell", "edit"}
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
		if err := child.AddAllowedTools("fetch"); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a.GetAllowedTools(), want) || !reflect.DeepEqual(child.GetAllowedTools(), []string{"shell", "edit", "fetch"}) {
			t.Fatal("get/options/clone aliased the parent allowlist")
		}
	}
}

func TestStartSession_AllowedToolsSnapshot(t *testing.T) {
	raw, err := New(map[string]any{
		"cmd": os.Args[0], "args": []string{"-test.run=^$"}, "work_dir": t.TempDir(),
		"env": map[string]string{"ACP_FAKE_SERVER": "1"}, "allowed_tools": []string{"shell"},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := raw.(*Agent)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, err := a.StartSession(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := a.AddAllowedTools("edit"); err != nil {
		t.Fatal(err)
	}
	second, err := a.StartSession(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if !reflect.DeepEqual(first.(*acpSession).allowedTools, []string{"shell"}) || !reflect.DeepEqual(second.(*acpSession).allowedTools, []string{"shell", "edit"}) {
		t.Fatal("allowlist changes were not isolated to the next session")
	}
	first.(*acpSession).allowedTools[0] = "changed"
	if a.GetAllowedTools()[0] != "shell" {
		t.Fatal("session aliases agent allowlist")
	}
}

func TestPermissionRequest_AllowedToolsAutoApprove(t *testing.T) {
	for _, tc := range []struct{ name, toolCall, options, wantOption string }{
		{"shell", `{"title":"shell","kind":"execute"}`, `[{"optionId":"reject","kind":"reject_once"},{"optionId":"allow-once","kind":"allow_once"},{"optionId":"always","kind":"allow_always"}]`, "allow-once"},
		{"first-allow", `{"title":"edit","kind":"edit"}`, `[{"optionId":"first","kind":"allow_always"},{"optionId":"second","kind":"allow_always"}]`, "first"},
		{"devin-meta", `{"_meta":{"cognition.ai/editableCommand":"pwd"}}`, `[{"optionId":"allow-once","kind":"allow_once"}]`, "allow-once"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, server, requests := newTestSession(t, nil)
			s.allowedTools = []string{"SHELL", "EDIT"}
			s.toolInputByID["call"] = "pwd"
			replies := make(chan json.RawMessage, 1)
			go func() {
				var data json.RawMessage
				if json.NewDecoder(requests).Decode(&data) == nil {
					replies <- data
				}
			}()
			var toolCall map[string]any
			if err := json.Unmarshal([]byte(tc.toolCall), &toolCall); err != nil {
				t.Fatal(err)
			}
			toolCall["toolCallId"] = "call"
			params, err := json.Marshal(map[string]any{"sessionId": "s", "toolCall": toolCall, "options": json.RawMessage(tc.options)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Fprintf(server, `{"jsonrpc":"2.0","id":"allow-1","method":"session/request_permission","params":%s}`+"\n", params); err != nil {
				t.Fatal(err)
			}
			select {
			case data := <-replies:
				var reply struct {
					ID     string `json:"id"`
					Result struct {
						Outcome struct{ Outcome, OptionID string } `json:"outcome"`
					} `json:"result"`
				}
				if err := json.Unmarshal(data, &reply); err != nil || reply.ID != "allow-1" || reply.Result.Outcome.Outcome != "selected" || reply.Result.Outcome.OptionID != tc.wantOption {
					t.Fatalf("response = %s, error = %v", data, err)
				}
			case ev := <-s.events:
				t.Fatalf("allowed permission emitted an event: %+v", ev)
			case <-time.After(2 * time.Second):
				t.Fatal("allowed permission did not respond")
			}
			s.wg.Wait()
			if len(s.events) != 0 {
				t.Fatal("auto-approval emitted an event")
			}
			s.permMu.Lock()
			pending := len(s.permByID)
			s.permMu.Unlock()
			if pending != 0 {
				t.Fatal("auto-approval left a pending permission entry")
			}
		})
	}
}

func TestPermissionRequest_AllowedToolsPreserveManualRequests(t *testing.T) {
	for _, tc := range []struct{ name, title, allowed, wantTool string }{
		{"nonmatching", "edit", "shell", "edit"},
		{"exact-only", "shell", "she*", "shell"},
		{"question", "AskUserQuestion", "askuserquestion", "AskUserQuestion"},
		{"unknown", "", "permission", "permission"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, server, _ := newTestSession(t, nil)
			s.allowedTools = []string{tc.allowed}
			s.toolInputByID["call"] = "input"
			params, err := json.Marshal(map[string]any{"toolCall": map[string]any{"toolCallId": "call", "title": tc.title}, "options": []any{map[string]any{"optionId": "allow-once", "kind": "allow_once"}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fmt.Fprintf(server, `{"jsonrpc":"2.0","id":2,"method":"session/request_permission","params":%s}`+"\n", params); err != nil {
				t.Fatal(err)
			}
			ev := readEvent(t, s)
			if ev.Type != core.EventPermissionRequest || ev.ToolName != tc.wantTool {
				t.Fatalf("event = %+v", ev)
			}
			s.wg.Wait()
			s.permMu.Lock()
			pending := len(s.permByID)
			s.permMu.Unlock()
			if pending != 1 {
				t.Fatal("manual permission entry was removed")
			}
		})
	}
}

func TestSession_AllowedToolsIgnoreToolNotifications(t *testing.T) {
	s, _, _ := newTestSession(t, nil)
	s.allowedTools = []string{"shell"}
	s.onNotification("session/update", json.RawMessage(`{"sessionId":"s","update":{"sessionUpdate":"tool_call","toolCallId":"call","title":"shell","kind":"execute","rawInput":{"command":"pwd"}}}`))
	ev := readEvent(t, s)
	if ev.Type != core.EventToolUse || ev.ToolName != "shell" {
		t.Fatalf("notification event = %+v", ev)
	}
	if len(s.permByID) != 0 {
		t.Fatal("tool-use notification created a pending permission")
	}
}
