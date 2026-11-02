package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestAppServerSession_OutputCitationBecomesAttachment(t *testing.T) {
	workDir := t.TempDir()
	deck := filepath.Join(workDir, "deck.pptx")
	if err := os.WriteFile(deck, []byte("pptx"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newTurnTestSession()
	s.workDir = workDir
	turnTestNotify(t, s, "turn/started", turnTestParams("parent-thread", "turn-1", "inProgress"))
	turnTestNotify(t, s, "item/completed", turnTestMessage(
		"parent-thread", "turn-1",
		"已生成。\n\n:codex-file-citation{path=\""+deck+"\" purpose=\"output\"}",
	))
	turnTestNotify(t, s, "turn/completed", turnTestParams("parent-thread", "turn-1", "completed"))

	var text string
	var result core.Event
	for len(s.events) > 0 {
		ev := <-s.events
		switch ev.Type {
		case core.EventText:
			text += ev.Content
		case core.EventResult:
			result = ev
		}
	}
	if strings.Contains(text, ":codex-file-citation{") {
		t.Fatalf("citation marker leaked into text: %q", text)
	}
	if !strings.Contains(text, "deck.pptx") {
		t.Fatalf("text = %q, want basename", text)
	}
	wantDeck, _ := filepath.EvalSymlinks(deck)
	if len(result.AttachmentPaths) != 1 || result.AttachmentPaths[0] != wantDeck {
		t.Fatalf("attachments = %#v, want [%q]", result.AttachmentPaths, wantDeck)
	}
}

func TestAppServerSession_SourceCitationIsNotAttached(t *testing.T) {
	workDir := t.TempDir()
	src := filepath.Join(workDir, "source.pptx")
	if err := os.WriteFile(src, []byte("pptx"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := newTurnTestSession()
	s.workDir = workDir
	turnTestNotify(t, s, "turn/started", turnTestParams("parent-thread", "turn-1", "inProgress"))
	turnTestNotify(t, s, "item/completed", turnTestMessage(
		"parent-thread", "turn-1",
		"审阅了 :codex-file-citation{path=\""+src+"\" purpose=\"source\"}",
	))
	turnTestNotify(t, s, "turn/completed", turnTestParams("parent-thread", "turn-1", "completed"))

	var result core.Event
	for len(s.events) > 0 {
		ev := <-s.events
		if ev.Type == core.EventResult {
			result = ev
		}
	}
	if len(result.AttachmentPaths) != 0 {
		t.Fatalf("source citation must not be attached: %#v", result.AttachmentPaths)
	}
}
