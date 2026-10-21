package cursor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestCommandDirs_ResolvesProjectAndHomeCommands(t *testing.T) {
	workDir, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	a := &Agent{workDir: workDir}
	provider, ok := any(a).(core.CommandProvider)
	if !ok {
		t.Fatal("Cursor must expose file-backed commands")
	}
	want := []string{filepath.Join(workDir, ".cursor", "commands"), filepath.Join(home, ".cursor", "commands")}
	if got := provider.CommandDirs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("CommandDirs() = %v, want %v", got, want)
	}
	for i, dir := range want {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		name := []string{"test", "global"}[i]
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte("---\ndescription: Test command\n---\nRun $1"), 0o644); err != nil {
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
}
