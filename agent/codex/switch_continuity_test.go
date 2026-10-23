package codex

import (
	"context"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestModeSwitch_ResumeLaunchAppliesSelectedMode(t *testing.T) {
	a := &Agent{workDir: t.TempDir(), activeIdx: -1}
	a.backend = "exec"
	a.codexHome = t.TempDir()
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
	s := session.(*codexSession)
	args, err := s.buildExecArgs("hello", nil)
	if err != nil {
		t.Fatal(err)
	}
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
		if arg == "--dangerously-bypass-approvals-and-sandbox" {
			foundMode = true
		}
	}
	if !foundMode {
		t.Fatalf("mode not passed on resume: %v", args)
	}
}
