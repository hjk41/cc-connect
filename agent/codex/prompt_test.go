package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestCodexAgent_NativeSystemPromptSupport(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	raw, err := New(map[string]any{
		"cmd":      os.Args[0],
		"language": "zh",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a, ok := raw.(*Agent)
	if !ok {
		t.Fatalf("New returned %T", raw)
	}
	if !a.HasSystemPromptSupport() {
		t.Fatal("codex must report native system prompt support")
	}
	if !strings.Contains(a.developerPrompt, "cc-connect send --file") {
		t.Fatalf("developer prompt missing send instructions: %q", a.developerPrompt)
	}
	if !strings.Contains(a.developerPrompt, "codex-file-citation") {
		t.Fatalf("developer prompt missing citation guidance: %q", a.developerPrompt)
	}
	if !strings.Contains(a.developerPrompt, "不要") && !strings.Contains(a.developerPrompt, "把生成的图片") {
		// The Chinese cc-connect prompt should be selected for language=zh.
		t.Fatalf("developer prompt is not localized: %q", a.developerPrompt[:200])
	}
}

func TestCodexAgent_PreservesConfiguredDeveloperInstructions(t *testing.T) {
	codexHome := t.TempDir()
	config := "developer_instructions = \"KEEP_MY_INSTRUCTIONS\"\n"
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)

	raw, err := New(map[string]any{"cmd": os.Args[0]})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := raw.(*Agent)
	if !strings.Contains(a.developerPrompt, "KEEP_MY_INSTRUCTIONS") {
		t.Fatalf("user developer_instructions were clobbered: %q", a.developerPrompt)
	}
	if !strings.Contains(a.developerPrompt, "cc-connect send --file") {
		t.Fatalf("cc-connect instructions missing: %q", a.developerPrompt)
	}
}

func TestCodexDeveloperArgs_EncodesTOMLBasicString(t *testing.T) {
	args := codexDeveloperArgs("line1\nline2 \"quoted\"")
	if len(args) != 2 || args[0] != "-c" {
		t.Fatalf("args = %#v", args)
	}
	const prefix = "developer_instructions="
	if !strings.HasPrefix(args[1], prefix) {
		t.Fatalf("arg = %q", args[1])
	}
	var decoded string
	if err := json.Unmarshal([]byte(strings.TrimPrefix(args[1], prefix)), &decoded); err != nil {
		t.Fatalf("decode TOML/JSON basic string: %v", err)
	}
	if decoded != "line1\nline2 \"quoted\"" {
		t.Fatalf("decoded = %q", decoded)
	}
}

func TestCodexDeveloperArgs_EmptyPrompt(t *testing.T) {
	if got := codexDeveloperArgs("  "); len(got) != 0 {
		t.Fatalf("args = %#v, want none", got)
	}
}

func TestBuildCodexDeveloperPrompt_OrderAndSections(t *testing.T) {
	got := buildCodexDeveloperPrompt(core.LangEnglish, "user instructions")
	if !strings.HasPrefix(got, "user instructions\n\n") {
		t.Fatalf("existing instructions not first: %q", got[:80])
	}
	if !strings.Contains(got, "Delivering generated files") {
		t.Fatal("attachment guidance missing")
	}
}
