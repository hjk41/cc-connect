package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) { RunShellHelper(); os.Exit(m.Run()) }
func TestShellPreservesArgumentsEnvironmentAndExit(t *testing.T) {
	t.Setenv("FIXTURE_VALUE", "env value")
	path := Shell(t, filepath.Join(t.TempDir(), "fake cli"), "#!/bin/sh\nprintf '%s\\n' \"$1\" \"$FIXTURE_VALUE\"\nexit 7\n")
	out, err := exec.Command(path, "an argument with spaces").CombinedOutput()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 7 {
		t.Fatalf("exit = %v; output = %s", err, out)
	}
	if got := strings.ReplaceAll(string(out), "\r\n", "\n"); got != "an argument with spaces\nenv value\n" {
		t.Fatalf("output = %q", got)
	}
}
