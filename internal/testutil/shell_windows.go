//go:build windows

package testutil

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Keep Bash and its descendants tied to the native fixture's lifetime. Without
// a job, CommandContext can kill the fixture while Bash keeps stdout open,
// leaving Cmd.Wait blocked in its pipe-copy goroutine until the test times out.
func containShellProcess() error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("create fixture job: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return fmt.Errorf("configure fixture job: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		_ = windows.CloseHandle(job)
		return fmt.Errorf("assign fixture job: %w", err)
	}
	// Intentionally retain this non-inheritable handle until process exit. The OS
	// closes it even after TerminateProcess, which terminates all descendants.
	return nil
}
