package acp

import (
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

// TestToolCallUpdates_DevinSequence replays the exact tool_call /
// tool_call_update sequence Devin sends for one shell command. The
// production progress card showed the result twice (terminal preview + exit
// update carry the same output) and titled it with the raw toolCallId
// because updates carry no title.
func TestToolCallUpdates_DevinSequence(t *testing.T) {
	s, _, _ := newTestSession(t, nil)
	const id = "7e65ab4f9#cf5ee50092374606977e4a40e896c554"
	for _, u := range []string{
		`{"sessionUpdate":"tool_call","toolCallId":"` + id + `","title":"Ran echo","kind":"execute","rawInput":{"command":"echo display-test"}}`,
		`{"sessionUpdate":"tool_call_update","toolCallId":"` + id + `","status":"in_progress"}`,
		`{"sessionUpdate":"tool_call_update","toolCallId":"` + id + `","status":"in_progress","content":[{"type":"content","content":{"type":"text","text":"display-test"}}],"_meta":{"cognition.ai/terminalPreview":true}}`,
		`{"sessionUpdate":"tool_call_update","toolCallId":"` + id + `","status":"in_progress","content":[{"type":"content","content":{"type":"text","text":"display-test"}}],"_meta":{"terminal_exit":{"exit_code":0}}}`,
		`{"sessionUpdate":"tool_call_update","toolCallId":"` + id + `","status":"completed"}`,
	} {
		s.onNotification("session/update", []byte(`{"sessionId":"test-session-id","update":`+u+`}`))
	}
	var results []core.Event
	for len(s.events) > 0 {
		if ev := <-s.events; ev.Type == core.EventToolResult {
			results = append(results, ev)
		}
	}
	if len(results) != 1 {
		t.Fatalf("got %d tool results, want 1: %+v", len(results), results)
	}
	if results[0].ToolName != "Ran echo" || results[0].Content != "display-test" {
		t.Fatalf("tool result = %q / %q, want \"Ran echo\" / \"display-test\"", results[0].ToolName, results[0].Content)
	}
	if !results[0].Partial || results[0].ToolCallID != id {
		t.Fatalf("snapshot = Partial %v ToolCallID %q, want partial with the tool call id", results[0].Partial, results[0].ToolCallID)
	}
}

// TestToolResultSnapshots_PartialThenEqualFinal keeps a completed result when
// its text matches the last in-progress snapshot, and drops only an identical
// partial repeat.
func TestToolResultSnapshots_PartialThenEqualFinal(t *testing.T) {
	s, _, _ := newTestSession(t, nil)
	const id = "call-git-log"
	for _, u := range []string{
		`{"sessionUpdate":"tool_call","toolCallId":"` + id + `","title":"git log","kind":"execute","rawInput":{"command":"git log -3"}}`,
		`{"sessionUpdate":"tool_call_update","toolCallId":"` + id + `","status":"in_progress","content":[{"type":"content","content":{"type":"text","text":"0abb492a"}}]}`,
		`{"sessionUpdate":"tool_call_update","toolCallId":"` + id + `","status":"in_progress","content":[{"type":"content","content":{"type":"text","text":"0abb492a"}}]}`,
		`{"sessionUpdate":"tool_call_update","toolCallId":"` + id + `","status":"in_progress","content":[{"type":"content","content":{"type":"text","text":"0abb492a\nsecond"}}]}`,
		`{"sessionUpdate":"tool_call_update","toolCallId":"` + id + `","status":"completed","content":[{"type":"content","content":{"type":"text","text":"0abb492a\nsecond"}}]}`,
	} {
		s.onNotification("session/update", []byte(`{"sessionId":"test-session-id","update":`+u+`}`))
	}
	var uses, results []core.Event
	for len(s.events) > 0 {
		ev := <-s.events
		switch ev.Type {
		case core.EventToolUse:
			uses = append(uses, ev)
		case core.EventToolResult:
			results = append(results, ev)
		}
	}
	if len(uses) != 1 || uses[0].ToolCallID != id {
		t.Fatalf("tool use = %+v, want one event with ToolCallID %s", uses, id)
	}
	if len(results) != 3 {
		t.Fatalf("got %d tool results, want partial, longer partial, final: %+v", len(results), results)
	}
	if !results[0].Partial || results[0].ToolCallID != id || results[0].Content != "0abb492a" {
		t.Fatalf("first snapshot = %+v", results[0])
	}
	if !results[1].Partial || results[1].Content != "0abb492a\nsecond" {
		t.Fatalf("second snapshot = %+v", results[1])
	}
	if results[2].Partial || results[2].ToolCallID != id || results[2].Content != "0abb492a\nsecond" {
		t.Fatalf("final equal to last partial was dropped or marked partial: %+v", results[2])
	}
}
