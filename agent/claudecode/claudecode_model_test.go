package claudecode

import (
	"context"
	"os/exec"
	"reflect"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestConfiguredModels_BoundaryConditions(t *testing.T) {
	a := &Agent{
		providers: []core.ProviderConfig{
			{Models: []core.ModelOption{{Name: "first"}}},
			{Models: []core.ModelOption{{Name: "second"}}},
		},
	}

	tests := []struct {
		name      string
		activeIdx int
		wantNil   bool
		wantName  string
	}{
		{name: "negative index", activeIdx: -1, wantNil: true},
		{name: "out of range", activeIdx: 2, wantNil: true},
		{name: "valid index", activeIdx: 1, wantName: "second"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a.activeIdx = tt.activeIdx
			got := a.configuredModels()
			if tt.wantNil {
				if got != nil {
					t.Fatalf("configuredModels() = %v, want nil", got)
				}
				return
			}
			if len(got) != 1 || got[0].Name != tt.wantName {
				t.Fatalf("configuredModels() = %v, want %q", got, tt.wantName)
			}
		})
	}
}

func TestGetModel_PrefersActiveProviderModel(t *testing.T) {
	a := &Agent{
		model: "sonnet",
		providers: []core.ProviderConfig{
			{Name: "anthropic", Model: "opus"},
		},
		activeIdx: 0,
	}

	if got := a.GetModel(); got != "opus" {
		t.Fatalf("GetModel() = %q, want opus", got)
	}
}

func TestModelPinned_OverridesProviderUntilSwitchOrClear(t *testing.T) {
	a := &Agent{model: "configured", activeIdx: 0, providers: []core.ProviderConfig{
		{Name: "first", Model: "provider-first"}, {Name: "second", Model: "provider-second"},
	}}
	a.SetModel("explicit")
	if got := a.GetModel(); got != "explicit" {
		t.Fatalf("pinned model = %q", got)
	}
	if a.SetActiveProvider("missing") || a.GetModel() != "explicit" {
		t.Fatal("failed switch changed pinned model")
	}
	if !a.SetActiveProvider("second") || a.GetModel() != "provider-second" {
		t.Fatal("provider switch did not unpin model")
	}
	a.SetModel("another")
	if !a.SetActiveProvider("") || a.modelPinned || a.GetModel() != "another" {
		t.Fatal("provider clear did not unpin model")
	}
	if a.providers[0].Model != "provider-first" || a.providers[1].Model != "provider-second" {
		t.Fatal("provider defaults mutated")
	}
}

func TestStartSession_UsesPinnedModel(t *testing.T) {
	bin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{cmd: bin, workDir: t.TempDir(), model: "configured", activeIdx: 0,
		providers: []core.ProviderConfig{{Model: "provider-default"}},
	}

	a.SetModel("explicit")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session, err := a.StartSession(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	args := session.(*claudeSession).cmd.Args
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--model" && args[i+1] == "explicit" {
			return
		}
	}
	t.Fatalf("pinned --model missing from %v", args)
}

func TestWorkspaceAgentOptions_RoundTripsPromptsAndEnv(t *testing.T) {
	bin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := New(map[string]any{"cmd": bin, "work_dir": t.TempDir(),
		"system_prompt": "custom system", "append_system_prompt": "extra instructions",
		"env": map[string]string{"CONFIG_ONLY": "a=b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	parent := raw.(*Agent)
	parent.SetSessionEnv([]string{"TRANSIENT=omit"})
	opts := parent.WorkspaceAgentOptions()
	if opts["system_prompt"] != "custom system" || opts["append_system_prompt"] != "extra instructions" {
		t.Fatalf("snapshot prompts = %#v", opts)
	}
	if !reflect.DeepEqual(opts["env"], map[string]string{"CONFIG_ONLY": "a=b"}) {
		t.Fatalf("snapshot env = %#v", opts["env"])
	}
	opts["work_dir"] = t.TempDir()
	childRaw, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	child := childRaw.(*Agent)
	if child.systemPrompt != parent.systemPrompt || child.appendSystemPrompt != parent.appendSystemPrompt || !reflect.DeepEqual(child.configEnv, parent.configEnv) {
		t.Fatal("workspace clone lost prompts or configured env")
	}
}
