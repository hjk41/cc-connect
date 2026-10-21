package codex

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestAgent_AllowedToolsWorkspaceSnapshot(t *testing.T) {
	for _, input := range []any{[]string{"Bash", "bash"}, []any{"Bash", "bash", 7}} {
		raw, err := New(map[string]any{"cmd": os.Args[0], "backend": "app_server", "allowed_tools": input})
		if err != nil {
			t.Fatal(err)
		}
		a := raw.(*Agent)
		var auth core.ToolAuthorizer = a
		if err := auth.AddAllowedTools("BASH", "Patch", "Patch"); err != nil {
			t.Fatal(err)
		}
		want := []string{"Bash", "Patch"}
		if got := auth.GetAllowedTools(); !reflect.DeepEqual(got, want) {
			t.Fatalf("allowed tools = %v, want %v", got, want)
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
		if err := child.AddAllowedTools("Permissions"); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a.GetAllowedTools(), want) || !reflect.DeepEqual(child.GetAllowedTools(), []string{"Bash", "Patch", "Permissions"}) {
			t.Fatal("get/options/clone aliased the parent allowlist")
		}
	}
}

func TestAgent_AllowedToolsExecBackendError(t *testing.T) {
	a := &Agent{backend: "exec"}
	if err := a.AddAllowedTools("Bash"); err == nil || err.Error() != "codex: /allow requires the app_server backend" {
		t.Fatalf("error = %v", err)
	}
	if len(a.GetAllowedTools()) != 0 {
		t.Fatal("exec backend stored an unsupported allowlist")
	}
}

func TestStartSession_AllowedToolsSnapshot(t *testing.T) {
	raw, err := New(map[string]any{
		"cmd":     []string{os.Args[0], "-test.run=^TestDesktopCLIProcess$", "--", "desktop-fixture"},
		"backend": "app_server", "work_dir": t.TempDir(), "codex_home": t.TempDir(),
		"env": map[string]string{"CC_DESKTOP_CLI_HELPER": "1"}, "allowed_tools": []string{"Bash"},
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
	if err := a.AddAllowedTools("Patch"); err != nil {
		t.Fatal(err)
	}
	second, err := a.StartSession(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if !reflect.DeepEqual(first.(*appServerSession).allowedTools, []string{"Bash"}) || !reflect.DeepEqual(second.(*appServerSession).allowedTools, []string{"Bash", "Patch"}) {
		t.Fatal("allowlist changes were not isolated to the next session")
	}
	first.(*appServerSession).allowedTools[0] = "changed"
	if a.GetAllowedTools()[0] != "Bash" {
		t.Fatal("session aliases agent allowlist")
	}
}

func TestAppServerSession_AllowedToolsAutoApprove(t *testing.T) {
	for _, tc := range []struct{ tool, method string }{
		{"bash", "item/commandExecution/requestApproval"},
		{"PATCH", "item/fileChange/requestApproval"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			s, responses := newElicitationTestSession(t)
			s.allowedTools = []string{tc.tool}
			s.pendingApprovals = make(map[string]chan core.PermissionResult)
			s.handleServerRequest(serverRequestProbe(t, `"allow-1"`, tc.method, map[string]any{"command": "pwd"}))
			select {
			case ev := <-s.events:
				t.Fatalf("auto-approval emitted an event: %+v", ev)
			default:
			}
			select {
			case data := <-responses:
				var reply struct {
					ID     string `json:"id"`
					Result struct {
						Decision string `json:"decision"`
					} `json:"result"`
				}
				if err := json.Unmarshal(data, &reply); err != nil || reply.ID != "allow-1" || reply.Result.Decision != "accept" {
					t.Fatalf("response = %s, error = %v", data, err)
				}
			default:
				t.Fatal("allowed approval did not write accept")
			}
			if len(s.pendingApprovals) != 0 {
				t.Fatal("auto-approval left a pending request")
			}
		})
	}
}

func TestAppServerSession_AllowedPermissionsReturnRequestedPermissions(t *testing.T) {
	s, responses := newElicitationTestSession(t)
	s.allowedTools = []string{"permissions"}
	s.pendingApprovals = make(map[string]chan core.PermissionResult)
	permissions := map[string]any{"network": map[string]any{"enabled": true}}
	s.handleServerRequest(serverRequestProbe(t, `42`, "item/permissions/requestApproval", map[string]any{"permissions": permissions}))
	select {
	case data := <-responses:
		var reply struct {
			ID     int `json:"id"`
			Result struct {
				Permissions map[string]any `json:"permissions"`
				Scope       string         `json:"scope"`
			} `json:"result"`
		}
		if err := json.Unmarshal(data, &reply); err != nil || reply.ID != 42 || reply.Result.Scope != "turn" || !reflect.DeepEqual(reply.Result.Permissions, permissions) {
			t.Fatalf("response = %s, error = %v", data, err)
		}
	default:
		t.Fatal("allowed permissions did not write a response")
	}
	if len(s.events) != 0 || len(s.pendingApprovals) != 0 {
		t.Fatal("auto-approval emitted an event or left a pending request")
	}
}

func TestAppServerSession_AllowedToolsPreserveManualRequests(t *testing.T) {
	for _, tc := range []struct {
		name, method, tool string
		params             map[string]any
	}{
		{"nonmatching", "item/fileChange/requestApproval", "Patch", map[string]any{}},
		{"question", "item/tool/requestUserInput", "AskUserQuestion", map[string]any{"questions": []any{map[string]any{"id": "q", "question": "Which?"}}}},
		{"permissions", "item/permissions/requestApproval", "Permissions", map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, responses := newElicitationTestSession(t)
			s.allowedTools = []string{"Bash", "AskUserQuestion", "Pat*"}
			s.pendingApprovals = make(map[string]chan core.PermissionResult)
			s.handleServerRequest(serverRequestProbe(t, `"manual"`, tc.method, tc.params))
			select {
			case ev := <-s.events:
				if ev.Type != core.EventPermissionRequest || ev.ToolName != tc.tool {
					t.Fatalf("event = %+v", ev)
				}
			default:
				t.Fatal("expected manual permission request")
			}
			select {
			case data := <-responses:
				t.Fatalf("manual request wrote an automatic response: %s", data)
			default:
			}
		})
	}
}

func TestAppServerSession_AllowedToolsIgnoreUnknownRequestsAndToolNotifications(t *testing.T) {
	s, responses := newElicitationTestSession(t)
	s.allowedTools = []string{"Bash", "unknown/requestApproval"}
	s.handleServerRequest(serverRequestProbe(t, `"unknown"`, "unknown/requestApproval", map[string]any{}))
	select {
	case data := <-responses:
		var reply struct {
			Error struct {
				Code int `json:"code"`
			} `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(data, &reply); err != nil || reply.Error.Code != -32601 || reply.Result != nil {
			t.Fatalf("unknown request response = %s, error = %v", data, err)
		}
	default:
		t.Fatal("expected method-not-found response")
	}
	s.handleNotification("item/started", json.RawMessage(`{"item":{"type":"commandExecution","command":"pwd"}}`))
	select {
	case ev := <-s.events:
		if ev.Type != core.EventToolUse || ev.ToolName != "Bash" {
			t.Fatalf("notification event = %+v", ev)
		}
	default:
		t.Fatal("expected tool-use notification")
	}
	select {
	case data := <-responses:
		t.Fatalf("tool-use notification wrote an approval: %s", data)
	default:
	}
}
