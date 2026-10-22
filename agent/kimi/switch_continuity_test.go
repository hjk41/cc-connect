package kimi

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
	s := session.(*kimiSession)
	args := s.buildArgs("hello")
	foundID := false
	for _, arg := range args {
		if arg == "resume-id" {
			foundID = true
		}
	}
	if !foundID {
		t.Fatalf("resume ID missing: %v", args)
	}
	if s.mode != "yolo" {
		t.Fatal("selected mode not copied to resumed session")
	}
	// Kimi's non-interactive prompt mode already auto-approves; no yolo flag.
	a.SetMode("plan")
	planned, err := a.StartSession(context.Background(), "resume-id")
	if err != nil {
		t.Fatal(err)
	}
	defer planned.Close()
	plannedArgs := planned.(*kimiSession).buildArgs("hello")
	foundPlan := false
	for _, arg := range plannedArgs {
		if arg == "--plan" {
			foundPlan = true
		}
	}
	if !foundPlan {
		t.Fatalf("plan mode not passed on resume: %v", plannedArgs)
	}
}
