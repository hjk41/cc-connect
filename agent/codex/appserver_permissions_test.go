package codex

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"
)

func TestAppServerSession_ModeProtocolPayloads(t *testing.T) {
	// Independent expectations catch accidental permission changes in the resolver.
	for _, tc := range []struct{ mode, sandbox, policy, reviewer, sandboxType string }{
		{"suggest", "read-only", "on-request", "user", "readOnly"},
		{"workspace", "workspace-write", "on-request", "user", "workspaceWrite"},
		{"auto-review", "workspace-write", "on-request", "auto_review", "workspaceWrite"},
		{"full-auto", "workspace-write", "never", "user", "workspaceWrite"},
		{"yolo", "danger-full-access", "never", "user", "dangerFullAccess"},
	} {
		for _, resume := range []bool{false, true} {
			name := tc.mode + "/start"
			if resume {
				name = tc.mode + "/resume"
			}
			t.Run(name, func(t *testing.T) {
				s := newTurnTestSession()
				s.mode = tc.mode
				var requests []parityRPCRequest
				s.stdin = &turnStartReplyWriter{write: func(p []byte) (int, error) {
					var req parityRPCRequest
					if err := json.Unmarshal(p, &req); err != nil {
						return 0, err
					}
					requests = append(requests, req)
					result := map[string]any{"turn": map[string]any{"id": "next-turn"}}
					if strings.HasPrefix(req.Method, "thread/") {
						result = map[string]any{"thread": map[string]any{"id": "parent-thread"},
							"approvalPolicy": tc.policy, "approvalsReviewer": tc.reviewer,
							"sandbox": map[string]any{"type": tc.sandboxType, "networkAccess": true}}
					}
					raw, err := json.Marshal(result)
					if err != nil {
						return 0, err
					}
					s.handleResponse(rpcResponseEnvelope{ID: req.ID, Result: raw})
					return len(p), nil
				}}
				resumeID, method := "", "thread/start"
				threadParams := map[string]any{"experimentalRawEvents": false, "persistExtendedHistory": false,
					"sandbox": tc.sandbox, "approvalPolicy": tc.policy, "approvalsReviewer": tc.reviewer}
				if resume {
					resumeID, method = "parent-thread", "thread/resume"
					threadParams["threadId"] = resumeID
					threadParams["persistExtendedHistory"] = true
				}
				if err := s.ensureThread(resumeID); err != nil {
					t.Fatal(err)
				}
				// Every turn repeats the explicit reviewer, without overriding any
				// sandbox/network settings inherited from the thread.
				for i := 0; i < 2; i++ {
					if err := s.Send("hello", "msg", nil, nil); err != nil {
						t.Fatal(err)
					}
				}
				if len(requests) != 3 || requests[0].Method != method || !reflect.DeepEqual(requests[0].Params, threadParams) {
					t.Fatalf("thread request = %#v, want %s %#v", requests, method, threadParams)
				}
				turnParams := map[string]any{"threadId": "parent-thread", "approvalPolicy": tc.policy, "approvalsReviewer": tc.reviewer,
					"input": []any{map[string]any{"type": "text", "text": "hello", "text_elements": []any{}}}}
				for _, req := range requests[1:] {
					if req.Method != "turn/start" || !reflect.DeepEqual(req.Params, turnParams) {
						t.Fatalf("turn request = %#v, want %#v (no sandbox, sandboxPolicy or permissions)", req, turnParams)
					}
				}
			})
		}
	}
}

func TestAppServerSession_EffectivePermissionsDecodedAndLogged(t *testing.T) {
	for _, method := range []string{"thread/start", "thread/resume"} {
		for _, tc := range []struct{ name, fields, want string }{
			{"matching", `"approvalPolicy":"on-request","approvalsReviewer":"auto_review","sandbox":{"type":"workspaceWrite","networkAccess":true},"activePermissionProfile":{"id":"workspace"}`, ""},
			{"changed reviewer", `"approvalPolicy":"on-request","approvalsReviewer":"user","sandbox":{"type":"workspaceWrite"}`, "mismatch"},
			{"changed policy", `"approvalPolicy":"never","approvalsReviewer":"auto_review","sandbox":{"type":"workspaceWrite"}`, "mismatch"},
			{"changed sandbox", `"approvalPolicy":"on-request","approvalsReviewer":"auto_review","sandbox":{"type":"readOnly"}`, "mismatch"},
			{"unsupported fields", `"sandbox":{"type":"futurePolicy"}`, "missing or unsupported"},
			{"structured policy", `"approvalPolicy":{"reject":{"rules":true}},"approvalsReviewer":"auto_review","sandbox":{"type":"workspaceWrite"}`, "mismatch"},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				var logs bytes.Buffer
				old := slog.Default()
				slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
				t.Cleanup(func() { slog.SetDefault(old) })
				s := newTurnTestSession()
				s.mode = "auto-review"
				s.stdin = &turnStartReplyWriter{write: func(p []byte) (int, error) {
					var req parityRPCRequest
					if err := json.Unmarshal(p, &req); err != nil {
						return 0, err
					}
					s.handleResponse(rpcResponseEnvelope{ID: req.ID, Result: json.RawMessage(`{"thread":{"id":"parent-thread"},` + tc.fields + `}`)})
					return len(p), nil
				}}
				id := ""
				if method == "thread/resume" {
					id = "parent-thread"
				}
				if err := s.ensureThread(id); err != nil {
					t.Fatal(err)
				}
				got := logs.String()
				if !strings.Contains(got, `"method":"`+method+`"`) || !strings.Contains(got, `"approvalsReviewer":`) || !strings.Contains(got, `"approvalPolicy":`) || !strings.Contains(got, `"activePermissionProfile":`) {
					t.Fatalf("effective state not logged: %s", got)
				}
				if tc.want == "" {
					if strings.Contains(got, `"level":"WARN"`) || !strings.Contains(got, `"approvalsReviewer":"auto_review"`) || !strings.Contains(got, `networkAccess`) || !strings.Contains(got, `workspace`) {
						t.Fatalf("matching response log: %s", got)
					}
				} else if !strings.Contains(got, tc.want) || !strings.Contains(got, `"level":"WARN"`) {
					t.Fatalf("missing %q warning: %s", tc.want, got)
				}
			})
		}
	}
}

func TestAppServerSession_UnsupportedApprovalSettingsReturnError(t *testing.T) {
	for _, method := range []string{"thread/start", "thread/resume", "turn/start"} {
		t.Run(method, func(t *testing.T) {
			s := newTurnTestSession()
			s.mode = "auto-review"
			s.stdin = &turnStartReplyWriter{write: func(p []byte) (int, error) {
				var req parityRPCRequest
				if err := json.Unmarshal(p, &req); err != nil {
					return 0, err
				}
				if req.Method != method {
					t.Errorf("method = %s, want %s", req.Method, method)
				}
				s.handleResponse(rpcResponseEnvelope{ID: req.ID, Error: &rpcError{Code: -32602, Message: "unsupported field approvalsReviewer"}})
				return len(p), nil
			}}
			var err error
			switch method {
			case "thread/start":
				err = s.ensureThread("")
			case "thread/resume":
				err = s.ensureThread("parent-thread")
			case "turn/start":
				err = s.Send("hello", "msg", nil, nil)
			}
			if err == nil || !strings.Contains(err.Error(), "unsupported field approvalsReviewer") {
				t.Fatalf("unsupported settings claimed success: %v", err)
			}
		})
	}
}
