package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestSwitchLaunchProcess(t *testing.T) {
	if os.Getenv("CC_PI_SWITCH_HELPER") != "1" {
		return
	}
	data, _ := json.Marshal(os.Args)
	if err := os.WriteFile(os.Getenv("CC_PI_SWITCH_ARGS"), data, 0600); err != nil {
		os.Exit(2)
	}
	rpc := false
	for i := 0; i+1 < len(os.Args); i++ {
		if os.Args[i] == "--mode" && os.Args[i+1] == "rpc" {
			rpc = true
		}
	}
	if rpc {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			fmt.Println(`{"id":"cc-connect-state-probe","type":"response","command":"get_state","success":true,"data":{"sessionId":"resume-id"}}`)
		}
	} else {
		fmt.Println(`{"type":"agent_end"}`)
	}
	os.Exit(0)
}

func TestPiReasoningSwitch_PreservesConversation(t *testing.T) {
	for _, rpc := range []bool{false, true} {
		t.Run(fmt.Sprintf("rpc=%v", rpc), func(t *testing.T) { testPiResumeSelection(t, rpc, true) })
	}
}
func TestModeSwitch_ResumeLaunchAppliesSelectedMode(t *testing.T) {
	for _, rpc := range []bool{false, true} {
		t.Run(fmt.Sprintf("rpc=%v", rpc), func(t *testing.T) { testPiResumeSelection(t, rpc, false) })
	}
}
func testPiResumeSelection(t *testing.T, rpc, reasoning bool) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.json")
	a := &Agent{cmd: os.Args[0], workDir: dir, rpc: rpc,
		cliExtraArgs: []string{"-test.run=^TestSwitchLaunchProcess$", "--"},
		sessionEnv:   []string{"CC_PI_SWITCH_HELPER=1", "CC_PI_SWITCH_ARGS=" + argsFile},
	}
	if reasoning {
		a.SetReasoningEffort("high")
		if applier, ok := any(a).(core.ResumeReasoningApplier); !ok || !applier.AppliesReasoningOnResume() {
			t.Fatal("reasoning switch must preserve conversation")
		}
	} else {
		a.SetMode("yolo")
		if applier, ok := any(a).(core.ResumeModeApplier); !ok || !applier.AppliesModeOnResume() {
			t.Fatal("mode switch must preserve conversation")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := a.StartSession(ctx, "resume-id")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.CurrentSessionID() != "resume-id" {
		t.Fatal("saved ID lost")
	}
	if !rpc {
		if err := session.Send("hello", "message", nil, nil); err != nil {
			t.Fatal(err)
		}
		for {
			select {
			case event := <-session.Events():
				if event.Type == core.EventResult {
					goto done
				}
				if event.Type == core.EventError {
					t.Fatal(event.Error)
				}
			case <-ctx.Done():
				t.Fatal("JSON launch did not complete")
			}
		}
	}
done:
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"--session-id": "resume-id"}
	if reasoning {
		want["--thinking"] = "high"
	} else {
		if session.(*piSession).mode != "yolo" {
			t.Fatal("mode not passed to resumed session")
		}
		found := false
		for _, entry := range session.(*piSession).extraEnv {
			if entry == "CC_PERMISSION_MODE=yolo" {
				found = true
			}
		}
		if !found {
			t.Fatal("permission-gate mode not passed on resume")
		}
	}
	for flag, value := range want {
		found := false
		for i := 0; i+1 < len(args); i++ {
			if args[i] == flag && args[i+1] == value {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %s %s: %v", flag, value, args)
		}
	}
}
