// Package testutil provides portable external-process fixtures for tests.
package testutil

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// Shell writes a shell fixture. On Windows a native copy of the test executable
// launches Git Bash; importing tests must call RunShellHelper from TestMain.
// This exercises the real Windows process boundary instead of skipping tests.
func Shell(t *testing.T, path, script string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	t.Setenv("GORACE", os.Getenv("GORACE")+" atexit_sleep_ms=0")
	// Paths interpolated by Go are Windows paths, while shell literals use MSYS
	// paths. Only rewrite absolute drive paths, preserving JSON and shell escapes.
	re := regexp.MustCompile(`[A-Za-z]:\\[^'"\r\n]*`)
	script = re.ReplaceAllStringFunc(script, func(s string) string { return strings.ReplaceAll(s, `\`, "/") })
	path += ".exe"
	src, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Windows locks every hard link to a running image, including the parent
	// test executable. Copy so completed fixtures can be removed independently.
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}

	if err := os.WriteFile(path+".shell-fixture", []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// RunShellHelper returns for ordinary tests and exits only in fixture copies.
func RunShellHelper() {
	if runtime.GOOS != "windows" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	path := exe + ".shell-fixture"
	if _, err := os.Stat(path); err != nil {
		return
	}
	if err := containShellProcess(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(126)
	}
	// Prefer Git Bash to the unrelated WSL bash launcher in System32.
	bash := filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe")
	if _, err := os.Stat(bash); err != nil {
		fmt.Fprintln(os.Stderr, "shell fixture requires Git for Windows Bash:", err)
		os.Exit(127)
	}
	cmd := exec.Command(bash, append([]string{filepath.ToSlash(path)}, os.Args[1:]...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			os.Exit(e.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(126)
	}
	os.Exit(0)
}
