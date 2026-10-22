package opencode

import (
	"context"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestModeSwitch_ResumeLaunchAppliesSelectedMode(t *testing.T) {
	a := &Agent{workDir: t.TempDir(), activeIdx: -1}

	a.SetMode("yolo")
	if applier, ok := any(a).(core.ResumeModeApplier); !ok || !applier.AppliesModeOnResume() {
		t.Fatal("mode switch must preserve the conversation")
	}
	session, err := a.StartSession(context.Background(), "resume-id")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.CurrentSessionID() != "resume-id" {
		t.Fatal("resume identity lost")
	}
	s := session.(*opencodeSession)
	args := s.buildRunArgs("hello", nil, s.CurrentSessionID())
	foundID := false
	for _, arg := range args {
		if arg == "resume-id" {
			foundID = true
		}
	}
	if !foundID {
		t.Fatalf("resume ID missing: %v", args)
	}
	foundMode := false
	for _, arg := range args {
		if arg == "--dangerously-skip-permissions" {
			foundMode = true
		}
	}
	if !foundMode {
		t.Fatalf("mode not passed on resume: %v", args)
	}
}

func TestModelSwitch_ExplicitSelectionOverridesProviderAtLaunch(t *testing.T) {
	a := &Agent{workDir: t.TempDir(), activeIdx: -1}
	a.SetProviders([]core.ProviderConfig{{Name: "provider", Model: "provider-default"}, {Name: "other", Model: "other-default"}})
	a.SetActiveProvider("provider")
	a.SetModel("workspace-model")
	if a.GetModel() != "workspace-model" {
		t.Fatal("provider overrode workspace model")
	}
	session, err := a.StartSession(context.Background(), "resume-id")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	s := session.(*opencodeSession)
	args := s.buildRunArgs("hello", nil, s.CurrentSessionID())
	found := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--model" && args[i+1] == "workspace-model" {
			found = true
		}
	}
	if !found {
		t.Fatalf("launch ignored workspace model: %v", args)
	}
	if a.GetActiveProvider().Model != "provider-default" {
		t.Fatal("provider configuration mutated")
	}
	a.SetActiveProvider("other")
	if a.GetModel() != "other-default" {
		t.Fatal("switching provider did not clear the pin")
	}
	a.SetModel("workspace-model")
	a.SetActiveProvider("")
	if a.GetActiveProvider() != nil {
		t.Fatal("provider was not cleared")
	}
}
