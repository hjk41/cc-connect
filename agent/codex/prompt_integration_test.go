package codex

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

// TestCodexDeveloperArgs_RealCodexShowsPrompt is an integration check that the
// -c developer_instructions pair produced by codexDeveloperArgs is accepted by
// the installed Codex CLI and reaches the model-visible prompt. It skips when
// codex is not installed or the local runtime cannot start, so CI without the
// CLI is unaffected.
func TestCodexDeveloperArgs_RealCodexShowsPrompt(t *testing.T) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex CLI not installed")
	}
	const marker = "CC_CONNECT_DEVELOPER_PROMPT_MARKER_9f31"
	prompt := buildCodexDeveloperPrompt(core.LangEnglish, "cc-connect integration "+marker)
	args := append(codexDeveloperArgs(prompt), "debug", "prompt-input")
	cmd := exec.Command(bin, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "CODEX_HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("codex debug prompt-input unavailable in this environment: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), marker) || !strings.Contains(string(out), "cc-connect send --file") {
		t.Fatalf("developer_instructions did not reach the Codex prompt; output=%s", truncate(string(out), 500))
	}
}
