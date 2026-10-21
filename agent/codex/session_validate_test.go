package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSessionID_RequiresMatchingIDAndWorkspace(t *testing.T) {
	for _, tc := range []struct {
		name, id, fileID, metaID, cwd, recordType string
		want                                      bool
	}{
		{name: "matching", id: "session-id", fileID: "session-id", metaID: "session-id", cwd: "same", recordType: "session_meta", want: true},
		{name: "canonical path", id: "session-id", fileID: "session-id", metaID: "session-id", cwd: "canonical", recordType: "session_meta", want: true},
		{name: "another cwd", id: "session-id", fileID: "session-id", metaID: "session-id", cwd: "other", recordType: "session_meta"},
		{name: "missing file", id: "session-id"},
		{name: "empty id"},
		{name: "substring", id: "session-id", fileID: "prefix-session-id", metaID: "prefix-session-id", cwd: "same", recordType: "session_meta"},
		{name: "id mismatch", id: "session-id", fileID: "session-id", metaID: "another-id", cwd: "same", recordType: "session_meta"},
		{name: "missing cwd", id: "session-id", fileID: "session-id", metaID: "session-id", recordType: "session_meta"},
		{name: "wrong first record", id: "session-id", fileID: "session-id", metaID: "session-id", cwd: "same", recordType: "event_msg"},
		{name: "glob id", id: "*", fileID: "session-id", metaID: "session-id", cwd: "same", recordType: "session_meta"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, workDir := t.TempDir(), t.TempDir()
			t.Setenv("CODEX_HOME", t.TempDir())
			a := &Agent{workDir: workDir, configEnv: []string{"CODEX_HOME=" + home}}
			cwd := ""
			switch tc.cwd {
			case "same":
				cwd = workDir
			case "canonical":
				cwd = workDir + string(filepath.Separator) + "."
			case "other":
				cwd = t.TempDir()
			}
			if tc.fileID != "" {
				writeValidationRollout(t, home, tc.fileID, tc.metaID, cwd, tc.recordType)
			}
			if got := a.ValidateSessionID(context.Background(), tc.id); got != tc.want {
				t.Fatalf("ValidateSessionID(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

func TestValidateSessionID_SymlinkedWorkspace(t *testing.T) {
	home, workDir := t.TempDir(), t.TempDir()
	link := filepath.Join(t.TempDir(), "workspace")
	if err := os.Symlink(workDir, link); err != nil {
		t.Fatal(err)
	}
	writeValidationRollout(t, home, "session-id", "session-id", workDir, "session_meta")
	a := &Agent{workDir: link, codexHome: home}
	if !a.ValidateSessionID(context.Background(), "session-id") {
		t.Fatal("symlinked workspace did not match its real directory")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if a.ValidateSessionID(ctx, "session-id") {
		t.Fatal("cancelled validation should fail")
	}
}

func writeValidationRollout(t *testing.T, home, fileID, metaID, cwd, recordType string) {
	t.Helper()
	dir := filepath.Join(home, "sessions", "2026", "10", "21")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"type": recordType, "payload": map[string]any{"id": metaID, "cwd": cwd}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-10-21T10-00-00-"+fileID+".jsonl")
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
