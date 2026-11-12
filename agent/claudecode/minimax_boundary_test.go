package claudecode

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

// The agent delegates inference to Claude Code. A local no-op executable checks
// that the selected model and provider reach that process without any inference.
func TestMiniMaxModelHandoffPreservesExplicitSelection(t *testing.T) {
	for _, model := range []string{"MiniMax-M3.1-Flash", "MiniMax-M3", "MiniMax-M2.7", "saved-custom-model", "claude-sonnet-4"} {
		t.Run(model, func(t *testing.T) {
			a := &Agent{
				cmd: os.Args[0], workDir: t.TempDir(), model: "agent-default", mode: "default",
				// The test binary doubles as a cross-platform stub: it reads
				// stdin until EOF and exits, so the assertion below can inspect
				// the subprocess arguments without depending on /bin/echo.
				cliExtraArgs: []string{"-test.run=TestHelperProcess", "--", "stdin-eof-exit"},
				sessionEnv:   []string{"GO_WANT_HELPER_PROCESS=1"},
				providers:    []core.ProviderConfig{{Name: "minimax", BaseURL: "https://api.minimax.io/anthropic", APIKey: "mock-key", Model: model}},
				activeIdx:    0,
			}
			env := envSliceToMap(a.providerEnvLocked())
			if env["ANTHROPIC_MODEL"] != model || env["ANTHROPIC_BASE_URL"] != "https://api.minimax.io/anthropic" || env["ANTHROPIC_AUTH_TOKEN"] != "mock-key" {
				t.Fatalf("provider handoff changed: %v", env)
			}
			if a.proxyLocalURL != "" {
				t.Fatal("ordinary preset unexpectedly enabled a thinking rewrite proxy")
			}
			session, err := a.StartSession(context.Background(), "")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := session.Close(); err != nil {
					t.Errorf("close mock session: %v", err)
				}
			})
			cs := session.(*claudeSession)
			if !strings.Contains(strings.Join(cs.cmd.Args, " "), "--model "+model) {
				t.Fatalf("selected model missing from subprocess: %v", cs.cmd.Args)
			}
		})
	}
}
