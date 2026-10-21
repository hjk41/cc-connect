//go:build unix

package dsh

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if path := os.Getenv("DSH_HOLD_LOCK"); path != "" {
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			os.Exit(2)
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			os.Exit(3)
		}
		_, _ = os.Stdout.WriteString("locked\n")
		_ = os.Stdout.Sync()
		time.Sleep(20 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestDeleteRefusesLiveLockAndSymlink(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	agent := testAgent(t, map[string]any{"work_dir": work, "dsh_home": home})
	id := "66666666-6666-6666-6666-666666666666"
	dir := writeSession(t, home, id, work, "locked")
	lockPath := filepath.Join(dir, "session.lock")

	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "DSH_HOLD_LOCK="+lockPath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(stdout)
		if sc.Scan() && sc.Text() == "locked" {
			close(done)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("lock helper did not start")
	}

	if err := agent.DeleteSession(context.Background(), id); err == nil {
		t.Fatal("locked session was deleted")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("locked directory was removed")
	}

	outside := t.TempDir()
	link := filepath.Join(home, "sessions", "slug-link", id+"-link")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := agent.DeleteSession(context.Background(), id+"-link"); err == nil {
		t.Fatal("symlink session directory was accepted")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("symlink target was removed")
	}
}
