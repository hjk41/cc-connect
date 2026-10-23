package codex

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const liveWorkspacePolicy = `{"type":"workspaceWrite","writableRoots":["/project","/extra"],"networkAccess":true,"excludeTmpdirEnvVar":true,"excludeSlashTmp":false,"readOnlyAccess":{"type":"restricted","includePlatformDefaults":true,"readableRoots":["/shared"]}}`

// Exercise the real thread response decoder and Send request builder, replacing
// only the app-server transport. No process or account is needed.
func newLiveModeTestSession(t *testing.T, mode, sandbox string, resume bool) (*appServerSession, func() []parityRPCRequest) {
	t.Helper()
	s := newTurnTestSession()
	s.mode = mode
	var mu sync.Mutex
	var requests []parityRPCRequest
	s.stdin = &turnStartReplyWriter{write: func(p []byte) (int, error) {
		var req parityRPCRequest
		if err := json.Unmarshal(p, &req); err != nil {
			return 0, err
		}
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()
		var result json.RawMessage
		if strings.HasPrefix(req.Method, "thread/") {
			result = json.RawMessage(`{"thread":{"id":"parent-thread"},"sandbox":` + sandbox + `}`)
		} else {
			result = json.RawMessage(fmt.Sprintf(`{"turn":{"id":"turn-%d"}}`, req.ID))
		}
		s.handleResponse(rpcResponseEnvelope{ID: req.ID, Result: result})
		return len(p), nil
	}}
	resumeID := ""
	if resume {
		resumeID = "parent-thread"
	}
	if err := s.ensureThread(resumeID); err != nil {
		t.Fatal(err)
	}
	return s, func() []parityRPCRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]parityRPCRequest(nil), requests...)
	}
}

func assertLiveTurnPermissions(t *testing.T, req parityRPCRequest, approval, reviewer, sandbox string) {
	t.Helper()
	if req.Method != "turn/start" || req.Params["approvalPolicy"] != approval || req.Params["approvalsReviewer"] != reviewer {
		t.Fatalf("turn permissions = %#v, want %s/%s", req, approval, reviewer)
	}
	if _, ok := req.Params["sandbox"]; ok {
		t.Fatalf("turn/start must not send sandbox: %#v", req.Params)
	}
	policy, ok := req.Params["sandboxPolicy"]
	if sandbox == "" {
		if ok {
			t.Fatalf("same-tier turn sent sandboxPolicy: %#v", policy)
		}
		return
	}
	var want map[string]any
	if err := json.Unmarshal([]byte(sandbox), &want); err != nil {
		t.Fatal(err)
	}
	if !ok || !reflect.DeepEqual(policy, want) {
		t.Fatalf("sandboxPolicy = %#v, want %#v", policy, want)
	}
}

func TestAppServerSession_SetLiveMode_SameTier(t *testing.T) {
	for _, tc := range []struct{ from, to, canonical, approval, reviewer, sandbox string }{
		{"workspace", "auto-review", "auto-review", "on-request", "auto_review", liveWorkspacePolicy},
		{"auto-review", "workspace", "workspace", "on-request", "user", liveWorkspacePolicy},
		{"workspace", "full-auto", "full-auto", "never", "user", liveWorkspacePolicy},
		{"full-auto", "workspace", "workspace", "on-request", "user", liveWorkspacePolicy},
		{"auto-review", "full-auto", "full-auto", "never", "user", liveWorkspacePolicy},
		{"full-auto", " AUTO_REVIEW ", "auto-review", "on-request", "auto_review", liveWorkspacePolicy},
		{"suggest", "readonly", "suggest", "on-request", "user", `{"type":"readOnly"}`},
		{"yolo", "bypass", "yolo", "never", "user", `{"type":"dangerFullAccess"}`},
	} {
		t.Run(tc.from+"/"+tc.to, func(t *testing.T) {
			s, requests := newLiveModeTestSession(t, tc.from, tc.sandbox, false)
			if !s.SetLiveMode(tc.to) || s.mode != tc.canonical {
				t.Fatalf("live mode = %q, want %q", s.mode, tc.canonical)
			}
			if err := s.Send("next", "msg", nil, nil); err != nil {
				t.Fatal(err)
			}
			got := requests()
			if len(got) != 2 {
				t.Fatalf("live switch restarted the thread: %#v", got)
			}
			assertLiveTurnPermissions(t, got[1], tc.approval, tc.reviewer, "")
		})
	}
}

func TestAppServerSession_SetLiveMode_SandboxOverridesPreserveInFlightTurn(t *testing.T) {
	for _, tc := range []struct{ mode, approval, policy string }{
		{"suggest", "on-request", `{"type":"readOnly","networkAccess":false}`},
		{"yolo", "never", `{"type":"dangerFullAccess"}`},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			s, requests := newLiveModeTestSession(t, "workspace", liveWorkspacePolicy, false)
			s.beginTurn("in-flight")
			if !s.SetLiveMode(tc.mode) {
				t.Fatal("live switch rejected")
			}
			if s.currentTurn != "in-flight" || !s.Alive() || s.CurrentSessionID() != "parent-thread" || len(requests()) != 1 {
				t.Fatal("live switch affected the in-flight turn or transport")
			}
			// Keep the explicit override on subsequent turns.
			for i := 0; i < 2; i++ {
				if err := s.Send("next", "msg", nil, nil); err != nil {
					t.Fatal(err)
				}
			}
			got := requests()
			if len(got) != 3 {
				t.Fatalf("live switch restarted the thread: %#v", got)
			}
			for _, req := range got[1:] {
				assertLiveTurnPermissions(t, req, tc.approval, "user", tc.policy)
			}
			// Keep the explicit policy on a same-tier switch; clearing it before
			// a turn carries the override would fall back to the native tier.
			if !s.SetLiveMode(tc.mode) || s.turnSandboxPolicy == nil {
				t.Fatal("same-tier switch dropped the explicit per-turn policy")
			}
			if err := s.Send("same tier", "msg", nil, nil); err != nil {
				t.Fatal(err)
			}
			assertLiveTurnPermissions(t, requests()[3], tc.approval, "user", tc.policy)
		})
	}
}

func TestAppServerSession_SetLiveMode_SameTierBeforeTurnKeepsOverride(t *testing.T) {
	s, requests := newLiveModeTestSession(t, "workspace", liveWorkspacePolicy, false)
	if !s.SetLiveMode("suggest") {
		t.Fatal("first same-tier switch rejected")
	}
	if !s.SetLiveMode("suggest") {
		t.Fatal("repeated same-tier switch rejected")
	}
	if err := s.Send("next", "msg", nil, nil); err != nil {
		t.Fatal(err)
	}
	assertLiveTurnPermissions(t, requests()[1], "on-request", "user", `{"type":"readOnly","networkAccess":false}`)
}

func TestAppServerSession_SetLiveMode_RestoresRememberedWorkspacePolicy(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, away := range []string{"suggest", "yolo"} {
			for _, target := range []struct{ mode, approval, reviewer string }{
				{"workspace", "on-request", "user"}, {"auto-review", "on-request", "auto_review"}, {"full-auto", "never", "user"},
			} {
				t.Run(fmt.Sprintf("resume=%t/%s/%s", resume, away, target.mode), func(t *testing.T) {
					// Check both network settings and both values of each tmp flag.
					policy := liveWorkspacePolicy
					if resume {
						policy = strings.ReplaceAll(policy, `"networkAccess":true`, `"networkAccess":false`)
						policy = strings.ReplaceAll(policy, `"excludeTmpdirEnvVar":true`, `"excludeTmpdirEnvVar":false`)
						policy = strings.ReplaceAll(policy, `"excludeSlashTmp":false`, `"excludeSlashTmp":true`)
					}
					s, requests := newLiveModeTestSession(t, "workspace", policy, resume)
					if !s.SetLiveMode(away) {
						t.Fatal("switch away rejected")
					}
					if err := s.Send("away", "msg", nil, nil); err != nil {
						t.Fatal(err)
					}
					if !s.SetLiveMode(target.mode) {
						t.Fatal("remembered workspace switch rejected")
					}
					if err := s.Send("restored", "msg", nil, nil); err != nil {
						t.Fatal(err)
					}
					got := requests()
					if len(got) != 3 || s.effectiveSandboxTier != "workspace-write" {
						t.Fatalf("restored tier=%s requests=%#v", s.effectiveSandboxTier, got)
					}
					assertLiveTurnPermissions(t, got[2], target.approval, target.reviewer, policy)
					// Restored policy and remembered policy must own separate roots
					// and nested read-access data, not just separate outer maps.
					remembered := cloneSandboxPolicy(s.lastWorkspacePolicy)
					s.turnSandboxPolicy["writableRoots"].([]any)[0] = "/changed"
					s.turnSandboxPolicy["readOnlyAccess"].(map[string]any)["readableRoots"].([]any)[0] = "/changed"
					if !reflect.DeepEqual(s.lastWorkspacePolicy, remembered) {
						t.Fatal("restored policy aliases remembered workspace policy")
					}
				})
			}
		}
	}
}

func TestAppServerSession_SetLiveMode_MissingWorkspacePolicyDoesNotMutate(t *testing.T) {
	for _, initial := range []struct{ mode, sandbox string }{
		{"suggest", `{"type":"readOnly"}`}, {"yolo", `{"type":"dangerFullAccess"}`},
	} {
		for _, target := range []string{"workspace", "auto-review", "full-auto"} {
			for _, pending := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/pending=%t", initial.mode, target, pending), func(t *testing.T) {
					s, requests := newLiveModeTestSession(t, initial.mode, initial.sandbox, false)
					if pending {
						away := "yolo"
						if initial.mode == "yolo" {
							away = "suggest"
						}
						if !s.SetLiveMode(away) {
							t.Fatal("switch away rejected")
						}
					}
					mode, tier := s.mode, s.effectiveSandboxTier
					policy := cloneSandboxPolicy(s.turnSandboxPolicy)
					if s.SetLiveMode(target) {
						t.Fatal("workspace switch without observed policy must fall back")
					}
					if s.mode != mode || s.effectiveSandboxTier != tier || s.lastWorkspacePolicy != nil || !reflect.DeepEqual(s.turnSandboxPolicy, policy) || len(requests()) != 1 {
						t.Fatal("fallback mutated permission state or transport")
					}
				})
			}
		}
	}
}

func TestAppServerSession_SetLiveMode_UnreadyOrInvalidDoesNotMutate(t *testing.T) {
	for _, tc := range []struct{ name, sandbox, mode string }{
		{"not-alive", liveWorkspacePolicy, "yolo"},
		{"no-thread", liveWorkspacePolicy, "yolo"},
		{"no-sandbox", `null`, "yolo"},
		{"unknown-sandbox", `{"type":"futurePolicy"}`, "yolo"},
		{"external-sandbox", `{"type":"externalSandbox"}`, "yolo"},
		{"invalid-mode", liveWorkspacePolicy, "hybrid"},
		{"requested-workspace-but-effective-readonly", `{"type":"readOnly"}`, "auto-review"},
		{"legacy-workspace-has-no-restorable-object", `"workspace-write"`, "workspace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, requests := newLiveModeTestSession(t, "workspace", tc.sandbox, false)
			if tc.name == "not-alive" {
				s.alive.Store(false)
			}
			if tc.name == "no-thread" {
				s.threadID.Store("")
			}
			if tc.name == "legacy-workspace-has-no-restorable-object" {
				if !s.SetLiveMode("suggest") {
					t.Fatal("switch away from legacy tier rejected")
				}
			}
			mode, tier := s.mode, s.effectiveSandboxTier
			workspace, turn := cloneSandboxPolicy(s.lastWorkspacePolicy), cloneSandboxPolicy(s.turnSandboxPolicy)
			if s.SetLiveMode(tc.mode) {
				t.Fatal("unready/invalid switch accepted")
			}
			if s.mode != mode || s.effectiveSandboxTier != tier || !reflect.DeepEqual(s.lastWorkspacePolicy, workspace) || !reflect.DeepEqual(s.turnSandboxPolicy, turn) || len(requests()) != 1 {
				t.Fatal("rejected switch mutated state or transport")
			}
		})
	}
}

func TestAppServerSession_ThreadSandboxRefreshClearsTurnOverride(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(fmt.Sprintf("resume=%t", resume), func(t *testing.T) {
			s, _ := newLiveModeTestSession(t, "workspace", liveWorkspacePolicy, resume)
			if !s.SetLiveMode("suggest") || s.turnSandboxPolicy == nil {
				t.Fatal("missing read-only override")
			}
			id := ""
			if resume {
				id = "parent-thread"
			}
			if err := s.ensureThread(id); err != nil {
				t.Fatal(err)
			}
			if s.effectiveSandboxTier != "workspace-write" || s.turnSandboxPolicy != nil || s.lastWorkspacePolicy == nil {
				t.Fatal("thread response did not replace effective sandbox and clear override")
			}
		})
	}
}

func TestAppServerSession_SetLiveMode_ConcurrentSendSnapshots(t *testing.T) {
	s, requests := newLiveModeTestSession(t, "workspace", liveWorkspacePolicy, false)
	var wg sync.WaitGroup
	for _, mode := range []string{"suggest", "yolo", "auto-review", "workspace", "full-auto"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if !s.SetLiveMode(mode) {
					t.Errorf("live switch to %s rejected", mode)
					return
				}
			}
		}()
	}
	for i := 0; i < 100; i++ {
		if err := s.Send("next", "msg", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	for _, req := range requests()[1:] {
		policy, ok := req.Params["sandboxPolicy"].(map[string]any)
		if !ok {
			continue // Same-tier transitions intentionally omit the sandbox.
		}
		switch policy["type"] {
		case "readOnly":
			assertLiveTurnPermissions(t, req, "on-request", "user", `{"type":"readOnly","networkAccess":false}`)
		case "dangerFullAccess":
			assertLiveTurnPermissions(t, req, "never", "user", `{"type":"dangerFullAccess"}`)
		case "workspaceWrite":
			approval, reviewer := req.Params["approvalPolicy"], req.Params["approvalsReviewer"]
			valid := (approval == "on-request" && (reviewer == "user" || reviewer == "auto_review")) ||
				(approval == "never" && reviewer == "user")
			if !valid {
				t.Fatalf("inconsistent workspace permissions: %#v", req.Params)
			}
			assertLiveTurnPermissions(t, req, approval.(string), reviewer.(string), liveWorkspacePolicy)
		default:
			t.Fatalf("unexpected sandbox policy: %#v", policy)
		}
	}
}
