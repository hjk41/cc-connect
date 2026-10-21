package codex

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestCommandDirs_ResolvesProjectAndEffectiveCodexHomeCommands(t *testing.T) {
	for _, source := range []string{"option", "environment", "config env", "provider env", "session env"} {
		t.Run(source, func(t *testing.T) {
			workDir, home := t.TempDir(), t.TempDir()
			t.Setenv("CODEX_HOME", t.TempDir())
			a := &Agent{workDir: workDir, activeIdx: -1}
			switch source {
			case "option":
				a.codexHome = home
				a.sessionEnv = []string{"CODEX_HOME=" + t.TempDir()}
			case "environment":
				t.Setenv("CODEX_HOME", home)
			case "config env":
				a.configEnv = []string{"CODEX_HOME=" + home}
			case "provider env":
				a.configEnv = []string{"CODEX_HOME=" + t.TempDir()}
				a.providers = []core.ProviderConfig{{Env: map[string]string{"CODEX_HOME": home}}}
				a.activeIdx = 0
			case "session env":
				a.configEnv = []string{"CODEX_HOME=" + t.TempDir()}
				a.sessionEnv = []string{"CODEX_HOME=" + home}
			}
			provider, ok := any(a).(core.CommandProvider)
			if !ok {
				t.Fatal("Codex must expose file-backed commands")
			}
			want := []string{filepath.Join(workDir, ".codex", "prompts"), filepath.Join(home, "prompts")}
			if got := provider.CommandDirs(); !reflect.DeepEqual(got, want) {
				t.Fatalf("CommandDirs() = %v, want %v", got, want)
			}
			for i, dir := range want {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				name := []string{"test", "global"}[i]
				if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte("---\ndescription: Test command\n---\nRun $ARGUMENTS"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			r := core.NewCommandRegistry()
			r.SetAgentDirs(provider.CommandDirs())
			for _, name := range []string{"test", "global"} {
				cmd, ok := r.Resolve(name)
				if !ok || cmd.Description != "Test command" || core.ExpandPrompt(cmd.Prompt, []string{"checks"}) != "Run checks" {
					t.Fatalf("Resolve(%q) = %+v, %v", name, cmd, ok)
				}
			}
		})
	}
}
