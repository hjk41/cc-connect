package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestCodexSession_OutputCitationBecomesAttachment(t *testing.T) {
	workDir := t.TempDir()
	deck := filepath.Join(workDir, "deck.pptx")
	if err := os.WriteFile(deck, []byte("pptx"), 0o644); err != nil {
		t.Fatal(err)
	}

	cs := &codexSession{
		ctx:     context.Background(),
		events:  make(chan core.Event, 8),
		workDir: workDir,
	}
	cs.handleEvent(map[string]any{"type": "turn.started"})
	cs.handleEvent(map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type": "agent_message",
			"content": []any{map[string]any{
				"type": "output_text",
				"text": "已生成。\n\n:codex-file-citation{path=\"" + deck + "\" purpose=\"output\"}",
			}},
		},
	})
	cs.handleEvent(map[string]any{"type": "turn.completed"})

	var text string
	var result core.Event
	for len(cs.events) > 0 {
		ev := <-cs.events
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
	wantDeck, _ := filepath.EvalSymlinks(deck)
	if len(result.AttachmentPaths) != 1 || result.AttachmentPaths[0] != wantDeck {
		t.Fatalf("attachments = %#v, want [%q]", result.AttachmentPaths, wantDeck)
	}
}
