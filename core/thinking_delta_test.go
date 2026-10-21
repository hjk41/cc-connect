package core

import (
	"strings"
	"testing"
	"time"
)

func TestThinkingDeltas_CoalescedIntoOneProgressEntry(t *testing.T) {
	t.Run("card", func(t *testing.T) {
		p := &stubCompactProgressPlatform{
			stubPlatformEngine: stubPlatformEngine{n: "feishu"},
			style:              "card",
			supportPayload:     true,
		}
		e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
		e.display.ProgressUpdateInterval = 0
		sessionKey := "feishu:delta-card"
		session := e.sessions.GetOrCreateActive(sessionKey)
		agentSession := newControllableSession("s-delta-card")
		state := &interactiveState{agentSession: agentSession, platform: p, replyCtx: "ctx-delta-card"}
		e.interactiveStates[sessionKey] = state

		for _, part := range []string{"ab", "cd", "ef"} {
			agentSession.events <- Event{Type: EventThinking, Content: part, Delta: true}
		}
		agentSession.events <- Event{Type: EventToolUse, ToolName: "Bash", ToolInput: "pwd"}
		agentSession.events <- Event{Type: EventResult, Content: "done", Done: true}
		e.processInteractiveEvents(state, session, e.sessions, sessionKey, "m-delta-card", time.Now(), nil, nil, state.replyCtx, 0)

		var found bool
		for _, raw := range append(p.getPreviewStarts(), p.getPreviewEdits()...) {
			payload, ok := ParseProgressCardPayload(raw)
			if !ok {
				continue
			}
			for i := 0; i+1 < len(payload.Items); i++ {
				if payload.Items[i].Kind == ProgressEntryThinking && payload.Items[i].Text == "abcdef" &&
					payload.Items[i+1].Kind == ProgressEntryToolUse {
					found = true
				}
			}
			for _, item := range payload.Items {
				if item.Kind == ProgressEntryThinking && item.Text != "abcdef" {
					t.Fatalf("fragmented thinking entry %q", item.Text)
				}
			}
		}
		if !found {
			t.Fatalf("no payload contained thinking abcdef before the tool; starts=%v edits=%v", p.getPreviewStarts(), p.getPreviewEdits())
		}
	})

	t.Run("legacy", func(t *testing.T) {
		p := &stubPlatformEngine{n: "plain"}
		e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
		sessionKey := "plain:delta"
		session := e.sessions.GetOrCreateActive(sessionKey)
		agentSession := newControllableSession("s-delta-legacy")
		state := &interactiveState{agentSession: agentSession, platform: p, replyCtx: "ctx-delta-legacy"}
		e.interactiveStates[sessionKey] = state

		for _, part := range []string{"ab", "cd", "ef"} {
			agentSession.events <- Event{Type: EventThinking, Content: part, Delta: true}
		}
		agentSession.events <- Event{Type: EventToolUse, ToolName: "Bash", ToolInput: "pwd"}
		agentSession.events <- Event{Type: EventResult, Content: "done", Done: true}
		e.processInteractiveEvents(state, session, e.sessions, sessionKey, "m-delta-legacy", time.Now(), nil, nil, state.replyCtx, 0)

		var thinking int
		for _, msg := range p.getSent() {
			if strings.Contains(msg, "💭") {
				thinking++
				if !strings.Contains(msg, "abcdef") {
					t.Fatalf("thinking message = %q, want coalesced abcdef", msg)
				}
			}
		}
		if thinking != 1 {
			t.Fatalf("thinking messages = %d, want 1; sent=%#v", thinking, p.getSent())
		}
	})

	t.Run("paragraph flush after 5s", func(t *testing.T) {
		p := &stubPlatformEngine{n: "plain"}
		e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
		t0 := time.Unix(1_700_000_000, 0)
		var calls int
		e.nowFunc = func() time.Time {
			calls++
			if calls == 1 {
				return t0
			}
			return t0.Add(thinkingDeltaFlushAfter)
		}
		sessionKey := "plain:para"
		session := e.sessions.GetOrCreateActive(sessionKey)
		agentSession := newControllableSession("s-delta-para")
		state := &interactiveState{agentSession: agentSession, platform: p, replyCtx: "ctx-delta-para"}
		e.interactiveStates[sessionKey] = state

		agentSession.events <- Event{Type: EventThinking, Content: "Para one.\n\n", Delta: true}
		agentSession.events <- Event{Type: EventThinking, Content: "tail", Delta: true}
		agentSession.events <- Event{Type: EventToolUse, ToolName: "Bash", ToolInput: "pwd"}
		agentSession.events <- Event{Type: EventResult, Content: "done", Done: true}
		e.processInteractiveEvents(state, session, e.sessions, sessionKey, "m-delta-para", time.Now(), nil, nil, state.replyCtx, 0)

		var head, rest bool
		for _, msg := range p.getSent() {
			if !strings.Contains(msg, "💭") {
				continue
			}
			if strings.Contains(msg, "Para one.") && !strings.Contains(msg, "tail") {
				head = true
			}
			if strings.Contains(msg, "tail") {
				rest = true
			}
		}
		if !head || !rest {
			t.Fatalf("paragraph flush head=%v rest=%v sent=%#v", head, rest, p.getSent())
		}
	})

	t.Run("no paragraph flush before 5s", func(t *testing.T) {
		p := &stubPlatformEngine{n: "plain"}
		e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
		frozen := time.Unix(1_700_000_000, 0)
		e.nowFunc = func() time.Time { return frozen }
		sessionKey := "plain:para-early"
		session := e.sessions.GetOrCreateActive(sessionKey)
		agentSession := newControllableSession("s-delta-early")
		state := &interactiveState{agentSession: agentSession, platform: p, replyCtx: "ctx-delta-early"}
		e.interactiveStates[sessionKey] = state

		agentSession.events <- Event{Type: EventThinking, Content: "Para one.\n\n", Delta: true}
		agentSession.events <- Event{Type: EventThinking, Content: "tail", Delta: true}
		agentSession.events <- Event{Type: EventToolUse, ToolName: "Bash", ToolInput: "pwd"}
		agentSession.events <- Event{Type: EventResult, Content: "done", Done: true}
		e.processInteractiveEvents(state, session, e.sessions, sessionKey, "m-delta-early", time.Now(), nil, nil, state.replyCtx, 0)

		var thinking []string
		for _, msg := range p.getSent() {
			if strings.Contains(msg, "💭") {
				thinking = append(thinking, msg)
			}
		}
		if len(thinking) != 1 || !strings.Contains(thinking[0], "Para one.") || !strings.Contains(thinking[0], "tail") {
			t.Fatalf("early paragraph split = %#v", thinking)
		}
	})
}
