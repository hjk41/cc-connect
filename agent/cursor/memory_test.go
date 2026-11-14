package cursor

import (
	"path/filepath"
	"testing"
)

func TestMemoryFiles_ProjectAGENTSAndNoGlobal(t *testing.T) {
	workDir := t.TempDir()
	a := &Agent{workDir: workDir}

	want := filepath.Join(workDir, "AGENTS.md")
	// The fork resolves the workspace through EvalSymlinks so a symlinked
	// work_dir writes AGENTS.md at the real project root (t.TempDir is behind
	// /private/var on macOS).
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil {
		want = filepath.Join(resolved, "AGENTS.md")
	}
	if got := a.ProjectMemoryFile(); got != want {
		t.Fatalf("ProjectMemoryFile() = %q, want %q", got, want)
	}
	if got := a.GlobalMemoryFile(); got != "" {
		t.Fatalf("GlobalMemoryFile() = %q, want empty (Cursor CLI has no user-level AGENTS.md)", got)
	}
}

func TestMemoryFiles_RelativeWorkDir(t *testing.T) {
	a := &Agent{workDir: filepath.Join("rel", "project")}
	abs, err := filepath.Abs(a.workDir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(abs, "AGENTS.md")
	if got := a.ProjectMemoryFile(); got != want {
		t.Fatalf("ProjectMemoryFile() = %q, want %q", got, want)
	}
}
