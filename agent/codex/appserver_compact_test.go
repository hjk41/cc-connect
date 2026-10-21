package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

type parityRPCRequest struct {
	ID     int64          `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

func TestAppServerSession_CompactWaitsForMatchingNotification(t *testing.T) {
	for _, notification := range []string{"thread/compacted", "item/completed"} {
		t.Run(notification, func(t *testing.T) {
			s := newTurnTestSession()
			s.compactTimeout = time.Second
			requests := make(chan parityRPCRequest, 1)
			s.stdin = &turnStartReplyWriter{write: func(p []byte) (int, error) {
				var req parityRPCRequest
				if err := json.Unmarshal(p, &req); err != nil {
					return 0, err
				}
				s.handleResponse(rpcResponseEnvelope{ID: req.ID, Result: json.RawMessage(`{}`)})
				requests <- req
				return len(p), nil
			}}
			finished := make(chan error, 1)
			go func() { finished <- s.Send(" \n/compact\t", "message", nil, nil) }()
			select {
			case req := <-requests:
				if req.Method != "thread/compact/start" || req.Params["threadId"] != "parent-thread" {
					t.Fatalf("request = %#v", req)
				}
			case <-time.After(time.Second):
				t.Fatal("no compact RPC")
			}
			params := map[string]any{"threadId": "child-thread", "turnId": "compact-turn", "item": map[string]any{"type": "contextCompaction"}}
			turnTestNotify(t, s, notification, params)
			select {
			case err := <-finished:
				t.Fatalf("unrelated thread completed compaction: %v", err)
			default:
			}
			assertNoTurnEvents(t, s)
			params["threadId"] = "parent-thread"
			turnTestNotify(t, s, notification, params)
			// Both notification forms, including duplicates, must produce one result.
			turnTestNotify(t, s, "thread/compacted", params)
			turnTestNotify(t, s, "item/completed", params)
			select {
			case err := <-finished:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("compaction did not finish")
			}
			assertTurnAnswer(t, s, "")
			turnTestNotify(t, s, notification, params)
			assertNoTurnEvents(t, s)
			if s.preambleSent {
				t.Fatal("compaction consumed the first-turn preamble")
			}
		})
	}
}

func TestAppServerSession_CompactNotificationBeforeRPCReply(t *testing.T) {
	s := newTurnTestSession()
	s.compactTimeout = time.Second
	s.stdin = &turnStartReplyWriter{write: func(p []byte) (int, error) {
		var req parityRPCRequest
		if err := json.Unmarshal(p, &req); err != nil {
			return 0, err
		}
		s.handleNotification("thread/compacted", json.RawMessage(`{"threadId":"parent-thread"}`))
		s.handleResponse(rpcResponseEnvelope{ID: req.ID, Result: json.RawMessage(`{}`)})
		return len(p), nil
	}}
	if err := s.Send("/compact", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	assertTurnAnswer(t, s, "")
}

func TestAppServerSession_CompactDoesNotReplaceNormalTurn(t *testing.T) {
	for _, prompt := range []string{"hello", "/compact extra"} {
		t.Run(prompt, func(t *testing.T) {
			s := newTurnTestSession()
			requests := make(chan parityRPCRequest, 1)
			s.stdin = &turnStartReplyWriter{write: func(p []byte) (int, error) {
				var req parityRPCRequest
				if err := json.Unmarshal(p, &req); err != nil {
					return 0, err
				}
				requests <- req
				s.handleResponse(rpcResponseEnvelope{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"turn-1"}}`)})
				return len(p), nil
			}}
			if err := s.Send(prompt, "", nil, nil); err != nil {
				t.Fatal(err)
			}
			if req := <-requests; req.Method != "turn/start" {
				t.Fatalf("method = %q", req.Method)
			}
		})
	}
}

func TestCompressCommand_BackendCapability(t *testing.T) {
	for backend, want := range map[string]string{"exec": "", "app_server": "/compact"} {
		if got := (&Agent{backend: backend}).CompressCommand(); got != want {
			t.Fatalf("%s: command %q, want %q", backend, got, want)
		}
	}
}

func TestAppServerSession_CompactRejectsBusyOrInvalidSession(t *testing.T) {
	for _, state := range []string{"active turn", "starting turn", "compacting", "closed", "no thread"} {
		t.Run(state, func(t *testing.T) {
			s := newTurnTestSession()
			switch state {
			case "active turn":
				s.currentTurn = "turn-1"
			case "starting turn":
				s.turnStarting = true
			case "compacting":
				s.compactDone = make(chan struct{}, 1)
			case "closed":
				s.alive.Store(false)
			case "no thread":
				s.threadID.Store("")
			}
			writes := make(chan struct{}, 1)
			s.stdin = &turnStartReplyWriter{write: func(p []byte) (int, error) { writes <- struct{}{}; return 0, fmt.Errorf("unexpected write") }}
			if err := s.Send("/compact", "", nil, nil); err == nil {
				t.Fatal("compaction should fail")
			}
			select {
			case <-writes:
				t.Fatal("rejected compaction wrote an RPC")
			default:
			}
		})
	}
}

func TestAppServerSession_CompactTimeoutCancellationAndRPCFailure(t *testing.T) {
	for _, outcome := range []string{"timeout", "cancel", "rpc error"} {
		t.Run(outcome, func(t *testing.T) {
			s := newTurnTestSession()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.ctx, s.cancel = ctx, cancel
			s.compactTimeout = time.Second
			if outcome == "timeout" {
				s.compactTimeout = 25 * time.Millisecond
			}
			s.stdin = &turnStartReplyWriter{write: func(p []byte) (int, error) {
				var req parityRPCRequest
				if err := json.Unmarshal(p, &req); err != nil {
					return 0, err
				}
				resp := rpcResponseEnvelope{ID: req.ID, Result: json.RawMessage(`{}`)}
				if outcome == "rpc error" {
					resp.Error = &rpcError{Message: "compact refused"}
				}
				s.handleResponse(resp)
				if outcome == "cancel" {
					cancel()
				}
				return len(p), nil
			}}
			err := s.Send("/compact", "", nil, nil)
			if err == nil {
				t.Fatal("expected compaction failure")
			}
			if outcome == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v", err)
			}
			if outcome == "timeout" && !strings.Contains(err.Error(), "timed out") {
				t.Fatalf("error = %v", err)
			}
			if outcome == "rpc error" && !strings.Contains(err.Error(), "compact refused") {
				t.Fatalf("error = %v", err)
			}
			if s.compactDone != nil {
				t.Fatal("failed compaction left a waiter behind")
			}
			assertNoTurnEvents(t, s)
		})
	}
}

func TestAppServerSession_CancelTurn(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, rpcFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("active=%v/error=%v", active, rpcFails), func(t *testing.T) {
				s := newTurnTestSession()
				if active {
					s.currentTurn = "turn-1"
				}
				requests := make(chan parityRPCRequest, 1)
				s.stdin = &turnStartReplyWriter{write: func(p []byte) (int, error) {
					var req parityRPCRequest
					if err := json.Unmarshal(p, &req); err != nil {
						return 0, err
					}
					requests <- req
					resp := rpcResponseEnvelope{ID: req.ID, Result: json.RawMessage(`{}`)}
					if rpcFails {
						resp.Error = &rpcError{Code: -1, Message: "interrupt refused"}
					}
					s.handleResponse(resp)
					return len(p), nil
				}}
				err := s.CancelTurn()
				if active && rpcFails {
					if err == nil || !strings.Contains(err.Error(), "turn/interrupt: interrupt refused") {
						t.Fatalf("error = %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if active {
					req := <-requests
					if req.Method != "turn/interrupt" || req.Params["threadId"] != "parent-thread" || req.Params["turnId"] != "turn-1" {
						t.Fatalf("request = %#v", req)
					}
				} else {
					select {
					case req := <-requests:
						t.Fatalf("inactive turn wrote %#v", req)
					default:
					}
				}
				if !s.Alive() {
					t.Fatal("CancelTurn closed the session")
				}
				assertNoTurnEvents(t, s)
			})
		}
	}
}

var _ core.ContextCompressor = (*Agent)(nil)
