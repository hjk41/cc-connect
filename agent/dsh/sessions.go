package dsh

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/chenhg5/cc-connect/agent/acp"
	"github.com/chenhg5/cc-connect/core"
)

// errSessionNotFound is returned when no on-disk session matches the id.
// Other read failures stay distinct so callers can keep a session id when
// the log is temporarily unreadable.
var errSessionNotFound = errors.New("session not found")

// ListSessions merges ACP session/list with on-disk titles, timestamps,
// and message counts. Results are limited to the current work_dir.
// Model-probe directories are omitted.
func (a *Agent) ListSessions(ctx context.Context) ([]core.AgentSessionInfo, error) {
	if a == nil || a.Agent == nil {
		return nil, fmt.Errorf("dsh: agent is not initialized")
	}
	listed, listErr := a.Agent.ListSessions(ctx)
	disk, diskErr := a.scanSessions()
	if listErr != nil && diskErr != nil {
		return nil, fmt.Errorf("dsh: list sessions: %w", listErr)
	}
	if listErr != nil {
		slog.Warn("dsh: session/list failed; using on-disk sessions", "error", listErr)
		listed = nil
	}
	if diskErr != nil {
		slog.Warn("dsh: session directory scan failed", "error", diskErr)
	}
	return mergeSessions(listed, disk), nil
}

// ValidateSessionID reports whether sessionID belongs to this work_dir
// by reading that session's on-disk log. A read error other than
// not-found keeps the id so a transient failure does not drop the
// conversation.
func (a *Agent) ValidateSessionID(_ context.Context, sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || !safeSessionID(sessionID) {
		return false
	}
	_, meta, err := a.findSessionDir(sessionID)
	if err != nil {
		return !errors.Is(err, errSessionNotFound)
	}
	work := ""
	if a != nil && a.Agent != nil {
		work = a.GetWorkDir()
	}
	if work != "" && !sameWorkDir(meta.Cwd, work) {
		return false
	}
	return true
}

// GetSessionTitle prefers a title learned from ACP, then the on-disk
// session/title record, then the first user prompt.
func (a *Agent) GetSessionTitle(sessionID string) string {
	if a != nil && a.Agent != nil {
		if title := strings.TrimSpace(a.Agent.GetSessionTitle(sessionID)); title != "" {
			return title
		}
	}
	meta, err := a.readSession(sessionID)
	if err != nil {
		return ""
	}
	return meta.Title
}

// GetSessionHistory reads the zstd JSONL log. dsh does not implement
// session/load, so history is not replayed over ACP.
func (a *Agent) GetSessionHistory(_ context.Context, sessionID string, limit int) ([]core.HistoryEntry, error) {
	meta, err := a.readSession(sessionID)
	if err != nil {
		return nil, err
	}
	entries := meta.History
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	return append([]core.HistoryEntry(nil), entries...), nil
}

// DeleteSession removes one session directory under DSH_HOME/sessions
// after the log header id matches and no live process holds session.lock.
// Nothing outside that directory is removed.
func (a *Agent) DeleteSession(_ context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if !safeSessionID(sessionID) {
		return fmt.Errorf("dsh: invalid session id")
	}
	dir, meta, err := a.findSessionDir(sessionID)
	if err != nil {
		return err
	}
	if meta.ID != sessionID {
		return fmt.Errorf("dsh: session id does not match log header")
	}
	locked, err := sessionDirLocked(dir)
	if err != nil {
		return fmt.Errorf("dsh: check session lock: %w", err)
	}
	if locked {
		return fmt.Errorf("dsh: session %s is in use", sessionID)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("dsh: delete session: %w", err)
	}
	return nil
}

func (a *Agent) readSession(sessionID string) (sessionMeta, error) {
	sessionID = strings.TrimSpace(sessionID)
	if !safeSessionID(sessionID) {
		return sessionMeta{}, fmt.Errorf("dsh: invalid session id")
	}
	_, meta, err := a.findSessionDir(sessionID)
	if err != nil {
		return sessionMeta{}, err
	}
	return meta, nil
}

func (a *Agent) scanSessions() ([]sessionMeta, error) {
	root, err := a.sessionsRoot()
	if err != nil {
		return nil, err
	}
	work := ""
	if a != nil && a.Agent != nil {
		work = a.GetWorkDir()
	}
	slugs, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []sessionMeta
	for _, slug := range slugs {
		slugPath := filepath.Join(root, slug.Name())
		if !isRealDir(slugPath) {
			continue
		}
		children, err := os.ReadDir(slugPath)
		if err != nil {
			continue
		}
		for _, child := range children {
			dir := filepath.Join(slugPath, child.Name())
			if !isRealDir(dir) {
				continue
			}
			meta, err := readSessionDir(dir)
			if err != nil || meta.ID == "" {
				continue
			}
			if a.isExcluded(meta.Cwd, meta.ID) {
				continue
			}
			if work != "" && !sameWorkDir(meta.Cwd, work) {
				continue
			}
			out = append(out, meta)
		}
	}
	return out, nil
}

// findSessionDir opens only the child directory named sessionID under each
// workspace slug, then checks the log header id. Other session directories
// are not read. A duplicate match or a path outside the sessions root is
// rejected.
func (a *Agent) findSessionDir(sessionID string) (string, sessionMeta, error) {
	if !safeSessionID(sessionID) {
		return "", sessionMeta{}, fmt.Errorf("dsh: invalid session id")
	}
	root, err := a.sessionsRoot()
	if err != nil {
		return "", sessionMeta{}, err
	}
	slugs, err := os.ReadDir(root)
	if err != nil {
		if _, statErr := os.Stat(root); os.IsNotExist(err) && os.IsNotExist(statErr) {
			return "", sessionMeta{}, sessionNotFound(sessionID)
		}
		return "", sessionMeta{}, fmt.Errorf("dsh: read sessions: %w", err)
	}
	var foundDir string
	var found sessionMeta
	matches := 0
	var readErr error
	for _, slug := range slugs {
		slugPath := filepath.Join(root, slug.Name())
		if !isRealDir(slugPath) {
			continue
		}
		dir := filepath.Join(slugPath, sessionID)
		info, err := os.Lstat(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				readErr = err
			}
			continue
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if !withinDir(root, dir) {
			continue
		}
		meta, err := readSessionDir(dir)
		if err != nil {
			readErr = err
			continue
		}
		if meta.ID != sessionID {
			continue
		}
		matches++
		foundDir = dir
		found = meta
	}
	if matches > 1 {
		return "", sessionMeta{}, fmt.Errorf("dsh: session %s matched more than one directory", sessionID)
	}
	if matches == 1 {
		return foundDir, found, nil
	}
	if readErr != nil {
		return "", sessionMeta{}, fmt.Errorf("dsh: read session %s: %w", sessionID, readErr)
	}
	return "", sessionMeta{}, sessionNotFound(sessionID)
}

func sessionNotFound(sessionID string) error {
	return fmt.Errorf("dsh: session %s not found: %w", sessionID, errSessionNotFound)
}

func (a *Agent) sessionsRoot() (string, error) {
	home := ""
	if a != nil {
		home = a.homeDir()
	}
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("dsh: DSH_HOME is not set and the user home is unknown")
	}
	root := filepath.Join(home, "sessions")
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("dsh: sessions path: %w", err)
	}
	return abs, nil
}

func readSessionDir(dir string) (sessionMeta, error) {
	logPath, ok := pickSessionLog(dir)
	if !ok {
		return sessionMeta{}, fmt.Errorf("dsh: no session log in %s", dir)
	}
	if !withinDir(dir, logPath) {
		return sessionMeta{}, fmt.Errorf("dsh: session log escapes its directory")
	}
	meta, err := parseSessionFile(logPath)
	if err != nil {
		return sessionMeta{}, err
	}
	return meta, nil
}

func mergeSessions(listed []core.AgentSessionInfo, disk []sessionMeta) []core.AgentSessionInfo {
	byID := make(map[string]sessionMeta, len(disk))
	for _, meta := range disk {
		if meta.ID == "" {
			continue
		}
		byID[meta.ID] = meta
	}
	seen := map[string]struct{}{}
	var out []core.AgentSessionInfo
	for _, item := range listed {
		if item.ID == "" {
			continue
		}
		if meta, ok := byID[item.ID]; ok {
			item = enrichSession(item, meta)
		}
		seen[item.ID] = struct{}{}
		out = append(out, item)
	}
	for _, meta := range disk {
		if meta.ID == "" {
			continue
		}
		if _, ok := seen[meta.ID]; ok {
			continue
		}
		out = append(out, enrichSession(core.AgentSessionInfo{ID: meta.ID}, meta))
	}
	return out
}

func enrichSession(item core.AgentSessionInfo, meta sessionMeta) core.AgentSessionInfo {
	if strings.TrimSpace(item.Summary) == "" {
		item.Summary = meta.Title
	}
	if item.MessageCount == 0 {
		item.MessageCount = len(meta.History)
	}
	if item.ModifiedAt.IsZero() {
		item.ModifiedAt = meta.ModifiedAt
	}
	return item
}

func sameWorkDir(a, b string) bool {
	return strings.TrimSpace(a) != "" && strings.TrimSpace(b) != "" && acp.SameCwd(a, b)
}

func isRealDir(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	return info.Mode()&os.ModeSymlink == 0
}

func withinDir(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

func safeSessionID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." {
		return false
	}
	if strings.ContainsAny(id, `/\`) {
		return false
	}
	return filepath.Clean(id) == id
}
