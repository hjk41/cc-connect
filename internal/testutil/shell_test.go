package testutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

type readyWriter struct {
	once  sync.Once
	ready chan struct{}
}

func (w *readyWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.ready) })
	return len(p), nil
}
func TestShellCancellationReleasesInheritedPipes(t *testing.T) {
	path := Shell(t, filepath.Join(t.TempDir(), "sleeping-cli"), "#!/bin/sh\necho ready\nexec sleep 30\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, path)
	writer := &readyWriter{ready: make(chan struct{})}
	cmd.Stdout = writer
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-writer.ready:
	case err := <-done:
		t.Fatalf("fixture exited before ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("fixture did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled process succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled fixture left descendants holding its pipes open")
	}
}
