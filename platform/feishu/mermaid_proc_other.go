//go:build !unix

package feishu

import (
	"os/exec"
	"time"
)

func configureMermaidProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		_ = cmd.Process.Kill()
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
}

func mermaidSignalProcessGroup(cmd *exec.Cmd, force bool) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
