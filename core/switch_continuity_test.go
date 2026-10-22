package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type continuitySession struct {
	stubAgentSession
	mu     sync.Mutex
	closes int
	live   bool
	modes  []string
}

func (s *continuitySession) Close() error { s.mu.Lock(); defer s.mu.Unlock(); s.closes++; return nil }
func (s *continuitySession) SetLiveMode(mode string) bool {
	s.modes = append(s.modes, mode)
	return s.live
}
func (s *continuitySession) closeCount() int { s.mu.Lock(); defer s.mu.Unlock(); return s.closes }

func continuityState(e *Engine, key string, p Platform, live bool) (*interactiveState, *continuitySession, *Session) {
	process := &continuitySession{live: live}
	state := &interactiveState{agentSession: process, platform: p, replyCtx: "ctx", pendingMessages: []queuedMessage{{content: "queued", platform: p, replyCtx: "ctx"}}}
	e.interactiveStates[e.interactiveKeyForSessionKey(key)] = state
	session := e.sessions.GetOrCreateActive(key)
	session.SetAgentSessionID("saved-session", "stub")
	session.AddHistory("user", "remember this")
	return state, process, session
}

func TestModeCard_LiveSuccessDoesNotCloseSession(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubModelModeAgent{}, []Platform{p}, "", LangEnglish)
	state, process, session := continuityState(e, "test:u", p, true)
	e.executeCardAction("/mode", "yolo", "test:u")
	if process.closeCount() != 0 || state.agentSession != process || len(state.pendingMessages) != 1 {
		t.Fatal("successful live mode card closed the process or dropped queued work")
	}
	if session.GetAgentSessionID() != "saved-session" || len(session.GetHistory(0)) != 1 {
		t.Fatal("conversation lost")
	}
}

func TestModeSelection_SlashAndCardContinuity(t *testing.T) {
	for _, path := range []string{"slash", "card"} {
		for _, tc := range []struct {
			name         string
			live, resume bool
		}{
			{"live", true, false}, {"live-failure-resumable", false, true}, {"non-resumable", false, false},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				p := &stubPlatformEngine{n: "test"}
				var agent Agent = &stubModelModeAgent{}
				if tc.resume {
					agent = &resumeModeAgent{}
				}
				e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
				state, process, session := continuityState(e, "test:u", p, tc.live)
				if path == "slash" {
					e.cmdMode(p, &Message{SessionKey: "test:u", ReplyCtx: "ctx"}, []string{"yolo"})
				} else {
					e.executeCardAction("/mode", "yolo", "test:u")
				}
				wantID, wantHistory, wantCloses := "", 0, 1
				if tc.live || tc.resume {
					wantID, wantHistory = "saved-session", 1
				}
				if tc.live {
					wantCloses = 0
				}
				if process.closeCount() != wantCloses || session.GetAgentSessionID() != wantID || len(session.GetHistory(0)) != wantHistory {
					t.Fatalf("closes=%d id=%q history=%v", process.closeCount(), session.GetAgentSessionID(), session.GetHistory(0))
				}
				if tc.live && len(state.pendingMessages) != 1 {
					t.Fatal("queued work dropped")
				}
				if !tc.live && len(state.pendingMessages) != 0 {
					t.Fatal("restart did not clean queue")
				}
			})
		}
	}
}

func TestProviderSelection_AlreadyActivePreservesConversation(t *testing.T) {
	for _, path := range []string{"slash", "card"} {
		for _, name := range []string{"provider", "clear"} {
			t.Run(path+"/"+name, func(t *testing.T) {
				p := &stubPlatformEngine{n: "test"}
				agent := &stubModelModeAgent{providers: []ProviderConfig{{Name: "provider"}}}
				if name != "clear" {
					agent.active = name
				}
				e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
				state, process, session := continuityState(e, "test:u", p, false)
				e.providerSaveFunc = func(string) error { t.Error("no-op saved configuration"); return nil }
				if path == "slash" {
					e.cmdProvider(p, &Message{SessionKey: "test:u", ReplyCtx: "ctx"}, []string{name})
				} else {
					e.executeCardAction("/provider", name, "test:u")
				}
				if process.closeCount() != 0 || len(state.pendingMessages) != 1 || session.GetAgentSessionID() != "saved-session" || len(session.GetHistory(0)) != 1 {
					t.Fatal("provider no-op interrupted conversation")
				}
				if path == "slash" && name != "clear" && !strings.Contains(strings.Join(p.getSent(), "\n"), "already active") {
					t.Fatal("missing already-active reply")
				}
			})
		}
	}
}

func TestWorkdir_UnchangedPreservesConversation(t *testing.T) {
	for _, path := range []string{"slash", "card"} {
		for _, target := range []string{"dot", "absolute", "symlink", "history", "reset", "relative-current"} {
			t.Run(path+"/"+target, func(t *testing.T) {
				dir := t.TempDir()
				agent := &stubWorkDirAgent{workDir: dir}
				p := &stubPlatformEngine{n: "test"}
				e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
				e.baseWorkDir = dir
				e.dirHistory = NewDirHistory(t.TempDir())
				e.dirHistory.Add("test", dir)
				arg := "."
				switch target {
				case "absolute":
					arg = dir
				case "symlink":
					arg = filepath.Join(t.TempDir(), "alias")
					if err := os.Symlink(dir, arg); err != nil {
						t.Skip(err)
					}
				case "history":
					arg = "1"
				case "reset":
					arg = "reset"
				case "relative-current":
					agent.workDir = "."
					e.baseWorkDir = "."
				}
				state, process, session := continuityState(e, "test:u", p, false)
				if path == "slash" {
					e.cmdDir(p, &Message{SessionKey: "test:u", ReplyCtx: "ctx"}, []string{arg})
				} else {
					if arg != "reset" {
						arg = "select " + arg
					}
					e.executeCardAction("/dir", arg, "test:u")
				}
				if process.closeCount() != 0 || len(state.pendingMessages) != 1 || session.GetAgentSessionID() != "saved-session" || len(session.GetHistory(0)) != 1 {
					t.Fatal("unchanged workdir interrupted conversation")
				}
			})
		}
	}
}

func TestModelCard_FailurePreservesSessionAndQueue(t *testing.T) {
	for _, path := range []string{"card-nav", "async-action"} {
		for _, failure := range []string{"validation", "model-save", "provider-save"} {
			t.Run(path+"/"+failure, func(t *testing.T) {
				p := &stubPlatformEngine{n: "test"}
				agent := &validatingModelAgent{stubModelModeAgent: stubModelModeAgent{model: "old"}}
				e := NewEngine("test", agent, []Platform{p}, "", LangEnglish)
				entered, release := make(chan struct{}), make(chan struct{})
				fail := func() error { close(entered); <-release; return errors.New("rejected") }
				switch failure {
				case "validation":
					agent.validate = func(context.Context, string) (string, error) { return "", fail() }
				case "model-save":
					e.modelSaveFunc = func(string) error { return fail() }
				case "provider-save":
					agent.providers, agent.active = []ProviderConfig{{Name: "p", Model: "old"}}, "p"
					e.providerModelSaveFunc = func(string, string) error { return fail() }
				}
				state, process, session := continuityState(e, "test:u", p, false)
				finished := make(chan struct{})
				if path == "card-nav" {
					go func() { e.handleCardNav("act:/model new", "test:u"); close(finished) }()
				} else {
					e.executeCardAction("/model", "new", "test:u")
				}
				select {
				case <-entered:
				case <-time.After(2 * time.Second):
					t.Fatal("switch never reached validation/persistence")
				}
				if process.closeCount() != 0 {
					t.Error("session closed before validation/persistence")
				}
				close(release)
				deadline := time.Now().Add(2 * time.Second)
				for {
					state.mu.Lock()
					done := state.modelSwitch != nil && state.modelSwitch.phase == "result"
					if path == "card-nav" {
						select {
						case <-finished:
							done = true
						default:
						}
					}
					state.mu.Unlock()
					if done {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("model result not delivered")
					}
					time.Sleep(time.Millisecond)
				}
				state.mu.Lock()
				kept := state.agentSession == process && len(state.pendingMessages) == 1
				state.mu.Unlock()
				if !kept || process.closeCount() != 0 || session.GetAgentSessionID() != "saved-session" || len(session.GetHistory(0)) != 1 || agent.model != "old" {
					t.Fatal("rejected model card interrupted session or changed model")
				}
			})
		}
	}
}
