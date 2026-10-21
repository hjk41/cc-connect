//go:build unix

package dsh

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestReconcilePermissionRefusesLockedSession(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	id := "cccccccc-cccc-cccc-cccc-cccccccccccc"
	agent := testAgent(t, map[string]any{"work_dir": work, "dsh_home": home})
	agent.SetMode("plan")
	path := writePermLog(t, home, id, work,
		sessionHeaderLine(id, work),
		`{"type":"permission/preset","seq":0,"time":1,"data":{"preset":"workspace-write"}}`,
		`{"type":"sandbox/mode","seq":1,"time":1,"data":{"mode":"workspace-write"}}`,
		`{"type":"approval/policy","seq":2,"time":1,"data":{"policy":"ask"}}`,
	)
	lockPath := filepath.Join(filepath.Dir(path), "session.lock")
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

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = agent.prepareResume(context.Background(), id)
	if !errors.Is(err, errPermissionModeNotApplied) {
		t.Fatalf("err = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("locked log was modified")
	}
}
