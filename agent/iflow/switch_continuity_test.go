package iflow

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
	// StartSession snapshots the launch config; Send supplies these to --yolo -r.
	if session.CurrentSessionID() != "resume-id" || session.(*iflowSession).mode != "yolo" {
		t.Fatal("mode or identity lost on resume")
	}
}
func TestModelSwitch_ExplicitSelectionOverridesProviderAtLaunch(t *testing.T) {
	a := &Agent{workDir: t.TempDir(), activeIdx: -1}
	a.SetProviders([]core.ProviderConfig{{Name: "provider", Model: "provider-default"}, {Name: "other", Model: "other-default"}})
	a.SetActiveProvider("provider")
	a.SetModel("workspace-model")
	if a.GetModel() != "workspace-model" {
		t.Fatal("provider overrode explicit workspace selection")
	}
	session, err := a.StartSession(context.Background(), "resume-id")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.(*iflowSession).model != "workspace-model" {
		t.Fatal("launch config ignored workspace model")
	}
	if a.GetActiveProvider().Model != "provider-default" {
		t.Fatal("runtime selection mutated provider configuration")
	}
	a.SetActiveProvider("other")
	if a.GetModel() != "other-default" {
		t.Fatal("switching provider failed to clear pin")
	}
	a.SetModel("workspace-model")
	a.SetActiveProvider("")
	if a.GetActiveProvider() != nil {
		t.Fatal("provider was not cleared")
	}
}
