package core

import (
	"strings"
	"time"
)

// thinkingDeltaFlushAfter is how long a streaming thinking block may buffer
// before a paragraph break is flushed so a long thought stays visible.
const thinkingDeltaFlushAfter = 5 * time.Second

// thinkingCoalescer concatenates consecutive EventThinking deltas into whole
// blocks. Downstream render paths only see non-delta thinking events.
type thinkingCoalescer struct {
	buf        strings.Builder
	sessionID  string
	started    time.Time
	now        func() time.Time
	flushAfter time.Duration
}

func newThinkingCoalescer(now func() time.Time) thinkingCoalescer {
	if now == nil {
		now = time.Now
	}
	return thinkingCoalescer{now: now, flushAfter: thinkingDeltaFlushAfter}
}

func (c *thinkingCoalescer) reset() {
	c.buf.Reset()
	c.sessionID = ""
	c.started = time.Time{}
}

// accept buffers a delta thinking event, or flushes the buffer ahead of any
// other event. emit is processed before event when keep is true. When keep is
// false, event was a delta and must not be rendered on its own.
func (c *thinkingCoalescer) accept(event Event) (emit []Event, keep bool) {
	now := c.now()
	if event.Type == EventThinking && event.Delta {
		if c.buf.Len() == 0 {
			c.started = now
		}
		if event.SessionID != "" {
			c.sessionID = event.SessionID
		}
		c.buf.WriteString(event.Content)
		if flushed, ok := c.flushParagraph(now); ok {
			return []Event{flushed}, false
		}
		return nil, false
	}
	if c.buf.Len() == 0 {
		return nil, true
	}
	ev := Event{Type: EventThinking, Content: c.buf.String(), SessionID: c.sessionID}
	c.reset()
	return []Event{ev}, true
}

// flushParagraph emits whole paragraphs once the block has been buffering for
// flushAfter and contains a blank line. The split is the last "\n\n"; the
// remainder stays buffered.
func (c *thinkingCoalescer) flushParagraph(now time.Time) (Event, bool) {
	if c.started.IsZero() || now.Sub(c.started) < c.flushAfter {
		return Event{}, false
	}
	s := c.buf.String()
	idx := strings.LastIndex(s, "\n\n")
	if idx < 0 {
		return Event{}, false
	}
	head := s[:idx+len("\n\n")]
	tail := s[idx+len("\n\n"):]
	ev := Event{Type: EventThinking, Content: head, SessionID: c.sessionID}
	c.buf.Reset()
	if tail == "" {
		c.sessionID = ""
		c.started = time.Time{}
		return ev, true
	}
	c.buf.WriteString(tail)
	c.started = now
	return ev, true
}
