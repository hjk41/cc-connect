package claudecode

import (
	"context"
	"os"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestModeSwitch_ResumeLaunchAppliesSelectedMode(t *testing.T) {
	for _, mode := range []string{"yolo", "plan", "auto"} {
		t.Run(mode, func(t *testing.T) {
			a := &Agent{cmd: os.Args[0], workDir: t.TempDir(), activeIdx: -1,
				cliExtraArgs: []string{"-test.run=TestHelperProcess", "--", "stdin-eof-exit"},
				sessionEnv:   []string{"GO_WANT_HELPER_PROCESS=1"},
			}
			a.SetMode(mode)
			if applier, ok := any(a).(core.ResumeModeApplier); !ok || !applier.AppliesModeOnResume() {
				t.Fatal("mode change must preserve conversation")
			}
			session, err := a.StartSession(context.Background(), "resume-id")
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			wantMode := a.GetMode()
			if os.Geteuid() == 0 && mode == "yolo" {
				wantMode = "auto"
			}
			args := session.(*claudeSession).cmd.Args
			for flag, want := range map[string]string{"--resume": "resume-id", "--permission-mode": wantMode} {
				found := false
				for i := 0; i+1 < len(args); i++ {
					if args[i] == flag && args[i+1] == want {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing %s %s: %v", flag, want, args)
				}
			}
		})
	}
}
