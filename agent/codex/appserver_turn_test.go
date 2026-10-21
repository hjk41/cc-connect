package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func newTurnTestSession() *appServerSession {
	s := &appServerSession{ctx: context.Background(), events: make(chan core.Event, 32)}
	s.threadID.Store("parent-thread")
	s.alive.Store(true)
	return s
}

func turnTestNotify(t *testing.T, s *appServerSession, method string, params map[string]any) {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	s.handleNotification(method, raw)
}

func turnTestParams(thread, turn, status string) map[string]any {
	return map[string]any{"threadId": thread, "turn": map[string]any{"id": turn, "status": status}}
}

func turnTestMessage(thread, turn, text string) map[string]any {
	return map[string]any{"threadId": thread, "turnId": turn, "item": map[string]any{
		"id": "answer", "type": "agentMessage", "phase": "final_answer", "text": text,
	}}
}

func assertNoTurnEvents(t *testing.T, s *appServerSession) {
	t.Helper()
	select {
	case event := <-s.events:
		t.Fatalf("unexpected event before parent turn completion: %#v", event)
	default:
	}
}

func assertTurnAnswer(t *testing.T, s *appServerSession, want string) {
	t.Helper()
	var text []string
	results := 0
	for len(s.events) > 0 {
		event := <-s.events
		switch event.Type {
		case core.EventText:
			text = append(text, event.Content)
		case core.EventResult:
			if !event.Done || event.SessionID != "parent-thread" {
				t.Fatalf("invalid parent completion: %#v", event)
			}
			results++
		default:
			t.Fatalf("unexpected parent event: %#v", event)
		}
	}
	if got := strings.Join(text, ""); got != want || results != 1 {
		t.Fatalf("answer=%q results=%d, want %q and one result", got, results, want)
	}
}

func TestAppServerSession_SubagentCompletionDoesNotEndParentTurn(t *testing.T) {
	s := newTurnTestSession()
	turnTestNotify(t, s, "turn/started", turnTestParams("parent-thread", "parent-turn", "inProgress"))
	turnTestNotify(t, s, "item/completed", turnTestMessage("parent-thread", "parent-turn", "parent answer"))

	turnTestNotify(t, s, "turn/started", turnTestParams("child-thread", "child-turn", "inProgress"))
	turnTestNotify(t, s, "item/completed", turnTestMessage("child-thread", "child-turn", "child answer"))
	turnTestNotify(t, s, "item/started", map[string]any{
		"threadId": "child-thread", "turnId": "child-turn",
		"item": map[string]any{"type": "commandExecution", "command": "echo child"},
	})
	turnTestNotify(t, s, "turn/completed", turnTestParams("child-thread", "child-turn", "completed"))
	turnTestNotify(t, s, "thread/status/changed", map[string]any{
		"threadId": "child-thread", "status": map[string]any{"type": "idle"},
	})
	assertNoTurnEvents(t, s)

	turnTestNotify(t, s, "turn/completed", turnTestParams("parent-thread", "parent-turn", "completed"))
	assertTurnAnswer(t, s, "parent answer")
}

func TestAppServerSession_IdleDoesNotCompleteActiveTurn(t *testing.T) {
	s := newTurnTestSession()
	turnTestNotify(t, s, "turn/started", turnTestParams("parent-thread", "parent-turn", "inProgress"))
	turnTestNotify(t, s, "thread/status/changed", map[string]any{
		"threadId": "parent-thread", "status": map[string]any{"type": "idle"},
	})
	assertNoTurnEvents(t, s)
	turnTestNotify(t, s, "item/completed", turnTestMessage("parent-thread", "parent-turn", "actual final answer"))
	turnTestNotify(t, s, "turn/completed", turnTestParams("parent-thread", "parent-turn", "completed"))
	assertTurnAnswer(t, s, "actual final answer")
}

func TestAppServerSession_StaleTurnEventsDoNotEndCurrentTurn(t *testing.T) {
	for _, status := range []string{"completed", "failed"} {
		t.Run(status, func(t *testing.T) {
			s := newTurnTestSession()
			turnTestNotify(t, s, "turn/started", turnTestParams("parent-thread", "new-turn", "inProgress"))
			turnTestNotify(t, s, "item/completed", turnTestMessage("parent-thread", "old-turn", "stale answer"))
			turnTestNotify(t, s, "turn/completed", turnTestParams("parent-thread", "old-turn", status))
			assertNoTurnEvents(t, s)
			turnTestNotify(t, s, "item/completed", turnTestMessage("parent-thread", "new-turn", "new answer"))
			turnTestNotify(t, s, "turn/completed", turnTestParams("parent-thread", "new-turn", "completed"))
			assertTurnAnswer(t, s, "new answer")
		})
	}
}

type turnStartReplyWriter struct {
	write func([]byte) (int, error)
}

func (w *turnStartReplyWriter) Write(p []byte) (int, error) { return w.write(p) }
func (w *turnStartReplyWriter) Close() error                { return nil }

func TestAppServerSession_SetLiveReasoningEffortPreservesThread(t *testing.T) {
	for _, tt := range []struct{ effort, want string }{
		{"high", "high"}, {" HIGH ", "high"}, {"med", "medium"}, {"very-high", "xhigh"},
	} {
		t.Run(tt.effort, func(t *testing.T) {
			s := newTurnTestSession()
			s.effort = "low"
			if !s.SetLiveReasoningEffort(tt.effort) {
				t.Fatal("live session rejected reasoning effort")
			}
			if got := s.GetReasoningEffort(); got != tt.want {
				t.Fatalf("effort = %q, want %q", got, tt.want)
			}
			if !s.Alive() || s.CurrentSessionID() != "parent-thread" {
				t.Fatal("reasoning change lost the live thread")
			}
		})
	}
}

func TestAppServerSession_SetLiveReasoningEffortRequiresLiveThread(t *testing.T) {
	for _, state := range []string{"closed", "no thread"} {
		t.Run(state, func(t *testing.T) {
			s := newTurnTestSession()
			s.effort = "low"
			if state == "closed" {
				s.alive.Store(false)
			} else {
				s.threadID.Store("")
			}
			if s.SetLiveReasoningEffort("high") {
				t.Fatal("accepted reasoning effort without a live thread")
			}
			if got := s.GetReasoningEffort(); got != "low" {
				t.Fatalf("rejected change updated effort to %q", got)
			}
		})
	}
}

func TestAppServerSession_SendUsesLiveReasoningEffort(t *testing.T) {
	s := newTurnTestSession()
	s.effort = "low"
	if !s.SetLiveReasoningEffort("high") {
		t.Fatal("live session rejected reasoning effort")
	}
	var method string
	var params map[string]any
	s.stdin = &turnStartReplyWriter{write: func(p []byte) (int, error) {
		var req struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(p, &req); err != nil {
			return 0, err
		}
		method, params = req.Method, req.Params
		s.handleResponse(rpcResponseEnvelope{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"next-turn"}}`)})
		return len(p), nil
	}}
	if err := s.Send("hello", "message", nil, nil); err != nil {
		t.Fatal(err)
	}
	if method != "turn/start" || params["effort"] != "high" || params["threadId"] != "parent-thread" {
		t.Fatalf("request = %q %#v, want turn/start with high effort on parent-thread", method, params)
	}
}

func TestAppServerSession_StartResponsePreservesEarlyTurnEvents(t *testing.T) {
	for _, completeBeforeReply := range []bool{false, true} {
		t.Run(fmt.Sprint("completed_before_reply=", completeBeforeReply), func(t *testing.T) {
			s := newTurnTestSession()
			s.stdin = &turnStartReplyWriter{write: func(p []byte) (int, error) {
				var req struct {
					ID     int64  `json:"id"`
					Method string `json:"method"`
				}
				if err := json.Unmarshal(p, &req); err != nil {
					return 0, err
				}
				if req.Method != "turn/start" {
					return 0, fmt.Errorf("unexpected method %q", req.Method)
				}
				turnTestNotify(t, s, "turn/started", turnTestParams("parent-thread", "parent-turn", "inProgress"))
				turnTestNotify(t, s, "item/completed", turnTestMessage("parent-thread", "parent-turn", "early final answer"))
				if completeBeforeReply {
					turnTestNotify(t, s, "turn/completed", turnTestParams("parent-thread", "parent-turn", "completed"))
				}
				s.handleResponse(rpcResponseEnvelope{ID: req.ID, Result: json.RawMessage(`{"turn":{"id":"parent-turn"}}`)})
				return len(p), nil
			}}
			if err := s.Send("hello", "message", nil, nil); err != nil {
				t.Fatal(err)
			}
			if !completeBeforeReply {
				turnTestNotify(t, s, "turn/completed", turnTestParams("parent-thread", "parent-turn", "completed"))
			}
			assertTurnAnswer(t, s, "early final answer")
			s.stateMu.Lock()
			currentTurn := s.currentTurn
			s.stateMu.Unlock()
			if currentTurn != "" {
				t.Fatalf("late start response resurrected completed turn %q", currentTurn)
			}
		})
	}
}
