//go:build !unix

package dsh

import (
	"os"
	"path/filepath"
)

// sessionDirLocked is a conservative check where flock is unavailable.
// DeepSeek Harness on Windows uses a named semaphore and writes no lock
// file. A missing lock file means this process cannot see a holder.
func sessionDirLocked(dir string) (bool, error) {
	_, err := os.Lstat(filepath.Join(dir, "session.lock"))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return false, nil
}
