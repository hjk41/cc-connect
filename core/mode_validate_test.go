package core

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type validatingModeAgent struct {
	stubModelModeAgent
	sets int
}

func (a *validatingModeAgent) ValidateMode(mode string) error {
	if mode != "default" && mode != "yolo" {
		return errors.New("unsupported permission mode")
	}
	return nil
}

func (a *validatingModeAgent) SetMode(mode string) {
	a.sets++
	a.stubModelModeAgent.SetMode(mode)
}

func TestCmdMode_InvalidModeDoesNotMutateAndIsLocalized(t *testing.T) {
	for _, lang := range []Language{LangEnglish, LangChinese, LangTraditionalChinese, LangJapanese, LangSpanish} {
		t.Run(string(lang), func(t *testing.T) {
			p := &stubPlatformEngine{n: "plain"}
			a := &validatingModeAgent{}
			e := NewEngine("test", a, []Platform{p}, "", lang)
			state, process, session := continuityState(e, "test:u", p, true)
			state.approveAll = true
			e.cmdMode(p, &Message{SessionKey: "test:u", ReplyCtx: "ctx"}, []string{"invalid"})
			assertInvalidModePreservedState(t, e, a, state, process, session)
			want := e.i18n.Tf(MsgModeInvalid, "invalid", a.ValidateMode("invalid"))
			if sent := p.getSent(); len(sent) != 1 || sent[0] != want || !strings.Contains(want, "/mode") {
				t.Fatalf("sent=%q, want localized error %q", sent, want)
			}
		})
	}
}

func TestModeCard_InvalidModeDoesNotMutate(t *testing.T) {
	p := &stubPlatformEngine{n: "plain"}
	a := &validatingModeAgent{}
	e := NewEngine("test", a, []Platform{p}, "", LangEnglish)
	state, process, session := continuityState(e, "test:u", p, true)
	state.approveAll = true
	if card := e.handleCardNav("act:/mode invalid", "test:u"); card == nil {
		t.Fatal("invalid mode action did not refresh the mode card")
	}
	assertInvalidModePreservedState(t, e, a, state, process, session)
}

func assertInvalidModePreservedState(t *testing.T, e *Engine, a *validatingModeAgent, state *interactiveState, process *continuitySession, session *Session) {
	t.Helper()
	if a.sets != 0 || a.GetMode() != "default" || !state.approveAll || state.agentSession != process || process.closeCount() != 0 || len(process.modes) != 0 || len(state.pendingMessages) != 1 {
		t.Fatalf("invalid mode mutated agent, approval grant, process or queue: sets=%d state=%+v", a.sets, state)
	}
	if e.interactiveStates[e.interactiveKeyForSessionKey("test:u")] != state || session.GetAgentSessionID() != "saved-session" || len(session.GetHistory(0)) != 1 {
		t.Fatal("invalid mode discarded session identity/history/state")
	}
}

func TestMgmt_InvalidModeReturns400WithoutMutationOrPersist(t *testing.T) {
	a := &validatingModeAgent{}
	e := NewEngine("test", a, nil, "", LangEnglish)
	m := NewManagementServer(0, "", nil)
	saved := false
	m.SetSaveProjectSettings(func(string, ProjectSettingsUpdate) error { saved = true; return nil })
	// Include another setting to ensure validation precedes every mutation.
	r := httptest.NewRequest(http.MethodPatch, "/", bytes.NewBufferString(`{"mode":"invalid","language":"zh"}`))
	w := httptest.NewRecorder()
	m.handleProjectDetail(w, r, "test", e)
	if w.Code != http.StatusBadRequest || a.sets != 0 || saved || a.GetMode() != "default" || e.i18n.CurrentLang() != LangEnglish {
		t.Fatalf("code=%d mode=%s sets=%d saved=%v body=%s", w.Code, a.GetMode(), a.sets, saved, w.Body.String())
	}
}

func TestModeChange_ClearsApproveAll(t *testing.T) {
	for _, path := range []string{"slash", "card"} {
		for _, live := range []bool{true, false} {
			name := path + "/restart"
			if live {
				name = path + "/live"
			}
			t.Run(name, func(t *testing.T) {
				p := &stubPlatformEngine{n: "plain"}
				a := &resumeModeAgent{}
				e := NewEngine("test", a, []Platform{p}, "", LangEnglish)
				state, process, session := continuityState(e, "test:u", p, live)
				state.approveAll = true
				if path == "slash" {
					e.cmdMode(p, &Message{SessionKey: "test:u", ReplyCtx: "ctx"}, []string{"yolo"})
				} else {
					e.executeCardAction("/mode", "yolo", "test:u")
				}
				state.mu.Lock()
				approveAll := state.approveAll
				state.mu.Unlock()
				if approveAll {
					t.Fatal("session approveAll survived mode change")
				}
				if session.GetAgentSessionID() != "saved-session" || len(session.GetHistory(0)) != 1 {
					t.Fatal("mode change lost resumable history")
				}
				if live && (process.closeCount() != 0 || len(state.pendingMessages) != 1) {
					t.Fatal("existing live-switch path lost process or queue")
				}
			})
		}
	}
}
