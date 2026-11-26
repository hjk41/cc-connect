package feishu

import (
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

// Progress panels are re-rendered on every card update. Rendering them
// expanded while the turn runs re-opens them on each refresh, which makes the
// text below the panels jump; they must stay collapsed in every state.
func TestProgressCards_PanelsStayCollapsed(t *testing.T) {
	exitCode := 0
	entries := []core.ProgressCardEntry{
		{Kind: core.ProgressEntryThinking, Text: "Inspecting event routing"},
		{Kind: core.ProgressEntryToolUse, Tool: "Bash", Text: "pwd"},
		{Kind: core.ProgressEntryToolResult, Tool: "Bash", Text: "/tmp/project", ExitCode: &exitCode},
		{Kind: core.ProgressEntryInfo, Text: "Context compacted"},
	}
	steps := []core.ToolStep{
		{Kind: core.ToolStepKindThinking, Summary: "Inspecting event routing"},
		{Kind: core.ToolStepKindTool, Name: "Bash", Summary: "pwd"},
	}

	cases := []struct {
		name   string
		card   string
		panels int
	}{
		{"progress_running", buildPreviewCardJSON(core.BuildProgressCardPayloadV2(entries, false, "Codex", core.LangEnglish, core.ProgressCardStateRunning)), 3},
		{"progress_completed", buildPreviewCardJSON(core.BuildProgressCardPayloadV2(entries, false, "Codex", core.LangEnglish, core.ProgressCardStateCompleted)), 3},
		{"rich_streaming", buildRichCard(core.CardStatusWorking, "", steps, "answer", true, ""), 2},
		{"rich_streaming_placeholder", buildRichCard(core.CardStatusThinking, "", nil, "", true, ""), 1},
		{"rich_done", buildRichCard(core.CardStatusDone, "", steps, "answer", false, ""), 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			panels := collectCardPanels(t, tc.card)
			if len(panels) != tc.panels {
				t.Fatalf("panel count = %d, want %d: %#v", len(panels), tc.panels, panels)
			}
			for _, panel := range panels {
				if expanded, _ := panel["expanded"].(bool); expanded {
					t.Fatalf("panel %q is expanded, want collapsed: %#v", cardPanelTitle(panel), panel)
				}
			}
		})
	}
}

// Regression for Feishu code=230099/11311 markdown parse errors: arbitrary
// tool input/output must never embed raw nested Markdown fences into the
// progress card. The outer fence is safe only because inner fence runs are
// neutralized before rendering.
func TestProgressToolFormatting_NeutralizesNestedCodeFences(t *testing.T) {
	raw := "python3 - <<'PY'\nprint('before')\n```\nprint('inner')\n```\n<xml><a href=\"x\">text</a><!CDATA[(.*?)\\]\\]>\nPY"
	input := formatProgressToolInput("Bash", raw)
	if !strings.HasPrefix(input, "```bash\n") {
		t.Fatalf("bash input should be fenced as bash: %q", input)
	}
	body := strings.TrimSuffix(strings.TrimPrefix(input, "```bash\n"), "\n```")
	if strings.Contains(body, "```") {
		t.Fatalf("tool input left a nested fence marker in the card: %q", body)
	}
	if strings.Contains(body, "<a") || strings.Contains(body, "</a>") || strings.Contains(body, "<!CDATA") {
		t.Fatalf("tool input left HTML-like tag openers in the card: %q", body)
	}

	result := formatProgressToolResult(raw)
	if !strings.HasPrefix(result, "```\n") {
		t.Fatalf("tool result should always be fenced: %q", result)
	}
	body = strings.TrimSuffix(strings.TrimPrefix(result, "```\n"), "\n```")
	if strings.Contains(body, "```") {
		t.Fatalf("tool result left a nested fence marker in the card: %q", body)
	}
}
