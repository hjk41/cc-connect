package codex

import (
	"fmt"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

const appServerCompactTimeout = 10 * time.Minute

func (s *appServerSession) compactThread() error {
	threadID := s.CurrentSessionID()
	if !s.Alive() || threadID == "" {
		return fmt.Errorf("codex app-server: compaction requires a live session with a thread id")
	}
	s.stateMu.Lock()
	if s.currentTurn != "" || s.turnStarting || s.compactDone != nil {
		s.stateMu.Unlock()
		return fmt.Errorf("codex app-server: cannot compact while a turn or compaction is in progress")
	}
	done := make(chan struct{}, 1)
	s.compactDone = done
	timeout := s.compactTimeout
	s.stateMu.Unlock()
	defer func() {
		s.stateMu.Lock()
		s.compactDone = nil
		s.stateMu.Unlock()
	}()
	if timeout <= 0 {
		timeout = appServerCompactTimeout
	}
	deadline := time.Now().Add(timeout)
	requestTimeout := min(timeout, appServerRequestTimeout)
	if err := s.requestWithTimeout("thread/compact/start", map[string]any{"threadId": threadID}, nil, requestTimeout); err != nil {
		return fmt.Errorf("codex app-server thread/compact/start: %w", err)
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-s.contextDone():
		return fmt.Errorf("codex app-server compaction: %w", s.contextErr())
	case <-timer.C:
		return fmt.Errorf("codex app-server: compaction timed out")
	case <-done:
		select {
		case <-s.contextDone():
			return fmt.Errorf("codex app-server compaction: %w", s.contextErr())
		default:
		}
		s.emit(core.Event{Type: core.EventResult, SessionID: threadID, Done: true})
		return nil
	}
}

func (s *appServerSession) signalCompaction(threadID string) {
	if threadID == "" || threadID != s.CurrentSessionID() {
		return
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.compactDone != nil {
		select {
		case s.compactDone <- struct{}{}:
		default:
		}
	}
}

var _ core.AgentSessionCanceller = (*appServerSession)(nil)

func (s *appServerSession) CancelTurn() error {
	s.stateMu.Lock()
	threadID, turnID := s.CurrentSessionID(), s.currentTurn
	s.stateMu.Unlock()
	if turnID == "" {
		return nil
	}
	if err := s.request("turn/interrupt", map[string]any{"threadId": threadID, "turnId": turnID}, nil); err != nil {
		return fmt.Errorf("codex app-server turn/interrupt: %w", err)
	}
	return nil
}
