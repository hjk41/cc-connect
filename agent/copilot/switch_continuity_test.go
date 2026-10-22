package copilot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestSwitchLaunchProcess(t *testing.T) {
	if os.Getenv("CC_COPILOT_SWITCH_HELPER") != "1" {
		return
	}
	runMockCopilot(func(method string, id json.RawMessage, body json.RawMessage, w *lspWriter) {
		if method == "session.resume" {
			if err := os.WriteFile(os.Getenv("CC_COPILOT_SWITCH_CONFIG"), body, 0600); err != nil {
				os.Exit(2)
			}
		}
		writeResponse(w, id, map[string]any{})
	})
	os.Exit(0)
}
func switchLaunchAgent(t *testing.T) (*Agent, string) {
	t.Helper()
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.json")
	return &Agent{cmd: os.Args[0], workDir: dir, activeIdx: -1,
		cliExtraArgs: []string{"-test.run=^TestSwitchLaunchProcess$", "--"},
		sessionEnv:   []string{"CC_COPILOT_SWITCH_HELPER=1", "CC_COPILOT_SWITCH_CONFIG=" + configFile},
	}, configFile
}
func TestModeSwitch_ResumeLaunchAppliesSelectedMode(t *testing.T) {
	a, _ := switchLaunchAgent(t)
	a.SetMode("yolo")
	if applier, ok := any(a).(core.ResumeModeApplier); !ok || !applier.AppliesModeOnResume() {
		t.Fatal("mode switch must preserve conversation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := a.StartSession(ctx, "resume-id")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	s := session.(*copilotSession)
	if session.CurrentSessionID() != "resume-id" || !s.autoApprove.Load() {
		t.Fatal("mode or identity lost on resume")
	}
}
func TestModelSwitch_ExplicitSelectionOverridesProviderAtLaunch(t *testing.T) {
	a, configFile := switchLaunchAgent(t)
	a.SetProviders([]core.ProviderConfig{{Name: "provider", Model: "provider-default", BaseURL: "https://example.invalid/v1"}, {Name: "other", Model: "other-default"}})
	a.SetActiveProvider("provider")
	a.SetModel("workspace-model")
	if a.GetModel() != "workspace-model" {
		t.Fatal("provider overrode explicit workspace model")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := a.StartSession(ctx, "resume-id")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Params copilotSessionConfig `json:"params"`
	}
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatal(err)
	}
	if request.Params.SessionID != "resume-id" || request.Params.Model != "workspace-model" || request.Params.Provider == nil || request.Params.Provider.ModelID != "workspace-model" {
		t.Fatalf("resume config = %+v", request.Params)
	}
	if a.GetActiveProvider().Model != "provider-default" {
		t.Fatal("provider configuration mutated")
	}
	a.SetActiveProvider("other")
	if a.GetModel() != "other-default" {
		t.Fatal("switching provider did not clear pin")
	}
	a.SetModel("workspace-model")
	a.SetActiveProvider("")
	if a.GetActiveProvider() != nil {
		t.Fatal("provider was not cleared")
	}
}
