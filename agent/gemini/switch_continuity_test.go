package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

// Re-execute only this test as the CLI, never a real installed agent.
func TestSwitchLaunchProcess(t *testing.T) {
	if os.Getenv("CC_SWITCH_LAUNCH_HELPER") != "1" {
		return
	}
	data, _ := json.Marshal(os.Args)
	if err := os.WriteFile(os.Getenv("CC_SWITCH_ARGS"), data, 0600); err != nil {
		os.Exit(2)
	}
	fmt.Println(`{"type":"result","result":"done"}`)
	os.Exit(0)
}

func switchLaunchAgent(t *testing.T) (*Agent, string) {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.json")
	a := &Agent{cmd: os.Args[0], workDir: dir, activeIdx: -1,
		cliExtraArgs: []string{"-test.run=^TestSwitchLaunchProcess$", "--"},
		sessionEnv:   []string{"CC_SWITCH_LAUNCH_HELPER=1", "CC_SWITCH_ARGS=" + argsFile},
	}
	return a, argsFile
}

func switchLaunchArgs(t *testing.T, a *Agent, argsFile string) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := a.StartSession(ctx, "resume-id")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.CurrentSessionID() != "resume-id" {
		t.Fatal("resume identity lost")
	}
	if err := session.Send("hello", "message", nil, nil); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case event := <-session.Events():
			if event.Type == core.EventError {
				t.Fatal(event.Error)
			}
			if event.Type == core.EventResult {
				data, err := os.ReadFile(argsFile)
				if err != nil {
					t.Fatal(err)
				}
				var args []string
				if err := json.Unmarshal(data, &args); err != nil {
					t.Fatal(err)
				}
				return args
			}
		case <-ctx.Done():
			t.Fatal("launch did not finish")
		}
	}
}
func requireSwitchArg(t *testing.T, args []string, flag, value string) {
	t.Helper()
	for i, arg := range args {
		if arg == flag && (value == "" || i+1 < len(args) && args[i+1] == value) {
			return
		}
	}
	t.Fatalf("missing %s %s in %v", flag, value, args)
}
func TestModeSwitch_ResumeLaunchAppliesSelectedMode(t *testing.T) {

	a, argsFile := switchLaunchAgent(t)
	a.SetMode("yolo")
	if applier, ok := any(a).(core.ResumeModeApplier); !ok || !applier.AppliesModeOnResume() {
		t.Fatal("mode change must preserve the conversation")
	}
	args := switchLaunchArgs(t, a, argsFile)
	requireSwitchArg(t, args, "--resume", "resume-id")
	requireSwitchArg(t, args, "-y", "")
}

func TestModelSwitch_ExplicitSelectionOverridesProviderAtLaunch(t *testing.T) {
	a, argsFile := switchLaunchAgent(t)
	a.SetProviders([]core.ProviderConfig{{Name: "provider", Model: "provider-default"}, {Name: "other", Model: "other-default"}})
	a.SetActiveProvider("provider")
	a.SetModel("workspace-model")
	if a.GetModel() != "workspace-model" {
		t.Fatal("provider overrode explicit workspace selection")
	}
	args := switchLaunchArgs(t, a, argsFile)
	requireSwitchArg(t, args, "-m", "workspace-model")
	if a.GetActiveProvider().Model != "provider-default" {
		t.Fatal("runtime selection changed provider configuration")
	}
	a.SetActiveProvider("other")
	if a.GetModel() != "other-default" {
		t.Fatal("provider change failed to clear the model pin")
	}
	a.SetModel("workspace-model")
	a.SetActiveProvider("")
	if a.GetActiveProvider() != nil {
		t.Fatal("provider was not cleared")
	}
}
