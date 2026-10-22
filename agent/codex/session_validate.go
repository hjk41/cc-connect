package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhg5/cc-connect/core"
)

var _ core.SessionIDValidator = (*Agent)(nil)

func (a *Agent) ValidateSessionID(ctx context.Context, sessionID string) bool {
	if strings.TrimSpace(sessionID) == "" || ctx.Err() != nil {
		return false
	}
	home, err := a.effectiveCodexHome()
	if err != nil {
		return false
	}
	path := findSessionFileInCodexHome(home, sessionID)
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	if !scanner.Scan() {
		return false
	}
	var meta struct {
		Type    string `json:"type"`
		Payload struct {
			ID  string `json:"id"`
			Cwd string `json:"cwd"`
		} `json:"payload"`
	}
	if json.Unmarshal(scanner.Bytes(), &meta) != nil || meta.Type != "session_meta" || meta.Payload.ID != sessionID || strings.TrimSpace(meta.Payload.Cwd) == "" {
		return false
	}
	a.mu.RLock()
	workDir := a.workDir
	a.mu.RUnlock()
	if strings.TrimSpace(workDir) == "" {
		return false
	}
	want, err := canonicalSessionDir(workDir)
	if err != nil {
		return false
	}
	got, err := canonicalSessionDir(meta.Payload.Cwd)
	return err == nil && got == want && ctx.Err() == nil
}

func canonicalSessionDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}
