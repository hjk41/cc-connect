//go:build unix

package feishu

import (
	"os/exec"
	"syscall"
	"time"
)

// configureMermaidProcess puts the renderer in its own process group so a
// timeout or shutdown can terminate Node together with every Chrome child.
// The first signal is SIGTERM so Node/Puppeteer can close Chrome cleanly;
// runMermaidCommand escalates to SIGKILL after a short grace period.
func configureMermaidProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
}

func mermaidSignalProcessGroup(cmd *exec.Cmd, force bool) {
	if cmd.Process == nil {
		return
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}
