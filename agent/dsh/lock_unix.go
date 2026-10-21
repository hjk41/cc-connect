//go:build unix

package dsh

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// sessionDirLocked reports whether a live process holds the POSIX
// flock on session.lock. The lock file survives after the writer exits,
// so its presence alone is not a lock.
func sessionDirLocked(dir string) (bool, error) {
	path := filepath.Join(dir, "session.lock")
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return true, nil
	}
	// Some platforms surface the contention as EWOULDBLOCK without wrapping.
	if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
		return true, nil
	}
	return false, err
}
